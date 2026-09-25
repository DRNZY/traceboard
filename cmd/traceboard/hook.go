package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"traceboard/internal/config"
	"traceboard/internal/event"
	"traceboard/internal/hooks"
	"traceboard/internal/ingest"
	"traceboard/internal/redact"
)

// hookTimeout is deliberately short: an agent's hook must never wait on the
// collector. Past this point the payload is spooled and the hook returns.
const hookTimeout = 400 * time.Millisecond

// runHook reads one native hook payload from stdin, maps it into the Traceboard
// event contract, and forwards it. It always exits successfully after bounded
// local handling so a telemetry problem can never fail an agent run.
func runHook(env *environment, arguments []string) int {
	if len(arguments) == 0 {
		return env.fail("usage: traceboard hook <%s>", strings.Join(hooks.Sources(), "|"))
	}
	source := arguments[0]
	if _, ok := hooks.For(source); !ok {
		return env.fail("unsupported source %q; expected one of %s", source, strings.Join(hooks.Sources(), ", "))
	}

	payload, err := io.ReadAll(io.LimitReader(env.stdin, ingest.MaxContentFieldBytes*4))
	if err != nil {
		fmt.Fprintln(env.stderr, "traceboard: could not read the hook payload; continuing")
		return 0
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return 0
	}

	cfg, cfgErr := env.config()
	if cfgErr != nil {
		fmt.Fprintln(env.stderr, "traceboard: configuration is unavailable; the event was dropped")
		return 0
	}

	redactor, err := redact.New(redact.Options{})
	if err != nil {
		fmt.Fprintln(env.stderr, "traceboard: redaction is unavailable; the event was dropped")
		return 0
	}
	redactedPayload, _ := redactor.Apply(string(payload))
	cleaned, ok := redactedPayload.(string)
	if !ok {
		cleaned = string(payload)
	}

	events, mappingErr := mapHookPayload(source, cfg, cleaned)
	if mappingErr != nil || len(events) == 0 {
		// A payload Traceboard cannot map is still worth recording, so it lands
		// in quarantine instead of vanishing.
		events = []event.Event{unmappedEvent(source, cfg, cleaned)}
	}

	spoolRoot := filepath.Join(filepath.Dir(env.resolveConfigPath()), "spool")
	spool, spoolErr := ingest.NewSpool(spoolRoot, redactor, ingest.DefaultSpoolLimitBytes, ingest.DefaultSpoolAge)
	if spoolErr == nil {
		if removed, _ := spool.Expire(); removed > 0 {
			fmt.Fprintf(env.stderr, "traceboard: dropped %d spooled events past the age limit\n", removed)
		}
	}

	delivered := deliverHook(cfg, events)
	if !delivered && spoolErr == nil {
		encoded := make([]json.RawMessage, 0, len(events))
		for _, item := range events {
			payload, marshalErr := json.Marshal(item)
			if marshalErr != nil {
				continue
			}
			encoded = append(encoded, payload)
		}
		if len(encoded) > 0 {
			if appendErr := spool.Append(source, encoded...); appendErr != nil {
				fmt.Fprintf(env.stderr, "traceboard: the event was buffered but the spool rejected it: %v\n", appendErr)
			} else {
				fmt.Fprintf(env.stderr, "traceboard: the collector is offline; %d event(s) spooled for %s\n", len(encoded), source)
			}
		}
		return 0
	}
	if !delivered {
		fmt.Fprintln(env.stderr, "traceboard: the collector is offline and the spool is unavailable; the event was dropped")
	}
	return 0
}

func deliverHook(cfg config.Config, events []event.Event) bool {
	body, err := json.Marshal(event.Batch{Events: events})
	if err != nil {
		return false
	}
	endpoint := strings.TrimSpace(os.Getenv("TRACEBOARD_INGEST_URL"))
	if endpoint == "" {
		endpoint = "http://" + cfg.ListenAddress + "/api/v1/events"
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return false
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+cfg.IngestToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.StatusCode >= 200 && response.StatusCode < 300
}

func mapHookPayload(source string, cfg config.Config, payload string) ([]event.Event, error) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return nil, fmt.Errorf("hook payload is not a JSON object: %w", err)
	}
	input, err := hookInputFromPayload(source, cfg, decoded)
	if err != nil {
		return nil, err
	}
	mapping, err := hooks.Normalize(input)
	if err != nil {
		return nil, err
	}
	events := mapping.Events
	for index := range events {
		mode := cfg.Sources[source].CaptureMode
		if mode == "" {
			mode = event.CaptureMetadata
		}
		events[index].Capture = map[string]any{"mode": string(mode)}
	}
	return events, nil
}

func hookInputFromPayload(source string, cfg config.Config, payload map[string]any) (hooks.HookInput, error) {
	nested := map[string]any{}
	if properties, ok := payload["properties"].(map[string]any); ok {
		for key, value := range properties {
			nested[key] = value
		}
	}
	for key, value := range payload {
		if key == "properties" {
			continue
		}
		if _, exists := nested[key]; !exists {
			nested[key] = value
		}
	}

	input := hooks.HookInput{
		Source:        source,
		SourceVersion: stringValue(nested, "source_version", "version", "agent_version", "sdk_version"),
		EventName:     stringValue(nested, "event_name", "hook_event_name", "event", "type", "name"),
		RunID:         stringValue(nested, "run_id", "session_id", "sessionID", "conversation_id", "trace_id"),
		StepID:        stringValue(nested, "step_id", "tool_use_id", "call_id", "callID", "tool_call_id", "turn_id"),
		ParentStepID:  stringValue(nested, "parent_step_id", "parent_tool_use_id", "parent_id"),
		Attributes:    nested,
		Raw:           payload,
	}
	if input.EventName == "" {
		input.EventName = os.Getenv("TRACEBOARD_HOOK_EVENT")
	}
	if input.RunID == "" {
		return input, errors.New("hook payload did not identify a run")
	}
	if occurred := stringValue(nested, "occurred_at", "timestamp", "time", "created_at"); occurred != "" {
		if parsed, err := time.Parse(time.RFC3339, occurred); err == nil {
			input.OccurredAt = parsed.UTC()
		}
	}
	if sequence, ok := nested["source_sequence"].(float64); ok {
		value := int64(sequence)
		input.SourceSequence = &value
	}
	if status := stringValue(nested, "status", "event_status"); status != "" {
		input.Status = event.Status(status)
	}
	if content := contentFromPayload(nested, captureModeFor(cfg, source)); len(content) > 0 {
		input.Content = content
	}
	return input, nil
}

func contentFromPayload(payload map[string]any, mode event.CaptureMode) map[string]any {
	if mode != event.CaptureDetailed {
		return nil
	}
	content := map[string]any{}
	for _, key := range []string{"prompt", "message", "tool_input", "tool_output", "output", "error", "result", "final_response"} {
		if value, ok := payload[key].(string); ok && value != "" {
			content[key] = value
			return content
		}
	}
	return nil
}

func captureModeFor(cfg config.Config, source string) event.CaptureMode {
	if sourceConfig, ok := cfg.Sources[source]; ok && sourceConfig.CaptureMode != "" {
		return sourceConfig.CaptureMode
	}
	return event.CaptureMetadata
}

func unmappedEvent(source string, cfg config.Config, payload string) event.Event {
	now := time.Now().UTC()
	mode := captureModeFor(cfg, source)
	var decoded any
	if err := json.Unmarshal([]byte(payload), &decoded); err == nil {
		return event.Event{
			SchemaVersion: event.SchemaVersion1,
			EventID:       deriveUnmappedID(source, now, payload),
			SourceEventID: deriveUnmappedID(source, now, payload),
			Source:        source,
			SourceVersion: "hook",
			RunID:         source + ":unattributed",
			OccurredAt:    now,
			Type:          event.TypeSourceExtension,
			Status:        event.StatusUnknown,
			Capture:       map[string]any{"mode": string(mode), "unmapped": true},
			Attributes:    map[string]any{"extension_kind": "hook", "source_type": "unmapped"},
		}
	}
	return event.Event{
		SchemaVersion: event.SchemaVersion1,
		EventID:       deriveUnmappedID(source, now, payload),
		SourceEventID: deriveUnmappedID(source, now, payload),
		Source:        source,
		RunID:         source + ":unattributed",
		OccurredAt:    now,
		Type:          event.TypeSourceExtension,
		Status:        event.StatusUnknown,
		Capture:       map[string]any{"mode": string(mode), "unmapped": true},
		Attributes:    map[string]any{"extension_kind": "hook", "source_type": "unparsed"},
	}
}

func deriveUnmappedID(source string, at time.Time, payload string) string {
	sum := sha256.Sum256([]byte(source + at.UTC().Format(time.RFC3339Nano) + payload))
	return "hook_" + hex.EncodeToString(sum[:16])
}

func stringValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && value != "" {
			return value
		}
		if value, ok := payload[key].(float64); ok {
			return fmt.Sprintf("%.0f", value)
		}
	}
	return ""
}
