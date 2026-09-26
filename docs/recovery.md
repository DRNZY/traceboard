# Recovery

## The collector was not running

Nothing is lost. Agent hooks and adapters spool locally whenever the collector
cannot be reached, and the collector drains the spool before it starts serving.

```sh
traceboard start    # prints "spool: replayed N buffered events" when it drains
```

A drain is idempotent. Events already stored are recognized by their source
event ID and skipped, so draining twice never duplicates an event.

## The spool hit its limit

The default ceiling is 100 MiB per source with a seven-day age limit. When the
ceiling is reached Traceboard keeps the newest events and records a visible
loss-risk warning in source health. Dropped data is never silent.

```sh
traceboard sources
```

The `QUARANTINE` column shows events that failed validation. A rising count
usually means a source changed its format or that credentials are missing from
the environment.

## An agent is not reporting

1. `traceboard sources` shows whether the source has ever reported and how long
   ago it last sent a heartbeat.
2. `traceboard doctor` checks that the integration is installed and that the
   collector is listening.
3. Check the source's own configuration. A hook that the agent does not trust
   never runs, and Traceboard cannot bypass that.
4. Restart the agent. The capture mode is read at collector start, so a mode
   change needs a restart too.

A source is reported as disconnected after three missed heartbeats. That is
deliberate: an agent restart should not look like a failure.

## A run is still marked running

A run with no terminal event stays active. That is the honest state: no source
reported an end. After fifteen minutes of silence the run is reported as stalled
and raises an alert. Mark the run handled from the dashboard, or wait for the
agent to report.

## A run's timeline looks incomplete

The dashboard says so explicitly rather than showing a short timeline as if it
were whole. Refetch the missing range from the event endpoint, or reload the
page to resync. This state appears when the live stream reported a gap or when a
client is recovering from a reconnect.

## Ingest is rejected

`401` means the ingest token is wrong. A hook reads it from the protected
configuration or from the environment; check that the environment variable is set
in the agent's context and not only in your shell.

`413` means the payload exceeded a limit. See the limits table in
[privacy.md](privacy.md).

## Recovery from a corrupt store

The database is a normal SQLite file. Take a copy before any repair.

```sh
sqlite3 traceboard.db 'PRAGMA integrity_check;'
sqlite3 traceboard.db 'VACUUM;'
```

Migrations are forward-only and refuse to run against an edited file, because
the recorded hash is what the data depends on. If a migration was applied
incorrectly during development, restore the migration file rather than editing
the database.

## Removing everything

```sh
traceboard delete <run-id> "<exact run title>"   # one run, with its search entries
traceboard retention preview                      # what policy would remove
traceboard retention apply                        # remove it now
rm -rf ~/.config/traceboard                      # the whole installation
```
