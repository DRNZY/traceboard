package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"traceboard/internal/store"
)

func TestExchangeDashboardTokenIsOneTime(t *testing.T) {
	manager, ctx := newTestManager(t)

	token, err := manager.IssueSignInToken(ctx)
	if err != nil {
		t.Fatalf("issue sign-in token: %v", err)
	}
	sessionToken, err := manager.ExchangeDashboardToken(ctx, token)
	if err != nil {
		t.Fatalf("exchange sign-in token: %v", err)
	}
	if err := manager.ValidateSession(ctx, sessionToken); err != nil {
		t.Fatalf("validate session: %v", err)
	}
	if _, err := manager.ExchangeDashboardToken(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("replayed sign-in token = %v, want ErrUnauthorized", err)
	}
}

func TestExchangeRejectsWrongToken(t *testing.T) {
	manager, ctx := newTestManager(t)
	if _, err := manager.IssueSignInToken(ctx); err != nil {
		t.Fatalf("issue sign-in token: %v", err)
	}
	if _, err := manager.ExchangeDashboardToken(ctx, "not-the-token"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token = %v, want ErrUnauthorized", err)
	}
}

func TestSessionExpires(t *testing.T) {
	manager, ctx := newTestManager(t)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	manager.SetClock(func() time.Time { return now })

	token, err := manager.IssueSignInToken(ctx)
	if err != nil {
		t.Fatalf("issue sign-in token: %v", err)
	}
	sessionToken, err := manager.ExchangeDashboardToken(ctx, token)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if err := manager.ValidateSession(ctx, sessionToken); err != nil {
		t.Fatalf("validate before expiry: %v", err)
	}
	manager.SetClock(func() time.Time { return now.Add(SessionTTL + time.Minute) })
	if err := manager.ValidateSession(ctx, sessionToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session = %v, want ErrUnauthorized", err)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	manager, ctx := newTestManager(t)
	token, _ := manager.IssueSignInToken(ctx)
	sessionToken, err := manager.ExchangeDashboardToken(ctx, token)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if err := manager.Logout(ctx, sessionToken); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if err := manager.ValidateSession(ctx, sessionToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("logged-out session = %v, want ErrUnauthorized", err)
	}
}

func TestRotateInvalidatesSessionsAndKeepsIngestToken(t *testing.T) {
	manager, ctx := newTestManager(t)
	token, _ := manager.IssueSignInToken(ctx)
	sessionToken, _ := manager.ExchangeDashboardToken(ctx, token)

	rotated, err := manager.RotateDashboard(ctx)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated == token {
		t.Fatal("rotation reused the previous sign-in token")
	}
	if err := manager.ValidateSession(ctx, sessionToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("session survived rotation: %v", err)
	}
	if err := manager.ValidateIngestToken("ingest-secret"); err != nil {
		t.Fatalf("rotation must not change the ingest token: %v", err)
	}
}

func TestValidateIngestTokenRejectsWrongAndEmptyValues(t *testing.T) {
	manager, _ := newTestManager(t)
	if err := manager.ValidateIngestToken("ingest-secret"); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	for _, presented := range []string{"", "ingest-secre", "ingest-secret ", "INGEST-SECRET"} {
		if err := manager.ValidateIngestToken(presented); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("token %q = %v, want ErrUnauthorized", presented, err)
		}
	}
}

// authTestDatabasePath exposes the on-disk database of a freshly built manager
// so the test can prove only hashes were written.
func authTestDatabasePath(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure temp directory: %v", err)
	}
	path := filepath.Join(directory, "traceboard.db")
	database, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	manager, err := New(database, "ingest-secret")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if _, err := manager.IssueSignInToken(context.Background()); err != nil {
		t.Fatalf("issue sign-in token: %v", err)
	}
	return path
}

func TestAuthManagerNeverPersistsPlaintextTokens(t *testing.T) {
	contents, err := os.ReadFile(authTestDatabasePath(t))
	if err != nil {
		t.Fatalf("read database: %v", err)
	}
	if strings.Contains(string(contents), "ingest-secret") {
		t.Fatal("the ingest token was persisted in the clear")
	}
}

func TestSignInURLIsNotLoggedInPlainForm(t *testing.T) {
	url := SignInURL("http://127.0.0.1:47821", "abc")
	if url != "http://127.0.0.1:47821/auth/signin?token=abc" {
		t.Fatalf("sign-in URL = %q", url)
	}
}

func TestPurgeExpiredRemovesOldSessions(t *testing.T) {
	manager, ctx := newTestManager(t)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	manager.SetClock(func() time.Time { return now })
	if _, err := manager.IssueSignInToken(ctx); err != nil {
		t.Fatalf("issue: %v", err)
	}
	manager.SetClock(func() time.Time { return now.Add(SignInTokenTTL + time.Hour) })
	removed, err := manager.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if removed == 0 {
		t.Fatal("expected expired sessions to be removed")
	}
}

func TestWriteSignInURLFileIsUserOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signin-url")
	if err := WriteSignInURLFile(path, "http://127.0.0.1:47821", "token"); err != nil {
		t.Fatalf("write sign-in url: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %04o, want 0600", info.Mode().Perm())
	}
}

func newTestManager(t *testing.T) (*Manager, context.Context) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure temp directory: %v", err)
	}
	path := filepath.Join(directory, "traceboard.db")
	database, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	manager, err := New(database, "ingest-secret")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return manager, context.Background()
}
