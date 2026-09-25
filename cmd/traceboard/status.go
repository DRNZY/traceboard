package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"traceboard/internal/alerts"
	"traceboard/internal/auth"
	"traceboard/internal/buildinfo"
	"traceboard/internal/config"
	"traceboard/internal/event"
	"traceboard/internal/hooks"
	"traceboard/internal/ingest"
	"traceboard/internal/redact"
	"traceboard/internal/store"
)

func runStatus(env *environment, arguments []string) int {
	set := newFlagSet("status", env)
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	cfg, err := env.config()
	if err != nil {
		return env.fail("configuration error: %v", err)
	}
	database, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return env.fail("database error: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		return env.fail("migration error: %v", err)
	}
	runCount, err := database.CountRuns(ctx)
	if err != nil {
		return env.fail("store error: %v", err)
	}
	quarantined, err := database.QuarantineCount(ctx)
	if err != nil {
		return env.fail("store error: %v", err)
	}
	openAlerts, err := database.ListAlerts(ctx, store.AlertFilter{State: "open", Limit: 500})
	if err != nil {
		return env.fail("store error: %v", err)
	}

	writer := tabwriter.NewWriter(env.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(writer, "endpoint\t%s\n", "http://"+cfg.ListenAddress)
	fmt.Fprintf(writer, "version\t%s\n", buildinfo.Version)
	fmt.Fprintf(writer, "config\t%s\n", env.resolveConfigPath())
	fmt.Fprintf(writer, "database\t%s\n", cfg.DatabasePath)
	fmt.Fprintf(writer, "runs\t%d\n", runCount)
	fmt.Fprintf(writer, "quarantined\t%d\n", quarantined)
	fmt.Fprintf(writer, "open_alerts\t%d\n", len(openAlerts))
	fmt.Fprintf(writer, "retention_days\t%d\n", cfg.RetentionDays)
	writer.Flush()
	return 0
}

func runSources(env *environment, arguments []string) int {
	set := newFlagSet("sources", env)
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	cfg, err := env.config()
	if err != nil {
		return env.fail("configuration error: %v", err)
	}
	database, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return env.fail("database error: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		return env.fail("migration error: %v", err)
	}
	for _, name := range hooks.Sources() {
		if err := database.EnsureSource(ctx, name, captureModeOf(cfg, name)); err != nil {
			return env.fail("store error: %v", err)
		}
	}
	sources, err := database.ListSources(ctx)
	if err != nil {
		return env.fail("store error: %v", err)
	}

	writer := tabwriter.NewWriter(env.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "SOURCE\tCAPTURE\tRUNS\tQUARANTINE\tLAST HEARTBEAT")
	for _, source := range sources {
		fmt.Fprintf(writer, "%s\t%s\t%d\t%d\t%s\n",
			source.Name, source.CaptureMode, source.RunCount, source.QuarantineCount, formatTime(source.LastHeartbeatAt))
	}
	writer.Flush()
	return 0
}

func runDoctor(env *environment, arguments []string) int {
	set := newFlagSet("doctor", env)
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	checks := make([]doctorCheck, 0, 10)
	failures := 0
	record := func(name, status, detail string) {
		checks = append(checks, doctorCheck{name: name, status: status, detail: detail})
		// A warning is information, not a broken installation.
		if status == "fail" {
			failures++
		}
	}

	configPath := env.resolveConfigPath()
	cfg, err := env.config()
	if err != nil {
		record("configuration", "fail", err.Error())
		printChecks(env, checks)
		return 1
	}
	record("configuration", "ok", "loaded from "+configPath)

	directoryInfo, err := os.Stat(filepath.Dir(configPath))
	if err != nil {
		record("config directory mode", "fail", err.Error())
	} else if directoryInfo.Mode().Perm() != 0o700 {
		record("config directory mode", "warn", fmt.Sprintf("%04o, expected 0700", directoryInfo.Mode().Perm()))
	} else {
		record("config directory mode", "ok", "0700")
	}

	database, err := store.Open(cfg.DatabasePath)
	if err != nil {
		record("database", "fail", err.Error())
		printChecks(env, checks)
		return 1
	}
	defer database.Close()

	databaseInfo, statErr := os.Stat(cfg.DatabasePath)
	switch {
	case statErr != nil:
		record("database file mode", "fail", statErr.Error())
	case databaseInfo.Mode().Perm() != 0o600:
		record("database file mode", "warn", fmt.Sprintf("%04o, expected 0600", databaseInfo.Mode().Perm()))
	default:
		record("database file mode", "ok", "0600")
	}

	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		record("migrations", "fail", err.Error())
	} else {
		record("migrations", "ok", "schema is current")
	}

	redactor, err := redact.New(redact.Options{})
	if err != nil {
		record("redaction", "fail", err.Error())
	} else {
		probe, matches := redactor.Apply("Bearer abcdefghijklmnopqrstuvwxyz")
		redacted, _ := probe.(string)
		if len(matches) == 0 || !strings.Contains(redacted, "[REDACTED:") {
			record("redaction", "fail", "a probe secret was not redacted")
		} else {
			record("redaction", "ok", "probe secret redacted before persistence")
		}
	}

	manager, err := auth.New(database, cfg.IngestToken)
	if err != nil {
		record("authentication", "fail", err.Error())
	} else if err := manager.ValidateIngestToken(cfg.IngestToken); err != nil {
		record("authentication", "fail", "the stored ingest token is unusable")
	} else {
		record("authentication", "ok", "ingest token and session store are usable")
	}

	evaluator := alerts.NewEvaluator(database, alerts.DefaultConfig(), nil)
	if err := evaluator.Evaluate(ctx, time.Now().UTC()); err != nil {
		record("alert evaluation", "warn", err.Error())
	} else {
		record("alert evaluation", "ok", "run and source health evaluated")
	}

	spoolRoot := filepath.Join(filepath.Dir(configPath), "spool")
	if _, err := newSpool(spoolRoot, redactor); err != nil {
		record("spool", "fail", err.Error())
	} else {
		record("spool", "ok", "spool ready at "+spoolRoot)
	}

	listenerReachable := probeListener(cfg.ListenAddress) == nil
	if listenerReachable {
		record("collector endpoint", "ok", "a collector is already listening on "+cfg.ListenAddress)
	} else {
		record("collector endpoint", "warn", "no collector is running on "+cfg.ListenAddress)
	}
	otlpStatus := "warn"
	if listenerReachable {
		otlpStatus = "ok"
	}
	record("otlp endpoints", otlpStatus, "reachable only while the collector is running")

	printChecks(env, checks)
	if failures > 0 {
		return 1
	}
	return 0
}

type doctorCheck struct {
	name   string
	status string
	detail string
}

func printChecks(env *environment, checks []doctorCheck) {
	writer := tabwriter.NewWriter(env.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "CHECK\tSTATUS\tDETAIL")
	for _, item := range checks {
		fmt.Fprintf(writer, "%s\t%s\t%s\n", item.name, strings.ToUpper(item.status), item.detail)
	}
	writer.Flush()
}

func runAuthRotate(env *environment, arguments []string) int {
	set := newFlagSet("auth rotate", env)
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	cfg, database, code, err := openStore(env)
	if err != nil {
		return code
	}
	defer database.Close()

	ctx := context.Background()
	manager, err := auth.New(database, cfg.IngestToken)
	if err != nil {
		return env.fail("authentication error: %v", err)
	}
	token, err := manager.RotateDashboard(ctx)
	if err != nil {
		return env.fail("rotation failed: %v", err)
	}
	fmt.Fprintln(env.stdout, "all dashboard sessions were invalidated")
	fmt.Fprintf(env.stdout, "the ingest token was not changed\n")
	fmt.Fprintf(env.stdout, "sign in with:\nhttp://%s/auth/signin?token=%s\n", cfg.ListenAddress, token)
	return 0
}

func captureModeOf(cfg config.Config, source string) event.CaptureMode {
	if sourceConfig, ok := cfg.Sources[source]; ok {
		return sourceConfig.CaptureMode
	}
	return event.CaptureMetadata
}

// probeListener reports whether a collector already answers on the address. It
// deliberately sends no credential so doctor never mutates server state.
func probeListener(address string) error {
	connection, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
	if err != nil {
		return err
	}
	return connection.Close()
}

func newSpool(root string, redactor redact.Redactor) (*ingest.Spool, error) {
	return ingest.NewSpool(root, redactor, ingest.DefaultSpoolLimitBytes, ingest.DefaultSpoolAge)
}

func formatTime(value *time.Time) string {
	if value == nil {
		return "never"
	}
	return value.Format(time.RFC3339)
}
