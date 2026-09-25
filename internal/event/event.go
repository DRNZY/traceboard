package event

import "time"

type CaptureMode string

const (
	CaptureOff      CaptureMode = "off"
	CaptureMetadata CaptureMode = "metadata"
	CaptureDetailed CaptureMode = "detailed"
)

type Status string

const (
	StatusStarted    Status = "started"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
	StatusIncomplete Status = "incomplete"
	StatusUnknown    Status = "unknown"
)

type Event struct {
	SchemaVersion  int            `json:"schema_version"`
	EventID        string         `json:"event_id"`
	SourceEventID  string         `json:"source_event_id"`
	Source         string         `json:"source"`
	SourceVersion  string         `json:"source_version"`
	RunID          string         `json:"run_id"`
	StepID         string         `json:"step_id,omitempty"`
	ParentStepID   string         `json:"parent_step_id,omitempty"`
	SourceSequence *int64         `json:"source_sequence,omitempty"`
	OccurredAt     time.Time      `json:"occurred_at"`
	Type           string         `json:"type"`
	Status         Status         `json:"status"`
	Capture        map[string]any `json:"capture"`
	Attributes     map[string]any `json:"attributes"`
	Content        map[string]any `json:"content,omitempty"`
	Raw            any            `json:"raw,omitempty"`
}

type Batch struct {
	Events []Event `json:"events"`
}

// SchemaVersion1 is the Traceboard event contract version this build accepts.
const SchemaVersion1 = 1

// Event types defined by the version one contract. A source-specific event with
// no safe normalized equivalent is stored as SourceExtension instead of being
// forced into one of these categories.
const (
	TypeRunStarted          = "run.started"
	TypeRunCompleted        = "run.completed"
	TypeRunFailed           = "run.failed"
	TypeRunCancelled        = "run.cancelled"
	TypeRunIncomplete       = "run.incomplete"
	TypePromptReceived      = "prompt.received"
	TypeModelRequested      = "model.requested"
	TypeModelCompleted      = "model.completed"
	TypeToolStarted         = "tool.started"
	TypeToolCompleted       = "tool.completed"
	TypeToolFailed          = "tool.failed"
	TypeFileChanged         = "file.changed"
	TypeCommandStarted      = "command.started"
	TypeCommandCompleted    = "command.completed"
	TypeCommandFailed       = "command.failed"
	TypePermissionRequested = "permission.requested"
	TypePermissionResolved  = "permission.resolved"
	TypeSubagentStarted     = "subagent.started"
	TypeSubagentCompleted   = "subagent.completed"
	TypeContextCompacted    = "context.compacted"
	TypeErrorRecorded       = "error.recorded"
	TypeSourceConnected     = "source.connected"
	TypeSourceDisconnected  = "source.disconnected"
	TypeSourceExtension     = "source.extension"
)

// KnownTypes is the closed set of normalized types. Anything else must be
// stored as TypeSourceExtension.
var KnownTypes = map[string]struct{}{
	TypeRunStarted: {}, TypeRunCompleted: {}, TypeRunFailed: {}, TypeRunCancelled: {},
	TypeRunIncomplete: {}, TypePromptReceived: {}, TypeModelRequested: {}, TypeModelCompleted: {},
	TypeToolStarted: {}, TypeToolCompleted: {}, TypeToolFailed: {}, TypeFileChanged: {},
	TypeCommandStarted: {}, TypeCommandCompleted: {}, TypeCommandFailed: {},
	TypePermissionRequested: {}, TypePermissionResolved: {}, TypeSubagentStarted: {},
	TypeSubagentCompleted: {}, TypeContextCompacted: {}, TypeErrorRecorded: {},
	TypeSourceConnected: {}, TypeSourceDisconnected: {}, TypeSourceExtension: {},
}

func IsKnownType(value string) bool {
	_, ok := KnownTypes[value]
	return ok
}
