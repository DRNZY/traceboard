# Privacy and security

Traceboard's boundary is narrow on purpose. It reads your agents' telemetry,
keeps it on your machine, and shows it to you.

## Where data lives

- SQLite database and its WAL, next to the configuration file.
- A bounded spool per source, used only while the collector is offline.
- A quarantine table for events that failed validation.
- Exports, only where you ask for them.

Nothing is sent anywhere. The dashboard loads no external script, font, image,
or analytics script, and makes no outbound request during normal operation. The
browser suite asserts that no external request is ever issued.

## Network boundary

- The listener binds to a loopback address. A non-loopback address is rejected at
  startup, not merely warned about.
- The `Host` header is validated against loopback and configured names, so a DNS
  rebinding attempt cannot reach the collector.
- Cross-origin mutations are rejected. The check uses `Origin` and, when a
  document has an opaque origin, the browser-supplied `Sec-Fetch-Site` header,
  which page script cannot set.
- Ingest and query routes both reject unauthenticated requests. Authentication
  errors carry a fixed message and never echo a response body, a path, or a
  configuration location.

## Credentials

On first start Traceboard creates a mode `0700` configuration directory and a
mode `0600` configuration file containing three random values of at least 32
bytes each:

| Credential | Used for |
| --- | --- |
| Ingest token | Local source adapters posting events |
| Dashboard sign-in token | A one-time URL printed at start |
| Session secret | Reserved for future cookie signing |

Only SHA-256 hashes of tokens reach the database. The dashboard never receives a
token: sign-in happens once, the token is exchanged for a session, and the
session lives in an `HttpOnly`, `SameSite=Strict` cookie. The settings page
reports only whether a token is configured, never its value.

`traceboard auth rotate` invalidates every session and prints a new one-time
sign-in URL. It does not change the ingest token, so running agents keep working.

## Redaction

Redaction runs before any event, raw payload, search index, spool file, or
quarantine record is written.

Version one detects bearer tokens, private-key blocks, common API key shapes,
credentialed database URLs, and any environment values you configure. You can add
your own regular expressions; a match is replaced with `[REDACTED:<rule>]` and
only the rule name and field path are recorded.

A match is never stored in full, and a bounded prefix is not kept either: a
truncated secret is still a leaked secret. The replacement is the whole value.

Redaction is a defence, not a guarantee. A secret in an unusual format that
matches no rule will be stored. Review what your agents capture.

## Capture limits

| Limit | Value |
| --- | --- |
| Request body | 4 MiB |
| Raw payload | 1 MiB |
| Content field | 256 KiB |
| Events per batch | 1 000 |

These are safety ceilings. A configuration file may lower them; nothing raises
them. Truncation is recorded in the event's capture record and shown in the
dashboard.

## Untrusted content

Captured prompts, tool output, errors, and raw payloads are untrusted input. They
are rendered as text. The Content Security Policy is `default-src 'none'` with
`script-src 'self'` and `style-src 'self'`, and the sign-in page is served its
own stylesheet from this process so no exception is needed. The browser suite
asserts that a hostile payload produces no element and executes nothing.

## What is not protected

Version one does not claim full-database encryption. The database and its WAL
are user-only files on your filesystem. Use full-disk encryption if the machine
itself is not trusted.
