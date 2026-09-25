package export

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"traceboard/internal/event"
	"traceboard/internal/store"
)

func newTestDatabase(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure temp directory: %v", err)
	}
	database, err := store.Open(filepath.Join(directory, "traceboard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	events := []event.Event{
		{
			SchemaVersion: event.SchemaVersion1,
			EventID:       "evt_1", SourceEventID: "src_1", Source: "opencode", SourceVersion: "test",
			RunID: "run_export", OccurredAt: started, Type: event.TypeRunStarted, Status: event.StatusStarted,
			Capture: map[string]any{"mode": "detailed"}, Attributes: map[string]any{},
		},
		{
			SchemaVersion: event.SchemaVersion1,
			EventID:       "evt_2", SourceEventID: "src_2", Source: "opencode", SourceVersion: "test",
			RunID: "run_export", StepID: "step_1", OccurredAt: started.Add(time.Second),
			Type: event.TypeToolFailed, Status: event.StatusFailed,
			Capture:    map[string]any{"mode": "detailed"},
			Attributes: map[string]any{"tool_name": "bash"},
			Content:    map[string]any{"text": "the command exited 1"},
			Raw:        map[string]any{"exit_code": float64(1)},
		},
		{
			SchemaVersion: event.SchemaVersion1,
			EventID:       "evt_3", SourceEventID: "src_3", Source: "opencode", SourceVersion: "test",
			RunID: "run_export", OccurredAt: started.Add(2 * time.Second),
			Type: event.TypeRunFailed, Status: event.StatusFailed,
			Capture: map[string]any{"mode": "detailed"}, Attributes: map[string]any{},
		},
	}
	events[0].Content = map[string]any{"text": "refactor the collector"}
	if _, err := database.InsertEvents(context.Background(), events...); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return database
}

func TestExportJSONContainsOnlyTheRequestedRun(t *testing.T) {
	database := newTestDatabase(t)
	if _, err := database.InsertEvents(context.Background(), event.Event{
		SchemaVersion: event.SchemaVersion1,
		EventID:       "evt_other", SourceEventID: "src_other", Source: "opencode",
		RunID: "run_other", OccurredAt: time.Now().UTC(), Type: event.TypeRunStarted,
		Status: event.StatusStarted, Capture: map[string]any{"mode": "metadata"},
		Attributes: map[string]any{"marker": "unrelated-run"},
	}); err != nil {
		t.Fatalf("seed other run: %v", err)
	}

	path := filepath.Join(t.TempDir(), "run.json")
	if _, err := Run(context.Background(), database, Request{RunID: "run_export", Format: FormatJSON, Destination: path}); err != nil {
		t.Fatalf("export: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	var document Document
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if document.Run.ID != "run_export" || len(document.Events) != 3 {
		t.Fatalf("document = %+v", document)
	}
	if strings.Contains(string(contents), "unrelated-run") {
		t.Fatal("an unrelated run leaked into the export")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestExportMarkdownIsReadable(t *testing.T) {
	database := newTestDatabase(t)
	path := filepath.Join(t.TempDir(), "run.md")
	if _, err := Run(context.Background(), database, Request{RunID: "run_export", Format: FormatMarkdown, Destination: path}); err != nil {
		t.Fatalf("export: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(contents)
	for _, expected := range []string{
		"# Run run_export",
		"- Source: opencode",
		"- Status: failed",
		"## Timeline",
		"tool.failed",
		"refactor the collector",
		"## Steps",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("markdown export is missing %q:\n%s", expected, text)
		}
	}
}

func TestExportRawBundleIsAValidArchive(t *testing.T) {
	database := newTestDatabase(t)
	path := filepath.Join(t.TempDir(), "run.zip")
	if _, err := Run(context.Background(), database, Request{RunID: "run_export", Format: FormatRaw, Destination: path}); err != nil {
		t.Fatalf("export: %v", err)
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	defer reader.Close()

	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	if !names["summary.json"] {
		t.Fatalf("bundle entries = %v", names)
	}
	var rawEntries int
	for name := range names {
		if strings.HasPrefix(name, "raw/") {
			rawEntries++
		}
	}
	if rawEntries != 1 {
		t.Fatalf("expected exactly the one event with a raw payload, got %d", rawEntries)
	}
}

func TestExportRefusesToOverwriteWithoutForce(t *testing.T) {
	database := newTestDatabase(t)
	path := filepath.Join(t.TempDir(), "run.json")
	if _, err := Run(context.Background(), database, Request{RunID: "run_export", Format: FormatJSON, Destination: path}); err != nil {
		t.Fatalf("first export: %v", err)
	}
	if _, err := Run(context.Background(), database, Request{RunID: "run_export", Format: FormatJSON, Destination: path}); err == nil {
		t.Fatal("expected the second export to refuse")
	}
	if _, err := Run(context.Background(), database, Request{RunID: "run_export", Format: FormatJSON, Destination: path, Force: true}); err != nil {
		t.Fatalf("forced export: %v", err)
	}
}

func TestExportRejectsUnknownRunAndFormat(t *testing.T) {
	database := newTestDatabase(t)
	if _, err := Run(context.Background(), database, Request{RunID: "run_missing", Format: FormatJSON, Destination: filepath.Join(t.TempDir(), "x.json")}); err == nil {
		t.Fatal("expected an unknown run to fail")
	}
	if _, err := Run(context.Background(), database, Request{RunID: "run_export", Format: "pdf", Destination: filepath.Join(t.TempDir(), "x.pdf")}); err == nil {
		t.Fatal("expected an unsupported format to fail")
	}
	if _, err := Run(context.Background(), database, Request{RunID: "run_export", Format: FormatJSON}); err == nil {
		t.Fatal("expected a missing destination to fail")
	}
}

func TestCollectGathersStepsAndAlerts(t *testing.T) {
	database := newTestDatabase(t)
	if _, _, err := database.OpenAlert(context.Background(), store.Alert{
		Type: "run_failed", RunID: strPtr("run_export"), Message: "run failed",
	}); err != nil {
		t.Fatalf("open alert: %v", err)
	}
	document, err := Collect(context.Background(), database, "run_export")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(document.Steps) != 1 || document.Steps[0].ID != "step_1" {
		t.Fatalf("steps = %+v", document.Steps)
	}
	if len(document.Alerts) != 1 {
		t.Fatalf("alerts = %+v", document.Alerts)
	}
	if document.Run.CaptureModes[0] != event.CaptureDetailed {
		t.Fatalf("capture modes = %+v", document.Run.CaptureModes)
	}
}

func strPtr(value string) *string {
	return &value
}
