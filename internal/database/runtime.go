package database

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrWorkUnavailable = errors.New("database work admission unavailable")
var ErrPoolUnavailable = errors.New("database pool temporarily unavailable")

// Runtime is the one serving-process budget shared by account, quota, relay,
// and cleanup work. The caller creates one instance and passes it to each
// component; offline migrations and startup verification use the raw pool.
type Runtime struct {
	Pool      *pgxpool.Pool
	admission chan struct{}
}

func NewRuntime(p *pgxpool.Pool) *Runtime {
	return &Runtime{Pool: p, admission: make(chan struct{}, 64)}
}

type unitKey struct{}

type lease struct {
	ctx     context.Context
	cancel  context.CancelFunc
	budget  *Runtime
	release sync.Once
}

func (l *lease) close() {
	l.release.Do(func() {
		if l.cancel != nil {
			l.cancel()
			<-l.budget.admission
		}
	})
}

func (r *Runtime) admit(parent context.Context) (*lease, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if owner, ok := parent.Value(unitKey{}).(*Runtime); ok && owner == r {
		return &lease{ctx: parent}, nil
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	select {
	case r.admission <- struct{}{}:
		return &lease{ctx: context.WithValue(ctx, unitKey{}, r), cancel: cancel, budget: r}, nil
	default:
		cancel()
		return nil, ErrWorkUnavailable
	}
}

// StartUnit admits before a logical guard and returns a context carrying the
// three-second deadline. Nested SQL work reuses this admission. Call release
// after the guard and all of its database work have completed.
func (r *Runtime) StartUnit(parent context.Context) (context.Context, func(), error) {
	l, err := r.admit(parent)
	if err != nil {
		return nil, nil, err
	}
	return l.ctx, l.close, nil
}

// WithUnit scopes admission to work and reports a deadline reached in work.
func (r *Runtime) WithUnit(parent context.Context, work func(context.Context) error) error {
	ctx, release, err := r.StartUnit(parent)
	if err != nil {
		return err
	}
	defer release()
	err = work(ctx)
	if err == nil {
		return ctx.Err()
	}
	return err
}

func (r *Runtime) acquire(l *lease) (*pgxpool.Conn, error) {
	ctx, cancel := context.WithTimeout(l.ctx, 500*time.Millisecond)
	defer cancel()
	conn, err := r.Pool.Acquire(ctx)
	if err != nil {
		return nil, ErrPoolUnavailable
	}
	return conn, nil
}

func (r *Runtime) Unit(parent context.Context, work func(context.Context, *pgxpool.Conn) error) error {
	return r.WithUnit(parent, func(ctx context.Context) error {
		l := &lease{ctx: ctx}
		conn, err := r.acquire(l)
		if err != nil {
			return err
		}
		defer conn.Release()
		return work(ctx, conn)
	})
}

func (r *Runtime) Exec(parent context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	var tag pgconn.CommandTag
	err := r.Unit(parent, func(ctx context.Context, conn *pgxpool.Conn) error {
		var err error
		tag, err = conn.Exec(ctx, sql, args...)
		return err
	})
	return tag, err
}

type errorRow struct{ err error }

func (r errorRow) Scan(...any) error { return r.err }

type leasedRow struct {
	pgx.Row
	conn *pgxpool.Conn
	unit *lease
}

func (r *leasedRow) Scan(dest ...any) error {
	defer r.unit.close()
	defer r.conn.Release()
	return r.Row.Scan(dest...)
}

func (r *Runtime) QueryRow(parent context.Context, sql string, args ...any) pgx.Row {
	l, err := r.admit(parent)
	if err != nil {
		return errorRow{err}
	}
	conn, err := r.acquire(l)
	if err != nil {
		l.close()
		return errorRow{err}
	}
	return &leasedRow{Row: conn.QueryRow(l.ctx, sql, args...), conn: conn, unit: l}
}

type leasedRows struct {
	pgx.Rows
	conn *pgxpool.Conn
	unit *lease
	once sync.Once
}

func (r *leasedRows) Close() {
	r.once.Do(func() {
		r.Rows.Close()
		r.conn.Release()
		r.unit.close()
	})
}

func (r *leasedRows) Next() bool {
	if !r.Rows.Next() {
		r.Close()
		return false
	}
	return true
}

func (r *Runtime) Query(parent context.Context, sql string, args ...any) (pgx.Rows, error) {
	l, err := r.admit(parent)
	if err != nil {
		return nil, err
	}
	conn, err := r.acquire(l)
	if err != nil {
		l.close()
		return nil, err
	}
	rows, err := conn.Query(l.ctx, sql, args...)
	if err != nil {
		conn.Release()
		l.close()
		return nil, err
	}
	return &leasedRows{Rows: rows, conn: conn, unit: l}, nil
}

type leasedTx struct {
	pgx.Tx
	conn *pgxpool.Conn
	unit *lease
	once sync.Once
}

func (t *leasedTx) close() {
	t.once.Do(func() {
		t.conn.Release()
		t.unit.close()
	})
}

func (t *leasedTx) Commit(_ context.Context) error {
	defer t.close()
	return t.Tx.Commit(t.unit.ctx)
}

func (t *leasedTx) Rollback(ctx context.Context) error {
	defer t.close()
	return t.Tx.Rollback(ctx)
}

func (t *leasedTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.Tx.Exec(t.unit.ctx, sql, args...)
}

func (t *leasedTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.Tx.Query(t.unit.ctx, sql, args...)
}

func (t *leasedTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	return t.Tx.QueryRow(t.unit.ctx, sql, args...)
}

func (r *Runtime) Begin(parent context.Context) (pgx.Tx, error) {
	return r.BeginTx(parent, pgx.TxOptions{})
}

func (r *Runtime) BeginTx(parent context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	l, err := r.admit(parent)
	if err != nil {
		return nil, err
	}
	conn, err := r.acquire(l)
	if err != nil {
		l.close()
		return nil, err
	}
	tx, err := conn.BeginTx(l.ctx, opts)
	if err != nil {
		conn.Release()
		l.close()
		return nil, err
	}
	return &leasedTx{Tx: tx, conn: conn, unit: l}, nil
}
