# Architecture

Traceboard is one Go process with an embedded Svelte application. It owns
authentication, ingestion, normalization, redaction, SQLite persistence, alert
evaluation, retention, export, and the live update stream. There is no second
service, no message broker, and no network dependency.

```text
  agent hook / plugin          OTLP exporter
          |                            |
   POST /api/v1/events          POST /v1/{traces,logs,metrics}
          |                            |
          +----------- collector ------+
                             |
              capture mode + redaction  (before any write)
                             |
                       SQLite (WAL)
                             |
              run summaries, steps, FTS
                             |
                  live stream (WebSocket)
                             |
                       Svelte dashboard
```

## The write path

Every event, from every source, takes the same path. There is exactly one place
where a source can affect storage.

1. **Authenticate.** Ingest routes require the bearer ingest token. Query routes
   require the dashboard session cookie. Only a SHA-256 hash of either ever
   reaches the database.
2. **Validate the envelope.** `internal/event` checks the schema version, the
   required identity fields, the status, and the capture mode. An unsupported
   schema version is never silently upgraded: it is quarantined with a reason.
3. **Derive missing identity.** A source event without a `source_event_id` gets
   a SHA-256 hash of its source, version, run, step, type, timestamp, and
   canonical payload. The same source event always derives the same identifier,
   so a retried delivery is deduplicated rather than stored twice.
4. **Apply the configured capture mode.** The mode set in the configuration wins
   over the mode a source claims for itself. `off` stores nothing. `metadata`
   withholds prompt and tool bodies and records which fields were withheld.
5. **Redact.** Secrets are replaced before the event reaches any sink. Content,
   attributes, and raw payloads all go through the redactor.
6. **Insert inside one transaction.** The event, its step, and the run summary
   are written together, so a reader never sees an event missing from its run.
   An invalid event is quarantined; the rest of the batch still commits.
7. **Publish.** After the commit, the changed run is published to the live
   stream. A subscriber that cannot keep up is skipped, and the missing stream ID
   is how the client detects the hole.

## The read path

- **Run index.** Cursor pagination over `(sort time, id)`, never offsets, so a
  page stays stable while new runs arrive. Filters for source, project, status,
  capture mode, alert state, time range, and parent run are evaluated in SQLite.
- **Search.** FTS5 over a redacted, per-event text column. The query is rebuilt
  from quoted terms, so user input cannot change the shape of the FTS query.
- **Timeline.** Events are ordered by `occurred_at`, then `source_sequence` when
  present, then the Traceboard ingest sequence. The ingest sequence is the
  tie-breaker that makes the order total and stable.
- **Ranges.** A range read returns a resume cursor and the run's current head.
  When the cursor sits behind the head, the client knows events are missing and
  refuses to describe the run as complete.

## The live channel

The dashboard opens one WebSocket at `/api/v1/stream`. The first frame states
the current stream ID. Each committed change carries a monotonically increasing
stream ID, so a dropped frame is detectable rather than silent.

The client keeps a per-run cursor of the highest contiguous ingest sequence it
holds. A skipped sequence does not advance that cursor: the gap keeps being
reported until the client acknowledges the range it actually refetched. A
reconnect is itself a gap, because anything may have been committed while the
client was away.

## Why the run summary lives in the same transaction

The design goal is that a developer can trust the timeline. If a run summary
were updated after the event insert, a crash in between would leave a run that
claims a count it does not have. Writing both in one transaction costs a
little work per event and removes the possibility of a lying summary.

## Storage

SQLite with WAL, foreign keys, and a per-run monotonic ingest sequence.

| Table | Holds |
| --- | --- |
| `sources` | Capture mode, version, heartbeat, connection state |
| `projects` | Project identity and path |
| `runs` | Run summary, status, timestamps, counts, next sequence, capture modes |
| `steps` | One bounded operation with parent and terminal state |
| `events` | One normalized event, its capture record, and its redacted payload |
| `events_fts` | FTS5 index over redacted text |
| `alerts` | One open alert per stable `(type, target)` key |
| `quarantine` | Malformed events with a redacted payload and a reason |
| `sessions` | Dashboard sessions, stored as hashes |
| `schema_migrations` | Applied migration filenames and hashes |

Migrations are forward-only. Editing an applied migration is refused, because
the recorded hash would no longer describe the schema the data depends on.

## Failure behaviour

- An adapter never blocks an agent. The hook command has a short timeout and then
  spools locally; it always exits successfully.
- A spool is bounded per source and aged out. When the ceiling is reached the
  newest events are kept and the loss is visible in source health.
- A source can be up to three missed heartbeats old before it is reported as
  disconnected, which tolerates an agent restart.
- A run with no terminal event stays active. It is reported as stalled after
  fifteen minutes of silence, never as a failure, because the source never
  reported one.
