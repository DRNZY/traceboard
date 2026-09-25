# Traceboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a local-first Go application that captures coding-agent runs, normalizes them into a searchable event model, and presents a live forensic run index and inspector.

**Architecture:** A loopback-only Go server owns authentication, JSON and OTLP ingestion, normalization, redaction, SQLite persistence, alerts, retention, export, and a WebSocket update stream. A Svelte + Vite application is embedded into the binary. Source-specific hook commands and an OpenCode plugin translate native lifecycle data into the shared `traceboard-event-v1` envelope.

**Tech Stack:** Go 1.24+, `modernc.org/sqlite`, `go-chi/chi`, `gorilla/websocket`, OpenTelemetry protobufs, Svelte 5, TypeScript, Vite, Vitest, Playwright, plain CSS.

**Spec:** `docs/superpowers/specs/2026-09-25-traceboard-design.md`

## Global Constraints

- Bind only to `127.0.0.1`; reject non-loopback bind addresses in version one.
- Default dashboard and ingest endpoint: `http://127.0.0.1:47821`.
- Store data in SQLite with WAL, foreign keys, unique source event IDs, per-run ingest sequence, and FTS5 search.
- Default capture mode is `metadata`; supported modes are `off`, `metadata`, and `detailed`.
- Redact content before database, FTS, quarantine, spool, log, or export persistence.
- Never capture hidden chain-of-thought.
- Never re-execute a prompt, tool, or command during replay.
- Keep prompts and tool bodies out of raw storage in `metadata` mode.
- Use one deterministic event schema for every source.
- No third-party dashboard scripts, fonts, analytics, or network calls.
- Use sharp corners, high contrast, mathematical spacing, visible focus, and non-color status labels.
- Do not use Lucide icons, glassmorphism, gradients, pill badges, generic feature-card layouts, or fade-on-hover interactions.
- Do not commit, push, publish, or modify the user's live agent configurations unless explicitly requested.
- Verify Linux amd64 builds locally. Treat arm64 and other platforms as release work until CI covers them.

---

### Task 1: Bootstrap the Go and web workspaces

**Files:**
- Create: `go.mod`
- Create: `cmd/traceboard/main.go`
- Create: `internal/buildinfo/buildinfo.go`
- Create: `internal/frontend/embed.go`
- Create: `internal/frontend/dist/index.html`
- Create: `web/package.json`
- Create: `web/tsconfig.json`
- Create: `web/vite.config.ts`
- Create: `web/src/main.ts`
- Create: `web/src/App.svelte`
- Create: `Makefile`
- Create: `.gitignore`

**Interfaces:**
- Produces: `buildinfo.Version`, `buildinfo.Commit`, `buildinfo.BuildDate`
- Produces: `frontend.FS() fs.FS`
- Produces: `main()` that returns a nonzero exit code for command failures

- [ ] **Step 1: Create the Go module and build metadata test**

```go
func TestVersionDefaultsToDevelopment(t *testing.T) {
    if Version == "" {
        t.Fatal("Version must not be empty")
    }
}
```

- [ ] **Step 2: Run the focused test and confirm it fails**

Run: `go test ./internal/buildinfo`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Add build metadata, embedded frontend access, and a minimal CLI**

```go
var (
    Version   = "dev"
    Commit    = "none"
    BuildDate = "unknown"
)
```

- [ ] **Step 4: Add the Svelte/Vite workspace and strict TypeScript configuration**

```json
{
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "check": "svelte-check --tsconfig ./tsconfig.json",
    "test": "vitest",
    "test:run": "vitest run"
  }
}
```

- [ ] **Step 5: Add Make targets**

```make
build: web-build go-build
web-build:
	cd web && npm ci && npm run build
go-build:
	go build -o bin/traceboard ./cmd/traceboard
test:
	go test ./...
	cd web && npm run test:run
check:
	go vet ./...
	cd web && npm run check
```

- [ ] **Step 6: Install dependencies and verify the bootstrap**

Run: `make build && make test && make check`

Expected: PASS with `bin/traceboard` produced.

- [ ] **Step 7: Inspect the working tree**

Run: `git status --short`

Expected: Only intended bootstrap files are present. Do not commit.

---

### Task 2: Implement configuration, event validation, and redaction

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/event/event.go`
- Create: `internal/event/validate.go`
- Create: `internal/event/validate_test.go`
- Create: `internal/redact/redact.go`
- Create: `internal/redact/redact_test.go`
- Modify: `cmd/traceboard/main.go`

**Interfaces:**
- Produces: `config.Load(path string) (config.Config, error)`
- Produces: `config.Config.Ensure(path string) (config.Config, error)`
- Produces: `event.Event`, `event.Batch`, `event.CaptureMode`, `event.Status`
- Produces: `event.Validate(Event) error`
- Produces: `redact.Redactor.Apply(value any) (any, []redact.Match)`

```go
type Config struct {
    ListenAddress   string        `json:"listen_address"`
    DatabasePath    string        `json:"database_path"`
    IngestToken     string        `json:"ingest_token"`
    DashboardToken  string        `json:"dashboard_token"`
    SessionSecret   string        `json:"session_secret"`
    RetentionDays   int           `json:"retention_days"`
    Sources         map[string]SourceConfig `json:"sources"`
}

type Event struct {
    SchemaVersion int            `json:"schema_version"`
    EventID       string         `json:"event_id"`
    SourceEventID string         `json:"source_event_id"`
    Source        string         `json:"source"`
    SourceVersion string         `json:"source_version"`
    RunID         string         `json:"run_id"`
    StepID        string         `json:"step_id,omitempty"`
    ParentStepID  string         `json:"parent_step_id,omitempty"`
    SourceSequence *int64        `json:"source_sequence,omitempty"`
    OccurredAt    time.Time      `json:"occurred_at"`
    Type          string         `json:"type"`
    Status        Status         `json:"status"`
    Capture       map[string]any `json:"capture"`
    Attributes    map[string]any `json:"attributes"`
    Content       map[string]any `json:"content,omitempty"`
    Raw           any            `json:"raw,omitempty"`
}
```

- [ ] **Step 1: Write failing tests for loopback enforcement and secure first-start files**

Test cases: default `127.0.0.1:47821`; non-loopback host rejected; config directory mode `0700`; credential-bearing config mode `0600`; generated tokens contain at least 32 random bytes.

- [ ] **Step 2: Run config tests and confirm failure**

Run: `go test ./internal/config`

Expected: FAIL because `Load` and `Ensure` are undefined.

- [ ] **Step 3: Implement JSON config loading with explicit unknown-field rejection**

Use `json.Decoder.DisallowUnknownFields`. Preserve an existing file when decoding fails.

- [ ] **Step 4: Write failing event validation tests**

Reject unsupported schema versions, missing source/run/time/type, invalid statuses, invalid capture modes, oversized raw values, and `content` in `off` mode.

- [ ] **Step 5: Implement event types and validation**

```go
func Validate(e Event) error {
    if e.SchemaVersion != 1 { return fmt.Errorf("unsupported schema version %d", e.SchemaVersion) }
    if e.Source == "" || e.RunID == "" || e.OccurredAt.IsZero() || e.Type == "" { return errors.New("missing required event identity") }
    return nil
}
```

- [ ] **Step 6: Write failing redaction tests**

Seed API keys, bearer tokens, private-key blocks, credentialed database URLs, configured environment values, and a custom regex. Assert matches never retain the full secret.

- [ ] **Step 7: Implement recursive JSON redaction**

Traverse maps and slices. Replace matches with `[REDACTED:<rule>]`, record field category and rule name, and recurse until payload limits are enforced elsewhere.

- [ ] **Step 8: Verify the task**

Run: `go test ./internal/config ./internal/event ./internal/redact && go vet ./internal/config ./internal/event ./internal/redact`

Expected: PASS.

---

### Task 3: Implement SQLite migrations and transactional storage

**Files:**
- Create: `migrations/001_initial.sql`
- Create: `internal/store/migrate.go`
- Create: `internal/store/migrate_test.go`
- Create: `internal/store/store.go`
- Create: `internal/store/events.go`
- Create: `internal/store/runs.go`
- Create: `internal/store/alerts.go`
- Create: `internal/store/store_test.go`

**Interfaces:**
- Produces: `store.Open(path string) (*Store, error)`
- Produces: `(*Store).Close() error`
- Produces: `(*Store).Migrate(context.Context) error`
- Produces: `(*Store).InsertEvents(context.Context, ...event.Event) (InsertResult, error)`
- Produces: `(*Store).ListRuns(context.Context, RunFilter, Cursor, int) (RunPage, error)`
- Produces: `(*Store).GetRun(context.Context, string) (Run, error)`
- Produces: `(*Store).ListEvents(context.Context, string, int64, int) (EventPage, error)`
- Produces: `(*Store).DeleteRun(context.Context, string) (DeleteResult, error)`

```go
type InsertResult struct {
    Accepted int
    Duplicate int
    Quarantined int
}
```

- [ ] **Step 1: Write migration tests against a temporary database**

Assert tables, indexes, foreign keys, WAL mode, FTS5 availability, and migration idempotency.

- [ ] **Step 2: Run migration tests and confirm failure**

Run: `go test ./internal/store -run TestMigrate`

Expected: FAIL because the store does not exist.

- [ ] **Step 3: Create the initial schema**

Create `schema_migrations`, `sources`, `projects`, `runs`, `steps`, `events`, `events_fts`, `alerts`, `quarantine`, and `sessions`. Add unique `(source, source_event_id)` and `(run_id, sequence)` constraints.

- [ ] **Step 4: Implement forward-only embedded migrations**

Track applied filenames and SHA-256 hashes in `schema_migrations`. Reject edits to an applied migration.

- [ ] **Step 5: Write failing idempotency and transaction tests**

Insert the same source event twice, insert a duplicate run sequence with a different source event, interrupt a transaction, and verify run summaries match committed events.

- [ ] **Step 6: Implement event insertion and run summary updates**

Allocate `runs.next_sequence` inside the same transaction as event insertion. Insert valid events, quarantine invalid events with redacted reasons, and update step and run states from event types.

- [ ] **Step 7: Write failing query tests**

Test cursor pagination, source/project/status/time filters, FTS search, event range reads, and deterministic replay ordering.

- [ ] **Step 8: Implement run and event queries**

Order timeline events by `occurred_at`, nullable `source_sequence`, and ingest `sequence`. Keep list pagination stable with `(started_at, id)` cursors.

- [ ] **Step 9: Write deletion and retention-selection tests**

Assert cascading deletion of steps, events, FTS rows, and alerts while active runs are never selected automatically.

- [ ] **Step 10: Implement deletion and retention selection**

Expose preview and execution as separate operations.

- [ ] **Step 11: Verify the task**

Run: `go test ./internal/store && go vet ./internal/store`

Expected: PASS.

---

### Task 4: Implement authentication and the local HTTP server

**Files:**
- Create: `internal/auth/auth.go`
- Create: `internal/auth/auth_test.go`
- Create: `internal/server/server.go`
- Create: `internal/server/middleware.go`
- Create: `internal/server/routes.go`
- Create: `internal/server/server_test.go`
- Create: `internal/live/hub.go`
- Create: `internal/live/hub_test.go`
- Modify: `cmd/traceboard/main.go`

**Interfaces:**
- Produces: `auth.Manager.ExchangeDashboardToken(context.Context, string) (string, error)`
- Produces: `auth.Manager.ValidateSession(context.Context, string) error`
- Produces: `auth.Manager.RotateDashboard(context.Context) (string, error)`
- Produces: `server.New(Dependencies) http.Handler`
- Produces: `live.Hub.Publish(any)`
- Produces: `live.Hub.Subscribe() (<-chan []byte, func())`

- [ ] **Step 1: Write failing auth tests**

Test one-time dashboard exchange, session expiry, logout, rotation, bearer ingest auth, constant-time token comparison, and rejection after use.

- [ ] **Step 2: Implement the auth manager**

Store only SHA-256 session-token hashes and expiry timestamps. Rotate the dashboard token and invalidate sessions whenever the server starts or `auth rotate` runs.

- [ ] **Step 3: Write failing HTTP middleware tests**

Reject non-loopback configuration, invalid Host headers, cross-origin mutations, unauthenticated ingest/query requests, oversized bodies, and missing security headers.

- [ ] **Step 4: Implement middleware**

Set CSP, `X-Content-Type-Options`, `Referrer-Policy`, `Permissions-Policy`, and `X-Frame-Options: DENY`. Limit ingest bodies to 4 MiB.

- [ ] **Step 5: Write failing route tests**

Exercise health, auth exchange, logout, run list/detail/events, source list, alert acknowledgement, static assets, and SPA fallback.

- [ ] **Step 6: Implement the HTTP routes**

Mount API routes under `/api/v1`, auth under `/auth`, health under `/health`, and static assets at `/`.

- [ ] **Step 7: Write failing WebSocket tests**

Verify authenticated upgrade, committed-change delivery, unsubscribe cleanup, sequence-gap message shape, and reconnect snapshot support.

- [ ] **Step 8: Implement the live hub and WebSocket route**

Publish only after store commit. Send `run.changed`, `alert.changed`, and `source.changed` messages with monotonically increasing stream IDs.

- [ ] **Step 9: Verify the task**

Run: `go test ./internal/auth ./internal/live ./internal/server && go vet ./internal/auth ./internal/live ./internal/server`

Expected: PASS.

---

### Task 5: Implement JSON ingestion, hook mappings, and durable spools

**Files:**
- Create: `internal/ingest/service.go`
- Create: `internal/ingest/service_test.go`
- Create: `internal/ingest/spool.go`
- Create: `internal/ingest/spool_test.go`
- Create: `internal/hooks/common.go`
- Create: `internal/hooks/opencode.go`
- Create: `internal/hooks/claude.go`
- Create: `internal/hooks/codex.go`
- Create: `internal/hooks/antigravity.go`
- Create: `internal/hooks/gemini.go`
- Create: `cmd/traceboard/hook.go`
- Create: `adapters/opencode/package.json`
- Create: `adapters/opencode/src/index.ts`
- Create: `adapters/opencode/test/index.test.ts`

**Interfaces:**
- Produces: `ingest.Service.Ingest(context.Context, event.Batch) ingest.Result`
- Produces: `ingest.Spool.Append(source string, payload []byte) error`
- Produces: `ingest.Spool.Drain(context.Context, func([]byte) error) (DrainResult, error)`
- Produces: `traceboard hook <opencode|claude-code|codex|antigravity|gemini-cli>`

- [ ] **Step 1: Write failing ingest-service tests**

Verify validation, redaction before store calls, capture-mode filtering, batch limits, deterministic derived source IDs, partial success, and quarantine behavior.

- [ ] **Step 2: Implement the JSON ingest service**

Accept `{events:[...]}` and single-event payloads. Derive missing `source_event_id` with SHA-256 over source, version, run ID, timestamp, type, and canonical raw payload.

- [ ] **Step 3: Write failing spool tests**

Cover unavailable collector, redacted NDJSON append, 100 MiB limit, seven-day age limit, restart drain, idempotent replay, and 24-hour loss-warning grace.

- [ ] **Step 4: Implement the bounded spool**

Use one mode-`0700` directory and one mode-`0600` file per source. Use atomic temporary-file rename before acknowledging append.

- [ ] **Step 5: Write failing hook mapping tests**

Use representative source payloads for successful, failed, cancelled, malformed, and out-of-order runs. Assert exact normalized event types and parent-step IDs.

- [ ] **Step 6: Implement canonical hook mappings**

Map lifecycle names to Traceboard event types. Never emit `error.recorded` for an unknown benign event; use `source.extension`.

- [ ] **Step 7: Implement the stdin hook command**

Read up to 1 MiB from stdin, redact, POST with the ingest bearer token, spool on connection failure, print no sensitive data, and return success after bounded local handling.

- [ ] **Step 8: Write failing OpenCode plugin tests**

Use a mocked `fetch` and fake clock. Assert batching, 750 ms timeout, redaction, spool fallback, and no modification of OpenCode inputs.

- [ ] **Step 9: Implement the OpenCode plugin**

Subscribe to session, message, tool, file, permission, compaction, error, and idle events. Export a factory function suitable for OpenCode's plugin API.

- [ ] **Step 10: Verify the task**

Run: `go test ./internal/ingest ./internal/hooks ./cmd/traceboard && npm install && npm run test:run --prefix adapters/opencode`

Expected: PASS.

---

### Task 6: Implement OTLP/HTTP ingestion

**Files:**
- Create: `internal/otlp/decoder.go`
- Create: `internal/otlp/decoder_test.go`
- Create: `internal/otlp/trace.go`
- Create: `internal/otlp/log.go`
- Create: `internal/otlp/metric.go`
- Create: `internal/otlp/normalize.go`
- Create: `internal/otlp/normalize_test.go`
- Create: `internal/server/otlp_routes_test.go`

**Interfaces:**
- Produces: `otlp.Decode(contentType string, body []byte, limit int64) (Signal, error)`
- Produces: `otlp.Normalize(Signal, SourceContext) ([]event.Event, error)`

- [ ] **Step 1: Write failing decoder tests**

Cover JSON and protobuf traces, logs, and metrics; gzip; invalid content; unsupported schema; malformed protobuf; and decompressed size limits.

- [ ] **Step 2: Implement OTLP decoding**

Use `go.opentelemetry.io/proto/otlp` and `protojson`. Detect JSON versus protobuf from content type and body rather than trusting one header.

- [ ] **Step 3: Write failing normalization tests**

Map trace roots to runs, spans to steps, model operations, tool calls, GenAI conversation IDs, token usage, errors, source extensions, and resource `service.name` to source names.

- [ ] **Step 4: Implement trace, log, and metric normalization**

Preserve unknown attributes in `raw` only when capture mode permits. Emit metric records as `source.extension` with `extension_kind=metric`.

- [ ] **Step 5: Write failing OTLP route tests**

Verify `/v1/traces`, `/v1/logs`, `/v1/metrics`, bearer auth, empty success responses, and redaction before store calls.

- [ ] **Step 6: Connect OTLP routes to the ingest service**

Return partial-success status when valid events are stored and invalid events are quarantined.

- [ ] **Step 7: Verify the task**

Run: `go test ./internal/otlp ./internal/server && go vet ./internal/otlp ./internal/server`

Expected: PASS.

---

### Task 7: Build the Svelte run index and forensic inspector

**Files:**
- Create: `web/src/lib/api.ts`
- Create: `web/src/lib/types.ts`
- Create: `web/src/lib/realtime.ts`
- Create: `web/src/lib/format.ts`
- Create: `web/src/components/RunIndex.svelte`
- Create: `web/src/components/RunInspector.svelte`
- Create: `web/src/components/EventTimeline.svelte`
- Create: `web/src/components/EventDetail.svelte`
- Create: `web/src/components/ReplayScrubber.svelte`
- Create: `web/src/components/SourceHealth.svelte`
- Create: `web/src/components/AlertBanner.svelte`
- Create: `web/src/pages/RunsPage.svelte`
- Create: `web/src/pages/SourcesPage.svelte`
- Create: `web/src/pages/SettingsPage.svelte`
- Create: `web/src/app.css`
- Modify: `web/src/App.svelte`
- Create: `web/src/lib/api.test.ts`
- Create: `web/src/components/RunIndex.test.ts`
- Create: `web/src/components/ReplayScrubber.test.ts`

**Interfaces:**
- Produces: `api.listRuns(filter, cursor): Promise<RunPage>`
- Produces: `api.getRun(id): Promise<RunDetail>`
- Produces: `api.listEvents(id, after, limit): Promise<EventPage>`
- Produces: `realtime.connect(onMessage, onGap): () => void`
- Produces: `RunIndex`, `RunInspector`, and `ReplayScrubber` Svelte components

- [ ] **Step 1: Write failing API-client tests**

Cover auth failure, run pagination, event range recovery, alert acknowledgement, and typed error handling without exposing response secrets.

- [ ] **Step 2: Implement the typed API client**

Use same-origin `fetch` with `credentials: include`. Never read bearer tokens in browser code.

- [ ] **Step 3: Write failing component tests**

Test filtering, selected-run state, status text plus shape, event expansion, non-executing replay, and missing-data labels.

- [ ] **Step 4: Implement the run index**

Use a two-pane grid with a fixed-width run list and fluid inspector. Keep DOM order equal to visual order. Use native buttons and inputs.

- [ ] **Step 5: Implement the inspector and timeline**

Show deterministic summary, capture mode, source compatibility, events, parent-child steps, changed paths, source-reported failure, and redacted raw details.

- [ ] **Step 6: Implement the replay scrubber**

Maintain an integer selected event index. Reconstruct only the events up to that index. Do not expose an execute or rerun action.

- [ ] **Step 7: Implement live updates and gap recovery**

Apply committed run versions. On stream gap, fetch changed runs and event ranges before resuming live updates.

- [ ] **Step 8: Implement source and settings pages**

Allow capture-mode selection, source health visibility, retention display, token rotation instruction, export, and confirmed deletion.

- [ ] **Step 9: Implement the visual system**

Use a black, white, warm-bone, and one red accent palette; sharp corners; 4/8-pixel spacing; condensed display typography; monospace metadata; visible `:focus-visible`; reduced-motion support; and no external assets.

- [ ] **Step 10: Verify the task**

Run: `npm run test:run && npm run check && npm run build --prefix web`

Expected: PASS and embedded assets generated under `internal/frontend/dist`.

---

### Task 8: Implement alerts, desktop notifications, export, and retention

**Files:**
- Create: `internal/alerts/evaluator.go`
- Create: `internal/alerts/evaluator_test.go`
- Create: `internal/notify/notify.go`
- Create: `internal/notify/notify_test.go`
- Create: `internal/export/export.go`
- Create: `internal/export/export_test.go`
- Create: `internal/retention/retention.go`
- Create: `internal/retention/retention_test.go`
- Modify: `internal/server/routes.go`
- Modify: `cmd/traceboard/main.go`

**Interfaces:**
- Produces: `alerts.Evaluator.Evaluate(context.Context, time.Time) error`
- Produces: `notify.Notifier.Notify(context.Context, Alert) error`
- Produces: `export.Run(ctx, store, runID, format, destination) error`
- Produces: `retention.Preview(ctx, store, Policy) (DeletionPreview, error)`
- Produces: `retention.Apply(ctx, store, Policy, time.Time) (DeletionResult, error)`

- [ ] **Step 1: Write failing alert tests**

Cover immediate failure, 15-minute stalled run, three missed 30-second source heartbeats, idempotent alert creation, acknowledgement, and resolution.

- [ ] **Step 2: Implement alert evaluation**

Run every 30 seconds. Create one open alert per stable `(type, target_id)` key.

- [ ] **Step 3: Write failing notifier tests**

Assert Linux `notify-send` argument construction, disabled notifier behavior, and content that excludes raw output and secrets.

- [ ] **Step 4: Implement the notifier**

Use `exec.CommandContext` with fixed executable and argument slices. Treat missing desktop-notification tools as disabled, not as server failure.

- [ ] **Step 5: Write failing export tests**

Export normalized JSON, readable Markdown, and a redacted raw ZIP. Assert no event or FTS row leaks into unrelated exports.

- [ ] **Step 6: Implement per-run export**

Write to a user-selected local path with mode `0600`. Refuse to overwrite unless an explicit force flag is present.

- [ ] **Step 7: Write failing retention tests**

Cover keep-infinite, fixed days, newest-N-per-project, active-run exemption, preview, and execution counts.

- [ ] **Step 8: Implement retention**

Preview before destructive automatic deletion. Active runs are excluded from all policies.

- [ ] **Step 9: Verify the task**

Run: `go test ./internal/alerts ./internal/notify ./internal/export ./internal/retention && go vet ./internal/alerts ./internal/notify ./internal/export ./internal/retention`

Expected: PASS.

---

### Task 9: Complete CLI operations and source configuration

**Files:**
- Create: `cmd/traceboard/start.go`
- Create: `cmd/traceboard/status.go`
- Create: `cmd/traceboard/sources.go`
- Create: `cmd/traceboard/configure.go`
- Create: `cmd/traceboard/doctor.go`
- Create: `cmd/traceboard/auth.go`
- Create: `cmd/traceboard/export.go`
- Create: `cmd/traceboard/delete.go`
- Create: `cmd/traceboard/retention.go`
- Create: `cmd/traceboard/daemon.go`
- Create: `cmd/traceboard/commands_test.go`
- Create: `internal/configure/configure.go`
- Create: `internal/configure/configure_test.go`

**Interfaces:**
- Produces: `traceboard start|status|sources|configure|doctor|auth rotate|export|delete|retention set|daemon install|daemon uninstall`
- Produces: `configure.Apply(source, root string, mode event.CaptureMode, dryRun bool) (ChangeSet, error)`

- [ ] **Step 1: Write failing command tests**

Cover help output, unknown commands, missing arguments, no secret leakage, and nonzero exit codes.

- [ ] **Step 2: Implement start, status, sources, doctor, and auth rotation**

`start` drains spools before serving and prints the one-time dashboard URL. `doctor` checks config, permissions, migrations, listeners, sources, and OTLP reachability.

- [ ] **Step 3: Write failing configure tests using temporary directories**

Assert backup creation, idempotent merge, preserved unknown JSON keys, dry-run output, and refusal to write outside a recognized agent configuration root.

- [ ] **Step 4: Implement source configuration**

Support OpenCode plugin registration, Claude Code environment/hooks, Codex hooks, Antigravity hooks, and Gemini CLI OTLP settings. Do not run these commands against live user configuration during tests or development.

- [ ] **Step 5: Implement export, delete, and retention commands**

Deletion requires the run ID plus the exact run title as confirmation. Export supports `json`, `markdown`, and `raw`.

- [ ] **Step 6: Implement systemd user daemon commands**

Generate a user service that runs the absolute binary path with the resolved config path. `uninstall` removes only the exact Traceboard unit and asks the operating system to reload the user daemon.

- [ ] **Step 7: Verify the task**

Run: `go test ./cmd/traceboard ./internal/configure && go build -o bin/traceboard ./cmd/traceboard`

Expected: PASS.

---

### Task 10: Add integration, browser, security, and release verification

**Files:**
- Create: `tests/integration/collector_test.go`
- Create: `tests/integration/reconnect_test.go`
- Create: `tests/integration/spool_test.go`
- Create: `web/playwright.config.ts`
- Create: `web/e2e/run-inspector.spec.ts`
- Create: `web/e2e/security.spec.ts`
- Create: `docs/compatibility.md`
- Create: `docs/privacy.md`
- Create: `docs/architecture.md`
- Create: `.github/workflows/ci.yml`
- Create: `.goreleaser.yml`
- Modify: `README.md`

**Interfaces:**
- Produces: `go test -tags=integration ./tests/integration/...`
- Produces: `npm run test:e2e --prefix web`
- Produces: `make verify`

- [ ] **Step 1: Write collector integration tests**

Start the real server with a temporary config and database. Ingest representative JSON and OTLP fixtures. Verify database rows, run summaries, and WebSocket delivery.

- [ ] **Step 2: Write reconnect and spool integration tests**

Stop the server, submit hooks, restart it, and verify ordered idempotent drain. Kill the process during a run and verify `incomplete` status.

- [ ] **Step 3: Write Playwright coverage**

Complete first-run capture setup, ingest a fixture, find the run, inspect and replay it, acknowledge an alert, export, and delete it without a page reload after live ingestion.

- [ ] **Step 4: Write browser security coverage**

Verify unauthenticated API rejection, loopback-only binding, Host rejection, cross-origin mutation rejection, inert HTML/SVG rendering, no external requests, and no token in visible DOM text.

- [ ] **Step 5: Document the system**

Create architecture, privacy, capture-mode, redaction, source compatibility, setup, and recovery documentation. Include AI Observer and other research references with MIT attribution.

- [ ] **Step 6: Add CI and release configuration**

CI runs Go tests, race tests, vet, frontend tests, Svelte checks, frontend build, Playwright tests, and integration tests. Release config builds Linux amd64 and arm64 binaries with embedded web assets.

- [ ] **Step 7: Run complete verification**

Run: `make verify`

Expected: PASS for Go tests, race tests, vet, frontend tests, Svelte check, frontend build, Playwright, and integration tests.

- [ ] **Step 8: Build the release binary**

Run: `make build && file bin/traceboard && bin/traceboard version`

Expected: Linux amd64 executable and version output containing the build commit.

- [ ] **Step 9: Perform a local live smoke test**

Start Traceboard with a temporary config, post a representative run, open the dashboard, and confirm the run updates without reload. Stop the service and remove only temporary test data.

- [ ] **Step 10: Inspect the final working tree**

Run: `git status --short && git diff --stat`

Expected: Only Traceboard implementation, tests, and documentation are changed. Do not commit or push.
