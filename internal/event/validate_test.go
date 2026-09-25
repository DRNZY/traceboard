package event

import (
	"strings"
	"testing"
	"time"
)

func TestValidateAcceptsSupportedEvent(t *testing.T) {
	e := validEvent()
	e.Capture = map[string]any{"mode": "detailed"}
	e.Content = map[string]any{"output": "done"}
	e.Raw = map[string]any{"provider": "example"}

	if err := Validate(e); err != nil {
		t.Fatalf("validate event: %v", err)
	}
}

func TestValidateRejectsUnsupportedSchemaVersion(t *testing.T) {
	e := validEvent()
	e.SchemaVersion = 2

	if err := Validate(e); err == nil || err.Error() != "unsupported schema version 2" {
		t.Fatalf("expected schema version error, got %v", err)
	}
}

func TestValidateRejectsMissingEventIdentity(t *testing.T) {
	tests := map[string]func(*Event){
		"source":      func(e *Event) { e.Source = "" },
		"run":         func(e *Event) { e.RunID = "" },
		"occurred at": func(e *Event) { e.OccurredAt = time.Time{} },
		"type":        func(e *Event) { e.Type = "" },
	}

	for name, remove := range tests {
		t.Run(name, func(t *testing.T) {
			e := validEvent()
			remove(&e)
			if err := Validate(e); err == nil || err.Error() != "missing required event identity" {
				t.Fatalf("expected identity error, got %v", err)
			}
		})
	}
}

func TestValidateRejectsInvalidStatus(t *testing.T) {
	e := validEvent()
	e.Status = Status("paused")

	if err := Validate(e); err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestValidateAcceptsSupportedStatuses(t *testing.T) {
	statuses := []Status{
		StatusStarted,
		StatusCompleted,
		StatusFailed,
		StatusCancelled,
		StatusIncomplete,
		StatusUnknown,
	}

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			e := validEvent()
			e.Status = status
			if err := Validate(e); err != nil {
				t.Fatalf("validate status %q: %v", status, err)
			}
		})
	}
}

func TestValidateRejectsInvalidCaptureMode(t *testing.T) {
	e := validEvent()
	e.Capture = map[string]any{"mode": "verbose"}

	if err := Validate(e); err == nil || !strings.Contains(err.Error(), "capture mode") {
		t.Fatalf("expected capture mode error, got %v", err)
	}
}

func TestValidateRejectsOversizedRawValue(t *testing.T) {
	e := validEvent()
	e.Raw = strings.Repeat("a", (1<<20)-1)

	if err := Validate(e); err == nil || !strings.Contains(err.Error(), "raw payload") {
		t.Fatalf("expected raw payload error, got %v", err)
	}
}

func TestValidateAcceptsRawValueAtLimit(t *testing.T) {
	e := validEvent()
	e.Raw = strings.Repeat("a", (1<<20)-2)

	if err := Validate(e); err != nil {
		t.Fatalf("validate raw payload at limit: %v", err)
	}
}

func TestValidateRejectsContentInOffMode(t *testing.T) {
	e := validEvent()
	e.Capture = map[string]any{"mode": "off"}
	e.Content = map[string]any{"prompt": "secret"}

	if err := Validate(e); err == nil || !strings.Contains(err.Error(), "off mode") {
		t.Fatalf("expected off mode error, got %v", err)
	}
}

func TestValidateRejectsNonNilEmptyContentInOffMode(t *testing.T) {
	e := validEvent()
	e.Capture = map[string]any{"mode": "off"}
	e.Content = map[string]any{}

	if err := Validate(e); err == nil || !strings.Contains(err.Error(), "off mode") {
		t.Fatalf("expected off mode error, got %v", err)
	}
}

func validEvent() Event {
	return Event{
		SchemaVersion: 1,
		EventID:       "evt_1",
		SourceEventID: "source_1",
		Source:        "opencode",
		SourceVersion: "1.0.0",
		RunID:         "run_1",
		StepID:        "step_1",
		OccurredAt:    time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
		Type:          "run.started",
		Status:        StatusStarted,
		Capture:       map[string]any{"mode": "metadata"},
		Attributes:    map[string]any{},
	}
}
