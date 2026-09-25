package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"traceboard/internal/event"
)

var ErrRunNotFound = errors.New("run not found")

type Run struct {
	ID           string              `json:"id"`
	Source       string              `json:"source"`
	ProjectID    *string             `json:"project_id,omitempty"`
	ProjectName  *string             `json:"project_name,omitempty"`
	Title        *string             `json:"title,omitempty"`
	Status       event.Status        `json:"status"`
	StartedAt    *time.Time          `json:"started_at,omitempty"`
	EndedAt      *time.Time          `json:"ended_at,omitempty"`
	LastEventAt  *time.Time          `json:"last_event_at,omitempty"`
	DurationMS   *int64              `json:"duration_ms,omitempty"`
	EventCount   int                 `json:"event_count"`
	ErrorCount   int                 `json:"error_count"`
	NextSequence int64               `json:"next_sequence"`
	CaptureModes []event.CaptureMode `json:"capture_modes"`
	Models       []string            `json:"models"`
	InputTokens  *int64              `json:"input_tokens,omitempty"`
	OutputTokens *int64              `json:"output_tokens,omitempty"`
	OpenAlerts   int                 `json:"open_alerts"`
	ParentRunID  *string             `json:"parent_run_id,omitempty"`
	SubagentRuns int                 `json:"subagent_runs"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

type RunFilter struct {
	Query         string
	Source        string
	ProjectID     string
	Status        string
	CaptureMode   string
	AlertState    string
	StartedAfter  *time.Time
	StartedBefore *time.Time
	ParentRunID   string
	SubagentOnly  *bool
}

type RunPage struct {
	Runs []Run `json:"runs"`
	// NextCursor is an explicit null rather than an omitted key, so a client
	// never has to distinguish "no more pages" from "field missing".
	NextCursor *string `json:"next_cursor"`
}

type Step struct {
	RunID        string       `json:"run_id"`
	ID           string       `json:"id"`
	ParentStepID *string      `json:"parent_step_id"`
	Type         string       `json:"type"`
	Status       event.Status `json:"status"`
	StartedAt    *time.Time   `json:"started_at"`
	EndedAt      *time.Time   `json:"ended_at"`
	EventCount   int          `json:"event_count"`
}

type DeleteResult struct {
	RunID         string `json:"run_id"`
	Events        int    `json:"events"`
	Steps         int    `json:"steps"`
	Alerts        int    `json:"alerts"`
	SearchEntries int    `json:"search_entries"`
}

type RetentionPolicy struct {
	Days                 int
	KeepNewestPerProject int
}

type runCursor struct {
	SortAt int64  `json:"sort_at"`
	ID     string `json:"id"`
}

const runSelectColumns = `
		r.id, r.source, r.project_id, p.name, r.title, r.status,
		r.started_at, r.ended_at, r.last_event_at, r.event_count, r.error_count,
		r.next_sequence, r.capture_modes, r.parent_run_id, r.created_at, r.updated_at,
		COALESCE((SELECT COUNT(*) FROM alerts a WHERE a.run_id = r.id AND a.resolved_at IS NULL), 0),
		COALESCE((SELECT COUNT(*) FROM runs child WHERE child.parent_run_id = r.id), 0)`

func (store *Store) GetRun(ctx context.Context, runID string) (Run, error) {
	query := `SELECT` + runSelectColumns + `
		FROM runs r
		LEFT JOIN projects p ON p.id = r.project_id
		WHERE r.id = ?`
	run, err := scanRun(store.db.QueryRowContext(ctx, query, runID))
	if err != nil {
		return Run{}, err
	}
	if err := store.loadRunDerived(ctx, &run); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (store *Store) ListRuns(ctx context.Context, filter RunFilter, cursor string, limit int) (RunPage, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	conditions := []string{"1 = 1"}
	arguments := make([]any, 0, 12)

	if filter.Source != "" {
		conditions = append(conditions, "r.source = ?")
		arguments = append(arguments, filter.Source)
	}
	if filter.ProjectID != "" {
		conditions = append(conditions, "r.project_id = ?")
		arguments = append(arguments, filter.ProjectID)
	}
	if filter.Status != "" {
		conditions = append(conditions, "r.status = ?")
		arguments = append(arguments, filter.Status)
	}
	if filter.CaptureMode != "" {
		conditions = append(conditions, `r.capture_modes LIKE ?`)
		arguments = append(arguments, "%\""+filter.CaptureMode+"\"%")
	}
	if filter.StartedAfter != nil {
		conditions = append(conditions, "COALESCE(r.started_at, r.last_event_at, r.created_at) >= ?")
		arguments = append(arguments, filter.StartedAfter.UnixNano())
	}
	if filter.StartedBefore != nil {
		conditions = append(conditions, "COALESCE(r.started_at, r.last_event_at, r.created_at) < ?")
		arguments = append(arguments, filter.StartedBefore.UnixNano())
	}
	if filter.ParentRunID != "" {
		conditions = append(conditions, "r.parent_run_id = ?")
		arguments = append(arguments, filter.ParentRunID)
	}
	if filter.SubagentOnly != nil {
		if *filter.SubagentOnly {
			conditions = append(conditions, "r.parent_run_id IS NOT NULL")
		} else {
			conditions = append(conditions, "r.parent_run_id IS NULL")
		}
	}
	switch filter.AlertState {
	case "open":
		conditions = append(conditions, "EXISTS (SELECT 1 FROM alerts a WHERE a.run_id = r.id AND a.resolved_at IS NULL)")
	case "none":
		conditions = append(conditions, "NOT EXISTS (SELECT 1 FROM alerts a WHERE a.run_id = r.id AND a.resolved_at IS NULL)")
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		conditions = append(conditions, "r.id IN (SELECT run_id FROM events_fts WHERE events_fts MATCH ?)")
		arguments = append(arguments, FTSQuery(query))
	}
	if cursor != "" {
		decoded, err := decodeRunCursor(cursor)
		if err != nil {
			return RunPage{}, err
		}
		conditions = append(conditions, "(COALESCE(r.started_at, r.last_event_at, r.created_at), r.id) < (?, ?)")
		arguments = append(arguments, decoded.SortAt, decoded.ID)
	}

	query := `SELECT` + runSelectColumns + `
		FROM runs r
		LEFT JOIN projects p ON p.id = r.project_id
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY COALESCE(r.started_at, r.last_event_at, r.created_at) DESC, r.id DESC
		LIMIT ?`
	arguments = append(arguments, limit+1)

	rows, err := store.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return RunPage{}, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	page := RunPage{Runs: make([]Run, 0, limit)}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return RunPage{}, err
		}
		page.Runs = append(page.Runs, run)
	}
	if err := rows.Err(); err != nil {
		return RunPage{}, fmt.Errorf("iterate runs: %w", err)
	}
	if len(page.Runs) > limit {
		page.Runs = page.Runs[:limit]
		last := page.Runs[limit-1]
		encoded := encodeRunCursor(runCursor{SortAt: runSortAt(last), ID: last.ID})
		page.NextCursor = &encoded
	}
	for index := range page.Runs {
		if err := store.loadRunDerived(ctx, &page.Runs[index]); err != nil {
			return RunPage{}, err
		}
	}
	return page, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(scanner rowScanner) (Run, error) {
	var run Run
	var projectID sql.NullString
	var projectName sql.NullString
	var title sql.NullString
	var parentRunID sql.NullString
	var status string
	var captureModes string
	var startedAt sql.NullInt64
	var endedAt sql.NullInt64
	var lastEventAt sql.NullInt64
	var createdAt int64
	var updatedAt int64
	if err := scanner.Scan(
		&run.ID, &run.Source, &projectID, &projectName, &title, &status,
		&startedAt, &endedAt, &lastEventAt, &run.EventCount, &run.ErrorCount,
		&run.NextSequence, &captureModes, &parentRunID, &createdAt, &updatedAt,
		&run.OpenAlerts, &run.SubagentRuns,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, ErrRunNotFound
		}
		return Run{}, fmt.Errorf("scan run: %w", err)
	}
	run.Status = event.Status(status)
	run.ProjectID = stringPointer(projectID)
	run.ProjectName = stringPointer(projectName)
	run.Title = stringPointer(title)
	run.ParentRunID = stringPointer(parentRunID)
	run.StartedAt = timePointer(startedAt)
	run.EndedAt = timePointer(endedAt)
	run.LastEventAt = timePointer(lastEventAt)
	run.CreatedAt = time.Unix(0, createdAt).UTC()
	run.UpdatedAt = time.Unix(0, updatedAt).UTC()
	run.Models = []string{}
	if err := json.Unmarshal([]byte(captureModes), &run.CaptureModes); err != nil {
		return Run{}, fmt.Errorf("decode run capture modes: %w", err)
	}
	run.DurationMS = durationMS(run)
	return run, nil
}

func (store *Store) loadRunDerived(ctx context.Context, run *Run) error {
	if run.Title == nil {
		run.Title = firstCapturedTitle(ctx, store.db, run.ID)
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT attributes FROM events
		WHERE run_id = ? AND (type LIKE 'model.%' OR type LIKE 'subagent.%')
		ORDER BY sequence`, run.ID)
	if err != nil {
		return fmt.Errorf("load run model usage: %w", err)
	}
	defer rows.Close()

	models := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	var inputTokens int64
	var outputTokens int64
	hasTokens := false
	for rows.Next() {
		var attributes string
		if err := rows.Scan(&attributes); err != nil {
			return fmt.Errorf("scan run model usage: %w", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(attributes), &decoded); err != nil {
			continue
		}
		if model, ok := decoded["model"].(string); ok && model != "" {
			if _, duplicate := seen[model]; !duplicate {
				seen[model] = struct{}{}
				models = append(models, model)
			}
		}
		input, inputOK := numberAttribute(decoded["input_tokens"])
		output, outputOK := numberAttribute(decoded["output_tokens"])
		if inputOK || outputOK {
			hasTokens = true
			inputTokens += input
			outputTokens += output
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate run model usage: %w", err)
	}
	run.Models = models
	if hasTokens {
		total := inputTokens
		run.InputTokens = &total
		totalOutput := outputTokens
		run.OutputTokens = &totalOutput
	}
	return nil
}

func numberAttribute(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case int64:
		return typed, true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func durationMS(run Run) *int64 {
	if run.StartedAt == nil {
		return nil
	}
	end := run.EndedAt
	if end == nil {
		end = run.LastEventAt
	}
	if end == nil {
		return nil
	}
	value := end.Sub(*run.StartedAt).Milliseconds()
	if value < 0 {
		return nil
	}
	return &value
}

func runSortAt(run Run) int64 {
	if run.StartedAt != nil {
		return run.StartedAt.UnixNano()
	}
	if run.LastEventAt != nil {
		return run.LastEventAt.UnixNano()
	}
	return run.CreatedAt.UnixNano()
}

func encodeRunCursor(cursor runCursor) string {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeRunCursor(value string) (runCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return runCursor{}, errors.New("invalid cursor")
	}
	var cursor runCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return runCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func (store *Store) ListSteps(ctx context.Context, runID string) ([]Step, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT s.run_id, s.id, s.parent_step_id, s.type, s.status, s.started_at, s.ended_at,
		       (SELECT COUNT(*) FROM events e WHERE e.run_id = s.run_id AND e.step_id = s.id)
		FROM steps s
		WHERE s.run_id = ?
		ORDER BY s.created_at, s.id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list steps: %w", err)
	}
	defer rows.Close()

	steps := make([]Step, 0)
	for rows.Next() {
		var step Step
		var parentStepID sql.NullString
		var status string
		var startedAt sql.NullInt64
		var endedAt sql.NullInt64
		if err := rows.Scan(&step.RunID, &step.ID, &parentStepID, &step.Type, &status, &startedAt, &endedAt, &step.EventCount); err != nil {
			return nil, fmt.Errorf("scan step: %w", err)
		}
		step.Status = event.Status(status)
		step.ParentStepID = stringPointer(parentStepID)
		step.StartedAt = timePointer(startedAt)
		step.EndedAt = timePointer(endedAt)
		steps = append(steps, step)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate steps: %w", err)
	}
	return steps, nil
}

func (store *Store) ListSubagentRuns(ctx context.Context, parentRunID string) ([]Run, error) {
	page, err := store.ListRuns(ctx, RunFilter{ParentRunID: parentRunID}, "", 200)
	if err != nil {
		return nil, err
	}
	return page.Runs, nil
}

func (store *Store) DeleteRun(ctx context.Context, runID string) (DeleteResult, error) {
	result := DeleteResult{RunID: runID}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin run deletion: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM runs WHERE id = ?)", runID).Scan(&exists); err != nil {
		return result, fmt.Errorf("check run existence: %w", err)
	}
	if exists != 1 {
		return result, ErrRunNotFound
	}

	counts := []struct {
		query string
		into  *int
	}{
		{"SELECT COUNT(*) FROM events_fts WHERE run_id = ?", &result.SearchEntries},
		{"SELECT COUNT(*) FROM events WHERE run_id = ?", &result.Events},
		{"SELECT COUNT(*) FROM steps WHERE run_id = ?", &result.Steps},
		{"SELECT COUNT(*) FROM alerts WHERE run_id = ?", &result.Alerts},
	}
	for _, count := range counts {
		if err := tx.QueryRowContext(ctx, count.query, runID).Scan(count.into); err != nil {
			return result, fmt.Errorf("count run rows: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE run_id = ?", runID); err != nil {
		return result, fmt.Errorf("delete run events: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM steps WHERE run_id = ?", runID); err != nil {
		return result, fmt.Errorf("delete run steps: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM alerts WHERE run_id = ?", runID); err != nil {
		return result, fmt.Errorf("delete run alerts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM runs WHERE id = ?", runID); err != nil {
		return result, fmt.Errorf("delete run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit run deletion: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA optimize"); err != nil {
		return result, fmt.Errorf("optimize database: %w", err)
	}
	return result, nil
}

func (store *Store) SelectRetentionCandidates(ctx context.Context, policy RetentionPolicy, now time.Time) ([]Run, error) {
	selected := make([]string, 0, 2)
	arguments := make([]any, 0, 2)
	if policy.Days > 0 {
		cutoff := now.Add(-time.Duration(policy.Days) * 24 * time.Hour).UnixNano()
		selected = append(selected, "COALESCE(started_at, last_event_at, created_at) < ?")
		arguments = append(arguments, cutoff)
	}
	if policy.KeepNewestPerProject > 0 {
		// A per-project cap only applies to runs that belong to a project. A run
		// with no project is not in a project, so the rule never silently
		// deletes it.
		selected = append(selected, `project_id IS NOT NULL AND id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (
					PARTITION BY project_id
					ORDER BY COALESCE(started_at, last_event_at, created_at) DESC, id DESC
				) AS position
				FROM runs
			) WHERE position > ?)`)
		arguments = append(arguments, policy.KeepNewestPerProject)
	}
	if len(selected) == 0 {
		return nil, nil
	}
	query := `SELECT id FROM runs
		WHERE ended_at IS NOT NULL AND (` + strings.Join(selected, " OR ") + `)
		ORDER BY COALESCE(started_at, last_event_at, created_at), id`

	rows, err := store.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("select retention candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]Run, 0)
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, fmt.Errorf("scan retention candidate: %w", err)
		}
		run, err := store.GetRun(ctx, runID)
		if err != nil {
			if errors.Is(err, ErrRunNotFound) {
				continue
			}
			return nil, err
		}
		candidates = append(candidates, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retention candidates: %w", err)
	}
	return candidates, nil
}

func (store *Store) CountRuns(ctx context.Context) (int, error) {
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM runs").Scan(&count); err != nil {
		return 0, fmt.Errorf("count runs: %w", err)
	}
	return count, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func stringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func timePointer(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.Unix(0, value.Int64).UTC()
	return &result
}

// AssignProject links existing runs to a project. It exists so callers outside
// this package (and its tests) can set project ownership without raw SQL.
func (store *Store) AssignProject(ctx context.Context, runIDs []string, projectID string) error {
	if len(runIDs) == 0 {
		return nil
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin project assignment: %w", err)
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(ctx, "UPDATE runs SET project_id = ? WHERE id = ?")
	if err != nil {
		return fmt.Errorf("prepare project assignment: %w", err)
	}
	defer statement.Close()
	for _, runID := range runIDs {
		if _, err := statement.ExecContext(ctx, projectID, runID); err != nil {
			return fmt.Errorf("assign project to run %s: %w", runID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project assignment: %w", err)
	}
	return nil
}
