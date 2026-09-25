package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"traceboard/internal/alerts"
	"traceboard/internal/auth"
	"traceboard/internal/buildinfo"
	"traceboard/internal/event"
	"traceboard/internal/ingest"
	"traceboard/internal/live"
	"traceboard/internal/notify"
	"traceboard/internal/redact"
	"traceboard/internal/server"
	"traceboard/internal/store"
)

func runStart(env *environment, arguments []string) int {
	set := newFlagSet("start", env)
	noBrowser := set.Bool("no-browser", false, "print the sign-in URL instead of opening a browser")
	foreground := set.Bool("foreground", true, "run in the foreground")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	_ = foreground

	cfg, err := env.config()
	if err != nil {
		return env.fail("configuration error: %v", err)
	}

	redactor, err := redact.New(redact.Options{})
	if err != nil {
		return env.fail("redaction error: %v", err)
	}

	database, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return env.fail("database error: %v", err)
	}
	defer database.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := database.Migrate(ctx); err != nil {
		return env.fail("migration error: %v", err)
	}

	manager, err := auth.New(database, cfg.IngestToken)
	if err != nil {
		return env.fail("authentication error: %v", err)
	}
	// A new dashboard token is minted on every start, so a leaked URL from a
	// previous process is never valid again.
	signInToken, err := manager.RotateDashboard(ctx)
	if err != nil {
		return env.fail("authentication error: %v", err)
	}

	hub := live.NewHub()
	service := ingest.NewService(database, redactor, ingest.DefaultLimits())
	otlpService := ingest.NewOTLPService(service)
	for source, sourceConfig := range cfg.Sources {
		service.SetCaptureMode(source, sourceConfig.CaptureMode)
		if err := database.EnsureSource(ctx, source, sourceConfig.CaptureMode); err != nil {
			return env.fail("source error: %v", err)
		}
	}

	spoolRoot := filepath.Join(filepath.Dir(env.resolveConfigPath()), "spool")
	spool, err := ingest.NewSpool(spoolRoot, redactor, ingest.DefaultSpoolLimitBytes, ingest.DefaultSpoolAge)
	if err != nil {
		return env.fail("spool error: %v", err)
	}
	expirySweeper := ingest.NewExpirySweeper(spool, 1*time.Hour)
	defer expirySweeper.Stop()

	if drained, err := drainSpool(ctx, spool, service); err != nil {
		fmt.Fprintf(env.stderr, "spool drain reported: %v\n", err)
	} else if drained.Drained > 0 {
		fmt.Fprintf(env.stdout, "spool: replayed %d buffered events\n", drained.Drained)
	}

	notifier := notify.NewNotifier(notify.Config{Enabled: true})
	evaluator := alerts.NewEvaluator(database, alerts.DefaultConfig(), notifier)

	// A committed ingest can change run health immediately, so evaluation is
	// triggered in the background instead of waiting for the next tick. One
	// evaluation runs at a time; a burst of events collapses into a single pass.
	var evaluateMu sync.Mutex
	evaluating := false
	triggerEvaluation := func() {
		evaluateMu.Lock()
		if evaluating {
			evaluateMu.Unlock()
			return
		}
		evaluating = true
		evaluateMu.Unlock()
		go func() {
			defer func() {
				evaluateMu.Lock()
				evaluating = false
				evaluateMu.Unlock()
			}()
			_ = evaluator.Evaluate(ctx, time.Now().UTC())
		}()
	}
	service.SetOnAccepted(func(runID string) {
		run, err := database.GetRun(ctx, runID)
		if err == nil {
			hub.PublishRunChanged(run)
		}
		triggerEvaluation()
	})

	handler := server.New(server.Dependencies{
		Config:  cfg,
		Store:   database,
		Auth:    manager,
		Hub:     hub,
		Ingest:  ingest.NewCombinedService(service, otlpService),
		Version: buildinfo.Version,
		Started: time.Now().UTC(),
	})

	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return env.fail("cannot listen on %s: %v", cfg.ListenAddress, err)
	}

	address := listener.Addr().String()
	baseURL := "http://" + address
	signInURL := auth.SignInURL(baseURL, signInToken)
	_ = auth.WriteSignInURLFile(filepath.Join(filepath.Dir(env.resolveConfigPath()), "signin-url"), baseURL, signInToken)

	fmt.Fprintf(env.stdout, "traceboard %s\n", buildinfo.Version)
	fmt.Fprintf(env.stdout, "listening:  %s\n", baseURL)
	fmt.Fprintf(env.stdout, "dashboard:  %s\n", signInURL)
	fmt.Fprintf(env.stdout, "database:   %s\n", cfg.DatabasePath)
	fmt.Fprintf(env.stdout, "spool:      %s\n", spoolRoot)
	if !*noBrowser {
		go openBrowser(baseURL)
		fmt.Fprintf(env.stdout, "opened the dashboard in your default browser\n")
	}

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go evaluator.Run(ctx, 30*time.Second)
	go expirySweeper.Run(ctx)

	serverErrors := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case <-ctx.Done():
		fmt.Fprintln(env.stdout, "\nshutting down")
	case err := <-serverErrors:
		return env.fail("server error: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return env.fail("shutdown error: %v", err)
	}
	return 0
}

func drainSpool(ctx context.Context, spool *ingest.Spool, service *ingest.Service) (ingest.DrainResult, error) {
	return spool.Drain(ctx, func(payload json.RawMessage) error {
		batch, err := decodeSpoolPayload(payload)
		if err != nil {
			return err
		}
		result := service.Ingest(ctx, batch)
		if result.Quarantined > 0 && result.Accepted == 0 {
			return errSpoolRejected
		}
		return nil
	})
}

var errSpoolRejected = errors.New("spooled payload was rejected")

func decodeSpoolPayload(payload json.RawMessage) (event.Batch, error) {
	var batch event.Batch
	if err := json.Unmarshal(payload, &batch); err == nil && len(batch.Events) > 0 {
		return batch, nil
	}
	var single event.Event
	if err := json.Unmarshal(payload, &single); err != nil {
		return event.Batch{}, err
	}
	return event.Batch{Events: []event.Event{single}}, nil
}

func openBrowser(baseURL string) {
	time.Sleep(150 * time.Millisecond)
	var command *exec.Cmd
	switch {
	case fileExists("/usr/bin/xdg-open"):
		command = exec.Command("/usr/bin/xdg-open", baseURL)
	case fileExists("/usr/bin/open"):
		command = exec.Command("/usr/bin/open", baseURL)
	default:
		return
	}
	_ = command.Start()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func newIndentedJSONEncoder(writer io.Writer) *json.Encoder {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder
}
