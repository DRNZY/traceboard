package ingest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"traceboard/internal/redact"
)

func newTestSpool(t *testing.T, limit int64, maxAge time.Duration) (*Spool, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "spool")
	redactor, err := redact.New(redact.Options{})
	if err != nil {
		t.Fatalf("redactor: %v", err)
	}
	spool, err := NewSpool(root, redactor, limit, maxAge)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	return spool, root
}

func payload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return encoded
}

func TestSpoolCreatesUserOnlyFiles(t *testing.T) {
	spool, root := newTestSpool(t, DefaultSpoolLimitBytes, DefaultSpoolAge)
	if err := spool.Append("opencode", payload(t, map[string]any{"run_id": "run_1"})); err != nil {
		t.Fatalf("append: %v", err)
	}
	directoryInfo, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat directory: %v", err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("spool directory mode = %04o, want 0700", directoryInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(filepath.Join(root, "opencode.ndjson"))
	if err != nil {
		t.Fatalf("stat spool file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("spool file mode = %04o, want 0600", fileInfo.Mode().Perm())
	}
}

func TestSpoolRedactsBeforeWriting(t *testing.T) {
	spool, root := newTestSpool(t, DefaultSpoolLimitBytes, DefaultSpoolAge)
	err := spool.Append("opencode", payload(t, map[string]any{
		"run_id": "run_1",
		"token":  "Bearer abcdefghijklmnopqrstuvwxyz",
		"key":    "sk-abcdefghijklmnopqrstuvwx",
	}))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "opencode.ndjson"))
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	text := string(contents)
	for _, secret := range []string{"abcdefghijklmnopqrstuvwxyz", "sk-abcdefghijklmnopqrstuvwx"} {
		if strings.Contains(text, secret) {
			t.Fatalf("secret %q was written to the spool: %s", secret, text)
		}
	}
}

func TestSpoolDrainsInOrder(t *testing.T) {
	spool, _ := newTestSpool(t, DefaultSpoolLimitBytes, DefaultSpoolAge)
	for index := 0; index < 5; index++ {
		if err := spool.Append("opencode", payload(t, map[string]any{"index": index})); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	consumed := make([]int, 0, 5)
	result, err := spool.Drain(context.Background(), func(raw json.RawMessage) error {
		var decoded struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		consumed = append(consumed, decoded.Index)
		return nil
	})
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if result.Drained != 5 {
		t.Fatalf("drained = %d", result.Drained)
	}
	for index, value := range consumed {
		if value != index {
			t.Fatalf("drain order = %v", consumed)
		}
	}
}

func TestSpoolDrainKeepsRejectedEvents(t *testing.T) {
	spool, _ := newTestSpool(t, DefaultSpoolLimitBytes, DefaultSpoolAge)
	for index := 0; index < 3; index++ {
		if err := spool.Append("opencode", payload(t, map[string]any{"index": index})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	result, err := spool.Drain(context.Background(), func(json.RawMessage) error {
		return errStoreFailed
	})
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if result.Drained != 0 || result.Failed != 3 || result.Remaining != 3 {
		t.Fatalf("result = %+v", result)
	}
	state, err := spool.State("opencode")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Events != 3 {
		t.Fatalf("state = %+v", state)
	}
}

func TestSpoolDrainIsIdempotentAfterRestart(t *testing.T) {
	spool, root := newTestSpool(t, DefaultSpoolLimitBytes, DefaultSpoolAge)
	_ = spool.Append("opencode", payload(t, map[string]any{"index": 1}), payload(t, map[string]any{"index": 2}))

	// A second Spool over the same directory models a collector restart.
	redactor, _ := redact.New(redact.Options{})
	reopened, err := NewSpool(root, redactor, DefaultSpoolLimitBytes, DefaultSpoolAge)
	if err != nil {
		t.Fatalf("reopen spool: %v", err)
	}
	first, err := reopened.Drain(context.Background(), func(json.RawMessage) error { return nil })
	if err != nil {
		t.Fatalf("first drain: %v", err)
	}
	second, err := reopened.Drain(context.Background(), func(json.RawMessage) error { return nil })
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if first.Drained != 2 || second.Drained != 0 {
		t.Fatalf("a restart must not replay: %+v then %+v", first, second)
	}
}

func TestSpoolEnforcesTheSizeCeilingAndKeepsNewest(t *testing.T) {
	spool, _ := newTestSpool(t, 4096, DefaultSpoolAge)
	blob := strings.Repeat("x", 512)
	for index := 0; index < 40; index++ {
		if err := spool.Append("opencode", payload(t, map[string]any{"index": index, "blob": blob})); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	state, err := spool.State("opencode")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Bytes > 4096 {
		t.Fatalf("spool grew to %d bytes past the 4096 ceiling", state.Bytes)
	}
	if state.Dropped == 0 {
		t.Fatal("dropping data at the ceiling must be reported")
	}
	if !state.AtRisk || state.AtRiskSince == nil {
		t.Fatalf("a full spool must report loss risk: %+v", state)
	}

	entries := make([]int, 0)
	_, _ = spool.Drain(context.Background(), func(raw json.RawMessage) error {
		var decoded struct {
			Index int `json:"index"`
		}
		_ = json.Unmarshal(raw, &decoded)
		entries = append(entries, decoded.Index)
		return nil
	})
	if len(entries) == 0 {
		t.Fatal("the spool was emptied entirely instead of keeping the newest data")
	}
	if entries[len(entries)-1] != 39 {
		t.Fatalf("the newest event was dropped: kept %v", entries)
	}
}

func TestSpoolExpireRemovesAgedEntries(t *testing.T) {
	spool, _ := newTestSpool(t, DefaultSpoolLimitBytes, 24*time.Hour)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	spool.SetClock(func() time.Time { return now })
	_ = spool.Append("opencode", payload(t, map[string]any{"old": true}))

	spool.SetClock(func() time.Time { return now.Add(25 * time.Hour) })
	_ = spool.Append("opencode", payload(t, map[string]any{"new": true}))

	removed, err := spool.Expire()
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	state, err := spool.State("opencode")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.Events != 1 {
		t.Fatalf("events after expiry = %d", state.Events)
	}
}

func TestSpoolRejectsOversizedAgeAndLimit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	redactor, _ := redact.New(redact.Options{})
	spool, err := NewSpool(root, redactor, 1<<40, 365*24*time.Hour)
	if err != nil {
		t.Fatalf("new spool: %v", err)
	}
	state, err := spool.State("opencode")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if state.LimitBytes != DefaultSpoolLimitBytes {
		t.Fatalf("limit = %d, want the compiled ceiling %d", state.LimitBytes, int64(DefaultSpoolLimitBytes))
	}
}

func TestSpoolRejectsUnsafeSourceNames(t *testing.T) {
	spool, root := newTestSpool(t, DefaultSpoolLimitBytes, DefaultSpoolAge)
	if err := spool.Append("../../escape", payload(t, map[string]any{"run_id": "run_1"})); err != nil {
		t.Fatalf("append: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "..") || strings.Contains(entry.Name(), "/") {
			t.Fatalf("unsafe spool file name: %q", entry.Name())
		}
	}
}

func TestSpoolClearRemovesOnlyTheNamedSource(t *testing.T) {
	spool, _ := newTestSpool(t, DefaultSpoolLimitBytes, DefaultSpoolAge)
	_ = spool.Append("opencode", payload(t, map[string]any{"a": 1}))
	_ = spool.Append("codex", payload(t, map[string]any{"b": 2}))
	if err := spool.Clear("opencode"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	opencode, _ := spool.State("opencode")
	codex, _ := spool.State("codex")
	if opencode.Events != 0 || codex.Events != 1 {
		t.Fatalf("opencode = %+v codex = %+v", opencode, codex)
	}
}
