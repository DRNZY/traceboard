package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"traceboard/internal/auth"
	"traceboard/internal/config"
	"traceboard/internal/event"
	"traceboard/internal/ingest"
	"traceboard/internal/live"
	"traceboard/internal/redact"
	"traceboard/internal/store"
)

type testServer struct {
	handler http.Handler
	store   *store.Store
	auth    *auth.Manager
	hub     *live.Hub
	signIn  string
	cookie  string
	cfg     config.Config
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure temp directory: %v", err)
	}
	cfg := config.Config{
		ListenAddress:  "127.0.0.1:47821",
		DatabasePath:   filepath.Join(directory, "traceboard.db"),
		IngestToken:    "ingest-secret",
		DashboardToken: "dashboard-secret",
		SessionSecret:  "session-secret",
		Sources:        map[string]config.SourceConfig{"opencode": {CaptureMode: event.CaptureDetailed}},
	}
	database, err := store.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	manager, err := auth.New(database, cfg.IngestToken)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	signIn, err := manager.IssueSignInToken(context.Background())
	if err != nil {
		t.Fatalf("sign-in token: %v", err)
	}
	redactor, err := redact.New(redact.Options{})
	if err != nil {
		t.Fatalf("redactor: %v", err)
	}
	service := ingest.NewService(database, redactor, ingest.DefaultLimits())
	for source, sourceConfig := range cfg.Sources {
		service.SetCaptureMode(source, sourceConfig.CaptureMode)
	}
	hub := live.NewHub()
	handler := New(Dependencies{
		Config:  cfg,
		Store:   database,
		Auth:    manager,
		Hub:     hub,
		Ingest:  ingest.NewCombinedService(service, ingest.NewOTLPService(service)),
		Version: "test",
		Started: time.Now().UTC(),
	})
	return &testServer{handler: handler, store: database, auth: manager, hub: hub, signIn: signIn, cfg: cfg}
}

// do serves a request through the full middleware chain. httptest defaults the
// Host header to example.com, so each request is pinned to the loopback address
// the server is configured to trust, exactly as a real browser would send it.
func (server *testServer) do(t *testing.T, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	if request.Host == "example.com" {
		request.Host = server.cfg.ListenAddress
	}
	recorder := httptest.NewRecorder()
	server.handler.ServeHTTP(recorder, request)
	return recorder
}

func (server *testServer) ingestRequest(t *testing.T, token string, body any) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request
}

func (server *testServer) signIn2(t *testing.T) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]string{"token": server.signIn})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/auth/session", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	recorder := server.do(t, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("sign-in = %d %s", recorder.Code, recorder.Body.String())
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == auth.SessionCookieName {
			return cookie.Value
		}
	}
	t.Fatal("no session cookie was issued")
	return ""
}

func (server *testServer) authed(t *testing.T, method, target string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: server.cookie})
	return request
}

func testEvent(runID, sourceEventID, eventType string, status event.Status) event.Event {
	return event.Event{
		SchemaVersion: 1,
		EventID:       sourceEventID,
		SourceEventID: sourceEventID,
		Source:        "opencode",
		SourceVersion: "test",
		RunID:         runID,
		OccurredAt:    time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC),
		Type:          eventType,
		Status:        status,
		Capture:       map[string]any{"mode": "metadata"},
		Attributes:    map[string]any{},
	}
}

func TestHealthReportsVersionAndListener(t *testing.T) {
	server := newTestServer(t)
	recorder := server.do(t, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("health = %d", recorder.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if payload["status"] != "ok" || payload["version"] != "test" {
		t.Fatalf("health payload = %v", payload)
	}
}

func TestSecurityHeadersArePresent(t *testing.T) {
	server := newTestServer(t)
	recorder := server.do(t, httptest.NewRequest(http.MethodGet, "/health", nil))
	for header, expected := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := recorder.Header().Get(header); got != expected {
			t.Fatalf("%s = %q, want %q", header, got, expected)
		}
	}
	if csp := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("content security policy = %q", csp)
	}
	if permissions := recorder.Header().Get("Permissions-Policy"); !strings.Contains(permissions, "camera=()") {
		t.Fatalf("permissions policy = %q", permissions)
	}
}

func TestHostHeaderMustBeLoopback(t *testing.T) {
	server := newTestServer(t)
	cases := map[string]int{
		"http://127.0.0.1:47821/health": http.StatusOK,
		"http://localhost:47821/health": http.StatusOK,
	}
	for target, want := range cases {
		recorder := server.do(t, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != want {
			t.Fatalf("%s = %d, want %d", target, recorder.Code, want)
		}
	}
	for _, target := range []string{"http://evil.example.com/health", "http://10.0.0.5:47821/health", "http://attacker.com/health"} {
		recorder := server.do(t, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusMisdirectedRequest {
			t.Fatalf("%s = %d, want 421", target, recorder.Code)
		}
	}
}

func TestCrossOriginMutationIsRejected(t *testing.T) {
	server := newTestServer(t)
	request := server.ingestRequest(t, server.cfg.IngestToken, event.Batch{})
	request.Header.Set("Origin", "https://evil.example.com")
	if recorder := server.do(t, request); recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin ingest = %d, want 403", recorder.Code)
	}

	loopback := server.ingestRequest(t, server.cfg.IngestToken, event.Batch{})
	loopback.Header.Set("Origin", "http://127.0.0.1:47821")
	if recorder := server.do(t, loopback); recorder.Code == http.StatusForbidden {
		t.Fatal("a loopback origin must be allowed")
	}
}

func TestIngestRequiresBearerToken(t *testing.T) {
	server := newTestServer(t)
	if recorder := server.do(t, server.ingestRequest(t, "", event.Batch{})); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing token = %d", recorder.Code)
	}
	if recorder := server.do(t, server.ingestRequest(t, "wrong", event.Batch{})); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", recorder.Code)
	}
	if recorder := server.do(t, server.ingestRequest(t, server.cfg.IngestToken, event.Batch{})); recorder.Code != http.StatusAccepted {
		t.Fatalf("valid token = %d", recorder.Code)
	}
}

func TestIngestRejectsOversizedBody(t *testing.T) {
	server := newTestServer(t)
	oversized := make([]byte, maxIngestBodyBytes+1)
	for index := range oversized {
		oversized[index] = 'a'
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewReader(oversized))
	request.Header.Set("Authorization", "Bearer "+server.cfg.IngestToken)
	recorder := server.do(t, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d, want 413", recorder.Code)
	}
}

func TestAuthenticationErrorsRevealNoSecret(t *testing.T) {
	server := newTestServer(t)
	recorder := server.do(t, server.ingestRequest(t, "wrong-token", event.Batch{}))
	body := recorder.Body.String()
	for _, secret := range []string{server.cfg.IngestToken, server.cfg.DashboardToken, server.cfg.SessionSecret, server.cfg.DatabasePath} {
		if strings.Contains(body, secret) {
			t.Fatalf("the error body leaked %q: %s", secret, body)
		}
	}
}

func TestQueryRoutesRequireASession(t *testing.T) {
	server := newTestServer(t)
	for _, target := range []string{"/api/v1/runs", "/api/v1/sources", "/api/v1/alerts", "/api/v1/settings", "/api/v1/projects", "/api/v1/quarantine"} {
		recorder := server.do(t, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a session = %d, want 401", target, recorder.Code)
		}
	}
}

func TestSignInExchangeIsOneTime(t *testing.T) {
	server := newTestServer(t)
	first := server.do(t, func() *http.Request {
		encoded, _ := json.Marshal(map[string]string{"token": server.signIn})
		return httptest.NewRequest(http.MethodPost, "/auth/session", bytes.NewReader(encoded))
	}())
	if first.Code != http.StatusOK {
		t.Fatalf("first exchange = %d %s", first.Code, first.Body.String())
	}
	second := server.do(t, func() *http.Request {
		encoded, _ := json.Marshal(map[string]string{"token": server.signIn})
		return httptest.NewRequest(http.MethodPost, "/auth/session", bytes.NewReader(encoded))
	}())
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("replayed exchange = %d, want 401", second.Code)
	}
}

func TestSessionCookieIsHttpOnlyAndStrict(t *testing.T) {
	server := newTestServer(t)
	encoded, _ := json.Marshal(map[string]string{"token": server.signIn})
	recorder := server.do(t, httptest.NewRequest(http.MethodPost, "/auth/session", bytes.NewReader(encoded)))
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %+v", cookies)
	}
	cookie := cookies[0]
	if !cookie.HttpOnly {
		t.Fatal("the session cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("same site = %v, want Strict", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Fatalf("cookie path = %q", cookie.Path)
	}
}

func TestLogoutClearsTheSession(t *testing.T) {
	server := newTestServer(t)
	server.cookie = server.signIn2(t)
	recorder := server.do(t, server.authed(t, http.MethodPost, "/auth/logout"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("logout = %d", recorder.Code)
	}
	after := server.do(t, server.authed(t, http.MethodGet, "/api/v1/runs"))
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("session after logout = %d, want 401", after.Code)
	}
}

func TestIngestAndQueryRoundTrip(t *testing.T) {
	server := newTestServer(t)
	server.cookie = server.signIn2(t)

	batch := event.Batch{Events: []event.Event{
		testEvent("run_1", "s1", event.TypeRunStarted, event.StatusStarted),
		testEvent("run_1", "s2", event.TypePromptReceived, event.StatusCompleted),
		testEvent("run_1", "s3", event.TypeRunFailed, event.StatusFailed),
	}}
	batch.Events[1].Content = map[string]any{"text": "repair the collector"}

	recorder := server.do(t, server.ingestRequest(t, server.cfg.IngestToken, batch))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("ingest = %d %s", recorder.Code, recorder.Body.String())
	}

	runs := server.do(t, server.authed(t, http.MethodGet, "/api/v1/runs"))
	var page store.RunPage
	if err := json.Unmarshal(runs.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if len(page.Runs) != 1 || page.Runs[0].ID != "run_1" || page.Runs[0].Status != event.StatusFailed {
		t.Fatalf("runs = %+v", page.Runs)
	}

	detail := server.do(t, server.authed(t, http.MethodGet, "/api/v1/runs/run_1"))
	if detail.Code != http.StatusOK {
		t.Fatalf("run detail = %d", detail.Code)
	}

	events := server.do(t, server.authed(t, http.MethodGet, "/api/v1/runs/run_1/events"))
	if events.Code != http.StatusOK {
		t.Fatalf("events = %d", events.Code)
	}
	var eventPage store.EventPage
	if err := json.Unmarshal(events.Body.Bytes(), &eventPage); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(eventPage.Events) != 3 {
		t.Fatalf("events = %+v", eventPage.Events)
	}
	if eventPage.Events[1].Content["text"] != "repair the collector" {
		t.Fatalf("content = %+v", eventPage.Events[1].Content)
	}
}

func TestIngestReportsPartialSuccess(t *testing.T) {
	server := newTestServer(t)
	invalid := testEvent("run_x", "s1", event.TypeRunStarted, event.StatusStarted)
	invalid.SchemaVersion = 42
	batch := event.Batch{Events: []event.Event{
		testEvent("run_x", "s2", event.TypeRunStarted, event.StatusStarted),
		invalid,
	}}
	recorder := server.do(t, server.ingestRequest(t, server.cfg.IngestToken, batch))
	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("partial success = %d, want 206", recorder.Code)
	}
	var result struct {
		Accepted    int `json:"accepted"`
		Quarantined int `json:"quarantined"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Accepted != 1 || result.Quarantined != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestOTLPRoutesAcceptJSONAndRejectMissingAuth(t *testing.T) {
	server := newTestServer(t)
	body := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174","name":"claude-code","kind":1,"startTimeUnixNano":"1750000000000000000","endTimeUnixNano":"1750000001000000000","status":{"code":1}}]}]}]}`

	unauthorized := httptest.NewRequest(http.MethodPost, "/v1/traces", strings.NewReader(body))
	unauthorized.Header.Set("Content-Type", "application/json")
	if recorder := server.do(t, unauthorized); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("otlp without auth = %d", recorder.Code)
	}

	for _, target := range []string{"/v1/traces", "/v1/logs", "/v1/metrics"} {
		request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+server.cfg.IngestToken)
		request.Header.Set("Content-Type", "application/json")
		recorder := server.do(t, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", target, recorder.Code, recorder.Body.String())
		}
	}
}

func TestDeleteRequiresTheExactRunTitle(t *testing.T) {
	server := newTestServer(t)
	server.cookie = server.signIn2(t)
	recorder := server.do(t, server.ingestRequest(t, server.cfg.IngestToken, event.Batch{Events: []event.Event{
		testEvent("run_del", "s1", event.TypeRunStarted, event.StatusStarted),
	}}))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("ingest = %d", recorder.Code)
	}

	wrong := server.do(t, func() *http.Request {
		request := server.authed(t, http.MethodDelete, "/api/v1/runs/run_del?confirm=wrong")
		return request
	}())
	if wrong.Code != http.StatusBadRequest {
		t.Fatalf("wrong confirmation = %d, want 400", wrong.Code)
	}

	right := server.do(t, server.authed(t, http.MethodDelete, "/api/v1/runs/run_del?confirm=run_del"))
	if right.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", right.Code, right.Body.String())
	}

	missing := server.do(t, server.authed(t, http.MethodGet, "/api/v1/runs/run_del"))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted run = %d, want 404", missing.Code)
	}
}

func TestCaptureModeEndpointValidatesInput(t *testing.T) {
	server := newTestServer(t)
	server.cookie = server.signIn2(t)

	invalid := func() *http.Request {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/sources/opencode/capture-mode", strings.NewReader(`{"capture_mode":"everything"}`))
		request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: server.cookie})
		return request
	}()
	if recorder := server.do(t, invalid); recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid capture mode = %d, want 400", recorder.Code)
	}

	valid := httptest.NewRequest(http.MethodPut, "/api/v1/sources/opencode/capture-mode", strings.NewReader(`{"capture_mode":"off"}`))
	valid.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: server.cookie})
	recorder := server.do(t, valid)
	if recorder.Code != http.StatusOK {
		t.Fatalf("valid capture mode = %d %s", recorder.Code, recorder.Body.String())
	}
	var source store.Source
	if err := json.Unmarshal(recorder.Body.Bytes(), &source); err != nil {
		t.Fatalf("decode source: %v", err)
	}
	if source.Name != "opencode" || source.CaptureMode != event.CaptureOff {
		t.Fatalf("source = %+v", source)
	}
}

func TestAlertAcknowledgementEndpoint(t *testing.T) {
	server := newTestServer(t)
	server.cookie = server.signIn2(t)
	if _, err := server.store.InsertEvents(context.Background(),
		testEvent("run_alert", "a1", event.TypeRunStarted, event.StatusStarted)); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if _, _, err := server.store.OpenAlert(context.Background(), store.Alert{
		Type: "run_failed", RunID: strPtr("run_alert"), Message: "run failed",
	}); err != nil {
		t.Fatalf("open alert: %v", err)
	}

	list := server.do(t, server.authed(t, http.MethodGet, "/api/v1/alerts"))
	var payload []store.Alert
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode alerts: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("alerts = %+v", payload)
	}
	acknowledge := server.do(t, server.authed(t, http.MethodPost, "/api/v1/alerts/"+payload[0].ID+"/acknowledge"))
	if acknowledge.Code != http.StatusOK {
		t.Fatalf("acknowledge = %d", acknowledge.Code)
	}
	var alert store.Alert
	if err := json.Unmarshal(acknowledge.Body.Bytes(), &alert); err != nil {
		t.Fatalf("decode alert: %v", err)
	}
	if alert.State != "acknowledged" {
		t.Fatalf("state = %q", alert.State)
	}
}

func TestSettingsEndpointNeverExposesCredentials(t *testing.T) {
	server := newTestServer(t)
	server.cookie = server.signIn2(t)
	recorder := server.do(t, server.authed(t, http.MethodGet, "/api/v1/settings"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("settings = %d", recorder.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	// The settings page must never be able to read a bearer credential.
	for _, forbidden := range []string{"ingest_token", "dashboard_token", "session_secret"} {
		if _, present := payload[forbidden]; present {
			t.Fatalf("settings exposed %q", forbidden)
		}
	}
	if payload["ingest_token_configured"] != true {
		t.Fatalf("settings = %v", payload)
	}
	if payload["listen_address"] != "127.0.0.1:47821" {
		t.Fatalf("listen address = %v", payload["listen_address"])
	}
}

func TestStaticShellIsServedForUnknownPaths(t *testing.T) {
	server := newTestServer(t)
	server.cookie = server.signIn2(t)
	recorder := server.do(t, server.authed(t, http.MethodGet, "/runs/run_1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("spa fallback = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "<div id=\"app\">") {
		t.Fatalf("spa fallback body = %q", recorder.Body.String()[:min(120, len(recorder.Body.String()))])
	}
	missingAPI := server.do(t, server.authed(t, http.MethodGet, "/api/v1/does-not-exist"))
	if missingAPI.Code != http.StatusNotFound {
		t.Fatalf("unknown api path = %d, want 404", missingAPI.Code)
	}
}

func TestSignInPageEscapesInjectedMarkup(t *testing.T) {
	page := signInPageHTML(`"><script>alert(1)</script>`, "")
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Fatal("the sign-in page rendered unescaped input")
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Fatalf("sign-in page = %q", page)
	}
}

func strPtr(value string) *string {
	return &value
}
