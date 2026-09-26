//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"traceboard/internal/event"
	"traceboard/internal/store"
)

// A committed event must be visible to an already-open dashboard without a
// reload, and it must arrive only after the store commit.
func TestCommittedRunReachesAnOpenDashboard(t *testing.T) {
	instance := newHarness(t, "live")
	cookie := instance.sessionCookie()
	connection := dialWebSocket(t, instance.baseURL, cookie)

	// The first frame states the stream position before anything is published.
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, hello, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read the stream hello frame: %v", err)
	}
	var opening liveFrame
	if err := json.Unmarshal(hello, &opening); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	if opening.Kind != "hello" {
		t.Fatalf("first frame = %s, want hello", opening.Kind)
	}

	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	instance.ingest(
		integrationEvent(1, "run_live", event.TypeRunStarted, event.StatusStarted, started),
		integrationEvent(2, "run_live", event.TypeRunCompleted, event.StatusCompleted, started.Add(time.Second)),
	)

	deadline := time.Now().Add(5 * time.Second)
	sawRun := false
	for time.Now().Before(deadline) && !sawRun {
		_ = connection.SetReadDeadline(deadline)
		_, raw, err := connection.ReadMessage()
		if err != nil {
			break
		}
		var frame liveFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatalf("decode frame: %v", err)
		}
		if frame.Kind == "run.updated" && frame.Run != nil && frame.Run["id"] == "run_live" {
			sawRun = true
			if frame.StreamID <= opening.StreamID {
				t.Fatalf("stream id %d did not advance past %d", frame.StreamID, opening.StreamID)
			}
		}
	}
	if !sawRun {
		t.Fatal("the committed run never reached the open stream")
	}

	// The same run is readable over the API with a coherent summary.
	status, body := instance.get(cookie, "/api/v1/runs/run_live")
	if status != http.StatusOK {
		t.Fatalf("run detail = %d", status)
	}
	var detail struct {
		Run store.Run `json:"run"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode run detail: %v", err)
	}
	if detail.Run.Status != event.StatusCompleted || detail.Run.EventCount != 2 {
		t.Fatalf("run = %+v", detail.Run)
	}
}

type liveFrame struct {
	StreamID int64          `json:"stream_id"`
	Kind     string         `json:"kind"`
	RunID    string         `json:"run_id"`
	Sequence int64          `json:"sequence"`
	Run      map[string]any `json:"run"`
}

// OTLP and the JSON endpoint share one write path, so a partial failure in one
// cannot corrupt the other and both land in the same transaction.
func TestOTLPAndJSONIngestShareOneWritePath(t *testing.T) {
	instance := newHarness(t, "paths")
	cookie := instance.sessionCookie()
	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	traces := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174","name":"chat","kind":3,"startTimeUnixNano":"1750000000000000000","endTimeUnixNano":"1750000001000000000","status":{"code":1},"attributes":[{"key":"gen_ai.conversation.id","value":{"stringValue":"conv_int"}}]}]}]}]}`
	request, err := http.NewRequest(http.MethodPost, instance.baseURL+"/v1/traces", strings.NewReader(traces))
	if err != nil {
		t.Fatalf("build otlp request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+instance.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("otlp ingest: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("otlp = %d: %s", response.StatusCode, readBody(t, response))
	}

	instance.ingest(integrationEvent(1, "run_json", event.TypeRunStarted, event.StatusStarted, started))

	status, body := instance.get(cookie, "/api/v1/runs")
	if status != http.StatusOK {
		t.Fatalf("run list = %d", status)
	}
	var page store.RunPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode run list: %v", err)
	}
	sources := map[string]bool{}
	for _, run := range page.Runs {
		sources[run.Source] = true
	}
	if !sources["claude-code"] || !sources["opencode"] {
		t.Fatalf("both write paths must reach the same store, got %v", sources)
	}
}

// A malformed event is quarantined with a redacted reason, and the valid events
// in the same batch are still committed.
func TestPartialIngestQuarantinesOnlyTheInvalidEvent(t *testing.T) {
	instance := newHarness(t, "quarantine")
	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	valid := integrationEvent(1, "run_mixed", event.TypeRunStarted, event.StatusStarted, started)
	invalid := integrationEvent(2, "run_mixed", event.TypeRunStarted, event.StatusStarted, started)
	invalid.SchemaVersion = 99
	invalid.Attributes = map[string]any{"leak": "sk-abcdefghijklmnopqrstuvwx"}

	result := instance.ingest(valid, invalid)
	if result.Accepted != 1 || result.Quarantined != 1 {
		t.Fatalf("result = %+v", result)
	}

	entries, err := instance.store.ListQuarantine(context.Background(), 10)
	if err != nil {
		t.Fatalf("list quarantine: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("quarantine entries = %+v", entries)
	}
	if strings.Contains(entries[0].Reason, "sk-abcdefghijklmnopqrstuvwx") ||
		strings.Contains(entries[0].Payload, "sk-abcdefghijklmnopqrstuvwx") {
		t.Fatalf("the quarantined payload was not redacted: %+v", entries[0])
	}
}

// A run with no terminal event stays active, and the operator-facing state
// reports it as such rather than inventing a failure.
func TestRunWithoutATerminalEventStaysActive(t *testing.T) {
	instance := newHarness(t, "partial")
	cookie := instance.sessionCookie()
	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	instance.ingest(
		integrationEvent(1, "run_open", event.TypeRunStarted, event.StatusStarted, started),
		integrationEvent(2, "run_open", event.TypeToolStarted, event.StatusStarted, started.Add(time.Second)),
	)

	status, body := instance.get(cookie, "/api/v1/runs/run_open")
	if status != http.StatusOK {
		t.Fatalf("run detail = %d", status)
	}
	var detail struct {
		Run store.Run `json:"run"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.Run.Status != event.StatusStarted {
		t.Fatalf("status = %s, want started", detail.Run.Status)
	}
	if detail.Run.EndedAt != nil {
		t.Fatalf("an open run must report no end time: %v", detail.Run.EndedAt)
	}
}

// Database and configuration files are created with user-only permissions.
func TestLocalFilesAreUserOnly(t *testing.T) {
	instance := newHarness(t, "permissions")
	instance.ingest(integrationEvent(1, "run_perm", event.TypeRunStarted, event.StatusStarted,
		time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)))

	for _, path := range []string{instance.configPath, strings.Replace(instance.configPath, "config.json", "traceboard.db", 1)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %04o, want 0600", filepath.Base(path), info.Mode().Perm())
		}
	}
}
