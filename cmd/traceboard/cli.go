package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"traceboard/internal/config"
	"traceboard/internal/event"
)

const usage = `traceboard - local-first observability for coding-agent runs

Usage:
  traceboard start                     Run the collector and dashboard
  traceboard status                    Show the configured endpoint and store state
  traceboard sources                   List configured sources and capture modes
  traceboard configure <source>        Apply source integration steps
  traceboard doctor                    Check the local installation end to end
  traceboard auth rotate               Invalidate sessions and print a new sign-in URL
  traceboard export <run-id>           Write a run to a local file
  traceboard delete <run-id>           Delete a run and its indexed data
  traceboard retention set <duration>  Set automatic run retention
  traceboard retention preview         Show the runs retention would delete
  traceboard retention apply           Delete the previewed runs now
  traceboard daemon install            Install a systemd user service
  traceboard daemon uninstall          Remove the systemd user service
  traceboard hook <source>             Forward one hook payload from stdin
  traceboard version                   Print build metadata

Run "traceboard <command> -h" for command flags.
`

type environment struct {
	configPath string
	stdout     io.Writer
	stderr     io.Writer
	stdin      io.Reader
}

func (env *environment) resolveConfigPath() string {
	if env.configPath != "" {
		return env.configPath
	}
	if override := strings.TrimSpace(os.Getenv("TRACEBOARD_CONFIG")); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "traceboard", "config.json")
}

func (env *environment) config() (config.Config, error) {
	return config.Ensure(env.resolveConfigPath())
}

func newFlagSet(name string, env *environment) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(env.stderr)
	set.Usage = func() {
		fmt.Fprintf(env.stderr, "Usage: traceboard %s [flags]\n\nFlags:\n", name)
		set.PrintDefaults()
	}
	return set
}

func (env *environment) fail(format string, arguments ...any) int {
	fmt.Fprintf(env.stderr, format+"\n", arguments...)
	return 1
}

func sourceNames() []string {
	names := make([]string, 0, 8)
	for key := range configuredSourceDefaults() {
		names = append(names, key)
	}
	sort.Strings(names)
	return names
}

func configuredSourceDefaults() map[string]event.CaptureMode {
	return map[string]event.CaptureMode{
		"opencode":    event.CaptureMetadata,
		"claude-code": event.CaptureMetadata,
		"codex":       event.CaptureMetadata,
		"antigravity": event.CaptureMetadata,
		"gemini-cli":  event.CaptureMetadata,
	}
}

func parseCaptureMode(value string) (event.CaptureMode, error) {
	switch event.CaptureMode(strings.ToLower(strings.TrimSpace(value))) {
	case event.CaptureOff:
		return event.CaptureOff, nil
	case event.CaptureMetadata:
		return event.CaptureMetadata, nil
	case event.CaptureDetailed:
		return event.CaptureDetailed, nil
	default:
		return "", fmt.Errorf("capture mode must be off, metadata, or detailed")
	}
}

func printJSON(writer io.Writer, payload any) error {
	encoder := newIndentedJSONEncoder(writer)
	return encoder.Encode(payload)
}
