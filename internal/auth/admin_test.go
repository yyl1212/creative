package auth

import (
	"errors"
	"github.com/yyl1212/creative/internal/store"
	"path/filepath"
	"testing"
)

func TestLoginFailuresAreThrottled(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.CreateAdmin("owner", "test-only-correct-password"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err = a.Authenticate("owner", "wrong", "source-1"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err = a.Authenticate("owner", "test-only-correct-password", "source-2"); !errors.Is(err, ErrThrottled) {
		t.Fatalf("account limit: %v", err)
	}
	if _, err = a.Authenticate("another", "wrong", "source-1"); !errors.Is(err, ErrThrottled) {
		t.Fatalf("source limit: %v", err)
	}
	if _, err = db.Exec("UPDATE login_limits SET expires=0"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Authenticate("owner", "test-only-correct-password", "source-1"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		a.Authenticate("owner", "wrong", "source-2")
	}
	if err = a.ResetPassword("owner", "test-only-new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Authenticate("owner", "test-only-new-password", "source-3"); err != nil {
		t.Fatalf("private recovery: %v", err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM audit_events").Scan(&count); err != nil || count < 5 {
		t.Fatalf("missing audit: %d %v", count, err)
	}
}
func TestCredentialsValidation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"owner", "short"}, {"bad user", "test-only-password"}} {
		if a.CreateAdmin(pair[0], pair[1]) == nil {
			t.Fatal("invalid credential accepted")
		}
	}
	if err = a.ResetPassword("missing", "test-only-password"); err == nil {
		t.Fatal("missing account recovered")
	}
}
