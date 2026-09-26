# Source compatibility

Traceboard records what a source actually reports. Where a source does not
expose a fact, the dashboard says the fact is missing rather than filling the
gap.

| Source | Integration | Tested from | Setup |
| --- | --- | --- | --- |
| OpenCode | TypeScript plugin + lifecycle hook | 0.4.0 | `traceboard configure opencode` |
| Claude Code | Lifecycle hooks, plus OTLP | 1.0.0 | `traceboard configure claude-code` |
| Codex | Lifecycle hooks | 0.20.0 | `traceboard configure codex` |
| Antigravity | IDE hooks, plus SDK OTLP | 1.0.0 | `traceboard configure antigravity` |
| Gemini CLI | OTLP | 0.1.0 | `traceboard configure gemini-cli` |

## What each source contributes

### OpenCode

The plugin subscribes to session, message, tool, file, permission, compaction,
and error events, and forwards them as one versioned batch with a 750 ms
timeout. It redacts before sending, spools when the collector is offline, and
never modifies an agent argument, a tool result, or a return value. A failure
inside the plugin is swallowed: telemetry can never slow or break a run.

Captured: run lifecycle, prompts, model calls, tool calls, file changes,
permissions, context compaction, errors.

Not captured: hidden chain of thought, which the agent never exposes.

### Claude Code

Lifecycle hooks carry the run boundary, prompts, and tool activity. OTLP traces
and logs carry the same structure with more detail. Raw API-body capture is not
enabled, and prompt, tool, and tool-body capture each remain separate decisions.

Hook trust belongs to Claude Code. `configure` registers the hook and tells you
to review it; it does not bypass that review.

### Codex

Documented session, turn, tool, permission, compaction, subagent, and stop hooks.
`configure` writes the hook configuration and tells you to review each hook
through `/hooks`. Traceboard never bypasses hook trust.

`codex exec --json` can be used as a secondary event stream for non-interactive
runs, but the lifecycle hooks remain the primary path.

### Antigravity

IDE hooks carry conversation and execution boundaries, tool calls, and stop
reasons. SDK agents created through the Google Antigravity SDK report through
OpenTelemetry instead. IDE transcript paths are source-owned files; they are not
copied wholesale unless detailed capture is enabled and retention is configured
for that source.

### Gemini CLI

Gemini CLI exports OpenTelemetry directly. Traceboard reads the GenAI semantic
conventions for conversation identifiers, model names, tool calls, and token
usage. Prompt and detailed trace capture stay under Gemini CLI's own settings and
Traceboard's capture mode.

## Capture gaps

These are facts a source does not expose, so Traceboard cannot show them:

- Hidden reasoning, for every source. Traceboard stores only what the source or
  model provider exposes.
- Exact cost, for every source. Token usage is stored when a source reports it;
  cost calculation is out of scope for version one.
- A terminal state, when an agent is killed. The run stays active and is
  reported as stalled after fifteen minutes of silence. It is never reported as a
  failure, because no source reported one.

## Version policy

Adapter behaviour is pinned to the source versions listed above. A source that
changes its event format is visible as a growth in the quarantine count, which is
shown in source health. The compatibility table is updated when a mapping is
verified, not before.

Because redaction runs before storage and a fixture that fails after a source
upgrade blocks release until the mapping is updated or the source version is
marked unsupported, a changed format is caught before it is silently
misinterpreted.

## Writing your own adapter

The `traceboard-event-v1` envelope is public. An adapter can emit the same JSON
without linking to any Traceboard package. Read the contract in
`internal/event/event.go`, post to `/api/v1/events` with the ingest bearer token,
and the rest of the pipeline applies unchanged.

A conformance check is planned but not part of version one.
