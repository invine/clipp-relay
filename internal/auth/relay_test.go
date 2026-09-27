package auth_test

import (
	"context"
	"errors"
	"testing"

	"clipp-relay/internal/auth"
)

func TestRelayCredentialSeparatesDatabaseOutageFromInvalidToken(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "relay-credential", "relay-credential@example.test", "")
	var account string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject='relay-credential'`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	code := authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect)
	token, _ := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, code)
	credential, err := s.AuthenticateRelay(context.Background(), token)
	if err != nil || credential.AccountID != account || credential.SessionLimit != 5 {
		t.Fatalf("active credential: %+v %v", credential, err)
	}
	if _, err = s.AuthenticateRelay(context.Background(), "invalid"); !errors.Is(err, auth.ErrInvalidAccess) {
		t.Fatalf("invalid token: %v", err)
	}
	const zeroPlan = "932eab06-1d9f-4ac4-a60c-6c91c43579ba"
	if _, err = db.Pool.Exec(context.Background(), `INSERT INTO public.quota_plans(id,name,weekly_bytes,sessions) VALUES($1,'No relay quota',0,0)`, zeroPlan); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(context.Background(), `UPDATE public.accounts SET plan_id=$1 WHERE id=$2`, zeroPlan, account); err != nil {
		t.Fatal(err)
	}
	zero, err := s.AuthenticateRelay(context.Background(), token)
	if err != nil || zero.SessionLimit != 0 {
		t.Fatalf("zero-quota account was denied discovery: %+v %v", zero, err)
	}
	db.Pool.Close()
	if _, err = s.AuthenticateRelay(context.Background(), token); err == nil || errors.Is(err, auth.ErrInvalidAccess) {
		t.Fatalf("database outage was mistaken for invalid token: %v", err)
	}
}
