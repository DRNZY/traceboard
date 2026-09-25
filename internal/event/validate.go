package event

import (
	"encoding/json"
	"errors"
	"fmt"
)

const maxRawPayloadBytes = 1 << 20

func Validate(e Event) error {
	if e.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema version %d", e.SchemaVersion)
	}
	if e.Source == "" || e.RunID == "" || e.OccurredAt.IsZero() || e.Type == "" {
		return errors.New("missing required event identity")
	}
	if !validStatus(e.Status) {
		return fmt.Errorf("invalid status %q", e.Status)
	}
	mode, err := captureMode(e.Capture)
	if err != nil {
		return err
	}
	if mode == CaptureOff && e.Content != nil {
		return errors.New("content is not allowed in off mode")
	}
	raw, err := json.Marshal(e.Raw)
	if err != nil {
		return fmt.Errorf("marshal raw payload: %w", err)
	}
	if len(raw) > maxRawPayloadBytes {
		return fmt.Errorf("raw payload exceeds %d bytes", maxRawPayloadBytes)
	}
	return nil
}

func validStatus(status Status) bool {
	switch status {
	case StatusStarted, StatusCompleted, StatusFailed, StatusCancelled, StatusIncomplete, StatusUnknown:
		return true
	default:
		return false
	}
}

func captureMode(capture map[string]any) (CaptureMode, error) {
	value, ok := capture["mode"]
	if !ok {
		return CaptureMetadata, nil
	}
	modeText, ok := value.(string)
	if !ok {
		return "", errors.New("capture mode must be a string")
	}
	mode := CaptureMode(modeText)
	switch mode {
	case CaptureOff, CaptureMetadata, CaptureDetailed:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid capture mode %q", mode)
	}
}
