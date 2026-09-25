// Package hooks translates native agent lifecycle payloads into the Traceboard
// event contract. Each adapter maps only what its source actually exposes and
// records unknown input as a source extension rather than guessing.
package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"traceboard/internal/event"
)

// Source names as declared by the compatibility matrix.
const (
	SourceOpenCode    = "opencode"
	SourceClaudeCode  = "claude-code"
	SourceCodex       = "codex"
	SourceAntigravity = "antigravity"
	SourceGeminiCLI   = "gemini-cli"
)

var SupportedSources = []string{
	SourceOpenCode, SourceClaudeCode, SourceCodex, SourceAntigravity, SourceGeminiCLI,
}

type HookInput struct {
	Source         string
	SourceVersion  string
	EventName      string
	RunID          string
	StepID         string
	ParentStepID   string
	SourceSequence *int64
	OccurredAt     time.Time
	Status         event.Status
	Attributes     map[string]any
	Content        map[string]any
	Raw            any
}

type Mapping struct {
	Source         string
	SourceVersion  string
	SupportedSince string
	Events         []event.Event
	Notes          []string
}

type Normalizer interface {
	Source() string
	SupportedVersions() []string
	Normalize(input HookInput) (Mapping, error)
}

func registry() map[string]Normalizer {
	adapters := make([]Normalizer, 0, 5)
	adapters = append(adapters, newOpenCodeAdapter(), newClaudeCodeAdapter(), newCodexAdapter(), newAntigravityAdapter(), newGeminiCLIAdapter())
	result := make(map[string]Normalizer, len(adapters))
	for _, adapter := range adapters {
		result[adapter.Source()] = adapter
	}
	return result
}

func For(source string) (Normalizer, bool) {
	adapter, ok := registry()[source]
	return adapter, ok
}

func Sources() []string {
	names := SupportedSources
	sort.Strings(names)
	return names
}

func Normalize(input HookInput) (Mapping, error) {
	adapter, ok := For(input.Source)
	if !ok {
		return Mapping{}, fmt.Errorf("unsupported source %q", input.Source)
	}
	return adapter.Normalize(input)
}

// build turns a resolved event type into a valid envelope. Identifier derivation
// is deterministic so a retried hook delivery is deduplicated by the store.
func build(input HookInput, eventType string, status event.Status, stepID string, attributes map[string]any, content map[string]any) event.Event {
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	output := event.Event{
		SchemaVersion:  event.SchemaVersion1,
		Source:         input.Source,
		SourceVersion:  input.SourceVersion,
		RunID:          input.RunID,
		StepID:         stepID,
		ParentStepID:   input.ParentStepID,
		SourceSequence: input.SourceSequence,
		OccurredAt:     occurredAt.UTC(),
		Type:           eventType,
		Status:         status,
		Capture:        map[string]any{"mode": string(event.CaptureMetadata)},
		Attributes:     attributes,
	}
	if output.RunID == "" {
		output.RunID = input.Source + ":unattributed"
	}
	if content != nil {
		output.Content = content
	}
	if input.Raw != nil {
		output.Raw = input.Raw
	}
	if output.Status == "" {
		output.Status = event.StatusUnknown
	}
	// Identifiers are derived from the source's own event name so an unmapped
	// lifecycle still deduplicates on retry.
	output.EventID = deriveID("evt", output, eventType)
	output.SourceEventID = deriveID("src", output, eventType)
	if _, known := event.KnownTypes[eventType]; !known {
		output.Type = event.TypeSourceExtension
		if output.Status != event.StatusStarted && output.Status != event.StatusCompleted {
			output.Status = event.StatusUnknown
		}
		if output.Attributes == nil {
			output.Attributes = map[string]any{}
		}
		output.Attributes["extension_kind"] = "hook"
		output.Attributes["source_type"] = eventType
	}
	return output
}

func deriveID(prefix string, output event.Event, sourceEventType string) string {
	canonical, err := json.Marshal(output.Raw)
	if err != nil {
		canonical = nil
	}
	sequence := ""
	if output.SourceSequence != nil {
		sequence = fmt.Sprintf("%d", *output.SourceSequence)
	}
	payload := strings.Join([]string{
		output.Source, output.SourceVersion, output.RunID, output.StepID,
		output.ParentStepID, sourceEventType, sequence,
		output.OccurredAt.UTC().Format(time.RFC3339Nano), string(canonical),
	}, "\x1f")
	sum := sha256.Sum256([]byte(payload))
	return prefix + "_" + hex.EncodeToString(sum[:16])
}

// extensionStatus keeps a benign unknown event out of the error vocabulary.
func unknownStatus(input HookInput) event.Status {
	if input.Status == event.StatusFailed {
		return event.StatusUnknown
	}
	return event.StatusUnknown
}

func copyMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
