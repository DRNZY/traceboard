# Traceboard

Local-first, open-source forensic observability for AI coding agents (OpenCode, Claude Code, Codex, Antigravity, Gemini CLI).

## Overview

Traceboard captures agent runs via local JSON hooks and OTLP telemetry, normalizes them into a versioned event model (`traceboard-event-v1`), and presents a searchable run index and timeline inspector in an embedded web dashboard.

## Key Features

- **100% Local & Private**: Binds strictly to `127.0.0.1`, makes zero external network requests, and persists data to a local SQLite database (WAL + FTS5).
- **Pre-Persistence Redaction**: Automatically sanitizes API keys, bearer tokens, PEM private keys, and database URLs before data is written to disk or search indexes.
- **Deterministic Replay**: Step-by-step state reconstruction across model calls, tool calls, and file modifications without re-executing actions.
- **Fast Standalone Binary**: Single Go binary embedding the compiled Svelte 5 + Vite forensic dashboard.

## Quick Start

```bash
# Build the standalone binary
make build

# Start the collector and dashboard
./bin/traceboard start

# Run doctor checks
./bin/traceboard doctor
```

## License

MIT
