package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"traceboard/internal/store"
)

const (
	// SessionTTL bounds how long a signed-in dashboard session stays valid.
	SessionTTL = 12 * time.Hour
	// SignInTokenTTL bounds how long a printed sign-in URL stays usable.
	SignInTokenTTL = 15 * time.Minute
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrTokenUsed    = errors.New("sign-in token has already been used")
)

const SessionCookieName = "traceboard_session"

// Manager owns the one-time dashboard sign-in token, the ingest bearer token,
// and the set of live dashboard sessions. Only hashes of bearer material reach
// the store, and no method ever returns a stored secret in an error.
type Manager struct {
	store *store.Store

	mu             sync.RWMutex
	ingestHash     string
	sessionSecrets map[string]string
	now            func() time.Time
}

func New(database *store.Store, ingestToken string) (*Manager, error) {
	if strings.TrimSpace(ingestToken) == "" {
		return nil, errors.New("ingest token is required")
	}
	return &Manager{
		store:          database,
		ingestHash:     hash(ingestToken),
		sessionSecrets: make(map[string]string),
		now:            func() time.Time { return time.Now().UTC() },
	}, nil
}

func (manager *Manager) SetClock(now func() time.Time) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	manager.now = now
}

func (manager *Manager) clock() time.Time {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.now()
}

// IssueSignInToken creates a fresh one-time dashboard token and stores only its
// hash. The plaintext is returned exactly once so the caller can print a URL.
func (manager *Manager) IssueSignInToken(ctx context.Context) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", fmt.Errorf("generate sign-in token: %w", err)
	}
	if err := manager.store.CreateSession(ctx, "signin:"+hash(token), token, manager.clock().Add(SignInTokenTTL)); err != nil {
		return "", err
	}
	return token, nil
}

// ExchangeDashboardToken trades the one-time sign-in token for a session token.
// The sign-in token is invalidated in the same store call so a replayed URL
// cannot mint a second session.
func (manager *Manager) ExchangeDashboardToken(ctx context.Context, token string) (string, error) {
	now := manager.clock()
	sessionToken, err := randomToken()
	if err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	sessionID := "session:" + hash(sessionToken)
	if err := manager.store.CreateSession(ctx, sessionID, sessionToken, now.Add(SessionTTL)); err != nil {
		return "", err
	}
	if _, err := manager.store.ConsumeSession(ctx, token, now); err != nil {
		_ = manager.store.DeleteSession(ctx, sessionID)
		if errors.Is(err, store.ErrSessionNotFound) {
			return "", ErrUnauthorized
		}
		return "", err
	}
	manager.mu.Lock()
	manager.sessionSecrets[sessionID] = sessionToken
	manager.mu.Unlock()
	return sessionToken, nil
}

func (manager *Manager) ValidateSession(ctx context.Context, sessionToken string) error {
	if strings.TrimSpace(sessionToken) == "" {
		return ErrUnauthorized
	}
	sessionID := "session:" + hash(sessionToken)
	if err := manager.store.SessionValid(ctx, sessionID, manager.clock()); err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return ErrUnauthorized
		}
		return err
	}
	return nil
}

func (manager *Manager) Logout(ctx context.Context, sessionToken string) error {
	if strings.TrimSpace(sessionToken) == "" {
		return nil
	}
	manager.mu.Lock()
	delete(manager.sessionSecrets, "session:"+hash(sessionToken))
	manager.mu.Unlock()
	return manager.store.DeleteSession(ctx, "session:"+hash(sessionToken))
}

// RotateDashboard invalidates every session and issues a new one-time sign-in
// token. The ingest token is deliberately left untouched.
func (manager *Manager) RotateDashboard(ctx context.Context) (string, error) {
	if err := manager.store.DeleteAllSessions(ctx); err != nil {
		return "", err
	}
	manager.mu.Lock()
	manager.sessionSecrets = make(map[string]string)
	manager.mu.Unlock()
	return manager.IssueSignInToken(ctx)
}

func (manager *Manager) ValidateIngestToken(presented string) error {
	manager.mu.RLock()
	expected := manager.ingestHash
	manager.mu.RUnlock()
	if presented == "" {
		return ErrUnauthorized
	}
	if subtle.ConstantTimeCompare([]byte(expected), []byte(hash(presented))) != 1 {
		return ErrUnauthorized
	}
	return nil
}

func (manager *Manager) PurgeExpired(ctx context.Context) (int64, error) {
	return manager.store.DeleteExpiredSessions(ctx, manager.clock())
}

func (manager *Manager) CountSessions() int {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return len(manager.sessionSecrets)
}

func hash(value string) string {
	return store.HashToken(value)
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// WriteSignInURLFile stores a one-time sign-in URL so a browser can open the
// dashboard even when the operator started the server without a terminal.
func WriteSignInURLFile(path, baseURL, token string) error {
	payload := map[string]string{"url": baseURL + "/auth/signin?token=" + token}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o600)
}

func SignInURL(baseURL, token string) string {
	return strings.TrimRight(baseURL, "/") + "/auth/signin?token=" + token
}

func DataDirectory() (string, error) {
	if override := strings.TrimSpace(os.Getenv("TRACEBOARD_CONFIG")); override != "" {
		return filepath.Dir(override), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "traceboard"), nil
}
