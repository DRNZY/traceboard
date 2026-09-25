// Package configure applies source-specific integration steps to a developer's
// agent configuration. Every write is previewable, backs up the file it changes,
// preserves unknown JSON keys, and refuses to touch a path outside a recognized
// agent configuration root.
package configure

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"traceboard/internal/event"
	"traceboard/internal/hooks"
)

type Change struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	Detail  string `json:"detail"`
	Backup  string `json:"backup,omitempty"`
	Applied bool   `json:"applied"`
}

type ChangeSet struct {
	Source      string            `json:"source"`
	CaptureMode event.CaptureMode `json:"capture_mode"`
	Changes     []Change          `json:"changes"`
	ManualSteps []string          `json:"manual_steps,omitempty"`
	DryRun      bool              `json:"dry_run"`
}

type Options struct {
	Home       string
	ConfigDir  string
	BinaryPath string
	DryRun     bool
}

// roots are the only directories a change may touch. Anything outside them is
// refused so a mistyped path cannot rewrite an unrelated file.
func roots(home string) []string {
	return []string{
		filepath.Join(home, ".config", "opencode"),
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".codex"),
		filepath.Join(home, ".antigravity"),
		filepath.Join(home, ".gemini"),
		filepath.Join(home, ".config", "gemini"),
	}
}

func IsRecognizedRoot(home, path string) bool {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, root := range roots(home) {
		absoluteRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if absolute == absoluteRoot || strings.HasPrefix(absolute, absoluteRoot+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func Apply(source string, home string, mode event.CaptureMode, options Options) (ChangeSet, error) {
	if home == "" {
		return ChangeSet{}, errors.New("a home directory is required")
	}
	if _, ok := hooks.For(source); !ok {
		return ChangeSet{}, fmt.Errorf("unsupported source %q", source)
	}
	switch mode {
	case event.CaptureOff, event.CaptureMetadata, event.CaptureDetailed:
	default:
		return ChangeSet{}, fmt.Errorf("invalid capture mode %q", mode)
	}

	set := ChangeSet{Source: source, CaptureMode: mode, DryRun: options.DryRun, Changes: []Change{}, ManualSteps: []string{}}
	var err error
	switch source {
	case hooks.SourceOpenCode:
		err = applyOpenCode(home, options, &set)
	case hooks.SourceClaudeCode:
		err = applyClaudeCode(home, options, &set)
	case hooks.SourceCodex:
		err = applyCodex(home, options, &set)
	case hooks.SourceAntigravity:
		err = applyAntigravity(home, options, &set)
	case hooks.SourceGeminiCLI:
		err = applyGeminiCLI(home, options, &set)
	default:
		err = fmt.Errorf("unsupported source %q", source)
	}
	if err != nil {
		return ChangeSet{}, err
	}
	return set, nil
}

func applyOpenCode(home string, options Options, set *ChangeSet) error {
	directory := filepath.Join(home, ".config", "opencode")
	pluginDirectory := filepath.Join(directory, "plugin")
	path := filepath.Join(pluginDirectory, "traceboard.ts")
	if err := ensurePath(path, options, set, "create the OpenCode plugin that forwards events"); err != nil {
		return err
	}
	modulePath := filepath.Join(pluginDirectory, "traceboard.json")
	if err := mergeJSON(modulePath, options, set, map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"plugin":  []string{"./plugin/traceboard.ts"},
	}, "register the Traceboard plugin"); err != nil {
		return err
	}
	set.ManualSteps = append(set.ManualSteps,
		fmt.Sprintf("Export TRACEBOARD_INGEST_TOKEN with the value from %s before starting OpenCode.", filepath.Join(options.ConfigDir, "config.json")),
		"Restart OpenCode so the plugin is loaded.",
	)
	return nil
}

func applyClaudeCode(home string, options Options, set *ChangeSet) error {
	directory := filepath.Join(home, ".claude")
	settingsPath := filepath.Join(directory, "settings.json")
	hook := map[string]any{
		"matcher": "*",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": fmt.Sprintf("%s hook %s", options.BinaryPath, hooks.SourceClaudeCode),
			},
		},
	}
	if err := mergeJSON(settingsPath, options, set, map[string]any{
		"hooks": map[string]any{
			"SessionStart":       []any{hook},
			"SessionEnd":         []any{hook},
			"UserPromptSubmit":   []any{hook},
			"PreToolUse":         []any{hook},
			"PostToolUse":        []any{hook},
			"PostToolUseFailure": []any{hook},
		},
	}, "register the Traceboard hook command for Claude Code"); err != nil {
		return err
	}
	set.ManualSteps = append(set.ManualSteps,
		"Review the hooks Claude Code reports on the next session; Traceboard never bypasses hook trust.",
		"Set CLAUDE_CODE_ENABLE_TELEMETRY=1 and OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:47821 for the OTLP path.",
	)
	return nil
}

func applyCodex(home string, options Options, set *ChangeSet) error {
	path := filepath.Join(home, ".codex", "hooks.json")
	if err := mergeJSON(path, options, set, map[string]any{
		"hooks": map[string]any{
			"session.start":  command(options, hooks.SourceCodex),
			"session.end":    command(options, hooks.SourceCodex),
			"turn.start":     command(options, hooks.SourceCodex),
			"turn.end":       command(options, hooks.SourceCodex),
			"tool.start":     command(options, hooks.SourceCodex),
			"tool.end":       command(options, hooks.SourceCodex),
			"command.finish": command(options, hooks.SourceCodex),
			"compaction":     command(options, hooks.SourceCodex),
			"subagent.start": command(options, hooks.SourceCodex),
			"subagent.end":   command(options, hooks.SourceCodex),
		},
	}, "register the Traceboard lifecycle hooks for Codex"); err != nil {
		return err
	}
	set.ManualSteps = append(set.ManualSteps,
		"Open Codex and review each hook in /hooks; Traceboard never bypasses hook trust.",
	)
	return nil
}

func applyAntigravity(home string, options Options, set *ChangeSet) error {
	path := filepath.Join(home, ".antigravity", "hooks.json")
	if err := mergeJSON(path, options, set, map[string]any{
		"hooks": map[string]any{
			"conversation.start": command(options, hooks.SourceAntigravity),
			"conversation.end":   command(options, hooks.SourceAntigravity),
			"execution.start":    command(options, hooks.SourceAntigravity),
			"execution.end":      command(options, hooks.SourceAntigravity),
			"tool.start":         command(options, hooks.SourceAntigravity),
			"tool.end":           command(options, hooks.SourceAntigravity),
		},
	}, "register the Traceboard hooks for Antigravity"); err != nil {
		return err
	}
	set.ManualSteps = append(set.ManualSteps,
		"Restart the Antigravity IDE so it reloads its hook file.",
		"For SDK agents, point OTEL_EXPORTER_OTLP_ENDPOINT at http://127.0.0.1:47821.",
	)
	return nil
}

func applyGeminiCLI(home string, options Options, set *ChangeSet) error {
	path := filepath.Join(home, ".gemini", "settings.json")
	if err := mergeJSON(path, options, set, map[string]any{
		"telemetry": map[string]any{
			"enabled":      true,
			"target":       "otlp",
			"otlpEndpoint": "http://127.0.0.1:47821",
		},
	}, "point Gemini CLI telemetry at the local OTLP endpoint"); err != nil {
		return err
	}
	set.ManualSteps = append(set.ManualSteps,
		"Set OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer <ingest token> before starting Gemini CLI.",
		"Restart the Gemini CLI session.",
	)
	return nil
}

func command(options Options, source string) []any {
	return []any{
		map[string]any{
			"type":    "command",
			"command": fmt.Sprintf("%s hook %s", options.BinaryPath, source),
		},
	}
}

func ensurePath(path string, options Options, set *ChangeSet, detail string) error {
	if !IsRecognizedRoot(options.Home, filepath.Dir(path)) {
		return fmt.Errorf("refusing to write outside a recognized agent configuration root: %s", path)
	}
	if _, err := os.Stat(path); err == nil {
		set.Changes = append(set.Changes, Change{Path: path, Action: "keep", Detail: detail + " (already present)", Applied: false})
		return nil
	}
	set.Changes = append(set.Changes, Change{Path: path, Action: "create", Detail: detail, Applied: !options.DryRun})
	if options.DryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	contents := fmt.Sprintf("// Traceboard OpenCode plugin placeholder.\n// Run: traceboard configure opencode --print-plugin > %s\n", path)
	return os.WriteFile(path, []byte(contents), 0o600)
}

// mergeJSON performs a shallow recursive merge that preserves keys Traceboard
// does not own, so an existing user configuration survives the change.
func mergeJSON(path string, options Options, set *ChangeSet, addition map[string]any, detail string) error {
	if !IsRecognizedRoot(options.Home, filepath.Dir(path)) {
		return fmt.Errorf("refusing to write outside a recognized agent configuration root: %s", path)
	}
	existing := map[string]any{}
	backup := ""
	if contents, err := os.ReadFile(path); err == nil {
		decoder := json.NewDecoder(bytes.NewReader(contents))
		if err := decoder.Decode(&existing); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
		if !options.DryRun {
			backup = path + ".traceboard-backup-" + time.Now().UTC().Format("20060102T150405Z")
			if err := os.WriteFile(backup, contents, 0o600); err != nil {
				return fmt.Errorf("back up %s: %w", path, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	merged := mergeMaps(existing, addition)
	encoded, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	set.Changes = append(set.Changes, Change{Path: path, Action: "merge", Detail: detail, Backup: backup, Applied: !options.DryRun})
	if options.DryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o600)
}

func mergeMaps(base, addition map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	for key, value := range addition {
		existing, present := base[key]
		if !present {
			base[key] = value
			continue
		}
		existingMap, existingIsMap := existing.(map[string]any)
		additionMap, additionIsMap := value.(map[string]any)
		if existingIsMap && additionIsMap {
			base[key] = mergeMaps(existingMap, additionMap)
			continue
		}
		base[key] = value
	}
	return base
}

// PrintPlugin writes the OpenCode plugin module to a writer so a user can
// inspect it before it is installed.
func PrintPlugin(writer interface{ Write([]byte) (int, error) }) error {
	_, err := writer.Write([]byte(openCodePluginSource))
	return err
}

const openCodePluginSource = `// Traceboard OpenCode plugin.
//
// This plugin only observes OpenCode. It never modifies arguments, returns, or
// tool behaviour, and it always returns quickly so telemetry can never slow or
// break an agent run.
import type { Plugin } from "@opencode-ai/plugin"

const ENDPOINT = process.env.TRACEBOARD_INGEST_URL ?? "http://127.0.0.1:47821/api/v1/events"
const TOKEN = process.env.TRACEBOARD_INGEST_TOKEN ?? ""
const TIMEOUT_MS = 750

const SECRET_PATTERNS: RegExp[] = [
  /Bearer\s+[A-Za-z0-9._~+/=-]{8,}/g,
  /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/g,
  /\b(sk-(?:proj-)?[A-Za-z0-9_-]{16,}|AIza[0-9A-Za-z_-]{20,}|AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,})\b/g,
  /\b(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis):\/\/[^\s/@:]+:[^\s/@]+@[^\s]+/g,
]

export function redact(value: unknown): unknown {
  if (typeof value === "string") {
    let out = value
    for (const pattern of SECRET_PATTERNS) out = out.replace(pattern, "[REDACTED]")
    return out
  }
  if (Array.isArray(value)) return value.map(redact)
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {}
    for (const [key, inner] of Object.entries(value)) out[key] = redact(inner)
    return out
  }
  return value
}

class Batcher {
  private queue: unknown[] = []
  private timer: ReturnType<typeof setTimeout> | null = null

  constructor(private readonly send: (events: unknown[]) => Promise<void>) {}

  push(event: unknown) {
    this.queue.push(event)
    if (this.timer) return
    this.timer = setTimeout(() => void this.flush(), 200)
  }

  async flush() {
    if (this.timer) { clearTimeout(this.timer); this.timer = null }
    const batch = this.queue
    this.queue = []
    if (batch.length === 0) return
    try { await this.send(batch) } catch { /* never surface telemetry errors to the agent */ }
  }
}

export const TraceboardPlugin: Plugin = async ({ client, $ }) => {
  const post = async (events: unknown[]) => {
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), TIMEOUT_MS)
    try {
      await fetch(ENDPOINT, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: "Bearer " + TOKEN },
        body: JSON.stringify({ events }),
        signal: controller.signal,
      })
    } finally {
      clearTimeout(timer)
    }
  }
  const batcher = new Batcher(post)
  const now = () => new Date().toISOString()

  return {
    event: async ({ event }) => {
      const properties = (event as { properties?: Record<string, unknown> }).properties ?? {}
      const sessionID = String(properties.sessionID ?? properties.session_id ?? "unknown")
      const runID = "opencode:" + sessionID
      let type = "source.extension"
      let status = "unknown"
      let stepID: string | undefined
      switch (event.type) {
        case "session.created": type = "run.started"; status = "started"; break
        case "session.completed": type = "run.completed"; status = "completed"; break
        case "session.error": type = "run.failed"; status = "failed"; break
        case "session.idle": type = "run.incomplete"; status = "incomplete"; break
        case "message.updated": type = "prompt.received"; status = "completed"; break
        case "tool.execute.before": type = "tool.started"; status = "started"; stepID = String(properties.callID ?? ""); break
        case "tool.execute.after": type = "tool.completed"; status = "completed"; stepID = String(properties.callID ?? ""); break
        case "file.changed": type = "file.changed"; status = "completed"; break
        case "permission.asked": type = "permission.requested"; status = "started"; break
        case "permission.replied": type = "permission.resolved"; status = "completed"; break
        case "session.compacted": type = "context.compacted"; status = "completed"; break
        default: break
      }
      batcher.push({
        schema_version: 1,
        source: "opencode",
        source_version: "plugin",
        run_id: runID,
        step_id: stepID,
        occurred_at: now(),
        type,
        status,
        capture: { mode: process.env.TRACEBOARD_CAPTURE_MODE ?? "metadata" },
        attributes: redact({ ...properties, event_name: event.type }),
      })
    },
  }
}

export default TraceboardPlugin
`
