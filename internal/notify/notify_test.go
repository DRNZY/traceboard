package notify

import (
	"context"
	"strings"
	"testing"

	"traceboard/internal/store"
)

func alert() store.Alert {
	return store.Alert{
		ID:      "run_failed:run_1",
		Type:    "run_failed",
		RunID:   strPtr("run_1"),
		Message: "run run_1 failed: fix the ingest pipeline",
	}
}

func TestNotifierBuildsFixedArgumentVector(t *testing.T) {
	recorder := &Recorder{}
	notifier := NewRecordingNotifier(Config{Enabled: true, Executable: "/usr/bin/notify-send"}, recorder)
	if err := notifier.Notify(context.Background(), alert()); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(recorder.Calls) != 1 {
		t.Fatalf("calls = %d", len(recorder.Calls))
	}
	call := recorder.Calls[0]
	if call.Name != "/usr/bin/notify-send" {
		t.Fatalf("executable = %q", call.Name)
	}
	arguments := strings.Join(call.Arguments, " ")
	for _, expected := range []string{"--app-name Traceboard", "--urgency critical", "Run run_1", "run run_1 failed: fix the ingest pipeline"} {
		if !strings.Contains(arguments, expected) {
			t.Fatalf("arguments = %v, want %q", call.Arguments, expected)
		}
	}
	if strings.Contains(arguments, "Bearer ") || strings.Contains(arguments, "sk-") {
		t.Fatalf("notification arguments contain credential-shaped text: %v", call.Arguments)
	}
}

func TestUrgencyPerAlertType(t *testing.T) {
	cases := map[string]string{
		"run_failed":          "critical",
		"run_stalled":         "normal",
		"source_disconnected": "normal",
		"run_incomplete":      "low",
	}
	for alertType, want := range cases {
		if got := urgency(alertType); got != want {
			t.Fatalf("urgency(%q) = %q, want %q", alertType, got, want)
		}
	}
}

func TestDisabledNotifierMakesNoCall(t *testing.T) {
	recorder := &Recorder{}
	notifier := NewRecordingNotifier(Config{Enabled: false, Executable: "/usr/bin/notify-send"}, recorder)
	if err := notifier.Notify(context.Background(), alert()); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(recorder.Calls) != 0 {
		t.Fatalf("a disabled notifier made %d calls", len(recorder.Calls))
	}
}

func TestMissingDesktopToolIsNotAnError(t *testing.T) {
	notifier := NewNotifier(Config{Enabled: true, Executable: ""})
	if err := notifier.Notify(context.Background(), alert()); err != nil {
		t.Fatalf("a missing notification tool must not fail: %v", err)
	}
}

func TestBodyStripsControlCharactersAndBoundsLength(t *testing.T) {
	long := store.Alert{Type: "run_failed", Message: strings.Repeat("x", 400) + "\n\x00 injected"}
	body := sanitize(long.Message)
	if strings.ContainsAny(body, "\n\x00") {
		t.Fatalf("body kept a control character: %q", body)
	}
	if len([]rune(body)) > 241 {
		t.Fatalf("body length = %d, want a bounded summary", len([]rune(body)))
	}
}

func TestBodyNeverIncludesRawToolOutput(t *testing.T) {
	// The notifier only ever reads Alert.Message. It has no path to the run's
	// captured content, so a captured tool body cannot reach a notification.
	notifier := NewNotifier(Config{Enabled: true})
	arguments := notifier.arguments(alert())
	body := arguments[len(arguments)-1]
	if strings.Contains(body, "{") || strings.Contains(body, `"`) {
		t.Fatalf("body looks like captured content: %q", body)
	}
	if strings.Contains(strings.Join(arguments, " "), "Bearer ") {
		t.Fatalf("arguments carry credential-shaped text: %v", arguments)
	}
}

func TestSummaryNamesTheTarget(t *testing.T) {
	if got := summary(alert()); got != "Run run_1" {
		t.Fatalf("run summary = %q", got)
	}
	if got := summary(store.Alert{Type: "source_disconnected", Source: strPtr("opencode")}); got != "Source opencode" {
		t.Fatalf("source summary = %q", got)
	}
	if got := summary(store.Alert{Type: "run_stalled"}); got != "Traceboard" {
		t.Fatalf("default summary = %q", got)
	}
}

func sanitize(value string) string {
	return body(store.Alert{Message: value})
}

func strPtr(value string) *string {
	return &value
}
