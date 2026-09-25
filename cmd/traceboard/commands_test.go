package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"traceboard/internal/event"
)

func newTestEnvironment(t *testing.T) (*environment, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure temp directory: %v", err)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	env := &environment{
		configPath: filepath.Join(directory, "config.json"),
		stdout:     stdout,
		stderr:     stderr,
		stdin:      strings.NewReader(""),
	}
	return env, stdout, stderr, directory
}

func TestNoArgumentsPrintsUsageAndFails(t *testing.T) {
	env, stdout, stderr, _ := newTestEnvironment(t)
	if code := dispatch(env, nil); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "traceboard start") {
		t.Fatalf("usage was not printed to stderr: %q (stdout %q)", stderr.String(), stdout.String())
	}
}

func TestHelpSucceedsAndListsEveryCommand(t *testing.T) {
	env, stdout, _, _ := newTestEnvironment(t)
	if code := dispatch(env, []string{"help"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	for _, command := range []string{
		"start", "status", "sources", "configure", "doctor", "auth rotate",
		"export", "delete", "retention set", "daemon install", "hook", "version",
	} {
		if !strings.Contains(stdout.String(), command) {
			t.Fatalf("help does not mention %q", command)
		}
	}
}

func TestUnknownCommandFails(t *testing.T) {
	env, _, stderr, _ := newTestEnvironment(t)
	if code := dispatch(env, []string{"teleport"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown command: teleport") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestVersionReportsBuildMetadata(t *testing.T) {
	env, stdout, _, _ := newTestEnvironment(t)
	if code := dispatch(env, []string{"version"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	for _, field := range []string{"version:", "commit:", "build_date:"} {
		if !strings.Contains(stdout.String(), field) {
			t.Fatalf("version output is missing %q: %q", field, stdout.String())
		}
	}
}

func TestVersionRejectsExtraArguments(t *testing.T) {
	env, _, stderr, _ := newTestEnvironment(t)
	if code := dispatch(env, []string{"version", "extra"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "usage: traceboard version") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestMissingRequiredArgumentsAreRejected(t *testing.T) {
	cases := []struct {
		arguments []string
		want      string
	}{
		{[]string{"configure"}, "usage: traceboard configure"},
		{[]string{"export"}, "usage: traceboard export"},
		{[]string{"delete"}, "usage: traceboard delete"},
		{[]string{"retention"}, "usage: traceboard retention"},
		{[]string{"retention", "warp"}, "unknown retention subcommand"},
		{[]string{"daemon"}, "usage: traceboard daemon"},
		{[]string{"daemon", "warp"}, "unknown daemon subcommand"},
		{[]string{"auth"}, "usage: traceboard auth"},
		{[]string{"auth", "warp"}, "unknown auth subcommand"},
		{[]string{"hook"}, "usage: traceboard hook"},
		{[]string{"hook", "not-a-source"}, "unsupported source"},
	}
	for _, testCase := range cases {
		env, _, stderr, _ := newTestEnvironment(t)
		if code := dispatch(env, testCase.arguments); code != 1 {
			t.Fatalf("%v exit code = %d, want 1", testCase.arguments, code)
		}
		if !strings.Contains(stderr.String(), testCase.want) {
			t.Fatalf("%v stderr = %q, want %q", testCase.arguments, stderr.String(), testCase.want)
		}
	}
}

func TestNoCommandLeaksASecret(t *testing.T) {
	env, stdout, stderr, directory := newTestEnvironment(t)
	dispatch(env, []string{"doctor"})
	combined := stdout.String() + stderr.String()
	config, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(config, &parsed); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	for _, key := range []string{"ingest_token", "dashboard_token", "session_secret"} {
		secret, _ := parsed[key].(string)
		if secret == "" {
			t.Fatalf("config is missing %s", key)
		}
		if strings.Contains(combined, secret) {
			t.Fatalf("%s leaked into command output", key)
		}
	}
}

func TestAuthRotatePrintsASignInURLWithoutTheIngestToken(t *testing.T) {
	env, stdout, stderr, directory := newTestEnvironment(t)
	if code := dispatch(env, []string{"auth", "rotate"}); code != 0 {
		t.Fatalf("exit code = %d: %s", code, stderr.String())
	}
	config, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(config, &parsed); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	ingestToken, _ := parsed["ingest_token"].(string)
	if strings.Contains(stdout.String(), ingestToken) {
		t.Fatal("the ingest token leaked into the rotation output")
	}
	if !strings.Contains(stdout.String(), "/auth/signin?token=") {
		t.Fatalf("no sign-in URL was printed: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "the ingest token was not changed") {
		t.Fatalf("the output does not state the ingest token is untouched: %q", stdout.String())
	}
}

func TestConfigureCaptureOnlyDoesNotTouchAgentFiles(t *testing.T) {
	env, stdout, _, _ := newTestEnvironment(t)
	home, err := os.MkdirTemp("", "traceboard-home-*")
	if err != nil {
		t.Fatalf("temp home: %v", err)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)

	if code := dispatch(env, []string{"configure", "opencode", "--capture-only", "--mode", "detailed"}); code != 0 {
		t.Fatalf("exit code = %d: %s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "no agent configuration file was changed") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode")); !os.IsNotExist(err) {
		t.Fatal("the agent configuration directory was created")
	}

	cfg, err := readTestConfig(t, env)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	sources, ok := cfg["sources"].(map[string]any)
	if !ok {
		t.Fatalf("sources = %+v", cfg["sources"])
	}
	opencode, ok := sources["opencode"].(map[string]any)
	if !ok || opencode["capture_mode"] != string(event.CaptureDetailed) {
		t.Fatalf("opencode capture mode = %+v", sources["opencode"])
	}
}

func TestConfigureRejectsAnInvalidCaptureMode(t *testing.T) {
	env, _, stderr, _ := newTestEnvironment(t)
	if code := dispatch(env, []string{"configure", "opencode", "--capture-only", "--mode", "everything"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "capture mode must be") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRetentionSetPersistsThePolicy(t *testing.T) {
	env, stdout, _, _ := newTestEnvironment(t)
	if code := dispatch(env, []string{"retention", "set", "45d"}); code != 0 {
		t.Fatalf("exit code = %d: %s", code, stdout.String())
	}
	cfg, err := readTestConfig(t, env)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if cfg["retention_days"] != float64(45) {
		t.Fatalf("retention_days = %+v", cfg["retention_days"])
	}
	if !strings.Contains(stdout.String(), "active runs are exempt") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRetentionSetRejectsABadDuration(t *testing.T) {
	env, _, stderr, _ := newTestEnvironment(t)
	if code := dispatch(env, []string{"retention", "set", "30y"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "retention unit must be") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRetentionSetAlwaysKeepsEverything(t *testing.T) {
	env, _, _, _ := newTestEnvironment(t)
	if code := dispatch(env, []string{"retention", "set", "always"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	cfg, err := readTestConfig(t, env)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if cfg["retention_days"] != float64(0) {
		t.Fatalf("retention_days = %+v", cfg["retention_days"])
	}
}

func TestSourcesListsEveryDeclaredSource(t *testing.T) {
	env, stdout, _, _ := newTestEnvironment(t)
	if code := dispatch(env, []string{"sources"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	for _, source := range []string{"opencode", "claude-code", "codex", "antigravity", "gemini-cli"} {
		if !strings.Contains(stdout.String(), source) {
			t.Fatalf("sources output is missing %q: %q", source, stdout.String())
		}
	}
}

func TestStatusReportsTheEndpointAndStore(t *testing.T) {
	env, stdout, _, _ := newTestEnvironment(t)
	if code := dispatch(env, []string{"status"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	for _, field := range []string{"endpoint", "http://127.0.0.1:47821", "runs", "quarantined", "retention_days"} {
		if !strings.Contains(stdout.String(), field) {
			t.Fatalf("status output is missing %q: %q", field, stdout.String())
		}
	}
}

func TestDeleteRequiresTheExactTitle(t *testing.T) {
	env, _, stderr, _ := newTestEnvironment(t)
	if code := dispatch(env, []string{"delete", "run_missing", "a title"}); code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "run not found") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestHookReturnsSuccessOnEmptyInput(t *testing.T) {
	env, _, _, _ := newTestEnvironment(t)
	env.stdin = strings.NewReader("")
	if code := dispatch(env, []string{"hook", "opencode"}); code != 0 {
		t.Fatalf("an empty hook payload must be a no-op, got exit code %d", code)
	}
}

func TestHookSpoolsWhenTheCollectorIsOffline(t *testing.T) {
	env, _, stderr, directory := newTestEnvironment(t)
	t.Setenv("TRACEBOARD_INGEST_URL", "http://127.0.0.1:1/api/v1/events")
	env.stdin = strings.NewReader(`{"event_name":"session.created","session_id":"sess_1","token":"Bearer abcdefghijklmnopqrstuvwxyz"}`)

	if code := dispatch(env, []string{"hook", "opencode"}); code != 0 {
		t.Fatalf("a hook must never fail the agent, got exit code %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "spooled") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	spoolPath := filepath.Join(directory, "spool", "opencode.ndjson")
	contents, err := os.ReadFile(spoolPath)
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if bytes.Contains(contents, []byte("abcdefghijklmnopqrstuvwxyz")) {
		t.Fatalf("a secret was written to the spool: %s", contents)
	}
	if !bytes.Contains(contents, []byte("REDACTED")) {
		t.Fatalf("the spool entry was not redacted: %s", contents)
	}
}

func TestHookDeliversToAReachableCollector(t *testing.T) {
	env, _, _, directory := newTestEnvironment(t)
	env.stdin = strings.NewReader(`{"event_name":"session.created","session_id":"sess_deliver"}`)

	collector := newHookCollector(t)
	t.Setenv("TRACEBOARD_INGEST_URL", collector.url)

	if code := dispatch(env, []string{"hook", "opencode"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	collector.wait(t)
	body, token := collector.request(t)
	if token != "Bearer "+readIngestToken(t, directory) {
		t.Fatalf("authorization = %q", token)
	}
	if !bytes.Contains(body, []byte("sess_deliver")) {
		t.Fatalf("the hook did not forward the run id: %s", body)
	}
	if !bytes.Contains(body, []byte("run.started")) {
		t.Fatalf("the hook did not map the lifecycle name: %s", body)
	}
}

func TestHookKeepsAnUnmappablePayloadVisible(t *testing.T) {
	env, _, _, _ := newTestEnvironment(t)
	env.stdin = strings.NewReader(`not json at all`)
	collector := newHookCollector(t)
	t.Setenv("TRACEBOARD_INGEST_URL", collector.url)

	if code := dispatch(env, []string{"hook", "opencode"}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	collector.wait(t)
	body, _ := collector.request(t)
	if !bytes.Contains(body, []byte("source.extension")) {
		t.Fatalf("an unparseable payload was dropped instead of recorded: %s", body)
	}
}

func readTestConfig(t *testing.T, env *environment) (map[string]any, error) {
	t.Helper()
	contents, err := os.ReadFile(env.configPath)
	if err != nil {
		return nil, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(contents, &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func readIngestToken(t *testing.T, directory string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var parsed struct {
		IngestToken string `json:"ingest_token"`
	}
	if err := json.Unmarshal(contents, &parsed); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return parsed.IngestToken
}
