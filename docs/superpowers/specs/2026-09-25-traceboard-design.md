# Traceboard design specification

Date: 2026-09-25
Status: Approved for implementation

## Summary

Traceboard is a local-first, open-source observability application for coding agents. It records runs from OpenCode, Claude Code, Codex, Antigravity, and Gemini CLI, normalizes their telemetry into one event model, and presents a searchable run index with a detailed event timeline.

The first useful outcome is straightforward: after an agent run finishes, a developer can open that run and reconstruct what happened, where it failed, and which tool activity led there. Traceboard does not re-execute captured actions. Its replay control reconstructs the recorded sequence only.

## Problem

Coding agents expose their work through different hooks, telemetry formats, transcripts, and lifecycle models. A developer investigating a failed run must move among terminal logs, tool-specific session files, and separate debugging tools. The same question remains hard to answer: what did the agent receive, what did it change, which tools ran, and where did the run fail?

Existing tracing tools often assume a hosted backend or focus on model calls rather than the full agent loop. Traceboard instead runs on the developer's machine, keeps captured data local by default, and treats prompts, tools, files, errors, and child-agent activity as first-class run events.

## Goals

1. Capture agent runs from the selected tools through supported native hooks or telemetry.
2. Normalize source-specific data into one versioned event model.
3. Show a searchable run index and a chronological explanation of each run.
4. Identify failed, interrupted, or stalled runs without re-executing their tools.
5. Keep telemetry on the local machine and minimize captured content by default.
6. Ship as an open-source project that other developers can run with one command.
7. Keep integrations isolated behind adapters that can be tested against versioned fixtures.
8. Degrade safely when the collector is unavailable or a source changes its event format.

## Non-goals for version one

1. Hosted accounts, subscriptions, billing, or a managed service.
2. Team workspaces, shared projects, or organization policy management.
3. Remote or LAN access. Version one rejects non-loopback bind addresses.
4. Deterministic replay or automatic re-execution of prompts, tools, or commands.
5. Automatic scoring of coding-agent output quality.
6. A general-purpose observability backend for unrelated services.
7. Complete cloud billing or cost attribution. Token usage is stored when a source provides it, but cost calculations are deferred.
8. Editing or replacing an agent's prompts, tools, or policies.

## Primary user story

As a developer using several coding agents, I open Traceboard after a task finishes and filter to the relevant project and run. I read the ordered prompts, model calls, tool calls, file changes, errors, and child-agent steps. I expand a raw event when I need source-specific detail, export the run for an issue or review, and delete it when I no longer need it.

## Product principles

- Explain runs rather than grade them.
- Show missing data instead of filling gaps with guesses.
- Keep telemetry collection visible and reversible.
- Preserve allowed source payloads for debugging while presenting one normalized model.
- Never block an agent because the dashboard is unavailable.
- Treat content capture as a separate choice from telemetry collection.
- Prefer a small local process with a stable event contract.

## System architecture

Traceboard is one Go process with an embedded Svelte application built through Vite. The binary contains the HTTP server, ingest pipeline, SQLite store, alert evaluator, and static web assets.

The process listens on `127.0.0.1` by default. The default HTTP port is `47821` and can be overridden in the configuration file. Source integrations use one of two local ingest paths:

- A JSON event endpoint for lifecycle hooks and plugins.
- OTLP/HTTP endpoints for traces and logs from sources that support OpenTelemetry.

Both paths pass through the same validation, redaction, normalization, persistence, and live-update pipeline.

### Components

#### Collector

The collector receives source events, authenticates the caller, enforces payload limits, validates the envelope, and assigns a stable source event identifier when the adapter does not provide one. In that case, the collector derives it from a SHA-256 hash of the source name, source version, source run ID, source timestamp, event type, and canonical raw payload. The collector must return quickly and must not wait on the agent's critical path.

#### Normalizer

Each adapter maps source data to Traceboard's versioned event envelope. The normalizer converts source-specific names and identifiers, assigns parent-child relationships, and records capture coverage. Unknown fields remain available in the redacted raw payload only when the selected capture mode permits them. In `metadata` mode, fields that can contain prompt or tool body content are removed before raw storage.

#### Store

SQLite stores runs, steps, events, projects, sources, alerts, and migration metadata. Write operations use transactions. Run summaries are updated in the same transaction as their events so the UI never displays a committed event that is missing from its run.

The store uses:

- WAL mode for concurrent readers during writes.
- Foreign keys with cascading deletion for run-owned data.
- A unique `(source, source_event_id)` key for idempotency.
- A monotonic Traceboard ingest sequence within each run.
- FTS5 for full-text search over indexed text fields.
- Forward-only schema migrations recorded in `schema_migrations`.

#### Query API

The query API serves run lists, filters, run summaries, ordered events, source health, alerts, export data, and retention controls. List endpoints use cursor pagination rather than offsets.

#### Live update channel

The web application opens an authenticated WebSocket connection. The server publishes committed run and alert changes. The client merges updates by entity version and refetches an event range when it detects a sequence gap.

#### Alert evaluator

The alert evaluator watches run state and source heartbeats. Version one supports alerts for failed runs, stalled active runs, disconnected sources, and repeated ingest errors. A user can acknowledge an alert from the dashboard or CLI.

#### Desktop notifier

The notifier sends local desktop notifications through the host operating system. Notifications contain the project, source, run title or short prompt summary, status, and a link back to the run. Notification bodies do not include raw tool output or secrets.

Default alert thresholds are configurable. A source-reported failure alerts immediately, an active run with no received event for 15 minutes is marked stalled, and a source is marked disconnected after three missed 30-second heartbeats.

## Repository shape

The planned repository uses this top-level structure:

```text
traceboard/
  cmd/traceboard/
  internal/
    collector/
    normalize/
    store/
    alerts/
    config/
    server/
    export/
  adapters/
    opencode/
    claude-code/
    codex/
    antigravity/
    gemini-cli/
  web/
    src/
    static/
  schema/
    traceboard-event-v1.json
  migrations/
  fixtures/
    opencode/
    claude-code/
    codex/
    antigravity/
    gemini-cli/
  tests/
    integration/
    e2e/
  docs/
```

Adapter packages remain small. They know how to read or receive source events and emit `traceboard-event-v1`. They do not query SQLite or call the web API directly.

## Event model

Every event uses a versioned envelope with these fields:

| Field | Purpose |
| --- | --- |
| `schema_version` | Identifies the Traceboard event contract. Version one uses `1`. |
| `event_id` | Globally unique Traceboard event identifier. |
| `source_event_id` | Stable source identifier used with `source` for deduplication. |
| `source` | Adapter name, such as `opencode` or `claude-code`. |
| `source_version` | Adapter version and, when available, source tool version. |
| `run_id` | Stable run identifier. |
| `step_id` | Stable step identifier within the run. |
| `parent_step_id` | Parent step for tool calls, retries, or child agents. |
| `sequence` | Monotonic Traceboard receive order used to detect ingestion gaps. |
| `source_sequence` | Optional source-defined order within a run or step. |
| `occurred_at` | Timestamp reported by the source. |
| `received_at` | Timestamp assigned by Traceboard. |
| `type` | Normalized event type. |
| `status` | Event status such as `started`, `completed`, `failed`, `cancelled`, or `unknown`. |
| `capture` | Fields captured for this event and any redaction applied. |
| `attributes` | Structured normalized fields. |
| `content` | Optional prompt, input, output, or error content. |
| `raw` | Optional redacted source payload retained for adapter debugging. |

### Event types

Version one defines these normalized event types:

- `run.started`
- `run.completed`
- `run.failed`
- `run.cancelled`
- `run.incomplete`
- `prompt.received`
- `model.requested`
- `model.completed`
- `tool.started`
- `tool.completed`
- `tool.failed`
- `file.changed`
- `command.started`
- `command.completed`
- `command.failed`
- `permission.requested`
- `permission.resolved`
- `subagent.started`
- `subagent.completed`
- `context.compacted`
- `error.recorded`
- `source.connected`
- `source.disconnected`

A source-specific event that has no safe normalized equivalent is stored as an extension event under the same envelope. Extension events do not imply an error; `error.recorded` is reserved for source-reported errors. The UI labels extension events with their source and type instead of forcing them into an inaccurate category.

### Run and step rules

- A run represents one source session or one explicitly delimited agent invocation.
- A step represents one model call, tool call, command, child agent, or other bounded operation.
- Retries create sibling steps linked through `retry_of_step_id` when the source exposes that relationship.
- Concurrent child agents use stable parent IDs and their own source sequences when the source provides them.
- Out-of-order arrival is accepted. The timeline groups events by step, then orders them by `occurred_at`, `source_sequence` when present, and Traceboard `sequence` as the final tie-breaker.
- A run with a missing terminal event remains active until the source heartbeat expires or an operator marks it incomplete.

## Capture modes

Traceboard exposes three capture modes per source:

| Mode | Captured by default |
| --- | --- |
| `off` | Source connection health only. No run events or content are stored. |
| `metadata` | Lifecycle, model, timing, tool names, file paths, status, and token usage when supplied. No prompt or tool body content. |
| `detailed` | Metadata plus bounded prompt, tool input, tool output, error, and final-response content. |

`metadata` is the default. The setup command and dashboard show the selected mode before enabling a source. A run's detail page lists the mode and fields captured for that run.

Default ingestion limits are 4 MiB per request, 1 MiB per raw payload, 256 KiB per content field, and 1,000 events per batch. These defaults are also the compiled safety maximum. Truncation is marked in `capture` and the event detail. Operators may lower these limits but may not raise them.

Traceboard never attempts to capture hidden chain-of-thought. It stores only prompts, messages, summaries, tool data, and diagnostics exposed by the source or model provider.

## Existing project research

A 2026 review found several active local-first projects with overlapping goals:

- `tobilg/ai-observer` is an MIT-licensed, active Go single-binary application that already receives OTLP telemetry for Claude Code, Codex, Gemini CLI, and OpenCode, imports and watches their local session files, stores data in DuckDB, and serves a React dashboard.
- `abekdwight/opencode-observability` is an MIT-licensed TypeScript local session viewer and live monitor for OpenCode, Claude Code, and Codex.
- `cleverb/agent-profiler` is an MIT-licensed TypeScript profiler that normalizes lifecycle hooks from Claude, Codex, and OpenCode into a canonical event contract.
- `traceroost/core` is an MIT-licensed TypeScript and VS Code extension that combines OTLP ingestion with local session-file fallback.
- `VasiHemanth/tokentelemetry` is an MIT-licensed Python and TypeScript local dashboard focused on token, cost, and trace analytics across many agent tools.

AI Observer is the closest match and is the most mature reference found during research. Traceboard will not ship as an unexplained copy. Its MIT notice and attribution will be retained for any reused code. The implementation will use AI Observer as a technical reference for OTLP decoding, embedded SPA packaging, historical file parsing, and graceful server behavior.

A literal AI Observer fork was rejected because Traceboard requires a different frontend, normalized run and step storage, loopback-only authentication, pre-persistence redaction, source-specific capture modes, durable hook ingestion, alerts, retention, and a run-centric forensic interface. Reusing its Go ingestion and server primitives while replacing its product-defining layers is smaller and clearer than maintaining a heavily divergent fork.

## Privacy and security

### Local-only defaults

- Bind to `127.0.0.1` by default.
- Do not enable LAN or remote listening in the default configuration.
- Send no telemetry to Traceboard-controlled services.
- Load no third-party scripts, fonts, or analytics in the dashboard.
- Keep all dashboard assets embedded in the binary.

### Authentication

The first start creates a mode `0700` configuration directory and mode `0600` credential files containing:

- A long-lived ingest token for local source adapters.
- A one-time dashboard sign-in token.

`traceboard start` prints a loopback dashboard URL containing the one-time sign-in token. The token is exchanged once for a random session ID in an `HttpOnly`, `SameSite=Strict`, path-scoped cookie, then invalidated. The clean dashboard URL does not contain a permanent credential. Source adapters read the ingest token from the protected configuration or receive it through a restricted environment variable.

The server also:

- Rejects unauthenticated ingest and query requests.
- Disables cross-origin requests unless explicitly configured.
- Validates the `Host` header against loopback and configured names.
- Sets a restrictive Content Security Policy, `X-Content-Type-Options`, `Referrer-Policy`, and `Permissions-Policy` headers.
- Returns generic authentication errors without configuration paths or secret values.

### Redaction

Redaction runs before any event, raw payload, search index, or spool file is persisted. Version one detects common API keys, bearer tokens, private-key blocks, database URLs with credentials, and configured environment-variable values.

Users can add regular expressions for project-specific secrets. Redaction replaces the value and records only the rule name and field category. A bounded number of unmasked characters may be retained around a match to help identify it, but the full secret is never stored.

### Content handling

All source content is untrusted. The dashboard renders it as text unless a source-specific renderer is explicitly implemented and sanitized. File paths, prompts, tool output, errors, and raw payloads cannot execute markup or scripts.

SQLite and its parent directory are created with user-only permissions. Version one does not claim full-database encryption. The setup documentation explains filesystem and disk-encryption options.

## Adapter strategy

Each adapter declares:

1. Supported source versions.
2. Capture surfaces used by the source.
3. Event mappings.
4. Content fields available in each capture mode.
5. Known gaps.
6. Installation and health-check commands.
7. Golden fixtures and smoke-test instructions.

### OpenCode adapter

The OpenCode adapter is a TypeScript plugin using documented plugin events, including session lifecycle, message, file, permission, and `tool.execute.before` / `tool.execute.after` hooks.

It maps:

- Session creation and terminal state to run events.
- Message updates to prompt and model events.
- Tool lifecycle hooks to paired tool steps.
- File events to `file.changed`.
- Permission events to permission steps.
- Session errors to error and failed-run events.

The plugin sends batches to the local JSON endpoint with a short timeout. It never modifies agent behavior.

### Claude Code adapter

The Claude Code adapter consumes OTLP traces, metrics, and logs for interaction, model, tool, hook, and subagent structure. Lifecycle hooks fill final prompt and response content only when `detailed` capture is enabled.

The adapter respects Claude Code's content controls. Prompts, tool details, tool bodies, and raw API bodies are separate opt-ins. Traceboard does not enable raw API-body capture in version one.

### Codex adapter

The Codex adapter uses documented lifecycle hooks for session, turn, tool, permission, compaction, subagent, and stop events. The installer writes the hook configuration and explains that the user must review and trust each hook through Codex's `/hooks` interface. Traceboard does not bypass hook trust.

For non-interactive runs launched through Traceboard, `codex exec --json` can provide a secondary event stream. The lifecycle hooks remain the primary integration path.

### Antigravity adapter

The Antigravity adapter supports two profiles:

- IDE hooks for interactive conversations and tools.
- SDK OpenTelemetry for agents created through the Google Antigravity SDK.

The adapter maps conversation and execution identifiers, tool calls, model invocations, errors, and stop reasons. IDE transcript paths are treated as source-owned files, not copied wholesale unless detailed capture is enabled and the user has configured retention for that source.

### Gemini CLI adapter

The Gemini CLI adapter receives local OTLP traces and logs. It uses GenAI semantic conventions for model calls, tool calls, conversation IDs, model names, and token usage. Prompt and detailed trace capture remain controlled by Gemini CLI settings and Traceboard's capture mode.

### Community adapters

The version-one JSON schema is public. An external adapter can emit the same envelope without linking to Traceboard's Go packages. A future conformance command may validate an adapter or sample event stream; it is not required for the first release.

## Data flow

1. A source emits an event through a hook, plugin, or OTLP exporter.
2. Traceboard authenticates and size-limits the request.
3. The source adapter or collector parses the source payload.
4. The payload is redacted before persistence.
5. The normalizer validates and maps it to `traceboard-event-v1`.
6. Traceboard inserts the event idempotently inside a transaction.
7. The same transaction updates the run summary, step state, and relevant search index.
8. The server publishes the committed change over the WebSocket.
9. The dashboard updates the run index and open timeline without a reload.
10. The alert evaluator evaluates the new state and creates or resolves alerts.

## Ingestion failure behavior

Telemetry must never block the user's agent.

- Adapters that run in user space use a bounded local disk spool when the collector is unavailable. OTLP sources use a documented durable file export or retry mechanism when their source supports one.
- Source health identifies whether the integration can survive a collector outage without data loss.
- The default spool limit is 100 MiB per source with a seven-day age limit.
- Retries use exponential backoff with jitter and preserve source order where possible.
- Duplicate source events are ignored through the unique source event key.
- Malformed events are moved to a quarantine table with a redacted payload and validation reason.
- Quarantine limits are visible in source health.
- A collector restart drains the spool before deleting it.
- When a spool reaches its limit, Traceboard records a visible loss-risk warning and retains the newest events within the limit. Oldest expired data is removed only after a 24-hour warning grace period.
- Agent hooks return a successful no-op after bounded local handling so they do not block the agent.

An interrupted run is marked `incomplete` when a terminal source event is missing and no source heartbeat arrives before the run timeout. Traceboard does not label an interrupted run as failed unless the source reports a failure.

## User interface

The approved layout is a run index with an inspector.

### Run index

The left pane contains searchable runs with source, project, status, start time, duration, and alert state. Filters cover:

- Free-text query.
- Source.
- Project.
- Status.
- Capture mode.
- Alert state.
- Time range.
- Parent run and subagent relationship.

Search covers prompt text when captured, model names, tool names, file paths, errors, and indexed raw attributes. Users can exclude raw payloads from search while retaining them for manual inspection.

### Run inspector

The main pane contains:

- Run title and source.
- Start time, duration, model, token usage when available, and terminal status.
- A short factual summary of the run's start, important changes, verification, and failure point.
- A chronological event timeline.
- Expandable event details and redacted raw payloads.
- Parent and child agent relationships.
- A replay scrubber that reconstructs the recorded state at each event.
- Export and delete actions.

The summary is deterministic. It uses the first captured prompt as a title candidate, counts model, tool, and file events, lists changed paths, and names the first source-reported terminal error. It does not infer hidden reasoning or invent a cause. If the source lacks a required field, the summary names the gap.

### Live behavior

Active runs update in place. A reconnecting client fetches changed runs and missing event ranges before returning to live mode. The interface never claims that an event is complete when the server reports a sequence gap.

### Alerts

In-app alerts appear in the run index. Desktop notifications are optional and can be disabled per source. Version one does not send alerts to a hosted notification service.

An alert contains a stable type, run or source target, creation time, acknowledgement time, and resolution time. Alert evaluation is idempotent so reconnects and repeated events do not create duplicate alerts.

## Command-line surface

Version one provides these primary commands:

```text
traceboard start
traceboard status
traceboard sources
traceboard configure <source>
traceboard doctor
traceboard auth rotate
traceboard export <run-id>
traceboard delete <run-id>
traceboard retention set <duration>
traceboard daemon install
traceboard daemon uninstall
```

`traceboard configure` performs source-specific setup, explains required trust or restart steps, and reports the resulting capture mode. `traceboard doctor` checks the server, database permissions, migrations, configuration, source installation, authentication, and OTLP reachability. `traceboard auth rotate` invalidates dashboard sessions and creates a new one-time dashboard token without changing the ingest token.

## Export and deletion

Exports are local files selected by the user. Version one supports:

- JSON containing the normalized run and events.
- Markdown containing a readable timeline and source metadata.
- A redacted raw bundle for bug reports.

Deletion removes the run, steps, events, search entries, alerts, and related spool data. Database maintenance runs after deletion. A confirmation displays the run title, source, event count, and earliest and latest timestamps.

## Retention

Retention is configurable by age and optional per project. Version one supports:

- Keep indefinitely.
- Keep for a fixed number of days.
- Keep the newest fixed number of runs per project.

Active runs are exempt from every automatic retention policy. The system previews the next deletion set and never deletes an active run. Retention jobs record counts and timestamps without storing deleted content.

## Testing strategy

### Go unit tests

Unit tests cover:

- Event validation and schema-version rejection.
- Idempotent inserts and duplicate source IDs.
- Sequence and timestamp handling.
- Parent-child step mapping.
- Redaction rules and secret boundary cases.
- Database migrations from every supported schema version.
- Cursor pagination and FTS search.
- Run summary transitions.
- Alert evaluation and acknowledgement.
- Export and deletion.
- HTTP authentication, host validation, origin policy, and payload limits.

### Adapter contract tests

Every adapter has checked-in fixtures for successful, failed, cancelled, incomplete, malformed, and out-of-order runs. Golden files contain the normalized event stream. Tests fail when an adapter changes output for a pinned source version without an intentional fixture update.

Source-version compatibility is explicit. A fixture that fails after a source upgrade blocks release until Traceboard either updates the mapping or marks that source version unsupported.

### Integration tests

Integration tests start the real collector and temporary SQLite database, then inject fixtures through each ingest path. They verify:

- HTTP and OTLP ingestion.
- Transactional run summaries.
- WebSocket publication after commit.
- Reconnect and sequence-gap recovery.
- Collector outage with adapter spooling.
- Restart and spool drain.
- Partial run detection.
- Quarantine behavior and limits.
- Permission checks on database and configuration files.

### Browser tests

Playwright covers the complete local workflow:

1. Start Traceboard in a clean temporary home.
2. Complete the capture-mode setup.
3. Ingest a representative run.
4. Find it through the run index.
5. Inspect events and expand a redacted raw payload.
6. Scrub the replay control without re-executing an action.
7. Acknowledge an alert.
8. Export the run.
9. Delete the run and confirm its search entries disappear.

The suite also checks keyboard navigation, focus visibility, zoom at 200%, narrow viewport behavior, and status labels that do not rely on color alone.

### Source smoke tests

A release candidate must pass one real smoke run for every supported source. The smoke test verifies start, model activity when exposed, at least one tool event, terminal status, and dashboard appearance without a manual reload. Tests requiring credentials or paid model access are opt-in in CI and mandatory in the maintainer release checklist.

## Security verification

Before release, tests confirm:

- The default listener cannot be reached from another device.
- Unauthenticated ingest and query requests fail.
- Cross-origin requests fail.
- Host-header bypass attempts fail.
- Captured HTML and SVG payloads render as inert content.
- Common secrets do not appear in SQLite, FTS indexes, spools, quarantine records, logs, or exports.
- Error messages and CLI output do not expose the ingest or session token.
- The embedded dashboard makes no external network requests.

## Performance targets

Version one targets these limits on a typical developer laptop:

- Start the service and open the dashboard within three seconds.
- Show a committed run in the open dashboard within one second under normal local load.
- Keep direct UI interactions responsive while displaying 10,000 events in a run.
- Search the first 100,000 indexed events in under one second.
- Continue accepting hook events when the dashboard is closed.
- Recover a valid spool after a collector restart without duplicating events.

These are release targets, not claims about the current implementation. Verification must measure them before they appear in project documentation.

## Compatibility strategy

Traceboard pins adapter behavior to tested source versions. Release notes identify newly supported versions and known capture gaps. Adapters must not silently reinterpret changed payloads.

The normalized schema is versioned independently from source versions. Breaking event-model changes require a new schema version and an explicit migration path. Additive optional attributes do not require a schema version change.

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| Source hooks or telemetry change without notice | Versioned fixtures, explicit compatibility metadata, and visible unknown-event handling. |
| Captured content contains secrets | Metadata-first defaults, pre-persistence redaction, payload limits, local-only storage, and export tests. |
| Agent execution slows or fails when telemetry breaks | Bounded asynchronous ingestion, disk spooling, short timeouts, and no-op hook behavior after local handling. |
| Missing terminal events leave runs active forever | Source heartbeats, configurable run timeouts, and explicit `incomplete` status. |
| Tool output contains active content | Render untrusted payloads as sanitized text with a restrictive Content Security Policy. |
| SQLite contention slows live updates | WAL mode, short transactions, cursor pagination, batching, and per-run event pages. |
| Open-source users run unsupported source versions | Show compatibility status and capture gaps instead of implying complete telemetry. |
| The project grows into a hosted platform | Keep the first release single-user and local-first; require a separate design for multi-user features. |

## Release packaging

The first release provides:

- A Go binary with embedded Svelte assets.
- Linux x86_64 and arm64 release artifacts.
- Source builds for other platforms on a best-effort basis.
- Adapter setup documentation for each supported source.
- A compatibility matrix listing source versions and capture gaps.
- Example sanitized fixtures for bug reports.

The initial dashboard uses a high-contrast, sharp-edged visual system with a dense information layout, strong typography, restrained color, visible keyboard focus, and non-color status labels. It avoids decorative effects that reduce readability.

## Acceptance criteria

Version one is ready when all of the following are true:

1. A developer can download one Linux binary, run `traceboard start`, and open the dashboard without installing a separate database or frontend server.
2. Traceboard makes no external network request during normal operation.
3. Each supported source has a documented install path, health check, compatibility statement, and passing smoke run.
4. A captured run appears in the open dashboard without a manual reload.
5. The inspector shows ordered prompts, model events, tools, file changes, errors, and terminal state when those fields are available.
6. Missing source fields and capture gaps are visible.
7. Search and source, project, status, and time filters return matching runs.
8. Replay reconstructs captured state and cannot re-execute a tool.
9. Collector failure does not block the source agent and produces a visible local spool state.
10. Duplicate and out-of-order events do not corrupt run state.
11. Interrupted runs become `incomplete` rather than false failures.
12. Redaction tests find no seeded secret in the database, search index, spool, quarantine table, logs, or exports.
13. Export produces the documented JSON and Markdown files.
14. Deletion removes the run and all associated indexed or alert data.
15. Go tests, adapter contract tests, integration tests, Playwright tests, security checks, and release smoke tests pass.

## Technical references

The initial adapter design is based on current native documentation:

- OpenCode plugins: https://opencode.ai/docs/plugins/
- Claude Code observability: https://code.claude.com/docs/en/agent-sdk/observability
- Claude Code hooks: https://code.claude.com/docs/en/hooks
- Codex hooks: https://developers.openai.com/codex/hooks
- Antigravity SDK lifecycle and hooks: https://antigravity.google/docs/sdk/lifecycle/
- Antigravity IDE hooks: https://www.antigravity.google/docs/ide/hooks/
- Gemini CLI telemetry: https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/telemetry.md

Source documentation can change. The checked-in compatibility fixtures and tested version declarations are the authority for what Traceboard supports at a given release.
