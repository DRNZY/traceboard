package configure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"traceboard/internal/event"
	"traceboard/internal/hooks"
)

func newTestHome(t *testing.T) (string, Options) {
	t.Helper()
	home := t.TempDir()
	options := Options{
		Home:       home,
		ConfigDir:  filepath.Join(home, ".config", "traceboard"),
		BinaryPath: "/usr/local/bin/traceboard",
	}
	return home, options
}

func TestApplyRejectsUnsupportedSourceAndMode(t *testing.T) {
	home, options := newTestHome(t)
	if _, err := Apply("not-a-source", home, event.CaptureMetadata, options); err == nil {
		t.Fatal("expected an unsupported source to be rejected")
	}
	if _, err := Apply(hooks.SourceOpenCode, home, event.CaptureMode("everything"), options); err == nil {
		t.Fatal("expected an invalid capture mode to be rejected")
	}
}

func TestApplyWritesEveryDeclaredSource(t *testing.T) {
	for _, source := range hooks.Sources() {
		home, options := newTestHome(t)
		set, err := Apply(source, home, event.CaptureMetadata, options)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if set.Source != source || len(set.Changes) == 0 {
			t.Fatalf("%s produced %+v", source, set)
		}
		if len(set.ManualSteps) == 0 {
			t.Fatalf("%s produced no manual trust or restart step", source)
		}
	}
}

func TestApplyBacksUpAndMergesWithoutLosingUnknownKeys(t *testing.T) {
	home, options := newTestHome(t)
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := `{"theme":"dark","hooks":{"Notification":[{"matcher":"*","hooks":[{"type":"command","command":"echo existing"}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := Apply(hooks.SourceClaudeCode, home, event.CaptureMetadata, options); err != nil {
		t.Fatalf("apply: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(contents, &merged); err != nil {
		t.Fatalf("the merged file is not valid JSON: %v\n%s", err, contents)
	}
	if merged["theme"] != "dark" {
		t.Fatalf("an unrelated key was lost: %v", merged)
	}
	hooksValue, ok := merged["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks = %v", merged["hooks"])
	}
	if _, exists := hooksValue["Notification"]; !exists {
		t.Fatalf("an existing hook was lost: %v", hooksValue)
	}
	if _, exists := hooksValue["PreToolUse"]; !exists {
		t.Fatalf("the Traceboard hook was not added: %v", hooksValue)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	var backup string
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".traceboard-backup-") {
			backup = entry.Name()
		}
	}
	if backup == "" {
		t.Fatal("no backup was created before editing an existing file")
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	home, options := newTestHome(t)
	if _, err := Apply(hooks.SourceCodex, home, event.CaptureMetadata, options); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	path := filepath.Join(home, ".codex", "hooks.json")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := Apply(hooks.SourceCodex, home, event.CaptureMetadata, options); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var firstDoc, secondDoc map[string]any
	if err := json.Unmarshal(first, &firstDoc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := json.Unmarshal(second, &secondDoc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(firstDoc) != len(secondDoc) {
		t.Fatalf("a repeated apply changed the file: %v vs %v", firstDoc, secondDoc)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	home, options := newTestHome(t)
	options.DryRun = true
	set, err := Apply(hooks.SourceCodex, home, event.CaptureDetailed, options)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !set.DryRun {
		t.Fatal("the change set is not marked as a dry run")
	}
	for _, change := range set.Changes {
		if change.Applied {
			t.Fatalf("a dry run reported an applied change: %+v", change)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Fatal("a dry run wrote a file")
	}
}

func TestRefusesPathsOutsideARecognizedRoot(t *testing.T) {
	home, options := newTestHome(t)
	outside := filepath.Join(t.TempDir(), "not-an-agent", "settings.json")
	if err := mergeJSON(outside, options, &ChangeSet{}, map[string]any{"a": 1}, "test"); err == nil {
		t.Fatal("expected a write outside a recognized root to be refused")
	}
	if IsRecognizedRoot(home, outside) {
		t.Fatal("a path outside every root was reported as recognized")
	}
	if !IsRecognizedRoot(home, filepath.Join(home, ".claude", "settings.json")) {
		t.Fatal("a real agent configuration path was not recognized")
	}
}

func TestRejectsInvalidExistingJSON(t *testing.T) {
	home, options := newTestHome(t)
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Apply(hooks.SourceCodex, home, event.CaptureMetadata, options); err == nil {
		t.Fatal("expected invalid existing JSON to be rejected rather than overwritten")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(contents) != "{not json" {
		t.Fatalf("the invalid file was modified: %q", contents)
	}
}

func TestHookCommandsPointAtThisBinary(t *testing.T) {
	home, options := newTestHome(t)
	if _, err := Apply(hooks.SourceCodex, home, event.CaptureMetadata, options); err != nil {
		t.Fatalf("apply: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(contents), "/usr/local/bin/traceboard hook codex") {
		t.Fatalf("hooks.json = %s", contents)
	}
}

func TestPrintedPluginRedactsAndTimesOut(t *testing.T) {
	var builder strings.Builder
	if err := PrintPlugin(&builder); err != nil {
		t.Fatalf("print plugin: %v", err)
	}
	source := builder.String()
	for _, expected := range []string{"TIMEOUT_MS = 750", "redact", "SECRET_PATTERNS", "export const TraceboardPlugin", "TRACEBOARD_INGEST_TOKEN"} {
		if !strings.Contains(source, expected) {
			t.Fatalf("the plugin is missing %q", expected)
		}
	}
	if strings.Contains(source, "http://") && strings.Contains(source, "https://opencode.ai/plugin") {
		t.Fatal("the plugin reaches an external endpoint")
	}
	if !strings.Contains(source, `const ENDPOINT = process.env.TRACEBOARD_INGEST_URL ?? "http://127.0.0.1:47821/api/v1/events"`) {
		t.Fatal("the plugin default endpoint is not the loopback collector")
	}
}

func TestGeminiCLIPointsAtTheLocalOTLPEndpoint(t *testing.T) {
	home, options := newTestHome(t)
	if _, err := Apply(hooks.SourceGeminiCLI, home, event.CaptureMetadata, options); err != nil {
		t.Fatalf("apply: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(contents), "http://127.0.0.1:47821") {
		t.Fatalf("settings = %s", contents)
	}
	if strings.Contains(string(contents), "https://") {
		t.Fatalf("settings reference an external endpoint: %s", contents)
	}
}
