# Setup

## First run

```sh
traceboard start
```

The first start creates a protected configuration directory and a mode `0600`
configuration file with three generated credentials, migrates the database, and
prints a sign-in URL containing a one-time token.

```text
traceboard dev
listening:  http://127.0.0.1:47821
dashboard:  http://127.0.0.1:47821/auth/signin?token=...
database:   /home/you/.config/traceboard/traceboard.db
spool:      /home/you/.config/traceboard/spool
```

Opening the dashboard URL exchanges the one-time token for a session cookie and
redirects to a clean URL, so nothing credential-bearing stays in the address bar
or in browser history.

The token is one-time. If you lose it, run `traceboard auth rotate`. Every start
rotates it, so a leaked URL from an earlier process is never valid again.

## Configuration

The configuration file is JSON with unknown keys rejected, so a typo is an error
rather than a silently ignored setting.

```json
{
  "listen_address": "127.0.0.1:47821",
  "database_path": "/home/you/.config/traceboard/traceboard.db",
  "ingest_token": "...",
  "dashboard_token": "...",
  "session_secret": "...",
  "retention_days": 30,
  "keep_newest_per_project": 0,
  "heartbeat_seconds": 30,
  "sources": {
    "opencode": { "capture_mode": "metadata" }
  }
}
```

Set `TRACEBOARD_CONFIG` to use a different file, and `TRACEBOARD_LISTEN` to run a
second collector on another loopback port. A non-loopback `listen_address` is
rejected.

## Capture modes

`metadata` is the default and captures lifecycle, timing, model and tool names,
file paths, status, and token usage, without prompt or tool bodies. `detailed`
adds bounded content. `off` records connection health only.

```sh
traceboard configure opencode --capture-only --mode detailed
traceboard sources
```

Changing a mode takes effect on the next collector start. The source itself must
also be restarted to pick it up.

## Connecting a source

```sh
traceboard configure opencode --mode metadata
```

`configure` prints what it will change and what you must do. Add `--dry-run` to
see the changes without writing them. The OpenCode plugin module can be printed
for review before installation:

```sh
traceboard configure opencode --print-plugin
```

`configure` backs up any file it edits, keeps keys it does not own, and refuses
to write outside a recognized agent configuration root.

## Running in the background

```sh
traceboard daemon install     # systemd user service
traceboard daemon uninstall   # removes only the traceboard unit
```

The unit runs the absolute binary path, restarts on failure, and reads the
dashboard URL from `journalctl --user -u traceboard`.

## Checking an installation

```sh
traceboard doctor
```

`doctor` verifies the configuration, file permissions, migrations, redaction, the
ingest credential, alert evaluation, the spool, and whether a collector is
already listening. Warnings are information; only a failure returns a nonzero
exit code.
