package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"traceboard/internal/event"
	"traceboard/internal/redact"
)

type InsertResult struct {
	Accepted    int
	Duplicate   int
	Quarantined int
}

type storedEvent struct {
	event.Event
	Sequence   int64
	ReceivedAt time.Time
}

func (store *Store) InsertEvents(ctx context.Context, inputs ...event.Event) (InsertResult, error) {
	var result InsertResult
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("begin event insertion: %w", err)
	}
	defer tx.Rollback()

	for _, input := range inputs {
		prepared, err := prepareEvent(input)
		if err != nil {
			if err := quarantineEvent(ctx, tx, input, err); err != nil {
				return InsertResult{}, err
			}
			result.Quarantined++
			continue
		}
		if prepared.SourceEventID != "" {
			var exists int
			err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM events WHERE source = ? AND source_event_id = ?)", prepared.Source, prepared.SourceEventID).Scan(&exists)
			if err != nil {
				return InsertResult{}, fmt.Errorf("check duplicate event: %w", err)
			}
			if exists == 1 {
				result.Duplicate++
				continue
			}
		}
		if err := insertEvent(ctx, tx, prepared); err != nil {
			return InsertResult{}, err
		}
		result.Accepted++
	}

	if err := tx.Commit(); err != nil {
		return InsertResult{}, fmt.Errorf("commit event insertion: %w", err)
	}
	return result, nil
}

func prepareEvent(input event.Event) (storedEvent, error) {
	if err := event.Validate(input); err != nil {
		return storedEvent{}, err
	}
	if input.EventID == "" {
		return storedEvent{}, errors.New("missing required event identity: event_id")
	}
	if input.SourceEventID == "" {
		return storedEvent{}, errors.New("missing required event identity: source_event_id")
	}

	redactor := redact.Default()
	stored := storedEvent{
		Event: event.Event{
			SchemaVersion: input.SchemaVersion,
			EventID:       redactedString(redactor, input.EventID),
			SourceEventID: redactedString(redactor, input.SourceEventID),
			Source:        redactedString(redactor, input.Source),
			SourceVersion: redactedString(redactor, input.SourceVersion),
			RunID:         redactedString(redactor, input.RunID),
			StepID:        redactedString(redactor, input.StepID),
			ParentStepID:  redactedString(redactor, input.ParentStepID),
			OccurredAt:    input.OccurredAt.UTC(),
			Type:          redactedString(redactor, input.Type),
			Status:        input.Status,
		},
		ReceivedAt: time.Now().UTC(),
	}
	if input.SourceSequence != nil {
		sequence := *input.SourceSequence
		stored.SourceSequence = &sequence
	}

	var err error
	if stored.Capture, err = redactedJSONMap(redactor, input.Capture); err != nil {
		return storedEvent{}, fmt.Errorf("capture: %w", err)
	}
	if stored.Attributes, err = redactedJSONMap(redactor, input.Attributes); err != nil {
		return storedEvent{}, fmt.Errorf("attributes: %w", err)
	}
	if stored.Content, err = redactedJSONMap(redactor, input.Content); err != nil {
		return storedEvent{}, fmt.Errorf("content: %w", err)
	}
	if input.Raw != nil {
		redacted, _ := redactor.Apply(input.Raw)
		stored.Raw = redacted
		if _, err := json.Marshal(redacted); err != nil {
			return storedEvent{}, fmt.Errorf("raw: %w", err)
		}
	}
	return stored, nil
}

func redactedJSONMap(redactor redact.Redactor, value map[string]any) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	redacted, _ := redactor.Apply(value)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func redactedString(redactor redact.Redactor, value string) string {
	if value == "" {
		return ""
	}
	redacted, _ := redactor.Apply(value)
	text, _ := redacted.(string)
	return text
}

func insertEvent(ctx context.Context, tx *sql.Tx, stored storedEvent) error {
	now := stored.ReceivedAt.UnixNano()
	if err := ensureSource(ctx, tx, stored, now); err != nil {
		return err
	}
	if err := ensureRun(ctx, tx, stored, now); err != nil {
		return err
	}
	sequence, err := allocateRunSequence(ctx, tx, stored.RunID, now)
	if err != nil {
		return err
	}
	if err := updateStep(ctx, tx, stored, now); err != nil {
		return err
	}
	if err := updateRun(ctx, tx, stored); err != nil {
		return err
	}

	capture, err := json.Marshal(stored.Capture)
	if err != nil {
		return fmt.Errorf("marshal capture: %w", err)
	}
	attributes, err := json.Marshal(stored.Attributes)
	if err != nil {
		return fmt.Errorf("marshal attributes: %w", err)
	}
	var content any
	if stored.Content != nil {
		encoded, err := json.Marshal(stored.Content)
		if err != nil {
			return fmt.Errorf("marshal content: %w", err)
		}
		content = string(encoded)
	}
	var raw any
	if stored.Raw != nil {
		encoded, err := json.Marshal(stored.Raw)
		if err != nil {
			return fmt.Errorf("marshal raw: %w", err)
		}
		raw = string(encoded)
	}

	searchText := searchableText(stored)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO events (
			event_id, source_event_id, source, source_version, run_id, step_id,
			parent_step_id, source_sequence, sequence, occurred_at, received_at,
			type, status, capture, attributes, content, raw, search_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stored.EventID, stored.SourceEventID, stored.Source, stored.SourceVersion, stored.RunID,
		nullableString(stored.StepID), nullableString(stored.ParentStepID), stored.SourceSequence,
		sequence, stored.OccurredAt.UnixNano(), stored.ReceivedAt.UnixNano(), stored.Type, stored.Status,
		string(capture), string(attributes), content, raw, searchText,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func ensureSource(ctx context.Context, tx *sql.Tx, stored storedEvent, now int64) error {
	mode := event.CaptureMetadata
	if text, ok := stored.Capture["mode"].(string); ok {
		mode = event.CaptureMode(text)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO sources(name, source_version, capture_mode, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			source_version = CASE WHEN excluded.source_version <> '' THEN excluded.source_version ELSE sources.source_version END,
			capture_mode = excluded.capture_mode,
			updated_at = excluded.updated_at`,
		stored.Source, stored.SourceVersion, mode, now, now,
	)
	if err != nil {
		return fmt.Errorf("ensure source: %w", err)
	}
	return nil
}

func ensureRun(ctx context.Context, tx *sql.Tx, stored storedEvent, now int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO runs(id, source, last_event_at, capture_modes, created_at, updated_at)
		VALUES (?, ?, ?, '[]', ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		stored.RunID, stored.Source, stored.OccurredAt.UnixNano(), now, now,
	)
	if err != nil {
		return fmt.Errorf("ensure run: %w", err)
	}
	return nil
}

func allocateRunSequence(ctx context.Context, tx *sql.Tx, runID string, now int64) (int64, error) {
	var sequence int64
	if err := tx.QueryRowContext(ctx, `
		UPDATE runs
		SET next_sequence = next_sequence + 1, updated_at = ?
		WHERE id = ?
		RETURNING next_sequence - 1`, now, runID).Scan(&sequence); err != nil {
		return 0, fmt.Errorf("allocate run sequence: %w", err)
	}
	return sequence, nil
}

func updateStep(ctx context.Context, tx *sql.Tx, stored storedEvent, now int64) error {
	if stored.StepID == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO steps(run_id, id, parent_step_id, type, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, id) DO NOTHING`,
		stored.RunID, stored.StepID, nullableString(stored.ParentStepID), stored.Type, stored.Status, now, now,
	)
	if err != nil {
		return fmt.Errorf("ensure step: %w", err)
	}

	terminal := terminalStatus(stored.Status)
	_, err = tx.ExecContext(ctx, `
		UPDATE steps SET
			parent_step_id = COALESCE(?, parent_step_id),
			type = ?,
			status = ?,
			started_at = CASE
				WHEN ? THEN CASE WHEN started_at IS NULL OR ? < started_at THEN ? ELSE started_at END
				ELSE started_at
			END,
			ended_at = CASE
				WHEN ? THEN CASE WHEN ended_at IS NULL OR ? < ended_at THEN ? ELSE ended_at END
				ELSE ended_at
			END,
			updated_at = ?
		WHERE run_id = ? AND id = ?`,
		nullableString(stored.ParentStepID), stored.Type, stored.Status,
		stored.Status == event.StatusStarted, stored.OccurredAt.UnixNano(), stored.OccurredAt.UnixNano(),
		terminal, stored.OccurredAt.UnixNano(), stored.OccurredAt.UnixNano(),
		now, stored.RunID, stored.StepID,
	)
	if err != nil {
		return fmt.Errorf("update step: %w", err)
	}
	return nil
}

func updateRun(ctx context.Context, tx *sql.Tx, stored storedEvent) error {
	terminal, terminalStatusValue := runTerminalStatus(stored.Type)
	modes, err := mergeCaptureModes(ctx, tx, stored.RunID, captureModeOf(stored))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE runs SET
			capture_modes = ?,
			status = CASE
				WHEN ? = 'run.started' AND ended_at IS NULL THEN ?
				WHEN ? AND (ended_at IS NULL OR ? < ended_at) THEN ?
				ELSE status
			END,
			started_at = CASE
				WHEN ? = 'run.started' THEN CASE WHEN started_at IS NULL OR ? < started_at THEN ? ELSE started_at END
				ELSE started_at
			END,
			ended_at = CASE
				WHEN ? AND (ended_at IS NULL OR ? < ended_at) THEN ?
				ELSE ended_at
			END,
			last_event_at = CASE
				WHEN last_event_at IS NULL OR ? > last_event_at THEN ? ELSE last_event_at END,
			event_count = event_count + 1,
			error_count = error_count + ?,
			updated_at = ?
		WHERE id = ?`,
		modes,
		stored.Type, stored.Status,
		terminal, stored.OccurredAt.UnixNano(), terminalStatusValue,
		stored.Type, stored.OccurredAt.UnixNano(), stored.OccurredAt.UnixNano(),
		terminal, stored.OccurredAt.UnixNano(), stored.OccurredAt.UnixNano(),
		stored.OccurredAt.UnixNano(), stored.OccurredAt.UnixNano(),
		boolInt(stored.Type == "error.recorded"), stored.ReceivedAt.UnixNano(), stored.RunID,
	)
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	return nil
}

func captureModeOf(stored storedEvent) event.CaptureMode {
	if text, ok := stored.Capture["mode"].(string); ok {
		return event.CaptureMode(text)
	}
	return event.CaptureMetadata
}

func mergeCaptureModes(ctx context.Context, tx *sql.Tx, runID string, mode event.CaptureMode) (string, error) {
	var encoded string
	if err := tx.QueryRowContext(ctx, "SELECT capture_modes FROM runs WHERE id = ?", runID).Scan(&encoded); err != nil {
		return "", fmt.Errorf("read run capture modes: %w", err)
	}
	var modes []event.CaptureMode
	if encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &modes); err != nil {
			return "", fmt.Errorf("decode run capture modes: %w", err)
		}
	}
	filtered := make([]event.CaptureMode, 0, len(modes)+1)
	for _, existing := range modes {
		// A later detailed event promotes the run's recorded coverage and
		// supersedes the weaker modes.
		if mode == event.CaptureDetailed && existing != event.CaptureDetailed {
			continue
		}
		if existing == mode {
			return encodeModes(modes)
		}
		filtered = append(filtered, existing)
	}
	return encodeModes(append(filtered, mode))
}

func encodeModes(modes []event.CaptureMode) (string, error) {
	encoded, err := json.Marshal(modes)
	if err != nil {
		return "", fmt.Errorf("encode run capture modes: %w", err)
	}
	return string(encoded), nil
}

func runTerminalStatus(eventType string) (bool, event.Status) {
	switch eventType {
	case "run.completed":
		return true, event.StatusCompleted
	case "run.failed":
		return true, event.StatusFailed
	case "run.cancelled":
		return true, event.StatusCancelled
	case "run.incomplete":
		return true, event.StatusIncomplete
	default:
		return false, event.StatusUnknown
	}
}

func terminalStatus(status event.Status) bool {
	switch status {
	case event.StatusCompleted, event.StatusFailed, event.StatusCancelled, event.StatusIncomplete:
		return true
	default:
		return false
	}
}

func searchableText(stored storedEvent) string {
	parts := make([]string, 0, 5)
	for _, value := range []any{stored.Capture, stored.Attributes, stored.Content, stored.Raw} {
		if value == nil {
			continue
		}
		encoded, err := json.Marshal(value)
		if err == nil {
			parts = append(parts, string(encoded))
		}
	}
	parts = append(parts, stored.Type, string(stored.Status))
	return strings.Join(parts, "\n")
}

func quarantineEvent(ctx context.Context, tx *sql.Tx, input event.Event, validationErr error) error {
	redactor := redact.Default()
	payload := map[string]any{
		"event_id":        redactedString(redactor, input.EventID),
		"source_event_id": redactedString(redactor, input.SourceEventID),
		"source":          redactedString(redactor, input.Source),
		"run_id":          redactedString(redactor, input.RunID),
	}
	if redactedRaw, _ := redactor.Apply(input.Raw); redactedRaw != nil {
		if _, err := json.Marshal(redactedRaw); err == nil {
			payload["raw"] = redactedRaw
		}
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal quarantine payload: %w", err)
	}
	reason := redactedString(redactor, validationErr.Error())
	_, err = tx.ExecContext(ctx, `
		INSERT INTO quarantine(source, source_event_id, payload, reason, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		nullableString(input.Source), nullableString(input.SourceEventID), string(encodedPayload), reason, time.Now().UTC().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("quarantine event: %w", err)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
