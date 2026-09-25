# Traceboard

Local-first observability for coding-agent runs. Traceboard records what
OpenCode, Claude Code, Codex, Antigravity, and Gemini CLI did, normalizes it into
one versioned event model, and shows a searchable run index with a detailed
timeline.

Everything stays on your machine. There is no account, no hosted service, and
no outbound request.

## What it answers

After a run finishes, open it and read what happened: the ordered prompts, model
calls, tool calls, file changes, child agents, and the first source-reported
failure. Replay reconstructs the recorded state at any point. Replay is a read
of captured data; it can never re-execute a prompt, a tool, or a command.

## Install

Download one Linux binary and run it:

```sh
traceboard start
```

`start` prints a loopback URL that contains a one-time sign-in token. Opening it
exchanges the token for a session cookie and lands you on a clean dashboard URL.
The process listens on `127.0.0.1:47821` and refuses any non-loopback bind
address.

## Connect an agent

```sh
traceboard configure opencode --mode metadata
```

`configure` writes the integration for the named source, backs up any file it
edits, and prints the manual steps that source requires (a restart, a hook-trust
review, an OTLP endpoint). It never bypasses an agent's own trust prompt.

Supported sources and the events each one contributes are listed in
[docs/compatibility.md](docs/compatibility.md).

## Read the data

```sh
traceboard status                 # endpoint, store, retention
traceboard sources                # capture mode and heartbeat per source
traceboard doctor                 # configuration, permissions, migrations, auth
traceboard export <run-id>        # json, markdown, or a redacted raw bundle
traceboard delete <run-id> "<exact run title>"
traceboard retention preview      # what automatic retention would remove
traceboard auth rotate            # invalidate sessions, print a new sign-in URL
```

## Capture modes

| Mode | What is stored |
| --- | --- |
| `off` | Connection health only. No run events, no content. |
| `metadata` | Lifecycle, timing, model and tool names, file paths, status, and token usage. No prompt or tool bodies. Default. |
| `detailed` | Everything above plus bounded prompt, tool input, tool output, and error content. |

`metadata` is the default. A source can ask for more coverage, but never less:
the configured mode is applied on the way in, so a misbehaving adapter cannot
upgrade its own capture level. The dashboard states which mode produced a run
and names anything that was withheld.

## Privacy

- Binds to loopback only. Rejects a non-loopback address at startup.
- Redaction runs before any database, search index, spool, quarantine, log, or
  export write.
- Captured content is rendered as inert text under a strict Content Security
  Policy. No external script, font, or image is ever loaded.
- See [docs/privacy.md](docs/privacy.md) for the full boundary.

## Build from source

```sh
make build      # web assets, then the Go binary
make test       # Go tests plus the frontend suite
make check      # go vet plus svelte-check
make verify     # everything above, plus race, browser, and integration suites
```

## Documentation

- [docs/architecture.md](docs/architecture.md) — how a captured event becomes a
  timeline row
- [docs/privacy.md](docs/privacy.md) — the security and redaction boundary
- [docs/compatibility.md](docs/compatibility.md) — per-source versions and known
  capture gaps
- [docs/setup.md](docs/setup.md) — first run, capture modes, systemd
- [docs/recovery.md](docs/recovery.md) — offline spools, restarts, repairs

## Licence and attribution

Traceboard is an independent implementation. Design work referenced these
MIT-licensed local-first projects for their approach to OTLP decoding, embedded
dashboard packaging, and historical session parsing:

- [`tobilg/ai-observer`](https://github.com/tobilg/ai-observer)
- [`abekdwight/opencode-observability`](https://github.com/abekdwight/opencode-observability)
- [`cleverb/agent-profiler`](https://github.com/cleverb/agent-profiler)

No code was copied from them. Any code reused in the future must retain its
MIT notice and attribution.
