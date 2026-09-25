package hooks

import (
	"errors"
	"strings"

	"traceboard/internal/event"
)

// Canonical lifecycle names. Every supported source maps its native names onto
// this set; anything unrecognized becomes a source extension.
const (
	RunStart         = "session.start"
	RunEnd           = "session.end"
	RunError         = "session.error"
	RunIdle          = "session.idle"
	PromptSubmit     = "prompt.submit"
	MessageReceived  = "message.received"
	ModelStart       = "model.request"
	ModelFinish      = "model.finish"
	ToolBefore       = "tool.execute.before"
	ToolAfter        = "tool.execute.after"
	ToolError        = "tool.execute.error"
	FileWrite        = "file.write"
	FileEdit         = "file.edit"
	FileRemove       = "file.remove"
	CommandStart     = "command.start"
	CommandFinish    = "command.finish"
	PermissionAsk    = "permission.ask"
	PermissionAnswer = "permission.answer"
	Compact          = "context.compact"
	SubagentStart    = "subagent.start"
	SubagentEnd      = "subagent.end"
	Heartbeat        = "source.heartbeat"
	SourceConnect    = "source.connect"
	SourceDisconnect = "source.disconnect"
)

type lifecycleRule struct {
	eventType string
	status    event.Status
	stepBound bool
}

var canonicalLifecycle = map[string]lifecycleRule{
	RunStart:         {event.TypeRunStarted, event.StatusStarted, false},
	RunEnd:           {event.TypeRunCompleted, event.StatusCompleted, false},
	RunError:         {event.TypeRunFailed, event.StatusFailed, false},
	RunIdle:          {event.TypeRunIncomplete, event.StatusIncomplete, false},
	PromptSubmit:     {event.TypePromptReceived, event.StatusCompleted, false},
	MessageReceived:  {event.TypePromptReceived, event.StatusCompleted, false},
	ModelStart:       {event.TypeModelRequested, event.StatusStarted, true},
	ModelFinish:      {event.TypeModelCompleted, event.StatusCompleted, true},
	ToolBefore:       {event.TypeToolStarted, event.StatusStarted, true},
	ToolAfter:        {event.TypeToolCompleted, event.StatusCompleted, true},
	ToolError:        {event.TypeToolFailed, event.StatusFailed, true},
	FileWrite:        {event.TypeFileChanged, event.StatusCompleted, false},
	FileEdit:         {event.TypeFileChanged, event.StatusCompleted, false},
	FileRemove:       {event.TypeFileChanged, event.StatusCompleted, false},
	CommandStart:     {event.TypeCommandStarted, event.StatusStarted, true},
	CommandFinish:    {event.TypeCommandCompleted, event.StatusCompleted, true},
	PermissionAsk:    {event.TypePermissionRequested, event.StatusStarted, true},
	PermissionAnswer: {event.TypePermissionResolved, event.StatusCompleted, true},
	Compact:          {event.TypeContextCompacted, event.StatusCompleted, false},
	SubagentStart:    {event.TypeSubagentStarted, event.StatusStarted, true},
	SubagentEnd:      {event.TypeSubagentCompleted, event.StatusCompleted, true},
	Heartbeat:        {event.TypeSourceConnected, event.StatusUnknown, false},
	SourceConnect:    {event.TypeSourceConnected, event.StatusUnknown, false},
	SourceDisconnect: {event.TypeSourceDisconnected, event.StatusUnknown, false},
}

// CommandFail shares the command family; a source reports a failed command
// through a status rather than a separate lifecycle name.
var commandFailure = "command.failed"

// baseAdapter carries the shared normalization rules every source uses.
type baseAdapter struct {
	source         string
	supportedSince string
	aliases        map[string]string
	rawPaths       []string
}

func (adapter baseAdapter) Source() string {
	return adapter.source
}

func (adapter baseAdapter) SupportedVersions() []string {
	return []string{adapter.supportedSince + "+"}
}

func (adapter baseAdapter) Normalize(input HookInput) (Mapping, error) {
	if strings.TrimSpace(input.EventName) == "" {
		return Mapping{}, errors.New("hook event name is required")
	}
	if strings.TrimSpace(input.RunID) == "" {
		return Mapping{}, errors.New("hook run id is required")
	}
	canonical, ok := adapter.resolve(input.EventName)
	if canonical == commandFailure {
		ok = true
	}
	mapping := Mapping{
		Source:         adapter.source,
		SourceVersion:  input.SourceVersion,
		SupportedSince: adapter.supportedSince,
	}
	attributes := adapter.attributes(input)

	if !ok {
		output := build(input, input.EventName, unknownStatus(input), input.StepID, attributes, input.Content)
		mapping.Events = append(mapping.Events, output)
		mapping.Notes = append(mapping.Notes, "unmapped lifecycle name stored as a source extension")
		return mapping, nil
	}

	rule := canonicalLifecycle[canonical]
	status := rule.status
	stepBound := rule.stepBound
	eventType := rule.eventType
	if canonical == commandFailure {
		status = event.StatusFailed
		stepBound = false
		eventType = "command.failed"
	}
	stepID := input.StepID
	if !stepBound {
		stepID = ""
	}
	output := build(input, eventType, status, stepID, attributes, input.Content)
	mapping.Events = append(mapping.Events, output)
	return mapping, nil
}

func (adapter baseAdapter) resolve(name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	if canonical, ok := canonicalLifecycle[trimmed]; ok {
		_ = canonical
		return trimmed, true
	}
	if alias, ok := adapter.aliases[trimmed]; ok {
		if _, known := canonicalLifecycle[alias]; known {
			return alias, true
		}
		return alias, true
	}
	return "", false
}

func (adapter baseAdapter) attributes(input HookInput) map[string]any {
	attributes := copyMap(input.Attributes)
	attributes["source_event_name"] = input.EventName
	if input.SourceVersion != "" {
		attributes["source_version"] = input.SourceVersion
	}
	if len(adapter.rawPaths) == 0 {
		return attributes
	}
	for _, path := range adapter.rawPaths {
		if value := dig(input.Raw, path); value != nil {
			attributes["source."+strings.ReplaceAll(path, ".", "_")] = value
		}
	}
	return attributes
}

func dig(payload any, path string) any {
	current := payload
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = object[segment]
		if !ok {
			return nil
		}
	}
	return current
}
