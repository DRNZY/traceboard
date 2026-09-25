package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"traceboard/internal/event"
)

type Source struct {
	Name            string            `json:"name"`
	SourceVersion   string            `json:"source_version"`
	CaptureMode     event.CaptureMode `json:"capture_mode"`
	Connected       bool              `json:"connected"`
	LastHeartbeatAt *time.Time        `json:"last_heartbeat_at,omitempty"`
	RunCount        int               `json:"run_count"`
	QuarantineCount int               `json:"quarantine_count"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type Project struct {
	ID        string    `json:"id"`
	Name      *string   `json:"name"`
	Path      *string   `json:"path"`
	RunCount  int       `json:"run_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type QuarantineEntry struct {
	ID            int64     `json:"id"`
	Source        *string   `json:"source"`
	SourceEventID *string   `json:"source_event_id"`
	Payload       string    `json:"payload"`
	Reason        string    `json:"reason"`
	CreatedAt     time.Time `json:"created_at"`
}

func (store *Store) ListSources(ctx context.Context) ([]Source, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT s.name, s.source_version, s.capture_mode, s.connected, s.last_heartbeat_at,
		       s.created_at, s.updated_at,
		       (SELECT COUNT(*) FROM runs r WHERE r.source = s.name),
		       (SELECT COUNT(*) FROM quarantine q WHERE q.source = s.name)
		FROM sources s
		ORDER BY s.name`)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer rows.Close()

	sources := make([]Source, 0, 8)
	for rows.Next() {
		var source Source
		var captureMode string
		var connected int
		var lastHeartbeatAt sql.NullInt64
		var createdAt int64
		var updatedAt int64
		if err := rows.Scan(&source.Name, &source.SourceVersion, &captureMode, &connected, &lastHeartbeatAt, &createdAt, &updatedAt, &source.RunCount, &source.QuarantineCount); err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		source.CaptureMode = event.CaptureMode(captureMode)
		source.Connected = connected == 1
		source.LastHeartbeatAt = timePointer(lastHeartbeatAt)
		source.CreatedAt = time.Unix(0, createdAt).UTC()
		source.UpdatedAt = time.Unix(0, updatedAt).UTC()
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sources: %w", err)
	}
	return sources, nil
}

func (store *Store) EnsureSource(ctx context.Context, name string, mode event.CaptureMode) error {
	now := time.Now().UTC().UnixNano()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO sources(name, capture_mode, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO NOTHING`, name, mode, now, now); err != nil {
		return fmt.Errorf("ensure source: %w", err)
	}
	return nil
}

func (store *Store) SetSourceCaptureMode(ctx context.Context, name string, mode event.CaptureMode) error {
	switch mode {
	case event.CaptureOff, event.CaptureMetadata, event.CaptureDetailed:
	default:
		return fmt.Errorf("invalid capture mode %q", mode)
	}
	now := time.Now().UTC().UnixNano()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO sources(name, capture_mode, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET capture_mode = excluded.capture_mode, updated_at = excluded.updated_at`,
		name, mode, now, now); err != nil {
		return fmt.Errorf("set source capture mode: %w", err)
	}
	return nil
}

func (store *Store) RecordHeartbeat(ctx context.Context, name string, at time.Time) error {
	now := at.UTC().UnixNano()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO sources(name, capture_mode, connected, last_heartbeat_at, created_at, updated_at)
		VALUES (?, ?, 1, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			connected = 1,
			last_heartbeat_at = excluded.last_heartbeat_at,
			updated_at = excluded.updated_at`,
		name, event.CaptureMetadata, now, now, now); err != nil {
		return fmt.Errorf("record heartbeat: %w", err)
	}
	return nil
}

func (store *Store) SetSourceConnected(ctx context.Context, name string, connected bool) error {
	value := 0
	if connected {
		value = 1
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO sources(name, capture_mode, connected, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET connected = excluded.connected, updated_at = excluded.updated_at`,
		name, event.CaptureMetadata, value, time.Now().UTC().UnixNano(), time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("set source connection: %w", err)
	}
	return nil
}

func (store *Store) UpsertProject(ctx context.Context, id string, name, path *string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("project id is required")
	}
	now := time.Now().UTC().UnixNano()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO projects(id, name, path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = COALESCE(excluded.name, projects.name),
			path = COALESCE(excluded.path, projects.path),
			updated_at = excluded.updated_at`,
		id, nullableStringValue(name), nullableStringValue(path), now, now); err != nil {
		return fmt.Errorf("upsert project: %w", err)
	}
	return nil
}

func (store *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT p.id, p.name, p.path, p.created_at, p.updated_at,
		       (SELECT COUNT(*) FROM runs r WHERE r.project_id = p.id)
		FROM projects p
		ORDER BY p.id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	projects := make([]Project, 0, 8)
	for rows.Next() {
		var project Project
		var name sql.NullString
		var path sql.NullString
		var createdAt int64
		var updatedAt int64
		if err := rows.Scan(&project.ID, &name, &path, &createdAt, &updatedAt, &project.RunCount); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		project.Name = stringPointer(name)
		project.Path = stringPointer(path)
		project.CreatedAt = time.Unix(0, createdAt).UTC()
		project.UpdatedAt = time.Unix(0, updatedAt).UTC()
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	return projects, nil
}

func (store *Store) ListQuarantine(ctx context.Context, limit int) ([]QuarantineEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT id, source, source_event_id, payload, reason, created_at
		FROM quarantine ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list quarantine: %w", err)
	}
	defer rows.Close()

	entries := make([]QuarantineEntry, 0, limit)
	for rows.Next() {
		var entry QuarantineEntry
		var source sql.NullString
		var sourceEventID sql.NullString
		var createdAt int64
		if err := rows.Scan(&entry.ID, &source, &sourceEventID, &entry.Payload, &entry.Reason, &createdAt); err != nil {
			return nil, fmt.Errorf("scan quarantine entry: %w", err)
		}
		entry.Source = stringPointer(source)
		entry.SourceEventID = stringPointer(sourceEventID)
		entry.CreatedAt = time.Unix(0, createdAt).UTC()
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quarantine: %w", err)
	}
	return entries, nil
}

func (store *Store) QuarantineCount(ctx context.Context) (int, error) {
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM quarantine").Scan(&count); err != nil {
		return 0, fmt.Errorf("count quarantine: %w", err)
	}
	return count, nil
}

func (store *Store) ClearQuarantine(ctx context.Context) (int64, error) {
	result, err := store.db.ExecContext(ctx, "DELETE FROM quarantine")
	if err != nil {
		return 0, fmt.Errorf("clear quarantine: %w", err)
	}
	return result.RowsAffected()
}

type Session struct {
	ID        string
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (store *Store) CreateSession(ctx context.Context, id, token string, expiresAt time.Time) error {
	now := time.Now().UTC()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO sessions(id, token_hash, created_at, expires_at)
		VALUES (?, ?, ?, ?)`,
		id, hashToken(token), now.UnixNano(), expiresAt.UTC().UnixNano()); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (store *Store) ConsumeSession(ctx context.Context, token string, at time.Time) (string, error) {
	var id string
	var storedHash string
	err := store.db.QueryRowContext(ctx, `
		SELECT id, token_hash FROM sessions
		WHERE token_hash = ? AND consumed_at IS NULL AND expires_at > ?`,
		hashToken(token), at.UTC().UnixNano()).Scan(&id, &storedHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrSessionNotFound
		}
		return "", fmt.Errorf("load session: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hashToken(token))) != 1 {
		return "", ErrSessionNotFound
	}
	result, err := store.db.ExecContext(ctx, `
		UPDATE sessions SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`, at.UTC().UnixNano(), id)
	if err != nil {
		return "", fmt.Errorf("consume session: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return "", ErrSessionNotFound
	}
	return id, nil
}

func (store *Store) SessionValid(ctx context.Context, id string, at time.Time) error {
	var expiresAt int64
	var consumedAt sql.NullInt64
	err := store.db.QueryRowContext(ctx, `SELECT expires_at, consumed_at FROM sessions WHERE id = ?`, id).
		Scan(&expiresAt, &consumedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("load session: %w", err)
	}
	if consumedAt.Valid {
		return ErrSessionNotFound
	}
	if time.Unix(0, expiresAt).UTC().Before(at.UTC()) {
		return ErrSessionNotFound
	}
	return nil
}

func (store *Store) DeleteSession(ctx context.Context, id string) error {
	if _, err := store.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (store *Store) DeleteAllSessions(ctx context.Context) error {
	if _, err := store.db.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}
	return nil
}

func (store *Store) CountSessions(ctx context.Context) (int, error) {
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE consumed_at IS NULL AND expires_at > ?", time.Now().UTC().UnixNano()).Scan(&count); err != nil {
		return 0, fmt.Errorf("count sessions: %w", err)
	}
	return count, nil
}

func (store *Store) DeleteExpiredSessions(ctx context.Context, at time.Time) (int64, error) {
	result, err := store.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", at.UTC().UnixNano())
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return result.RowsAffected()
}

var ErrSessionNotFound = errors.New("session not found")

// HashToken returns the storage form of bearer material. Only hashes are
// persisted, so a database copy never reveals a usable token.
func HashToken(token string) string {
	return hashToken(token)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func RandomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func nullableStringValue(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

func joinConditions(conditions []string) string {
	if len(conditions) == 0 {
		return "1 = 1"
	}
	result := conditions[0]
	for _, condition := range conditions[1:] {
		result += " AND " + condition
	}
	return result
}

// GetSource reads one source, including the fields the list endpoint reports.
func (store *Store) GetSource(ctx context.Context, name string) (Source, error) {
	sources, err := store.ListSources(ctx)
	if err != nil {
		return Source{}, err
	}
	for _, source := range sources {
		if source.Name == name {
			return source, nil
		}
	}
	return Source{}, ErrSourceNotFound
}

var ErrSourceNotFound = errors.New("source not found")
