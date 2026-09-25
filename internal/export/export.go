// Package export writes a captured run to a local file the operator chose.
// Exports are user-only files, are never written over without an explicit
// force flag, and contain only data that already passed redaction.
package export

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"traceboard/internal/event"
	"traceboard/internal/store"
)

type Format string

const (
	FormatJSON     Format = "json"
	FormatMarkdown Format = "markdown"
	FormatRaw      Format = "raw"
)

type Request struct {
	RunID       string
	Format      Format
	Destination string
	Force       bool
}

type Document struct {
	ExportedAt time.Time           `json:"exported_at"`
	Run        store.Run           `json:"run"`
	Steps      []store.Step        `json:"steps"`
	Events     []store.EventRecord `json:"events"`
	Alerts     []store.Alert       `json:"alerts"`
}

// ExportResult reports what a completed export wrote.
type ExportResult struct {
	Path   string `json:"path"`
	Format Format `json:"format"`
	Bytes  int64  `json:"bytes"`
}

type Runner interface {
	GetRun(ctx context.Context, runID string) (store.Run, error)
	ListSteps(ctx context.Context, runID string) ([]store.Step, error)
	ListAllEvents(ctx context.Context, runID string) ([]store.EventRecord, error)
	ListAlerts(ctx context.Context, filter store.AlertFilter) ([]store.Alert, error)
}

func Collect(ctx context.Context, database Runner, runID string) (Document, error) {
	run, err := database.GetRun(ctx, runID)
	if err != nil {
		return Document{}, err
	}
	steps, err := database.ListSteps(ctx, runID)
	if err != nil {
		return Document{}, err
	}
	events, err := database.ListAllEvents(ctx, runID)
	if err != nil {
		return Document{}, err
	}
	alerts, err := database.ListAlerts(ctx, store.AlertFilter{RunID: runID, Limit: 200})
	if err != nil {
		return Document{}, err
	}
	return Document{
		ExportedAt: time.Now().UTC(),
		Run:        run,
		Steps:      steps,
		Events:     events,
		Alerts:     alerts,
	}, nil
}

func Run(ctx context.Context, database Runner, request Request) (string, error) {
	if strings.TrimSpace(request.Destination) == "" {
		return "", errors.New("a destination path is required")
	}
	document, err := Collect(ctx, database, request.RunID)
	if err != nil {
		return "", err
	}

	absolute, err := filepath.Abs(request.Destination)
	if err != nil {
		return "", fmt.Errorf("resolve destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return "", fmt.Errorf("create destination directory: %w", err)
	}
	if _, err := os.Stat(absolute); err == nil && !request.Force {
		return "", fmt.Errorf("%s already exists; pass --force to replace it", absolute)
	}

	switch request.Format {
	case FormatJSON:
		return writeJSON(absolute, document)
	case FormatMarkdown:
		return writeMarkdown(absolute, document)
	case FormatRaw:
		return writeRawBundle(absolute, document)
	default:
		return "", fmt.Errorf("unsupported export format %q", request.Format)
	}
}

func writeJSON(path string, document Document) (string, error) {
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode export: %w", err)
	}
	return path, writePrivateFile(path, append(encoded, '\n'))
}

func writeMarkdown(path string, document Document) (string, error) {
	var builder strings.Builder
	title := document.Run.ID
	if document.Run.Title != nil && *document.Run.Title != "" {
		title = *document.Run.Title
	}
	fmt.Fprintf(&builder, "# Run %s\n\n", document.Run.ID)
	fmt.Fprintf(&builder, "- Title: %s\n", title)
	fmt.Fprintf(&builder, "- Source: %s\n", document.Run.Source)
	fmt.Fprintf(&builder, "- Status: %s\n", document.Run.Status)
	fmt.Fprintf(&builder, "- Capture mode: %s\n", captureSummary(document.Run))
	fmt.Fprintf(&builder, "- Events: %d\n", document.Run.EventCount)
	if document.Run.StartedAt != nil {
		fmt.Fprintf(&builder, "- Started: %s\n", document.Run.StartedAt.Format(time.RFC3339))
	}
	if document.Run.EndedAt != nil {
		fmt.Fprintf(&builder, "- Ended: %s\n", document.Run.EndedAt.Format(time.RFC3339))
	}
	if len(document.Run.Models) > 0 {
		fmt.Fprintf(&builder, "- Models: %s\n", strings.Join(document.Run.Models, ", "))
	}
	if document.Run.InputTokens != nil {
		fmt.Fprintf(&builder, "- Input tokens: %d\n", *document.Run.InputTokens)
	}
	if document.Run.OutputTokens != nil {
		fmt.Fprintf(&builder, "- Output tokens: %d\n", *document.Run.OutputTokens)
	}

	fmt.Fprintf(&builder, "\n## Timeline\n\n")
	for _, record := range document.Events {
		fmt.Fprintf(&builder, "### %s\n\n", record.OccurredAt.Format(time.RFC3339))
		fmt.Fprintf(&builder, "- Type: `%s`\n", record.Type)
		fmt.Fprintf(&builder, "- Status: `%s`\n", record.Status)
		fmt.Fprintf(&builder, "- Ingest sequence: %d\n", record.Sequence)
		if record.StepID != nil {
			fmt.Fprintf(&builder, "- Step: `%s`\n", *record.StepID)
		}
		if record.ParentStepID != nil {
			fmt.Fprintf(&builder, "- Parent step: `%s`\n", *record.ParentStepID)
		}
		if text, ok := record.Content["text"].(string); ok && text != "" {
			fmt.Fprintf(&builder, "\n```text\n%s\n```\n", text)
		}
		if message, ok := record.Attributes["status_message"].(string); ok && message != "" {
			fmt.Fprintf(&builder, "\n%s\n", message)
		}
		fmt.Fprintln(&builder)
	}

	if len(document.Steps) > 0 {
		fmt.Fprintf(&builder, "## Steps\n\n| Step | Type | Status | Events |\n| --- | --- | --- | --- |\n")
		for _, step := range document.Steps {
			fmt.Fprintf(&builder, "| `%s` | `%s` | `%s` | %d |\n", step.ID, step.Type, step.Status, step.EventCount)
		}
	}
	if len(document.Alerts) > 0 {
		fmt.Fprintf(&builder, "\n## Alerts\n\n")
		for _, alert := range document.Alerts {
			fmt.Fprintf(&builder, "- `%s` %s (%s)\n", alert.Type, alert.Message, alert.State)
		}
	}
	return path, writePrivateFile(path, []byte(builder.String()))
}

func writeRawBundle(path string, document Document) (string, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_TRUNC, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if err := os.Remove(path); err != nil {
				return "", fmt.Errorf("replace raw bundle: %w", err)
			}
			return writeRawBundle(path, document)
		}
		return "", fmt.Errorf("create raw bundle: %w", err)
	}
	defer file.Close()

	archive := zip.NewWriter(file)
	summary, err := json.MarshalIndent(struct {
		Run           store.Run `json:"run"`
		ExportedAt    time.Time `json:"exported_at"`
		SchemaVersion int       `json:"schema_version"`
		Note          string    `json:"note"`
	}{Run: document.Run, ExportedAt: document.ExportedAt, SchemaVersion: 1, Note: "Raw payloads are already redacted."}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode bundle summary: %w", err)
	}
	writer, err := archive.Create("summary.json")
	if err != nil {
		return "", fmt.Errorf("create bundle entry: %w", err)
	}
	if _, err := writer.Write(summary); err != nil {
		return "", fmt.Errorf("write bundle entry: %w", err)
	}

	withRaw := make([]store.EventRecord, 0, len(document.Events))
	for _, record := range document.Events {
		if record.Raw != nil {
			withRaw = append(withRaw, record)
		}
	}
	sort.SliceStable(withRaw, func(left, right int) bool {
		return withRaw[left].Sequence < withRaw[right].Sequence
	})
	for _, record := range withRaw {
		name := fmt.Sprintf("raw/%s-%d.json", record.EventID, record.Sequence)
		entry, err := archive.Create(name)
		if err != nil {
			return "", fmt.Errorf("create bundle entry: %w", err)
		}
		encoded, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return "", fmt.Errorf("encode raw payload: %w", err)
		}
		if _, err := entry.Write(encoded); err != nil {
			return "", fmt.Errorf("write bundle entry: %w", err)
		}
	}
	if err := archive.Close(); err != nil {
		return "", fmt.Errorf("finalize bundle: %w", err)
	}
	return path, nil
}

func writePrivateFile(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create export: %w", err)
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure export: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		return fmt.Errorf("write export: %w", err)
	}
	return file.Sync()
}

func captureSummary(run store.Run) string {
	if len(run.CaptureModes) == 0 {
		return string(event.CaptureMetadata)
	}
	modes := make([]string, 0, len(run.CaptureModes))
	for _, mode := range run.CaptureModes {
		modes = append(modes, string(mode))
	}
	return strings.Join(modes, ", ")
}
