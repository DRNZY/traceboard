package hooks

// opencodeAdapter maps the documented OpenCode plugin events. The plugin
// renames each native event to a canonical lifecycle name before calling the
// hook, so this adapter only has to translate legacy aliases.
type opencodeAdapter struct {
	baseAdapter
}

func newOpenCodeAdapter() opencodeAdapter {
	return opencodeAdapter{baseAdapter{
		source:         SourceOpenCode,
		supportedSince: "0.4.0",
		aliases: map[string]string{
			"session.created":      RunStart,
			"session.updated":      RunIdle,
			"session.completed":    RunEnd,
			"session.error":        RunError,
			"message.updated":      MessageReceived,
			"message.part.updated": ModelFinish,
			"tool.execute.before":  ToolBefore,
			"tool.execute.after":   ToolAfter,
			"file.changed":         FileWrite,
			"permission.asked":     PermissionAsk,
			"permission.replied":   PermissionAnswer,
			"session.compacted":    Compact,
			"error":                RunError,
			"idle":                 RunIdle,
		},
		rawPaths: []string{"sessionID", "messageID", "partID", "tool", "path", "callID"},
	}}
}

// claudeCodeAdapter maps Claude Code lifecycle hooks. OTLP signals arrive on the
// OTLP endpoints instead, so this adapter only covers hook-delivered events.
type claudeCodeAdapter struct {
	baseAdapter
}

func newClaudeCodeAdapter() claudeCodeAdapter {
	return claudeCodeAdapter{baseAdapter{
		source:         SourceClaudeCode,
		supportedSince: "1.0.0",
		aliases: map[string]string{
			"SessionStart":        RunStart,
			"SessionEnd":          RunEnd,
			"UserPromptSubmit":    PromptSubmit,
			"PreToolUse":          ToolBefore,
			"PostToolUse":         ToolAfter,
			"PostToolUseFailure":  ToolError,
			"Notification":        MessageReceived,
			"Stop":                RunEnd,
			"SubagentStart":       SubagentStart,
			"SubagentStop":        SubagentEnd,
			"PreCompact":          Compact,
			"PermissionRequest":   PermissionAsk,
			"SessionStartResumed": RunStart,
		},
		rawPaths: []string{"session_id", "tool_name", "tool_input", "transcript_path"},
	}}
}

// codexAdapter maps the documented Codex lifecycle hooks. Hook trust stays with
// the user: Traceboard writes configuration but never bypasses /hooks.
type codexAdapter struct {
	baseAdapter
}

func newCodexAdapter() codexAdapter {
	return codexAdapter{baseAdapter{
		source:         SourceCodex,
		supportedSince: "0.20.0",
		aliases: map[string]string{
			"session.start":    RunStart,
			"session.end":      RunEnd,
			"turn.start":       PromptSubmit,
			"turn.end":         RunEnd,
			"tool.start":       ToolBefore,
			"tool.end":         ToolAfter,
			"tool.failed":      ToolError,
			"command.start":    CommandStart,
			"command.finish":   CommandFinish,
			"command.failed":   commandFailure,
			"permission.ask":   PermissionAsk,
			"permission.reply": PermissionAnswer,
			"compaction":       Compact,
			"subagent.start":   SubagentStart,
			"subagent.end":     SubagentEnd,
			"error":            RunError,
			"stop":             RunEnd,
		},
		rawPaths: []string{"session_id", "turn_id", "call_id", "command", "exit_code"},
	}}
}

// antigravityAdapter maps the Antigravity IDE hooks. SDK OpenTelemetry traffic
// uses the OTLP endpoints and is normalized there.
type antigravityAdapter struct {
	baseAdapter
}

func newAntigravityAdapter() antigravityAdapter {
	return antigravityAdapter{baseAdapter{
		source:         SourceAntigravity,
		supportedSince: "1.0.0",
		aliases: map[string]string{
			"conversation.start":  RunStart,
			"conversation.end":    RunEnd,
			"execution.start":     RunStart,
			"execution.end":       RunEnd,
			"execution.error":     RunError,
			"tool.start":          ToolBefore,
			"tool.end":            ToolAfter,
			"tool.failed":         ToolError,
			"model.request":       ModelStart,
			"model.response":      ModelFinish,
			"permission.request":  PermissionAsk,
			"permission.resolved": PermissionAnswer,
			"context.compacted":   Compact,
			"subagent.start":      SubagentStart,
			"subagent.end":        SubagentEnd,
			"stop.reason":         RunEnd,
		},
		rawPaths: []string{"conversation_id", "execution_id", "tool_name", "stop_reason"},
	}}
}

// geminiCLIAdapter maps Gemini CLI log records. Gemini CLI emits OpenTelemetry
// directly, so this adapter exists for the small set of structured log records
// that describe a turn boundary.
type geminiCLIAdapter struct {
	baseAdapter
}

func newGeminiCLIAdapter() geminiCLIAdapter {
	return geminiCLIAdapter{baseAdapter{
		source:         SourceGeminiCLI,
		supportedSince: "0.1.0",
		aliases: map[string]string{
			"turn.start":      PromptSubmit,
			"turn.end":        RunEnd,
			"turn.error":      RunError,
			"tool.call":       ToolBefore,
			"tool.result":     ToolAfter,
			"model.request":   ModelStart,
			"model.response":  ModelFinish,
			"session.start":   RunStart,
			"session.end":     RunEnd,
			"context.compact": Compact,
		},
		rawPaths: []string{"session_id", "turn_id", "tool_name", "model"},
	}}
}
