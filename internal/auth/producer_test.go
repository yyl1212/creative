package auth

import (
	"errors"
	"github.com/yyl1212/creative/internal/store"
	"path/filepath"
	"strings"
	"testing"
)

func TestProducerCredentialsAndAudit(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	p, secret, err := a.CreateProducer("搜寻执行端", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) < 40 {
		t.Fatal("weak token")
	}
	if got, err := a.AuthenticateProducer(secret); err != nil || got.ID != p.ID {
		t.Fatal("new token rejected", err)
	}
	var digest string
	if err = db.QueryRow("SELECT token_hash FROM producers WHERE id=?", p.ID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(digest, secret) || len(digest) != 64 {
		t.Fatal("token stored without digest")
	}
	next, newSecret, err := a.RotateProducer(p.ID, p.Version, "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.AuthenticateProducer(secret); !errors.Is(err, ErrProducerInvalid) {
		t.Fatal("old token accepted", err)
	}
	if _, _, err = a.RotateProducer(p.ID, p.Version, "owner", "local"); !errors.Is(err, ErrProducerConflict) {
		t.Fatal("stale rotation accepted", err)
	}
	if err = a.RevokeProducer(next.ID, next.Version, "owner", "local"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.AuthenticateProducer(newSecret); !errors.Is(err, ErrProducerInvalid) {
		t.Fatal("revoked token accepted", err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM audit_events WHERE object_id=?", p.ID).Scan(&count); err != nil || count != 3 {
		t.Fatal("missing audit", count, err)
	}
	rows, err := db.Query("SELECT details FROM audit_events")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(s, secret) || strings.Contains(s, newSecret) {
			t.Fatal("secret in audit")
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestProducerMutationRollsBackWithoutAudit(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	p, secret, err := a.CreateProducer("original", "owner", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TRIGGER audit_failure BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.RotateProducer(p.ID, p.Version, "owner", "local"); err == nil {
		t.Fatal("rotation without audit accepted")
	}
	if _, err = a.AuthenticateProducer(secret); err != nil {
		t.Fatal("failed rotation destroyed old credential", err)
	}
}
