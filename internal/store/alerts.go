package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Alert struct {
	ID             string     `json:"id"`
	Type           string     `json:"type"`
	RunID          *string    `json:"run_id"`
	Source         *string    `json:"source"`
	Message        string     `json:"message"`
	CreatedAt      time.Time  `json:"created_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	State          string     `json:"state"`
}

type AlertFilter struct {
	RunID   string
	State   string
	Limit   int
	SinceID int64
}

var ErrAlertNotFound = errors.New("alert not found")

// OpenAlert creates at most one open alert per (type, run, source) key and
// reports whether this call created it. Evaluation is idempotent, so a repeated
// pass returns the existing alert and created=false.
func (store *Store) OpenAlert(ctx context.Context, alert Alert) (Alert, bool, error) {
	now := time.Now().UTC()
	if alert.ID == "" {
		alert.ID = alertStableID(alert)
	}
	if alert.CreatedAt.IsZero() {
		alert.CreatedAt = now
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO alerts(id, run_id, source, type, message, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(type, COALESCE(run_id, ''), COALESCE(source, '')) WHERE resolved_at IS NULL
		DO NOTHING`,
		alert.ID, nullableString(derefOrEmpty(alert.RunID)), nullableString(derefOrEmpty(alert.Source)),
		alert.Type, alert.Message, alert.CreatedAt.UnixNano(),
	); err != nil {
		return Alert{}, false, fmt.Errorf("open alert: %w", err)
	}
	existing, err := store.GetAlertByKey(ctx, alert.Type, alert.RunID, alert.Source)
	if err != nil {
		return Alert{}, false, err
	}
	return existing, existing.ID == alert.ID && existing.CreatedAt.Equal(alert.CreatedAt), nil
}

func (store *Store) GetAlertByKey(ctx context.Context, alertType string, runID, source *string) (Alert, error) {
	row := store.db.QueryRowContext(ctx, `
		SELECT id, type, run_id, source, message, created_at, acknowledged_at, resolved_at
		FROM alerts
		WHERE type = ? AND COALESCE(run_id, '') = ? AND COALESCE(source, '') = ? AND resolved_at IS NULL
		ORDER BY created_at LIMIT 1`,
		alertType, derefOrEmpty(runID), derefOrEmpty(source))
	return scanAlert(row)
}

func (store *Store) GetAlert(ctx context.Context, alertID string) (Alert, error) {
	row := store.db.QueryRowContext(ctx, `
		SELECT id, type, run_id, source, message, created_at, acknowledged_at, resolved_at
		FROM alerts WHERE id = ?`, alertID)
	return scanAlert(row)
}

func (store *Store) ListAlerts(ctx context.Context, filter AlertFilter) ([]Alert, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	conditions := []string{"1 = 1"}
	arguments := make([]any, 0, 4)
	if filter.RunID != "" {
		conditions = append(conditions, "run_id = ?")
		arguments = append(arguments, filter.RunID)
	}
	switch filter.State {
	case "open":
		conditions = append(conditions, "resolved_at IS NULL AND acknowledged_at IS NULL")
	case "acknowledged":
		conditions = append(conditions, "resolved_at IS NULL AND acknowledged_at IS NOT NULL")
	case "resolved":
		conditions = append(conditions, "resolved_at IS NOT NULL")
	}
	query := `
		SELECT id, type, run_id, source, message, created_at, acknowledged_at, resolved_at
		FROM alerts
		WHERE ` + joinConditions(conditions) + `
		ORDER BY created_at DESC, id
		LIMIT ?`
	arguments = append(arguments, limit)

	rows, err := store.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()

	alerts := make([]Alert, 0, limit)
	for rows.Next() {
		alert, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alerts: %w", err)
	}
	return alerts, nil
}

func (store *Store) AcknowledgeAlert(ctx context.Context, alertID string) (Alert, error) {
	result, err := store.db.ExecContext(ctx, `
		UPDATE alerts SET acknowledged_at = ?
		WHERE id = ? AND acknowledged_at IS NULL AND resolved_at IS NULL`,
		time.Now().UTC().UnixNano(), alertID)
	if err != nil {
		return Alert{}, fmt.Errorf("acknowledge alert: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		if _, err := store.GetAlert(ctx, alertID); err != nil {
			return Alert{}, err
		}
	}
	return store.GetAlert(ctx, alertID)
}

// ResolveAlerts clears open alerts. An empty alertType, runID, or source is a
// wildcard, which is what resolving every alert on a finished run needs.
func (store *Store) ResolveAlerts(ctx context.Context, alertType string, runID, source *string, at time.Time) (int64, error) {
	conditions := []string{"resolved_at IS NULL"}
	arguments := []any{at.UTC().UnixNano()}
	if alertType != "" {
		conditions = append(conditions, "type = ?")
		arguments = append(arguments, alertType)
	}
	if runID != nil {
		conditions = append(conditions, "COALESCE(run_id, '') = ?")
		arguments = append(arguments, *runID)
	}
	if source != nil {
		conditions = append(conditions, "COALESCE(source, '') = ?")
		arguments = append(arguments, *source)
	}
	arguments = append(arguments, at.UTC().UnixNano())
	result, err := store.db.ExecContext(ctx,
		"UPDATE alerts SET resolved_at = ? WHERE "+strings.Join(conditions, " AND "),
		arguments...)
	if err != nil {
		return 0, fmt.Errorf("resolve alerts: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("resolve alerts count: %w", err)
	}
	return affected, nil
}

func (store *Store) ResolveAlertsForRun(ctx context.Context, runID string, at time.Time) (int64, error) {
	return store.ResolveAlerts(ctx, "", &runID, nil, at)
}

func scanAlert(scanner rowScanner) (Alert, error) {
	var alert Alert
	var runID sql.NullString
	var source sql.NullString
	var createdAt int64
	var acknowledgedAt sql.NullInt64
	var resolvedAt sql.NullInt64
	if err := scanner.Scan(&alert.ID, &alert.Type, &runID, &source, &alert.Message, &createdAt, &acknowledgedAt, &resolvedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Alert{}, ErrAlertNotFound
		}
		return Alert{}, fmt.Errorf("scan alert: %w", err)
	}
	alert.RunID = stringPointer(runID)
	alert.Source = stringPointer(source)
	alert.CreatedAt = time.Unix(0, createdAt).UTC()
	alert.AcknowledgedAt = timePointer(acknowledgedAt)
	alert.ResolvedAt = timePointer(resolvedAt)
	switch {
	case alert.ResolvedAt != nil:
		alert.State = "resolved"
	case alert.AcknowledgedAt != nil:
		alert.State = "acknowledged"
	default:
		alert.State = "open"
	}
	return alert, nil
}

func alertStableID(alert Alert) string {
	return fmt.Sprintf("%s:%s:%s", alert.Type, derefOrEmpty(alert.RunID), derefOrEmpty(alert.Source))
}

func derefOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
