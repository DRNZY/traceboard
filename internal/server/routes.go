package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"traceboard/internal/auth"
	"traceboard/internal/buildinfo"
	"traceboard/internal/config"
	"traceboard/internal/event"
	"traceboard/internal/export"
	"traceboard/internal/frontend"
	"traceboard/internal/ingest"
	"traceboard/internal/live"
	"traceboard/internal/otlp"
	"traceboard/internal/store"
)

// Ingestor is the single write path into the store. Both the JSON endpoint and
// the OTLP endpoints funnel through it so validation, redaction, and
// publication behave identically.
type Ingestor interface {
	Ingest(ctx context.Context, batch event.Batch) ingest.Result
	IngestOTLP(ctx context.Context, source string, body []byte, contentType string) ingest.Result
}

type Dependencies struct {
	Config  config.Config
	Store   *store.Store
	Auth    *auth.Manager
	Hub     *live.Hub
	Ingest  Ingestor
	Assets  fs.FS
	Version string
	Started time.Time
	// ExportDir is where dashboard-triggered exports are written. It must be a
	// user-only directory owned by this process.
	ExportDir string
	// ConfigPath is the credential file location reported by the settings page.
	ConfigPath string
	Commit     string
}

type Server struct {
	deps      Dependencies
	router    *http.ServeMux
	mux       middleware
	baseURL   string
	exportDir string
}

func New(deps Dependencies) http.Handler {
	if deps.Assets == nil {
		deps.Assets = frontend.FS()
	}
	if deps.Started.IsZero() {
		deps.Started = time.Now().UTC()
	}
	if deps.Version == "" {
		deps.Version = buildinfo.Version
	}
	if deps.Commit == "" {
		deps.Commit = buildinfo.Commit
	}
	if deps.ExportDir == "" {
		deps.ExportDir = filepath.Join(filepath.Dir(deps.Config.DatabasePath), "exports")
	}
	if err := os.MkdirAll(deps.ExportDir, 0o700); err != nil {
		deps.ExportDir = filepath.Dir(deps.Config.DatabasePath)
	}
	server := &Server{
		deps:      deps,
		router:    http.NewServeMux(),
		mux:       newMiddleware(deps.Auth, deps.Config.ListenAddress),
		exportDir: deps.ExportDir,
	}
	server.baseURL = "http://" + deps.Config.ListenAddress
	server.routes()
	return server.handler()
}

func (s *Server) handler() http.Handler {
	var handler http.Handler = s.router
	handler = s.mux.securityHeaders(handler)
	handler = s.mux.validateHost(handler)
	handler = s.mux.requireSameOrigin(handler)
	return handler
}

func (s *Server) routes() {
	authenticated := func(pattern string, handler http.HandlerFunc) {
		s.router.Handle(pattern, s.mux.requireSession(handler))
	}
	ingest := func(pattern string, handler http.HandlerFunc) {
		s.router.Handle(pattern, s.mux.requireIngestToken(handler))
	}

	s.router.HandleFunc("GET /health", s.handleHealth)

	s.router.Handle("GET /auth/signin", s.mux.requireSameOrigin(http.HandlerFunc(s.handleSignInPage)))
	s.router.Handle("GET /auth/signin.css", http.HandlerFunc(handleSignInStylesheet))
	// The exchange itself is unauthenticated: the one-time token in the body is
	// the credential, and requiring a session first would make sign-in
	// impossible.
	s.router.Handle("POST /auth/session", s.mux.requireSameOrigin(http.HandlerFunc(s.handleExchange)))
	authenticated("POST /auth/logout", s.handleLogout)
	authenticated("GET /auth/session", s.handleSession)

	ingest("POST /api/v1/events", s.handleIngestEvents)
	ingest("POST /v1/traces", s.handleOTLPTraces)
	ingest("POST /v1/logs", s.handleOTLPLogs)
	ingest("POST /v1/metrics", s.handleOTLPMetrics)

	authenticated("GET /api/v1/runs", s.handleListRuns)
	authenticated("GET /api/v1/runs/changed", s.handleListChangedRuns)
	authenticated("GET /api/v1/runs/{runID}", s.handleGetRun)
	authenticated("GET /api/v1/runs/{runID}/events", s.handleListRunEvents)
	authenticated("POST /api/v1/runs/{runID}/export", s.handleExportRun)
	authenticated("DELETE /api/v1/runs/{runID}", s.handleDeleteRun)
	authenticated("GET /api/v1/sources", s.handleListSources)
	authenticated("PUT /api/v1/sources/{source}/capture-mode", s.handleSetCaptureMode)
	authenticated("GET /api/v1/projects", s.handleListProjects)
	authenticated("GET /api/v1/alerts", s.handleListAlerts)
	authenticated("POST /api/v1/alerts/{alertID}/acknowledge", s.handleAcknowledgeAlert)
	authenticated("GET /api/v1/quarantine", s.handleListQuarantine)
	authenticated("GET /api/v1/settings", s.handleSettings)
	authenticated("GET /api/v1/stream", s.handleWebSocket)

	s.router.Handle("/", s.mux.requireSession(http.HandlerFunc(s.handleAssets)))
}

func (s *Server) handleHealth(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"status":         "ok",
		"version":        s.deps.Version,
		"started_at":     s.deps.Started,
		"listen_address": s.deps.Config.ListenAddress,
	})
}

func (s *Server) handleSession(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"authenticated": true, "version": s.deps.Version})
}

// handleSignInPage serves a tiny loopback-only form. The one-time token lives in
// the URL, so it is pushed into a POST body instead of being echoed into a
// script or stored in the browser.
func (s *Server) handleSignInPage(writer http.ResponseWriter, request *http.Request) {
	token := request.URL.Query().Get("token")
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	if token == "" {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, signInPageHTML("", "Start the server and use the sign-in URL it prints."))
		return
	}
	_, _ = io.WriteString(writer, signInPageHTML(token, ""))
}

func (s *Server) handleExchange(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxQueryBodyBytes)

	// A browser posts the sign-in form; the dashboard client posts JSON. Both
	// carry the same one-time token, and only the response shape differs.
	fromForm := strings.HasPrefix(request.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
	var token string
	if fromForm {
		if err := request.ParseForm(); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "a sign-in token is required")
			return
		}
		token = request.PostFormValue("token")
	} else {
		var payload struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "a sign-in token is required")
			return
		}
		token = payload.Token
	}

	sessionToken, err := s.deps.Auth.ExchangeDashboardToken(request.Context(), token)
	if err != nil {
		if fromForm {
			renderSignInFailure(writer, request)
			return
		}
		writeError(writer, http.StatusUnauthorized, "unauthorized", "authentication failed")
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   false,
		MaxAge:   int(auth.SessionTTL.Seconds()),
	})
	if fromForm {
		// The one-time token is now in the session cookie, so the clean URL is
		// safe to show and nothing credential-bearing stays in the address bar.
		http.Redirect(writer, request, "/", http.StatusSeeOther)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"authenticated": true})
}

func (s *Server) handleLogout(writer http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie(auth.SessionCookieName); err == nil {
		_ = s.deps.Auth.Logout(request.Context(), cookie.Value)
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"authenticated": false})
}

func (s *Server) handleIngestEvents(writer http.ResponseWriter, request *http.Request) {
	body, ok := readLimited(writer, request, maxIngestBodyBytes)
	if !ok {
		return
	}
	batch, err := decodeEventPayload(body)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	result := s.deps.Ingest.Ingest(request.Context(), batch)
	s.publishIngest(result)
	writeJSON(writer, ingestStatus(result), map[string]any{
		"accepted":    result.Accepted,
		"duplicate":   result.Duplicate,
		"quarantined": result.Quarantined,
	})
}

func (s *Server) handleOTLPTraces(writer http.ResponseWriter, request *http.Request) {
	s.handleOTLP(writer, request, "traces")
}

func (s *Server) handleOTLPLogs(writer http.ResponseWriter, request *http.Request) {
	s.handleOTLP(writer, request, "logs")
}

func (s *Server) handleOTLPMetrics(writer http.ResponseWriter, request *http.Request) {
	s.handleOTLP(writer, request, "metrics")
}

func (s *Server) handleOTLP(writer http.ResponseWriter, request *http.Request, kind string) {
	body, ok := readLimited(writer, request, maxIngestBodyBytes)
	if !ok {
		return
	}
	result := s.deps.Ingest.IngestOTLP(request.Context(), otlpSourceFromPath(kind), body, request.Header.Get("Content-Type"))
	s.publishIngest(result)
	writer.Header().Set("Content-Type", "application/x-protobuf")
	if result.Quarantined > 0 && result.Accepted > 0 {
		writer.WriteHeader(http.StatusPartialContent)
	}
	if result.Accepted == 0 && result.Quarantined > 0 {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(otlp.EmptySuccess(otlpKind(kind)))
}

// publishIngest tells an open dashboard that committed runs need re-reading. The
// client refetches, so nothing is ever served from this message.
func (s *Server) publishIngest(result ingest.Result) {
	if result.Accepted == 0 {
		return
	}
	s.deps.Hub.Publish(live.Message{
		Kind:    live.KindRunUpdated,
		Payload: map[string]any{"accepted": result.Accepted, "quarantined": result.Quarantined},
	})
}

func (s *Server) handleListRuns(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	filter := runFilterFrom(query)
	if after, ok := parseOptionalTime(query.Get("after")); ok {
		filter.StartedAfter = &after
	}
	if before, ok := parseOptionalTime(query.Get("before")); ok {
		filter.StartedBefore = &before
	}
	limit := 100
	if value, valid := parseNonNegative(query.Get("limit")); valid && value > 0 {
		limit = int(value)
	}
	page, err := s.deps.Store.ListRuns(request.Context(), filter, query.Get("cursor"), limit)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", "the run query could not be evaluated")
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

// runFilterFrom maps the documented query names onto the store filter. Only the
// documented names are read, so an unknown parameter can never widen a search.
func runFilterFrom(query url.Values) store.RunFilter {
	filter := store.RunFilter{
		Query:       query.Get("q"),
		Source:      query.Get("source"),
		ProjectID:   query.Get("project_id"),
		Status:      query.Get("status"),
		CaptureMode: query.Get("capture_mode"),
		AlertState:  query.Get("alert_state"),
		ParentRunID: query.Get("parent_run_id"),
	}
	if value := query.Get("subagent_only"); value != "" {
		only := value == "true"
		filter.SubagentOnly = &only
	}
	if after, ok := parseOptionalTime(query.Get("started_after")); ok {
		filter.StartedAfter = &after
	}
	if before, ok := parseOptionalTime(query.Get("started_before")); ok {
		filter.StartedBefore = &before
	}
	return filter
}

// handleListChangedRuns serves the reconnect snapshot: every run committed since
// a point in time, so a client can resync without refetching all history.
func (s *Server) handleListChangedRuns(writer http.ResponseWriter, request *http.Request) {
	since, ok := parseOptionalTime(request.URL.Query().Get("since"))
	if !ok {
		writeError(writer, http.StatusBadRequest, "invalid_query", "since must be an RFC 3339 timestamp")
		return
	}
	page, err := s.deps.Store.ListRuns(request.Context(), store.RunFilter{StartedAfter: &since}, "", 500)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", "the changed run query could not be evaluated")
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (s *Server) handleGetRun(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	runID := request.PathValue("runID")
	run, err := s.deps.Store.GetRun(ctx, runID)
	if err != nil {
		writeError(writer, http.StatusNotFound, "not_found", "run not found")
		return
	}
	steps, err := s.deps.Store.ListSteps(ctx, runID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "run steps could not be read")
		return
	}
	subagents, err := s.deps.Store.ListSubagentRuns(ctx, runID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "subagent runs could not be read")
		return
	}
	alerts, err := s.deps.Store.ListAlerts(ctx, store.AlertFilter{RunID: runID, Limit: 50})
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "run alerts could not be read")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"run":           run,
		"steps":         steps,
		"subagent_runs": subagents,
		"alerts":        alerts,
	})
}

func (s *Server) handleListRunEvents(writer http.ResponseWriter, request *http.Request) {
	after, valid := parseNonNegative(request.URL.Query().Get("after"))
	if !valid {
		writeError(writer, http.StatusBadRequest, "invalid_query", "after must be a non-negative sequence")
		return
	}
	limit := 200
	if value, valid := parseNonNegative(request.URL.Query().Get("limit")); valid && value > 0 {
		limit = int(value)
	}
	page, err := s.deps.Store.ListEvents(request.Context(), request.PathValue("runID"), after, limit)
	if err != nil {
		writeError(writer, http.StatusNotFound, "not_found", "run not found")
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (s *Server) handleDeleteRun(writer http.ResponseWriter, request *http.Request) {
	runID := request.PathValue("runID")
	run, err := s.deps.Store.GetRun(request.Context(), runID)
	if err != nil {
		writeError(writer, http.StatusNotFound, "not_found", "run not found")
		return
	}
	confirmation := request.URL.Query().Get("confirm")
	expected := runTitle(run)
	if confirmation != expected {
		writeError(writer, http.StatusBadRequest, "confirmation_required", "the exact run title is required to delete a run")
		return
	}
	result, err := s.deps.Store.DeleteRun(request.Context(), runID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "run could not be deleted")
		return
	}
	s.deps.Hub.Publish(live.Message{Kind: live.KindRunDeleted, RunID: runID})
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) handleListSources(writer http.ResponseWriter, request *http.Request) {
	sources, err := s.deps.Store.ListSources(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "sources could not be read")
		return
	}
	if sources == nil {
		sources = []store.Source{}
	}
	writeJSON(writer, http.StatusOK, sources)
}

func (s *Server) handleSetCaptureMode(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPut {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxQueryBodyBytes)
	var payload struct {
		CaptureMode string `json:"capture_mode"`
	}
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "a capture mode is required")
		return
	}
	mode := event.CaptureMode(payload.CaptureMode)
	switch mode {
	case event.CaptureOff, event.CaptureMetadata, event.CaptureDetailed:
	default:
		writeError(writer, http.StatusBadRequest, "invalid_request", "capture mode must be off, metadata, or detailed")
		return
	}
	source := request.PathValue("source")
	if err := s.deps.Store.SetSourceCaptureMode(request.Context(), source, mode); err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "capture mode could not be stored")
		return
	}
	s.deps.Hub.Publish(live.Message{Kind: live.KindSourceUpdated, Source: source})
	updated, err := s.deps.Store.GetSource(request.Context(), source)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "the source could not be read back")
		return
	}
	writeJSON(writer, http.StatusOK, updated)
}

func (s *Server) handleListProjects(writer http.ResponseWriter, request *http.Request) {
	projects, err := s.deps.Store.ListProjects(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "projects could not be read")
		return
	}
	if projects == nil {
		projects = []store.Project{}
	}
	writeJSON(writer, http.StatusOK, projects)
}

// handleExportRun writes a run to a local file the operator chose. The browser
// never uploads anything and never receives a credential.
func (s *Server) handleExportRun(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxQueryBodyBytes)
	var payload struct {
		Format string `json:"format"`
		Force  bool   `json:"force"`
	}
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		payload.Format = "json"
	}
	format := export.Format(payload.Format)
	switch format {
	case export.FormatJSON, export.FormatMarkdown, export.FormatRaw:
	default:
		writeError(writer, http.StatusBadRequest, "invalid_request", "format must be json, markdown, or raw")
		return
	}
	runID := request.PathValue("runID")
	if _, err := s.deps.Store.GetRun(request.Context(), runID); err != nil {
		writeError(writer, http.StatusNotFound, "not_found", "run not found")
		return
	}
	destination := filepath.Join(s.exportDir, sanitizeExportName(runID)+"."+string(format))
	written, err := export.Run(request.Context(), s.deps.Store, export.Request{
		RunID:       runID,
		Format:      format,
		Destination: destination,
		Force:       payload.Force,
	})
	if err != nil {
		if errors.Is(err, os.ErrExist) || strings.Contains(err.Error(), "already exists") {
			writeError(writer, http.StatusConflict, "already_exists", "that export file already exists")
			return
		}
		writeError(writer, http.StatusInternalServerError, "internal_error", "the run could not be exported")
		return
	}
	info, err := os.Stat(written)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "the export could not be verified")
		return
	}
	writeJSON(writer, http.StatusOK, export.ExportResult{Path: written, Format: format, Bytes: info.Size()})
}

func sanitizeExportName(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	trimmed := strings.Trim(builder.String(), "-")
	if len(trimmed) > 80 {
		trimmed = trimmed[:80]
	}
	if trimmed == "" {
		return "run"
	}
	return trimmed
}

func (s *Server) handleListAlerts(writer http.ResponseWriter, request *http.Request) {
	limit := 100
	if value, valid := parseNonNegative(request.URL.Query().Get("limit")); valid && value > 0 {
		limit = int(value)
	}
	alerts, err := s.deps.Store.ListAlerts(request.Context(), store.AlertFilter{
		RunID: request.URL.Query().Get("run_id"),
		State: request.URL.Query().Get("state"),
		Limit: limit,
	})
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "alerts could not be read")
		return
	}
	writeJSON(writer, http.StatusOK, alerts)
}

func (s *Server) handleAcknowledgeAlert(writer http.ResponseWriter, request *http.Request) {
	alert, err := s.deps.Store.AcknowledgeAlert(request.Context(), request.PathValue("alertID"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "not_found", "alert not found")
		return
	}
	s.deps.Hub.PublishAlert(alert, deref(alert.RunID))
	writeJSON(writer, http.StatusOK, alert)
}

func (s *Server) handleListQuarantine(writer http.ResponseWriter, request *http.Request) {
	entries, err := s.deps.Store.ListQuarantine(request.Context(), 100)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "quarantine could not be read")
		return
	}
	writeJSON(writer, http.StatusOK, entries)
}

// handleSettings reports the collector's own configuration. It never returns a
// token: only whether one is configured, plus the paths the operator already
// knows, so the dashboard can be inspected without leaking bearer material.
func (s *Server) handleSettings(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	quarantineCount, err := s.deps.Store.QuarantineCount(ctx)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "settings could not be read")
		return
	}
	sessions, err := s.deps.Store.CountSessions(ctx)
	if err != nil {
		sessions = 0
	}
	configPath := s.deps.ConfigPath
	if configPath == "" {
		configPath = filepath.Dir(s.deps.Config.DatabasePath)
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"version":                    s.deps.Version,
		"commit":                     buildinfo.Commit,
		"listen_address":             s.deps.Config.ListenAddress,
		"retention_days":             s.deps.Config.RetentionDays,
		"keep_newest_per_project":    s.deps.Config.KeepNewestPerProject,
		"database_path":              s.deps.Config.DatabasePath,
		"config_path":                configPath,
		"sessions_valid":             sessions,
		"ingest_token_configured":    s.deps.Config.IngestToken != "",
		"dashboard_token_configured": s.deps.Config.DashboardToken != "",
		"quarantine_count":           quarantineCount,
		"started_at":                 s.deps.Started,
		"export_dir":                 s.exportDir,
		"heartbeat_seconds":          s.deps.Config.HeartbeatSeconds,
	})
}

// handleAssets serves the embedded single-page application. Unknown paths fall
// back to the app shell so client-side routes survive a reload.
func (s *Server) handleAssets(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	cleaned := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	if cleaned == "" || cleaned == "." {
		cleaned = "index.html"
	}
	file, err := s.deps.Assets.Open(cleaned)
	if err != nil {
		s.serveShell(writer, request)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		s.serveShell(writer, request)
		return
	}
	if strings.HasPrefix(cleaned, "assets/") {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		writer.Header().Set("Cache-Control", "no-store")
	}
	http.ServeContent(writer, request, info.Name(), info.ModTime(), file.(readSeeker))
}

func (s *Server) serveShell(writer http.ResponseWriter, request *http.Request) {
	if strings.HasPrefix(request.URL.Path, "/api/") || strings.HasPrefix(request.URL.Path, "/auth/") {
		writeError(writer, http.StatusNotFound, "not_found", "not found")
		return
	}
	shell, err := s.deps.Assets.Open("index.html")
	if err != nil {
		writeError(writer, http.StatusNotFound, "not_found", "dashboard assets are unavailable")
		return
	}
	defer shell.Close()
	info, err := shell.Stat()
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "internal_error", "dashboard assets are unavailable")
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	http.ServeContent(writer, request, "index.html", info.ModTime(), shell.(readSeeker))
}

func decodeEventPayload(body []byte) (event.Batch, error) {
	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var events []event.Event
		if err := json.Unmarshal([]byte(trimmed), &events); err != nil {
			return event.Batch{}, errInvalidPayload
		}
		return event.Batch{Events: events}, nil
	}
	// A payload is a batch when it carries an "events" member, even if that
	// member is null. Otherwise a single envelope object is accepted.
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(body, &shape); err != nil {
		return event.Batch{}, errInvalidPayload
	}
	if _, isBatch := shape["events"]; isBatch {
		var batch event.Batch
		if err := json.Unmarshal(body, &batch); err != nil {
			return event.Batch{}, errInvalidPayload
		}
		if batch.Events == nil {
			batch.Events = []event.Event{}
		}
		return batch, nil
	}
	var single event.Event
	if err := json.Unmarshal(body, &single); err != nil {
		return event.Batch{}, errInvalidPayload
	}
	return event.Batch{Events: []event.Event{single}}, nil
}

var errInvalidPayload = &payloadError{message: "the request body is not a valid event batch"}

type payloadError struct{ message string }

func (e *payloadError) Error() string { return e.message }

func ingestStatus(result ingest.Result) int {
	switch {
	case result.Accepted == 0 && result.Quarantined == 0:
		return http.StatusAccepted
	case result.Quarantined > 0 && result.Accepted > 0:
		return http.StatusPartialContent
	case result.Quarantined > 0:
		return http.StatusBadRequest
	default:
		return http.StatusAccepted
	}
}

func readLimited(writer http.ResponseWriter, request *http.Request, limit int64) ([]byte, bool) {
	if request.ContentLength > limit {
		writeError(writer, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the ingest limit")
		return nil, false
	}
	if request.Body == nil {
		return nil, true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		writeError(writer, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the ingest limit")
		return nil, false
	}
	return body, true
}

func parseOptionalTime(value string) (time.Time, bool) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		if numeric, numericErr := strconv.ParseInt(value, 10, 64); numericErr == nil {
			return time.Unix(0, numeric).UTC(), true
		}
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func otlpSourceFromPath(kind string) string {
	return "otlp-" + kind
}

func otlpKind(path string) otlp.Kind {
	switch path {
	case "traces":
		return otlp.KindTraces
	case "logs":
		return otlp.KindLogs
	case "metrics":
		return otlp.KindMetrics
	default:
		return otlp.KindTraces
	}
}

func runTitle(run store.Run) string {
	if run.Title != nil && *run.Title != "" {
		return *run.Title
	}
	return run.ID
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
