package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"traceboard/internal/config"
	"traceboard/internal/configure"
	"traceboard/internal/event"
	"traceboard/internal/export"
	"traceboard/internal/hooks"
	"traceboard/internal/retention"
	"traceboard/internal/store"
)

func runConfigure(env *environment, arguments []string) int {
	if len(arguments) == 0 {
		return env.fail("usage: traceboard configure <source> [--mode off|metadata|detailed] [--dry-run]")
	}
	source := arguments[0]
	set := newFlagSet("configure", env)
	mode := set.String("mode", string(event.CaptureMetadata), "capture mode: off, metadata, or detailed")
	dryRun := set.Bool("dry-run", false, "print the changes without writing them")
	captureOnly := set.Bool("capture-only", false, "record the capture mode without editing agent configuration files")
	printPlugin := set.Bool("print-plugin", false, "print the OpenCode plugin module and exit")
	if err := set.Parse(arguments[1:]); err != nil {
		return 2
	}

	if *printPlugin {
		if source != hooks.SourceOpenCode {
			return env.fail("--print-plugin is only available for the opencode source")
		}
		if err := configure.PrintPlugin(env.stdout); err != nil {
			return env.fail("could not print the plugin: %v", err)
		}
		return 0
	}

	captureMode, err := parseCaptureMode(*mode)
	if err != nil {
		return env.fail("%v", err)
	}
	if *captureOnly {
		if err := persistCaptureMode(env, source, captureMode); err != nil {
			return env.fail("could not record the capture mode: %v", err)
		}
		fmt.Fprintf(env.stdout, "capture mode for %s recorded as %s\n", source, captureMode)
		fmt.Fprintln(env.stdout, "no agent configuration file was changed; run without --capture-only to install the integration")
		return 0
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return env.fail("could not resolve the binary path: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return env.fail("could not resolve the home directory: %v", err)
	}

	changeSet, err := configure.Apply(source, home, captureMode, configure.Options{
		Home:       home,
		ConfigDir:  filepath.Dir(env.resolveConfigPath()),
		BinaryPath: binaryPath,
		DryRun:     *dryRun,
	})
	if err != nil {
		return env.fail("%v", err)
	}

	writer := tabwriter.NewWriter(env.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(writer, "source\t%s\ncapture_mode\t%s\ndry_run\t%t\n\n", changeSet.Source, changeSet.CaptureMode, changeSet.DryRun)
	fmt.Fprintln(writer, "ACTION\tFILE\tBACKUP\tAPPLIED")
	for _, change := range changeSet.Changes {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%t\n", change.Action, change.Path, change.Backup, change.Applied)
	}
	writer.Flush()
	if len(changeSet.ManualSteps) > 0 {
		fmt.Fprintln(env.stdout, "\nmanual steps:")
		for _, step := range changeSet.ManualSteps {
			fmt.Fprintf(env.stdout, "  - %s\n", step)
		}
	}

	if *dryRun {
		return 0
	}
	if err := persistCaptureMode(env, source, captureMode); err != nil {
		return env.fail("could not record the capture mode: %v", err)
	}
	fmt.Fprintf(env.stdout, "\ncapture mode recorded as %s; restart the agent to activate it\n", captureMode)
	return 0
}

func persistCaptureMode(env *environment, source string, mode event.CaptureMode) error {
	path := env.resolveConfigPath()
	cfg, err := config.Ensure(path)
	if err != nil {
		return err
	}
	if cfg.Sources == nil {
		cfg.Sources = map[string]config.SourceConfig{}
	}
	cfg.Sources[source] = config.SourceConfig{CaptureMode: mode}
	if err := config.Save(path, cfg); err != nil {
		return err
	}
	database, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Migrate(context.Background()); err != nil {
		return err
	}
	return database.SetSourceCaptureMode(context.Background(), source, mode)
}

func runExport(env *environment, arguments []string) int {
	if len(arguments) == 0 {
		return env.fail("usage: traceboard export <run-id> [--format json|markdown|raw] [--out path] [--force]")
	}
	runID := arguments[0]
	set := newFlagSet("export", env)
	format := set.String("format", string(export.FormatJSON), "export format: json, markdown, or raw")
	out := set.String("out", "", "destination file path")
	force := set.Bool("force", false, "replace an existing destination file")
	if err := set.Parse(arguments[1:]); err != nil {
		return 2
	}
	if strings.TrimSpace(runID) == "" {
		return env.fail("a run id is required")
	}
	destination := *out
	if destination == "" {
		destination = filepath.Join(".", sanitizeFilename(runID)+"."+string(*format))
	}
	_, database, code, err := openStore(env)
	if err != nil {
		return code
	}
	defer database.Close()

	written, err := export.Run(context.Background(), database, export.Request{
		RunID:       runID,
		Format:      export.Format(*format),
		Destination: destination,
		Force:       *force,
	})
	if err != nil {
		if errors.Is(err, store.ErrRunNotFound) {
			return env.fail("run not found: %s", runID)
		}
		return env.fail("export failed: %v", err)
	}
	fmt.Fprintf(env.stdout, "wrote %s\n", written)
	return 0
}

func runDelete(env *environment, arguments []string) int {
	if len(arguments) < 2 {
		return env.fail("usage: traceboard delete <run-id> <exact-run-title>")
	}
	runID := arguments[0]
	confirmation := strings.Join(arguments[1:], " ")

	_, database, code, err := openStore(env)
	if err != nil {
		return code
	}
	defer database.Close()

	ctx := context.Background()
	run, err := database.GetRun(ctx, runID)
	if err != nil {
		return env.fail("run not found: %s", runID)
	}
	expected := run.ID
	if run.Title != nil && *run.Title != "" {
		expected = *run.Title
	}
	if confirmation != expected {
		return env.fail("confirmation does not match. The run title is: %q", expected)
	}
	result, err := database.DeleteRun(ctx, runID)
	if err != nil {
		return env.fail("delete failed: %v", err)
	}
	fmt.Fprintf(env.stdout, "deleted run %s (%d events, %d steps, %d alerts, %d search entries)\n",
		result.RunID, result.Events, result.Steps, result.Alerts, result.SearchEntries)
	return 0
}

func runRetention(env *environment, arguments []string) int {
	if len(arguments) == 0 {
		return env.fail("usage: traceboard retention set <30d|always> | preview | apply")
	}
	switch arguments[0] {
	case "set":
		return runRetentionSet(env, arguments[1:])
	case "preview":
		return runRetentionPreview(env)
	case "apply":
		return runRetentionApply(env)
	default:
		return env.fail("unknown retention subcommand: %s", arguments[0])
	}
}

func runRetentionSet(env *environment, arguments []string) int {
	set := newFlagSet("retention set", env)
	perProject := set.Int("keep-newest-per-project", 0, "also keep only the newest N runs per project")
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	positional := set.Args()
	if len(positional) != 1 {
		return env.fail("usage: traceboard retention set <30d|always> [--keep-newest-per-project N]")
	}
	days, err := retention.ParseDuration(positional[0])
	if err != nil {
		return env.fail("%v", err)
	}
	if *perProject < 0 {
		return env.fail("--keep-newest-per-project must not be negative")
	}
	path := env.resolveConfigPath()
	cfg, err := config.Ensure(path)
	if err != nil {
		return env.fail("configuration error: %v", err)
	}
	cfg.RetentionDays = days
	if err := config.Save(path, cfg); err != nil {
		return env.fail("could not save the configuration: %v", err)
	}
	policy := retention.Policy{Days: days, KeepNewestPerProject: *perProject}
	fmt.Fprintf(env.stdout, "retention: %s\n", policy.Describe())
	fmt.Fprintln(env.stdout, "active runs are exempt from every retention policy")
	return 0
}

func runRetentionPreview(env *environment) int {
	cfg, database, code, err := openStore(env)
	if err != nil {
		return code
	}
	defer database.Close()

	policy := retention.Policy{Days: cfg.RetentionDays}
	preview, err := retention.Preview(context.Background(), database, policy, nowUTC())
	if err != nil {
		return env.fail("retention preview failed: %v", err)
	}
	if len(preview.Runs) == 0 {
		fmt.Fprintf(env.stdout, "retention: %s\nno runs match\n", policy.Describe())
		return 0
	}
	writer := tabwriter.NewWriter(env.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "RUN\tSOURCE\tSTATUS\tEVENTS\tLAST EVENT")
	for _, run := range preview.Runs {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%s\n", run.ID, run.Source, run.Status, run.EventCount, formatTime(run.LastEventAt))
	}
	writer.Flush()
	fmt.Fprintf(env.stdout, "\n%d runs and %d events would be deleted\n", len(preview.Runs), preview.Events)
	return 0
}

func runRetentionApply(env *environment) int {
	cfg, database, code, err := openStore(env)
	if err != nil {
		return code
	}
	defer database.Close()

	policy := retention.Policy{Days: cfg.RetentionDays}
	result, err := retention.Apply(context.Background(), database, policy, nowUTC())
	if err != nil {
		return env.fail("retention apply failed: %v", err)
	}
	fmt.Fprintf(env.stdout, "retention: %s\n", policy.Describe())
	fmt.Fprintf(env.stdout, "deleted %d runs, %d events, %d steps, %d alerts\n", result.Runs, result.Events, result.Steps, result.Alerts)
	return 0
}

func openStore(env *environment) (config.Config, *store.Store, int, error) {
	cfg, err := env.config()
	if err != nil {
		return config.Config{}, nil, env.fail("configuration error: %v", err), err
	}
	database, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return config.Config{}, nil, env.fail("database error: %v", err), err
	}
	if err := database.Migrate(context.Background()); err != nil {
		database.Close()
		return config.Config{}, nil, env.fail("migration error: %v", err), err
	}
	return cfg, database, 0, nil
}

func sanitizeFilename(value string) string {
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
	if trimmed == "" {
		return "run"
	}
	if len(trimmed) > 80 {
		trimmed = trimmed[:80]
	}
	return trimmed
}
