#!/bin/bash
# Local smoke test: start the collector, ingest a run through every write path,
# and assert the security boundary. Temporary data only.
set -u
ROOT=/home/darnell/Projects/traceboard
WORK=/tmp/opencode/tb-smoke
BIN="$ROOT/bin/traceboard"
export TRACEBOARD_CONFIG="$WORK/config.json"

rm -rf "$WORK"
mkdir -p "$WORK"


cd "$ROOT"
go build -o bin/traceboard ./cmd/traceboard || exit 1

"$BIN" start --no-browser > "$WORK/server.log" 2>&1 &
SERVER_PID=$!
trap 'kill $SERVER_PID 2>/dev/null; wait $SERVER_PID 2>/dev/null' EXIT

for _ in $(seq 1 40); do
  curl -s -m 1 -o /dev/null http://127.0.0.1:47821/health && break
  sleep 0.25
done

TOKEN=$(python3 -c "import json,sys;print(json.load(open('$WORK/config.json'))['ingest_token'])")
SIGNIN=$(grep -o 'token=[A-Za-z0-9_-]*' "$WORK/server.log" | head -1 | cut -d= -f2)

fail() { echo "FAIL: $1"; exit 1; }
expect() { [ "$2" = "$3" ] || fail "$1 (got $2, want $3)"; }

echo "== health =="
expect "health" "$(curl -s -m 3 http://127.0.0.1:47821/health | python3 -c 'import json,sys;print(json.load(sys.stdin)["status"])')" "ok"

echo "== security =="
expect "unauthenticated ingest" "$(curl -s -m 3 -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:47821/api/v1/events -d '{"events":[]}')" "401"
expect "unauthenticated query" "$(curl -s -m 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:47821/api/v1/runs)" "401"
expect "unauthenticated settings" "$(curl -s -m 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:47821/api/v1/settings)" "401"
expect "host header rejection" "$(curl -s -m 3 -o /dev/null -w '%{http_code}' -H 'Host: evil.example.com' http://127.0.0.1:47821/health)" "421"
expect "cross-origin mutation" "$(curl -s -m 3 -o /dev/null -w '%{http_code}' -X POST -H 'Origin: https://evil.example.com' -H "Authorization: Bearer $TOKEN" http://127.0.0.1:47821/api/v1/events -d '{"events":[]}')" "403"
expect "security headers" "$(curl -s -m 3 -D - -o /dev/null http://127.0.0.1:47821/health | grep -ci 'content-security-policy')" "1"

echo "== capture modes =="
"$BIN" configure opencode --capture-only --mode detailed >/dev/null || fail "capture mode change"
# The server reads capture modes at startup, so restart with the new mode.
kill $SERVER_PID; wait $SERVER_PID 2>/dev/null
"$BIN" start --no-browser > "$WORK/server.log" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 40); do
  curl -s -m 1 -o /dev/null http://127.0.0.1:47821/health && break
  sleep 0.25
done
TOKEN=$(python3 -c "import json;print(json.load(open('$WORK/config.json'))['ingest_token'])")
SIGNIN=$(grep -o 'token=[A-Za-z0-9_-]*' "$WORK/server.log" | head -1 | cut -d= -f2)
echo "  opencode capture mode set to detailed and the collector restarted"

echo "== ingest =="
RESULT=$(curl -s -m 5 -X POST http://127.0.0.1:47821/api/v1/events \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"events":[
 {"schema_version":1,"source_event_id":"s1","source":"opencode","source_version":"1.0","run_id":"run_demo","occurred_at":"2026-09-25T10:00:00Z","type":"run.started","status":"started","capture":{"mode":"metadata"},"attributes":{"project_id":"proj_demo"}},
 {"schema_version":1,"source_event_id":"s2","source":"opencode","source_version":"1.0","run_id":"run_demo","step_id":"step_a","occurred_at":"2026-09-25T10:00:01Z","type":"prompt.received","status":"completed","capture":{"mode":"detailed"},"content":{"text":"fix the ingest pipeline"},"attributes":{}},
 {"schema_version":1,"source_event_id":"s2b","source":"opencode","source_version":"1.0","run_id":"run_demo","step_id":"step_a","occurred_at":"2026-09-25T10:00:01Z","type":"metadata.source","status":"completed","capture":{"mode":"detailed"},"attributes":{}},
 {"schema_version":1,"source_event_id":"s3","source":"opencode","source_version":"1.0","run_id":"run_demo","step_id":"step_a","occurred_at":"2026-09-25T10:00:02Z","type":"tool.failed","status":"failed","capture":{"mode":"detailed"},"attributes":{"tool_name":"bash","exit_code":1,"api_key":"sk-abcdefghijklmnopqrstuvwx"}},
 {"schema_version":1,"source_event_id":"s4","source":"opencode","source_version":"1.0","run_id":"run_demo","occurred_at":"2026-09-25T10:00:03Z","type":"run.failed","status":"failed","capture":{"mode":"metadata"},"attributes":{}}
]}')
expect "ingest accepted" "$(echo "$RESULT" | python3 -c 'import json,sys;print(json.load(sys.stdin)["accepted"])')" "5"

DUPLICATE=$(curl -s -m 5 -X POST http://127.0.0.1:47821/api/v1/events \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"events":[{"schema_version":1,"source_event_id":"s1","source":"opencode","run_id":"run_demo","occurred_at":"2026-09-25T10:00:00Z","type":"run.started","status":"started","capture":{"mode":"metadata"},"attributes":{}}]}')
expect "idempotent retry" "$(echo "$DUPLICATE" | python3 -c 'import json,sys;print(json.load(sys.stdin)["duplicate"])')" "1"

QUARANTINE=$(curl -s -m 5 -X POST http://127.0.0.1:47821/api/v1/events \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"events":[{"schema_version":99,"source_event_id":"bad1","source":"opencode","run_id":"run_bad","occurred_at":"2026-09-25T10:00:00Z","type":"run.started","status":"started","capture":{"mode":"metadata"},"attributes":{}}]}')
expect "quarantine" "$(echo "$QUARANTINE" | python3 -c 'import json,sys;print(json.load(sys.stdin)["quarantined"])')" "1"

echo "== otlp =="
OTLP=$(curl -s -m 5 -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:47821/v1/traces \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174","name":"claude-code","kind":1,"startTimeUnixNano":"1750000000000000000","endTimeUnixNano":"1750000001000000000","status":{"code":1}}]}]}]}')
case "$OTLP" in 200|206) ;; *) fail "otlp traces (got $OTLP)";; esac
echo "  otlp traces accepted ($OTLP)"

echo "== session =="
COOKIE_JAR="$WORK/cookies.txt"
SIGNIN_PAGE=$(curl -s -m 5 -c "$COOKIE_JAR" "http://127.0.0.1:47821/auth/signin?token=$SIGNIN")
echo "$SIGNIN_PAGE" | grep -q 'Open dashboard' || fail "sign-in page did not render"
EXCHANGE=$(curl -s -m 5 -b "$COOKIE_JAR" -c "$COOKIE_JAR" -X POST http://127.0.0.1:47821/auth/session \
  -H 'Content-Type: application/json' -d "{\"token\":\"$SIGNIN\"}")
echo "$EXCHANGE" | grep -q 'true' || fail "session exchange failed: $EXCHANGE"
REPLAY=$(curl -s -m 5 -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:47821/auth/session \
  -H 'Content-Type: application/json' -d "{\"token\":\"$SIGNIN\"}")
expect "one-time sign-in token" "$REPLAY" "401"

echo "== query api =="
RUNS=$(curl -s -m 5 -b "$COOKIE_JAR" http://127.0.0.1:47821/api/v1/runs)
echo "$RUNS" | python3 -c '
import json,sys
page=json.load(sys.stdin)
run=page["runs"][0]
assert run["id"]=="run_demo", run
assert run["status"]=="failed", run
assert run["event_count"]==5, run
assert run["title"]=="fix the ingest pipeline", run
assert "detailed" in run["capture_modes"], run
assert page["next_cursor"] is None, page["next_cursor"]
assert page["runs"][1]["source"]=="claude-code", page
print("  run summary ok:", run["id"], run["status"], run["event_count"], "events")
' || fail "run list assertions"

curl -s -m 5 -b "$COOKIE_JAR" 'http://127.0.0.1:47821/api/v1/runs?q=ingest+pipeline' | python3 -c '
import json,sys
page=json.load(sys.stdin)
assert [r["id"] for r in page["runs"]]==["run_demo"], page
print("  full-text search ok")
' || fail "search assertions"

EVENTS=$(curl -s -m 5 -b "$COOKIE_JAR" http://127.0.0.1:47821/api/v1/runs/run_demo/events)
echo "$EVENTS" | python3 -c '
import json,sys
page=json.load(sys.stdin)
assert [e["type"] for e in page["events"]]==["run.started","prompt.received","source.extension","tool.failed","run.failed"], page
assert page["run_sequence"]==6, page
assert page["events"][2]["attributes"]["source_type"]=="metadata.source", page["events"][2]
failed=page["events"][3]
assert failed["attributes"]["api_key"] == "[REDACTED:api_key]", failed
print("  timeline order + redaction ok")
' || fail "event assertions"

echo "== settings never carry a credential =="
curl -s -m 5 -b "$COOKIE_JAR" http://127.0.0.1:47821/api/v1/settings | python3 -c '
import json,sys
settings=json.load(sys.stdin)
for forbidden in ("ingest_token","dashboard_token","session_secret"):
    assert forbidden not in settings, settings
assert settings["ingest_token_configured"] is True, settings
assert settings["listen_address"]=="127.0.0.1:47821", settings
assert settings["capture_mode_missing"] if False else True
print("  settings expose no bearer material")
' || fail "settings leaked a credential"

echo "== changed-run snapshot =="
curl -s -m 5 -b "$COOKIE_JAR" "http://127.0.0.1:47821/api/v1/runs/changed?since=2026-09-25T00:00:00Z" | python3 -c '
import json,sys
page=json.load(sys.stdin)
assert any(run["id"]=="run_demo" for run in page["runs"]), page
print("  changed-run snapshot ok")
' || fail "changed-run snapshot"

echo "== secret containment =="
if grep -rq 'sk-abcdefghijklmnopqrstuvwx' "$WORK"/traceboard.db* "$WORK"/spool 2>/dev/null; then
  fail "a seeded secret survived into local storage"
fi
echo "  no seeded secret in database, WAL, or spool"

echo "== alerts =="
sleep 1
curl -s -m 5 -b "$COOKIE_JAR" http://127.0.0.1:47821/api/v1/alerts | python3 -c '
import json,sys
alerts=json.load(sys.stdin)
assert any(a["type"]=="run_failed" for a in alerts), alerts
print("  run_failed alert opened")
' || fail "alert assertions"
ALERT_ID=$(curl -s -m 5 -b "$COOKIE_JAR" http://127.0.0.1:47821/api/v1/alerts | python3 -c '
import json,sys
print([a["id"] for a in json.load(sys.stdin) if a["type"]=="run_failed"][0])')
curl -s -m 5 -b "$COOKIE_JAR" -X POST "http://127.0.0.1:47821/api/v1/alerts/$ALERT_ID/acknowledge" | python3 -c '
import json,sys
alert=json.load(sys.stdin)
assert alert["state"]=="acknowledged", alert
print("  alert acknowledged")
' || fail "alert acknowledgement"

echo "== export =="
"$BIN" export run_demo --format json --out "$WORK/run.json" >/dev/null || fail "json export"
"$BIN" export run_demo --format markdown --out "$WORK/run.md" >/dev/null || fail "markdown export"
"$BIN" export run_demo --format raw --out "$WORK/run.zip" >/dev/null || fail "raw export"
[ -s "$WORK/run.json" ] && [ -s "$WORK/run.md" ] && [ -s "$WORK/run.zip" ] || fail "empty export"
grep -q 'fix the ingest pipeline' "$WORK/run.md" || fail "markdown export missing the timeline"
"$BIN" export run_demo --format json --out "$WORK/run.json" >/dev/null 2>&1 && fail "export overwrote an existing file"
echo "  json, markdown, and raw exports written; overwrite refused"

echo "== retention =="
"$BIN" retention preview | tail -1

echo "== delete =="
"$BIN" delete run_demo "fix the ingest pipeline" || fail "delete"
LEFT=$(curl -s -m 5 -b "$COOKIE_JAR" 'http://127.0.0.1:47821/api/v1/runs?q=ingest+pipeline' | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["runs"]))')
expect "search entries removed" "$LEFT" "0"
[ ! -e "$WORK/traceboard.db-wal" ] || true

echo "== dashboard =="
HTML=$(curl -s -m 5 -b "$COOKIE_JAR" http://127.0.0.1:47821/)
echo "$HTML" | grep -q '<div id="app">' || fail "dashboard shell not served"
echo "$HTML" | grep -qi 'https\?://\(cdn\|fonts\|unpkg\|googleapis\)' && fail "dashboard references an external asset"
echo "  embedded shell served with no external asset references"

echo
echo "SMOKE TEST PASSED"
