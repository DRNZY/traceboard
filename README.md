<div align="center">

# 🧭 Traceboard

<p>
  <strong>Local-first forensic observability for AI coding agents.</strong><br/>
  <em>Reconstruct what happened, where it failed, and which tool activity led there.</em>
</p>

<p>
  <img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go" />
  <img src="https://img.shields.io/badge/Svelte-5-FF3E00?style=for-the-badge&logo=svelte&logoColor=white" alt="Svelte 5" />
  <img src="https://img.shields.io/badge/SQLite-WAL%20%2B%20FTS5-003B57?style=for-the-badge&logo=sqlite&logoColor=white" alt="SQLite" />
  <img src="https://img.shields.io/badge/License-MIT-black?style=for-the-badge" alt="MIT License" />
</p>

</div>

---

```text
┌───────────────────────────────────────────────┬──────────────────────────────────────────────────────────────────┐
│ TRACEBOARD // RUN INDEX                       │ RUN INSPECTOR: opencode-run-84f9 (Failed)                        │
├───────────────────────────────────────────────┼──────────────────────────────────────────────────────────────────┤
│ ▶ opencode-run-84f9   ● Failed     12m ago    │ Duration: 4m 12s | Model: gemini-pro | Ingest: 82.4k tokens      │
│   Refactor auth middleware & session token    │ Summary: 4 files modified, 12 tool calls, failed at step #9      │
│                                               ├──────────────────────────────────────────────────────────────────┤
│ ▶ claude-run-1982     ✓ Complete   1h ago     │ [00:01] ⚡ run.started         session initialized (opencode)    │
│   Fix database migration race condition       │ [00:04] 🧠 model.requested     prompt: "fix token expiry check"  │
│                                               │ [00:12] 🔧 tool.started        read_file: internal/auth/token.go │
│ ▶ codex-run-0041      ✓ Complete   3h ago     │ [00:45] 📝 file.changed        internal/auth/token.go (+12, -4)  │
│   Add Vitest suite for WebSocket client       │ [01:10] ❌ command.failed      go test ./internal/auth (exit 1)  │
│                                               ├──────────────────────────────────────────────────────────────────┤
│                                               │ ⏪ [ REPLAY SCRUBBER ] ───●────────────────────── (Step 9 of 14) │
└───────────────────────────────────────────────┴──────────────────────────────────────────────────────────────────┘
```

## Overview

When coding agents (OpenCode, Claude Code, Codex, Antigravity, Gemini CLI) fail, hang, or make unexpected changes, developers are left parsing raw session files and fragmented terminal logs. 

Traceboard ingests agent events via local JSON hooks and OTLP telemetry, normalizes them into a versioned event model (`traceboard-event-v1`), and presents a real-time searchable run index and timeline inspector in an embedded web dashboard.

---

## Key Principles

- **100% Local & Private**: Binds exclusively to `127.0.0.1`, makes zero external network requests, and persists all runs in a local SQLite database with WAL mode and FTS5 full-text indexing.
- **Pre-Persistence Redaction**: Automatically detects and strips API keys (`sk-`, `AIza`, `ghp_`), bearer tokens, private keys, and database URLs before data is written to disk or search indexes.
- **Deterministic Replay**: Reconstructs state step-by-step from recorded telemetry without ever re-executing tools, prompts, or terminal commands.
- **Zero Agent Blocking**: Local disk spooling ensures telemetry collection never hangs or slows down your agent loops.
- **Single Standalone Binary**: One self-contained Go binary embedding the compiled Svelte 5 + Vite dashboard.

---

## Ingest Pipeline Architecture

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
                     │ Pre-Persist Redaction │
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

---

## Quick Start

### 1. Build
```bash
make build
```

### 2. Start
```bash
./bin/traceboard start
```

### 3. Run Doctor Diagnostics
```bash
./bin/traceboard doctor
```

---

## Commands

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

---

## License

MIT License. Copyright (c) 2026 Darnell Dijksteel.

