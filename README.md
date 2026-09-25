# Traceboard

Local observability tool for coding agents, including OpenCode, Claude Code, Codex, Antigravity, and Gemini CLI. It records runs locally, shows an event timeline, and helps you find where a task failed.

```text
┌───────────────────────────────────────────────┬──────────────────────────────────────────────────────────────────┐
│ TRACEBOARD // RUN INDEX                       │ RUN INSPECTOR: opencode-run-84f9 (Failed)                        │
├───────────────────────────────────────────────┼──────────────────────────────────────────────────────────────────┤
│ ▶ opencode-run-84f9   ● Failed     12m ago    │ Duration: 4m 12s | Model: gemini-pro | Ingest: 82.4k tokens      │
│   Refactor auth middleware & session token    │ Summary: 4 files modified, 12 tool calls, failed at step #9      │
│                                               ├──────────────────────────────────────────────────────────────────┤
│ ▶ claude-run-1982     ✓ Complete   1h ago     │ [00:01] run.started            session initialized (opencode)    │
│   Fix database migration race condition       │ [00:04] model.requested        prompt: "fix token expiry check"  │
│                                               │ [00:12] tool.started           read_file: internal/auth/token.go │
│ ▶ codex-run-0041      ✓ Complete   3h ago     │ [00:45] file.changed           internal/auth/token.go (+12, -4)  │
│   Add Vitest suite for WebSocket client       │ [01:10] command.failed         go test ./internal/auth (exit 1)  │
│                                               ├──────────────────────────────────────────────────────────────────┤
│                                               │ [ REPLAY SCRUBBER ] ────────●──────────────────── (Step 9 of 14) │
└───────────────────────────────────────────────┴──────────────────────────────────────────────────────────────────┘
```

## How it works

When an agent fails, hangs, or edits files unexpectedly, tracing what happened usually means digging through terminal output and JSON session logs. 

Traceboard listens on `127.0.0.1:47821` for agent hook events and OTLP traces. It normalizes incoming events into a single schema (`traceboard-event-v1`), strips detected API keys and secrets before saving anything, and writes everything to a local SQLite database with WAL mode and FTS5 search enabled.

A web UI built with Svelte 5 is embedded directly into the Go binary. You can search runs, read through the chronological event log, inspect tool payloads, and use the replay scrubber to see the state at any point in the run without re-executing tools.

```text
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  OpenCode Hook  │     │   Claude Code   │     │   Antigravity   │
│   (TypeScript)  │     │      (OTLP)     │     │    IDE & SDK    │
└────────┬────────┘     └────────┬────────┘     └────────┬────────┘
         │                       │                       │
         └───────────────────────┼───────────────────────┘
                                 ▼
                     ┌───────────────────────┐
                     │   Local HTTP / OTLP   │
                     │  (127.0.0.1:47821)    │
                     └───────────┬───────────┘
                                 ▼
                     ┌───────────────────────┐
                     │ Pre-persist redaction │
                     └───────────┬───────────┘
                                 ▼
                     ┌───────────────────────┐
                     │  SQLite (WAL + FTS5)  │
                     └───────────┬───────────┘
                                 ▼
                     ┌───────────────────────┐
                     │ Embedded Svelte 5 UI  │
                     │  (Live WebSockets)    │
                     └───────────────────────┘
```

## Setup and usage

### Build from source

```bash
make build
```

This compiles the frontend assets, embeds them, and outputs `bin/traceboard`.

### Run the server

```bash
./bin/traceboard start
```

This starts the collector and prints a one-time sign-in link for the local dashboard.

### Check installation health

```bash
./bin/traceboard doctor
```

## CLI reference

```text
traceboard start                     Run the collector and embedded dashboard
traceboard status                    Show the configured endpoint and store state
traceboard sources                   List configured sources and capture modes
traceboard configure <source>        Apply source integration steps
traceboard doctor                    Check the local installation end to end
traceboard auth rotate               Invalidate sessions and print a new sign-in URL
traceboard export <run-id>           Write a run to a local file
traceboard delete <run-id>           Delete a run and its indexed data
traceboard retention set <duration>  Set automatic run retention
traceboard daemon install            Install a systemd user service
```

## License

MIT License. Copyright (c) 2026 Darnell Dijksteel.

