package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"traceboard/internal/event"
)

type EventRecord struct {
	EventID        string         `json:"event_id"`
	SourceEventID  string         `json:"source_event_id"`
	Source         string         `json:"source"`
	SourceVersion  string         `json:"source_version"`
	RunID          string         `json:"run_id"`
	StepID         *string        `json:"step_id"`
	ParentStepID   *string        `json:"parent_step_id"`
	SourceSequence *int64         `json:"source_sequence"`
	Sequence       int64          `json:"sequence"`
	OccurredAt     time.Time      `json:"occurred_at"`
	ReceivedAt     time.Time      `json:"received_at"`
	Type           string         `json:"type"`
	Status         event.Status   `json:"status"`
	Capture        map[string]any `json:"capture"`
	Attributes     map[string]any `json:"attributes"`
	Content        map[string]any `json:"content"`
	Raw            any            `json:"raw"`
	Searchable     bool           `json:"searchable"`
}

type EventPage struct {
	Events       []EventRecord `json:"events"`
	NextSequence int64         `json:"next_sequence"`
	RunSequence  int64         `json:"run_sequence"`
	HasMore      bool          `json:"has_more"`
	RunStatus    event.Status  `json:"run_status"`
}

const eventSelectColumns = `
		event_id, source_event_id, source, source_version, run_id, step_id, parent_step_id,
		source_sequence, sequence, occurred_at, received_at, type, status,
		capture, attributes, content, raw`

const timelineOrder = `ORDER BY occurred_at, CASE WHEN source_sequence IS NULL THEN 1 ELSE 0 END, source_sequence, sequence`

func (store *Store) ListEvents(ctx context.Context, runID string, afterSequence int64, limit int) (EventPage, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	page := EventPage{Events: make([]EventRecord, 0, limit), NextSequence: afterSequence}

	var runStatus string
	var runSequence int64
	err := store.db.QueryRowContext(ctx, "SELECT status, next_sequence FROM runs WHERE id = ?", runID).Scan(&runStatus, &runSequence)
	if err != nil {
		return EventPage{}, ErrRunNotFound
	}
	page.NextSequence = afterSequence
	page.RunSequence = runSequence
	page.RunStatus = event.Status(runStatus)

	rows, err := store.db.QueryContext(ctx, `
		SELECT`+eventSelectColumns+`
		FROM events
		WHERE run_id = ? AND sequence > ?
		`+timelineOrder+`
		LIMIT ?`, runID, afterSequence, limit+1)
	if err != nil {
		return EventPage{}, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		record, err := scanEventRecord(rows)
		if err != nil {
			return EventPage{}, err
		}
		page.Events = append(page.Events, record)
	}
	if err := rows.Err(); err != nil {
		return EventPage{}, fmt.Errorf("iterate events: %w", err)
	}
	if len(page.Events) > limit {
		page.Events = page.Events[:limit]
		page.HasMore = true
	}
	// The resume cursor is the highest ingest sequence carried by this page, so a
	// client can page without ever replaying or skipping an event.
	for _, record := range page.Events {
		if record.Sequence > page.NextSequence {
			page.NextSequence = record.Sequence
		}
	}
	return page, nil
}

func (store *Store) ListAllEvents(ctx context.Context, runID string) ([]EventRecord, error) {
	collected := make([]EventRecord, 0, 64)
	cursor := int64(0)
	for {
		batch, err := store.ListEvents(ctx, runID, cursor, 1000)
		if err != nil {
			return nil, err
		}
		collected = append(collected, batch.Events...)
		cursor = batch.NextSequence
		if !batch.HasMore || len(batch.Events) == 0 {
			return collected, nil
		}
	}
}

func (store *Store) GetEvent(ctx context.Context, runID, eventID string) (EventRecord, error) {
	row := store.db.QueryRowContext(ctx, `
		SELECT`+eventSelectColumns+`
		FROM events
		WHERE run_id = ? AND event_id = ?`, runID, eventID)
	record, err := scanEventRecord(row)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			return EventRecord{}, ErrEventNotFound
		}
		return EventRecord{}, err
	}
	return record, nil
}

var ErrEventNotFound = errors.New("event not found")

func scanEventRecord(scanner rowScanner) (EventRecord, error) {
	var record EventRecord
	var stepID sql.NullString
	var parentStepID sql.NullString
	var sourceSequence sql.NullInt64
	var occurredAt int64
	var receivedAt int64
	var status string
	var capture string
	var attributes string
	var content sql.NullString
	var raw sql.NullString
	if err := scanner.Scan(
		&record.EventID, &record.SourceEventID, &record.Source, &record.SourceVersion, &record.RunID,
		&stepID, &parentStepID, &sourceSequence, &record.Sequence, &occurredAt, &receivedAt,
		&record.Type, &status, &capture, &attributes, &content, &raw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EventRecord{}, ErrEventNotFound
		}
		return EventRecord{}, fmt.Errorf("scan event: %w", err)
	}
	record.StepID = stringPointer(stepID)
	record.ParentStepID = stringPointer(parentStepID)
	if sourceSequence.Valid {
		value := sourceSequence.Int64
		record.SourceSequence = &value
	}
	record.OccurredAt = time.Unix(0, occurredAt).UTC()
	record.ReceivedAt = time.Unix(0, receivedAt).UTC()
	record.Status = event.Status(status)
	if err := json.Unmarshal([]byte(capture), &record.Capture); err != nil {
		return EventRecord{}, fmt.Errorf("decode event capture: %w", err)
	}
	if record.Capture == nil {
		record.Capture = map[string]any{}
	}
	if err := json.Unmarshal([]byte(attributes), &record.Attributes); err != nil {
		return EventRecord{}, fmt.Errorf("decode event attributes: %w", err)
	}
	if record.Attributes == nil {
		record.Attributes = map[string]any{}
	}
	if content.Valid && content.String != "" {
		decoded, err := decodeContentText(content.String)
		if err != nil {
			return EventRecord{}, fmt.Errorf("decode event content: %w", err)
		}
		record.Content = map[string]any{"text": decoded}
	}
	if raw.Valid && raw.String != "" {
		var decoded any
		if err := json.Unmarshal([]byte(raw.String), &decoded); err == nil {
			record.Raw = decoded
		}
	}
	record.Searchable = record.Content != nil || record.Raw != nil
	return record, nil
}

func decodeContentText(encoded string) (string, error) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		return "", err
	}
	for _, key := range []string{"text", "prompt", "output", "input", "error", "response"} {
		if value, ok := decoded[key].(string); ok && value != "" {
			return value, nil
		}
	}
	return "", nil
}

func firstCapturedTitle(ctx context.Context, database queryer, runID string) *string {
	var content string
	if err := database.QueryRowContext(ctx, `
		SELECT content FROM events
		WHERE run_id = ? AND content IS NOT NULL AND content <> ''
		ORDER BY sequence LIMIT 1`, runID).Scan(&content); err != nil {
		return nil
	}
	text, err := decodeContentText(content)
	if err != nil || text == "" {
		return nil
	}
	return &text
}

// FTSQuery converts free-text user input into a safe FTS5 MATCH expression.
// Every term is quoted so operator characters in user input cannot change the
// query shape, and terms are combined with an implicit AND.
func FTSQuery(input string) string {
	terms := make([]string, 0, 8)
	fields := strings.FieldsFunc(input, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.' && r != '/'
	})
	for _, term := range fields {
		trimmed := strings.Trim(term, `"-./_`)
		if trimmed == "" {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(trimmed, `"`, `""`)+`"`)
	}
	if len(terms) == 0 {
		return `""`
	}
	return strings.Join(terms, " ")
}
