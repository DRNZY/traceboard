// Package ingest turns untrusted source payloads into validated, redacted,
// versioned events and writes them to the store. It is the only path that may
// add events, so every capture-mode and redaction rule applies exactly once.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"traceboard/internal/event"
	"traceboard/internal/redact"
	"traceboard/internal/store"
)

const (
	// MaxEventsPerBatch is the compiled safety maximum for one request.
	MaxEventsPerBatch = 1000
	// MaxContentFieldBytes bounds any single captured prompt or tool body.
	MaxContentFieldBytes = 256 << 10
	// MaxRawPayloadBytes bounds one retained raw source payload.
	MaxRawPayloadBytes = 1 << 20
)

type Result struct {
	Accepted    int `json:"accepted"`
	Duplicate   int `json:"duplicate"`
	Quarantined int `json:"quarantined"`
	Dropped     int `json:"dropped"`
}

type Store interface {
	InsertEvents(ctx context.Context, events ...event.Event) (store.InsertResult, error)
}

type Limits struct {
	MaxEventsPerBatch   int
	MaxContentFieldSize int
	MaxRawPayloadSize   int
}

func DefaultLimits() Limits {
	return Limits{
		MaxEventsPerBatch:   MaxEventsPerBatch,
		MaxContentFieldSize: MaxContentFieldBytes,
		MaxRawPayloadSize:   MaxRawPayloadBytes,
	}
}

type Publisher interface {
	RunChanged(runID string)
}

type Service struct {
	store      Store
	redactor   redact.Redactor
	limits     Limits
	modes      map[string]event.CaptureMode
	publisher  Publisher
	now        func() time.Time
	onAccepted func(runID string)
}

func NewService(database Store, redactor redact.Redactor, limits Limits) *Service {
	return &Service{
		store:      database,
		redactor:   redactor,
		limits:     limits,
		modes:      make(map[string]event.CaptureMode),
		now:        func() time.Time { return time.Now().UTC() },
		onAccepted: func(string) {},
	}
}

func (service *Service) SetCaptureMode(source string, mode event.CaptureMode) {
	service.modes[source] = mode
}

func (service *Service) CaptureMode(source string) event.CaptureMode {
	if mode, ok := service.modes[source]; ok {
		return mode
	}
	return event.CaptureMetadata
}

func (service *Service) SetClock(now func() time.Time) {
	if now != nil {
		service.now = now
	}
}

func (service *Service) SetOnAccepted(handler func(runID string)) {
	if handler != nil {
		service.onAccepted = handler
	}
}

// Ingest accepts a batch, applies capture-mode filtering, derives stable source
// identifiers, redacts every field, and writes the survivors in one store call.
// A malformed event is quarantined by the store rather than failing the batch.
func (service *Service) Ingest(ctx context.Context, batch event.Batch) Result {
	var result Result
	if len(batch.Events) == 0 {
		return result
	}
	if len(batch.Events) > service.limits.MaxEventsPerBatch {
		result.Dropped = len(batch.Events) - service.limits.MaxEventsPerBatch
		batch.Events = batch.Events[:service.limits.MaxEventsPerBatch]
	}

	prepared := make([]event.Event, 0, len(batch.Events))
	touchedRuns := make(map[string]struct{}, 4)
	for _, input := range batch.Events {
		output, keep := service.prepare(input)
		if !keep {
			result.Dropped++
			continue
		}
		prepared = append(prepared, output)
		touchedRuns[output.RunID] = struct{}{}
	}
	if len(prepared) == 0 {
		return result
	}

	inserted, err := service.store.InsertEvents(ctx, prepared...)
	if err != nil {
		result.Quarantined += len(prepared)
		return result
	}
	result.Accepted = inserted.Accepted
	result.Duplicate = inserted.Duplicate
	result.Quarantined += inserted.Quarantined
	for runID := range touchedRuns {
		service.onAccepted(runID)
	}
	return result
}

func (service *Service) prepare(input event.Event) (event.Event, bool) {
	if input.SchemaVersion != event.SchemaVersion1 {
		// An unsupported contract version is never silently upgraded. The event
		// is forwarded untouched so the store quarantines it with a reason the
		// operator can act on.
		return input, true
	}
	mode := service.CaptureMode(input.Source)
	if mode == event.CaptureOff {
		return event.Event{}, false
	}

	output := event.Event{
		SchemaVersion:  event.SchemaVersion1,
		Source:         input.Source,
		RunID:          input.RunID,
		StepID:         input.StepID,
		ParentStepID:   input.ParentStepID,
		Type:           input.Type,
		OccurredAt:     input.OccurredAt.UTC(),
		SourceSequence: input.SourceSequence,
	}
	if output.OccurredAt.IsZero() {
		output.OccurredAt = service.now()
	}
	output.SourceVersion = input.SourceVersion
	output.Status = input.Status
	if output.Status == "" {
		output.Status = event.StatusUnknown
	}
	output.EventID = strings.TrimSpace(input.EventID)
	if output.EventID == "" {
		output.EventID = deriveIdentifier("evt", input, service.canonicalRaw(input.Raw))
	}
	output.SourceEventID = strings.TrimSpace(input.SourceEventID)
	if output.SourceEventID == "" {
		output.SourceEventID = deriveIdentifier("src", input, service.canonicalRaw(input.Raw))
	}

	capture := map[string]any{"mode": string(mode)}
	for key, value := range input.Capture {
		if key == "mode" {
			continue
		}
		capture[key] = value
	}
	output.Capture = capture

	attributes, redactedCount, truncated := service.redactMap(input.Attributes)
	output.Attributes = attributes
	if truncated {
		capture["attributes_truncated"] = true
	}

	// A source-specific type with no safe normalized equivalent becomes a source
	// extension that keeps the original name. It is never reclassified as an
	// error, and error.recorded stays reserved for source-reported errors.
	if _, known := event.KnownTypes[output.Type]; !known {
		output.Type = event.TypeSourceExtension
		// An extension event does not imply an error. error.recorded stays
		// reserved for an error the source actually reported.
		if output.Status != event.StatusStarted && output.Status != event.StatusCompleted {
			output.Status = event.StatusUnknown
		}
		if _, exists := output.Attributes["extension_kind"]; !exists {
			output.Attributes["extension_kind"] = "event"
		}
		if _, exists := output.Attributes["source_type"]; !exists {
			output.Attributes["source_type"] = input.Type
		}
	}

	// Prompt and tool bodies are withheld in metadata mode. The event is still
	// recorded, and capture.marks states exactly what was withheld.
	if mode == event.CaptureMetadata && len(input.Content) > 0 {
		capture["content_withheld"] = true
		capture["content_fields"] = sortedKeys(input.Content)
		output.Content = nil
	} else if mode == event.CaptureDetailed && len(input.Content) > 0 {
		content, _, truncated := service.redactMap(input.Content)
		output.Content = content
		if truncated {
			capture["content_truncated"] = true
		}
	}

	if mode == event.CaptureDetailed && input.Raw != nil {
		raw := service.canonicalRaw(input.Raw)
		if raw != nil {
			redacted, _ := service.redactor.Apply(raw)
			if encoded, err := json.Marshal(redacted); err == nil && len(encoded) > service.limits.MaxRawPayloadSize {
				capture["raw_truncated"] = true
				output.Raw = map[string]any{
					"truncated": true,
					"bytes":     len(encoded),
				}
			} else {
				output.Raw = redacted
			}
		}
	} else if input.Raw != nil {
		capture["raw_withheld"] = true
	}

	if redactedCount > 0 {
		capture["redacted_fields"] = redactedCount
	}
	return output, true
}

// redactMap redacts a structured field and enforces the per-field ceiling. It
// reports how many matches were replaced and whether any value was cut.
func (service *Service) redactMap(input map[string]any) (map[string]any, int, bool) {
	if input == nil {
		return map[string]any{}, 0, false
	}
	redacted, matches := service.redactor.Apply(input)
	result, ok := redacted.(map[string]any)
	if !ok || result == nil {
		return map[string]any{}, len(matches), false
	}
	bounded, truncated := service.boundMap(result)
	return bounded, len(matches), truncated
}

// boundMap enforces the per-field content ceiling without ever splitting a
// secret-bearing value in a way that would leave a usable fragment behind.
func (service *Service) boundMap(input map[string]any) (map[string]any, bool) {
	truncated := false
	result := make(map[string]any, len(input))
	for key, value := range input {
		if text, ok := value.(string); ok && len(text) > service.limits.MaxContentFieldSize {
			redacted, _ := service.redactor.Apply(text)
			safe, _ := redacted.(string)
			cut := safe[:min(len(safe), service.limits.MaxContentFieldSize)]
			result[key] = cut
			truncated = true
			continue
		}
		result[key] = value
	}
	return result, truncated
}

func (service *Service) canonicalRaw(raw any) any {
	if raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil
	}
	return decoded
}

// deriveIdentifier produces a stable, source-independent identifier. The same
// source event always hashes to the same identifier, so a retry after a lost
// response is deduplicated rather than stored twice.
func deriveIdentifier(prefix string, input event.Event, raw any) string {
	canonical, err := json.Marshal(raw)
	if err != nil {
		canonical = nil
	}
	payload := strings.Join([]string{
		input.Source,
		input.SourceVersion,
		input.RunID,
		input.StepID,
		input.Type,
		input.OccurredAt.UTC().Format(time.RFC3339Nano),
		string(canonical),
	}, "\x1f")
	sum := sha256.Sum256([]byte(payload))
	return prefix + "_" + hex.EncodeToString(sum[:16])
}

func sortedKeys(input map[string]any) []string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var ErrEmptyBatch = errors.New("no events supplied")

func Describe(result Result) string {
	return fmt.Sprintf("accepted=%d duplicate=%d quarantined=%d dropped=%d",
		result.Accepted, result.Duplicate, result.Quarantined, result.Dropped)
}
