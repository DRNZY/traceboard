// Package notify sends local desktop notifications through the host operating
// system. A missing notification tool is treated as "desktop notifications are
// unavailable", never as a server failure, and no notification body ever
// contains raw tool output or captured content.
package notify

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"traceboard/internal/store"
)

const appName = "Traceboard"

type Config struct {
	Enabled bool
	// Executable overrides the detected notifier, mainly for tests.
	Executable string
	Timeout    time.Duration
}

type Notifier struct {
	config     Config
	executable string
	runner     func(ctx context.Context, name string, arguments ...string) error
}

type Recorder struct {
	Calls []Invocation
}

type Invocation struct {
	Name      string
	Arguments []string
}

func (recorder *Recorder) call(_ context.Context, name string, arguments ...string) error {
	recorder.Calls = append(recorder.Calls, Invocation{Name: name, Arguments: arguments})
	return nil
}

func NewNotifier(config Config) *Notifier {
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	executable := config.Executable
	if executable == "" {
		executable = detectExecutable()
	}
	return &Notifier{
		config:     config,
		executable: executable,
		runner: func(ctx context.Context, name string, arguments ...string) error {
			callCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return exec.CommandContext(callCtx, name, arguments...).Run()
		},
	}
}

func NewRecordingNotifier(config Config, recorder *Recorder) *Notifier {
	notifier := NewNotifier(config)
	notifier.runner = recorder.call
	return notifier
}

func detectExecutable() string {
	for _, candidate := range []string{"/usr/bin/notify-send", "/usr/local/bin/notify-send", "/bin/notify-send"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

func (notifier *Notifier) Available() bool {
	return notifier.executable != ""
}

func (notifier *Notifier) Notify(ctx context.Context, alert store.Alert) error {
	if !notifier.config.Enabled || !notifier.Available() {
		return nil
	}
	arguments := notifier.arguments(alert)
	if err := notifier.runner(ctx, notifier.executable, arguments...); err != nil {
		return fmt.Errorf("send desktop notification: %w", err)
	}
	return nil
}

func (notifier *Notifier) arguments(alert store.Alert) []string {
	return []string{
		"--app-name", appName,
		"--urgency", urgency(alert.Type),
		"--expire-time", "8000",
		summary(alert),
		body(alert),
	}
}

func summary(alert store.Alert) string {
	target := "Traceboard"
	switch {
	case alert.RunID != nil:
		target = "Run " + *alert.RunID
	case alert.Source != nil:
		target = "Source " + *alert.Source
	}
	return target
}

// body is built only from the alert's own summary text. It never reads the run's
// captured prompt, tool output, or raw payloads.
func body(alert store.Alert) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, alert.Message)
	if len(cleaned) > 240 {
		cleaned = cleaned[:240] + "…"
	}
	return cleaned
}

func urgency(alertType string) string {
	switch alertType {
	case "run_failed":
		return "critical"
	case "run_stalled", "source_disconnected":
		return "normal"
	default:
		return "low"
	}
}

var ErrUnavailable = errors.New("no desktop notification tool was found")
