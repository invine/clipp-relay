package database

import (
	"context"
	"errors"
	"sync"
	"time"

	"clipp-relay/internal/diagnostic"
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
	Pool        *pgxpool.Pool
	admission   chan struct{}
	transitions *diagnostic.Transitions
}

func (r *Runtime) SetTransitions(t *diagnostic.Transitions) { r.transitions = t }
func (r *Runtime) observe(ctx context.Context, started time.Time, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	r.transitions.Dependency("dependency.database", err, ctx, time.Since(started))
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
	started := time.Now()
	l, err := r.admit(parent)
	if err != nil {
		r.observe(parent, started, err)
		return nil, nil, err
	}
	return l.ctx, l.close, nil
}

// WithUnit scopes admission to work and reports a deadline reached in work.
func (r *Runtime) WithUnit(parent context.Context, work func(context.Context) error) (result error) {
	started := time.Now()
	defer func() { r.observe(parent, started, result) }()
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
	conn    *pgxpool.Conn
	unit    *lease
	observe func(error)
}

func (r *leasedRow) Scan(dest ...any) (err error) {
	defer r.unit.close()
	defer r.conn.Release()
	err = r.Row.Scan(dest...)
	r.observe(err)
	return err
}

func (r *Runtime) QueryRow(parent context.Context, sql string, args ...any) pgx.Row {
	started := time.Now()
	l, err := r.admit(parent)
	if err != nil {
		r.observe(parent, started, err)
		return errorRow{err}
	}
	conn, err := r.acquire(l)
	if err != nil {
		l.close()
		r.observe(parent, started, err)
		return errorRow{err}
	}
	return &leasedRow{Row: conn.QueryRow(l.ctx, sql, args...), conn: conn, unit: l, observe: func(err error) { r.observe(parent, started, err) }}
}

type leasedRows struct {
	pgx.Rows
	conn    *pgxpool.Conn
	unit    *lease
	once    sync.Once
	observe func(error)
}

func (r *leasedRows) Close() {
	r.once.Do(func() {
		r.Rows.Close()
		r.observe(r.Rows.Err())
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
	started := time.Now()
	l, err := r.admit(parent)
	if err != nil {
		r.observe(parent, started, err)
		return nil, err
	}
	conn, err := r.acquire(l)
	if err != nil {
		l.close()
		r.observe(parent, started, err)
		return nil, err
	}
	rows, err := conn.Query(l.ctx, sql, args...)
	if err != nil {
		conn.Release()
		l.close()
		r.observe(parent, started, err)
		return nil, err
	}
	return &leasedRows{Rows: rows, conn: conn, unit: l, observe: func(err error) { r.observe(parent, started, err) }}, nil
}

type leasedTx struct {
	pgx.Tx
	conn    *pgxpool.Conn
	unit    *lease
	once    sync.Once
	observe func(context.Context, error)
}

// A transaction must obey both the unit deadline and the context supplied for
// this particular statement. A later statement may have a narrower deadline
// or be canceled independently of the transaction's original parent.
func transactionCallContext(unit, caller context.Context) (context.Context, func()) {
	var ctx context.Context
	var cancel context.CancelFunc
	if deadline, ok := caller.Deadline(); ok {
		ctx, cancel = context.WithDeadline(unit, deadline)
	} else {
		ctx, cancel = context.WithCancel(unit)
	}
	stop := context.AfterFunc(caller, cancel)
	if caller.Err() != nil {
		cancel()
	}
	return ctx, func() {
		stop()
		cancel()
	}
}

type boundedRow struct {
	pgx.Row
	finish  func()
	once    sync.Once
	observe func(error)
}

func (r *boundedRow) Scan(dest ...any) error {
	defer r.once.Do(r.finish)
	err := r.Row.Scan(dest...)
	r.observe(err)
	return err
}

type boundedRows struct {
	pgx.Rows
	finish  func()
	once    sync.Once
	observe func(error)
}

func (r *boundedRows) Close() {
	r.once.Do(func() {
		r.Rows.Close()
		r.observe(r.Rows.Err())
		r.finish()
	})
}

func (r *boundedRows) Next() bool {
	if !r.Rows.Next() {
		r.Close()
		return false
	}
	return true
}

func (t *leasedTx) close() {
	t.once.Do(func() {
		t.conn.Release()
		t.unit.close()
	})
}

func (t *leasedTx) Commit(ctx context.Context) error {
	defer t.close()
	bounded, finish := transactionCallContext(t.unit.ctx, ctx)
	defer finish()
	err := t.Tx.Commit(bounded)
	t.observe(ctx, err)
	return err
}

func (t *leasedTx) Rollback(ctx context.Context) error {
	defer t.close()
	bounded, finish := transactionCallContext(t.unit.ctx, ctx)
	defer finish()
	// A deadline may prevent rollback; releasing a non-idle pgxpool connection
	// destroys it rather than treating cancellation as rollback proof.
	return t.Tx.Rollback(bounded)
}

func (t *leasedTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	bounded, finish := transactionCallContext(t.unit.ctx, ctx)
	defer finish()
	tag, err := t.Tx.Exec(bounded, sql, args...)
	t.observe(ctx, err)
	return tag, err
}

func (t *leasedTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	bounded, finish := transactionCallContext(t.unit.ctx, ctx)
	rows, err := t.Tx.Query(bounded, sql, args...)
	if err != nil {
		t.observe(ctx, err)
		finish()
		return nil, err
	}
	return &boundedRows{Rows: rows, finish: finish, observe: func(err error) { t.observe(ctx, err) }}, nil
}

func (t *leasedTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	bounded, finish := transactionCallContext(t.unit.ctx, ctx)
	return &boundedRow{Row: t.Tx.QueryRow(bounded, sql, args...), finish: finish, observe: func(err error) { t.observe(ctx, err) }}
}

func (r *Runtime) Begin(parent context.Context) (pgx.Tx, error) {
	return r.BeginTx(parent, pgx.TxOptions{})
}

func (r *Runtime) BeginTx(parent context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	started := time.Now()
	l, err := r.admit(parent)
	if err != nil {
		r.observe(parent, started, err)
		return nil, err
	}
	conn, err := r.acquire(l)
	if err != nil {
		l.close()
		r.observe(parent, started, err)
		return nil, err
	}
	tx, err := conn.BeginTx(l.ctx, opts)
	if err != nil {
		conn.Release()
		l.close()
		r.observe(parent, started, err)
		return nil, err
	}
	r.observe(parent, started, nil)
	return &leasedTx{Tx: tx, conn: conn, unit: l, observe: func(ctx context.Context, err error) { r.observe(ctx, started, err) }}, nil
}
