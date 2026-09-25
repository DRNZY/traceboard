package hooks

import (
	"testing"
	"time"

	"traceboard/internal/event"
)

var baseTime = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

func hookInput(source, name string) HookInput {
	return HookInput{
		Source:        source,
		SourceVersion: "1.0.0",
		EventName:     name,
		RunID:         "run_1",
		OccurredAt:    baseTime,
		Attributes:    map[string]any{},
	}
}

func TestEveryDeclaredSourceHasAnAdapter(t *testing.T) {
	for _, source := range SupportedSources {
		adapter, ok := For(source)
		if !ok {
			t.Fatalf("source %q has no adapter", source)
		}
		if adapter.Source() != source {
			t.Fatalf("adapter reports %q for %q", adapter.Source(), source)
		}
		if len(adapter.SupportedVersions()) == 0 {
			t.Fatalf("source %q declares no supported version", source)
		}
	}
}

func TestNormalizeRejectsIncompleteInput(t *testing.T) {
	if _, err := Normalize(hookInput(SourceOpenCode, "")); err == nil {
		t.Fatal("expected an error for a missing event name")
	}
	input := hookInput(SourceOpenCode, RunStart)
	input.RunID = ""
	if _, err := Normalize(input); err == nil {
		t.Fatal("expected an error for a missing run id")
	}
	unsupported := hookInput("not-a-source", RunStart)
	if _, err := Normalize(unsupported); err == nil {
		t.Fatal("expected an error for an unsupported source")
	}
}

func TestOpenCodeLifecycleMapping(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		status    event.Status
		stepBound bool
	}{
		{"session.created", event.TypeRunStarted, event.StatusStarted, false},
		{"session.completed", event.TypeRunCompleted, event.StatusCompleted, false},
		{"session.error", event.TypeRunFailed, event.StatusFailed, false},
		{"session.idle", event.TypeRunIncomplete, event.StatusIncomplete, false},
		{"message.updated", event.TypePromptReceived, event.StatusCompleted, false},
		{"tool.execute.before", event.TypeToolStarted, event.StatusStarted, true},
		{"tool.execute.after", event.TypeToolCompleted, event.StatusCompleted, true},
		{"file.changed", event.TypeFileChanged, event.StatusCompleted, false},
		{"permission.asked", event.TypePermissionRequested, event.StatusStarted, true},
		{"permission.replied", event.TypePermissionResolved, event.StatusCompleted, true},
		{"session.compacted", event.TypeContextCompacted, event.StatusCompleted, false},
	}
	for _, testCase := range cases {
		input := hookInput(SourceOpenCode, testCase.name)
		input.StepID = "step_1"
		mapping, err := Normalize(input)
		if err != nil {
			t.Fatalf("%s: %v", testCase.name, err)
		}
		if len(mapping.Events) != 1 {
			t.Fatalf("%s produced %d events", testCase.name, len(mapping.Events))
		}
		output := mapping.Events[0]
		if output.Type != testCase.eventType || output.Status != testCase.status {
			t.Fatalf("%s = %s/%s, want %s/%s", testCase.name, output.Type, output.Status, testCase.eventType, testCase.status)
		}
		if testCase.stepBound && output.StepID != "step_1" {
			t.Fatalf("%s lost its step id: %+v", testCase.name, output)
		}
		if !testCase.stepBound && output.StepID != "" {
			t.Fatalf("%s must not be step bound, got %q", testCase.name, output.StepID)
		}
	}
}

func TestClaudeCodeLifecycleMapping(t *testing.T) {
	cases := map[string]string{
		"SessionStart":       event.TypeRunStarted,
		"SessionEnd":         event.TypeRunCompleted,
		"UserPromptSubmit":   event.TypePromptReceived,
		"PreToolUse":         event.TypeToolStarted,
		"PostToolUse":        event.TypeToolCompleted,
		"PostToolUseFailure": event.TypeToolFailed,
		"SubagentStart":      event.TypeSubagentStarted,
		"SubagentStop":       event.TypeSubagentCompleted,
		"PreCompact":         event.TypeContextCompacted,
		"PermissionRequest":  event.TypePermissionRequested,
	}
	for name, want := range cases {
		mapping, err := Normalize(hookInput(SourceClaudeCode, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if mapping.Events[0].Type != want {
			t.Fatalf("%s = %s, want %s", name, mapping.Events[0].Type, want)
		}
	}
}

func TestCodexLifecycleMapping(t *testing.T) {
	cases := map[string]string{
		"session.start":  event.TypeRunStarted,
		"session.end":    event.TypeRunCompleted,
		"turn.start":     event.TypePromptReceived,
		"tool.start":     event.TypeToolStarted,
		"tool.end":       event.TypeToolCompleted,
		"tool.failed":    event.TypeToolFailed,
		"permission.ask": event.TypePermissionRequested,
		"compaction":     event.TypeContextCompacted,
		"subagent.start": event.TypeSubagentStarted,
		"subagent.end":   event.TypeSubagentCompleted,
		"error":          event.TypeRunFailed,
	}
	for name, want := range cases {
		mapping, err := Normalize(hookInput(SourceCodex, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if mapping.Events[0].Type != want {
			t.Fatalf("%s = %s, want %s", name, mapping.Events[0].Type, want)
		}
	}
}

func TestAntigravityAndGeminiCLIMapping(t *testing.T) {
	cases := []struct {
		source string
		name   string
		want   string
	}{
		{SourceAntigravity, "conversation.start", event.TypeRunStarted},
		{SourceAntigravity, "execution.end", event.TypeRunCompleted},
		{SourceAntigravity, "tool.failed", event.TypeToolFailed},
		{SourceAntigravity, "context.compacted", event.TypeContextCompacted},
		{SourceGeminiCLI, "turn.start", event.TypePromptReceived},
		{SourceGeminiCLI, "turn.error", event.TypeRunFailed},
		{SourceGeminiCLI, "tool.call", event.TypeToolStarted},
		{SourceGeminiCLI, "context.compact", event.TypeContextCompacted},
	}
	for _, testCase := range cases {
		mapping, err := Normalize(hookInput(testCase.source, testCase.name))
		if err != nil {
			t.Fatalf("%s %s: %v", testCase.source, testCase.name, err)
		}
		if mapping.Events[0].Type != testCase.want {
			t.Fatalf("%s %s = %s, want %s", testCase.source, testCase.name, mapping.Events[0].Type, testCase.want)
		}
	}
}

func TestCommandFailureMapsToTheCommandFailedType(t *testing.T) {
	mapping, err := Normalize(hookInput(SourceCodex, "command.failed"))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if mapping.Events[0].Type != "command.failed" || mapping.Events[0].Status != event.StatusFailed {
		t.Fatalf("command failure = %s/%s", mapping.Events[0].Type, mapping.Events[0].Status)
	}
}

func TestUnknownLifecycleBecomesAnExtensionNotAnError(t *testing.T) {
	input := hookInput(SourceOpenCode, "vendor.new.event")
	input.Status = event.StatusFailed
	mapping, err := Normalize(input)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	output := mapping.Events[0]
	if output.Type != event.TypeSourceExtension {
		t.Fatalf("type = %q, want a source extension", output.Type)
	}
	if output.Status == event.StatusFailed {
		t.Fatal("an unknown benign event must not be recorded as an error")
	}
	if output.Attributes["source_type"] != "vendor.new.event" {
		t.Fatalf("source_type = %+v", output.Attributes["source_type"])
	}
	if output.Attributes["extension_kind"] != "hook" {
		t.Fatalf("extension_kind = %+v", output.Attributes["extension_kind"])
	}
	if len(mapping.Notes) == 0 {
		t.Fatal("an unmapped lifecycle name must leave a note about the gap")
	}
}

func TestIdentifiersAreDeterministicPerSourceEvent(t *testing.T) {
	first, err := Normalize(hookInput(SourceOpenCode, ToolBefore))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	second, err := Normalize(hookInput(SourceOpenCode, ToolBefore))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if first.Events[0].SourceEventID != second.Events[0].SourceEventID {
		t.Fatal("the same source event must derive the same source event id")
	}
	third, err := Normalize(hookInput(SourceOpenCode, ToolAfter))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if first.Events[0].SourceEventID == third.Events[0].SourceEventID {
		t.Fatal("different lifecycle events must derive different source event ids")
	}
}

func TestParentStepIsCarriedThrough(t *testing.T) {
	input := hookInput(SourceCodex, "tool.start")
	input.StepID = "step_child"
	input.ParentStepID = "step_parent"
	mapping, err := Normalize(input)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if mapping.Events[0].ParentStepID != "step_parent" {
		t.Fatalf("parent step = %q", mapping.Events[0].ParentStepID)
	}
}

func TestSourceAttributesArePreservedWithNamespacing(t *testing.T) {
	input := hookInput(SourceOpenCode, ToolBefore)
	input.Attributes = map[string]any{"tool": "bash"}
	input.Raw = map[string]any{"sessionID": "sess_1", "callID": "call_1"}
	mapping, err := Normalize(input)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	attributes := mapping.Events[0].Attributes
	if attributes["tool"] != "bash" {
		t.Fatalf("tool = %+v", attributes["tool"])
	}
	if attributes["source.sessionID"] != "sess_1" || attributes["source.callID"] != "call_1" {
		t.Fatalf("namespaced source attributes = %+v", attributes)
	}
	if attributes["source_event_name"] != ToolBefore {
		t.Fatalf("source_event_name = %+v", attributes["source_event_name"])
	}
}

func TestMappingReportsSourceAndSupportedVersion(t *testing.T) {
	mapping, err := Normalize(hookInput(SourceOpenCode, RunStart))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if mapping.Source != SourceOpenCode {
		t.Fatalf("source = %q", mapping.Source)
	}
	if mapping.SourceVersion != "1.0.0" {
		t.Fatalf("source version = %q", mapping.SourceVersion)
	}
	if mapping.SupportedSince == "" {
		t.Fatal("a mapping must report the version it was tested against")
	}
}
