//go:build integration

// Package integration starts the real collector over a real listener and
// exercises the paths a unit test cannot reach: a live socket, an outage with
// a spooling adapter, a restart, and a run that never reported a terminal state.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"traceboard/internal/auth"
	"traceboard/internal/config"
	"traceboard/internal/event"
	"traceboard/internal/ingest"
	"traceboard/internal/live"
	"traceboard/internal/redact"
	"traceboard/internal/server"
	"traceboard/internal/store"
)

type harness struct {
	t          *testing.T
	address    string
	baseURL    string
	configPath string
	store      *store.Store
	hub        *live.Hub
	handler    http.Handler
	token      string
	// signInToken is the one-time dashboard token the harness exposes.
	signInToken string
}

func newHarness(t *testing.T, name string) *harness {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure temp directory: %v", err)
	}
	configPath := filepath.Join(directory, "config.json")
	// A fixed loopback port per test keeps the suite honest about the listener
	// while staying inside the loopback-only rule.
	address := freeLoopbackAddress(t)

	cfg := config.Config{
		ListenAddress:  address,
		DatabasePath:   filepath.Join(directory, "traceboard.db"),
		IngestToken:    "integration-ingest-token",
		DashboardToken: "integration-dashboard-token",
		SessionSecret:  "integration-session-secret",
		RetentionDays:  30,
		Sources:        map[string]config.SourceConfig{"opencode": {CaptureMode: event.CaptureDetailed}},
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	instance := &harness{
		t:          t,
		address:    address,
		baseURL:    "http://" + address,
		configPath: configPath,
		hub:        live.NewHub(),
		token:      cfg.IngestToken,
	}
	instance.open(t, name)
	t.Cleanup(func() { instance.store.Close() })
	return instance
}

func (h *harness) open(t *testing.T, name string) {
	t.Helper()
	cfg, err := config.Load(h.configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	database, err := store.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	manager, err := auth.New(database, cfg.IngestToken)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	token, err := manager.IssueSignInToken(context.Background())
	if err != nil {
		t.Fatalf("sign-in token: %v", err)
	}
	h.signInToken = token

	redactor, err := redact.New(redact.Options{})
	if err != nil {
		t.Fatalf("redactor: %v", err)
	}
	service := ingest.NewService(database, redactor, ingest.DefaultLimits())
	service.SetOnAccepted(func(runID string) {
		run, err := database.GetRun(context.Background(), runID)
		if err == nil {
			h.hub.PublishRunChanged(run)
		}
	})
	for source, sourceConfig := range cfg.Sources {
		service.SetCaptureMode(source, sourceConfig.CaptureMode)
	}

	h.store = database
	h.handler = server.New(server.Dependencies{
		Config:     cfg,
		Store:      database,
		Auth:       manager,
		Hub:        h.hub,
		Ingest:     ingest.NewCombinedService(service, ingest.NewOTLPService(service)),
		Version:    "integration",
		ConfigPath: h.configPath,
		Started:    time.Now().UTC(),
	})

	listener, err := listen(h.address)
	if err != nil {
		t.Fatalf("listen on %s: %v", h.address, err)
	}
	httpServer := &http.Server{Handler: h.handler}
	go func() { _ = httpServer.Serve(listener) }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	})

	h.waitHealthy()
}

func (h *harness) waitHealthy() {
	h.t.Helper()
	for attempt := 0; attempt < 80; attempt++ {
		response, err := http.Get(h.baseURL + "/health")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	h.t.Fatal("the collector never became healthy")
}

func (h *harness) sessionCookie() *http.Cookie {
	h.t.Helper()
	encoded, _ := json.Marshal(map[string]string{"token": h.signInToken})
	request, err := http.NewRequest(http.MethodPost, h.baseURL+"/auth/session", bytes.NewReader(encoded))
	if err != nil {
		h.t.Fatalf("build sign-in request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		h.t.Fatalf("sign in: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		h.t.Fatalf("sign in = %d: %s", response.StatusCode, readBody(h.t, response))
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == auth.SessionCookieName {
			return cookie
		}
	}
	h.t.Fatal("no session cookie was issued")
	return nil
}

func (h *harness) ingest(events ...event.Event) ingest.Result {
	h.t.Helper()
	encoded, err := json.Marshal(event.Batch{Events: events})
	if err != nil {
		h.t.Fatalf("encode batch: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, h.baseURL+"/api/v1/events", bytes.NewReader(encoded))
	if err != nil {
		h.t.Fatalf("build ingest request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+h.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		h.t.Fatalf("ingest: %v", err)
	}
	defer response.Body.Close()
	body := readBody(h.t, response)
	if response.StatusCode >= http.StatusBadRequest {
		h.t.Fatalf("ingest = %d: %s", response.StatusCode, body)
	}
	var result ingest.Result
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		h.t.Fatalf("decode ingest result: %v", err)
	}
	return result
}

func (h *harness) get(cookie *http.Cookie, path string) (int, []byte) {
	h.t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.baseURL+path, nil)
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	request.AddCookie(cookie)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		h.t.Fatalf("get %s: %v", path, err)
	}
	defer response.Body.Close()
	return response.StatusCode, readBodyAll(response)
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	return string(readBodyAll(response))
}

func readBodyAll(response *http.Response) []byte {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return body
}

func integrationEvent(index int, runID, eventType string, status event.Status, at time.Time) event.Event {
	return event.Event{
		SchemaVersion: event.SchemaVersion1,
		EventID:       "evt_" + runID + "_" + itoa(index),
		SourceEventID: "src_" + runID + "_" + itoa(index),
		Source:        "opencode",
		SourceVersion: "1.0.0",
		RunID:         runID,
		OccurredAt:    at,
		Type:          eventType,
		Status:        status,
		Capture:       map[string]any{"mode": "detailed"},
		Attributes:    map[string]any{},
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func freeLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a loopback port: %v", err)
	}
	address := listener.Addr().String()
	closeListener(listener)
	return address
}

func dialWebSocket(t *testing.T, baseURL string, cookie *http.Cookie) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Cookie", cookie.Name+"="+cookie.Value)
	connection, _, err := websocket.DefaultDialer.Dial("ws://"+strings.TrimPrefix(baseURL, "http://")+"/api/v1/stream", header)
	if err != nil {
		t.Fatalf("dial the live stream: %v", err)
	}
	t.Cleanup(func() { connection.Close() })
	return connection
}

func listen(address string) (net.Listener, error) {
	return net.Listen("tcp", address)
}

func closeListener(listener net.Listener) {
	_ = listener.Close()
}
