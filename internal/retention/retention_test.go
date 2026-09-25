package retention

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"traceboard/internal/event"
	"traceboard/internal/store"
)

func newTestDatabase(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure temp directory: %v", err)
	}
	database, err := store.Open(filepath.Join(directory, "traceboard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func seed(t *testing.T, database *store.Store, runID string, at time.Time, eventType string, status event.Status) {
	t.Helper()
	if _, err := database.InsertEvents(context.Background(), event.Event{
		SchemaVersion: event.SchemaVersion1,
		EventID:       runID + "-evt", SourceEventID: runID + "-evt",
		Source: "opencode", SourceVersion: "test", RunID: runID, OccurredAt: at,
		Type: eventType, Status: status,
		Capture: map[string]any{"mode": "metadata"}, Attributes: map[string]any{},
	}); err != nil {
		t.Fatalf("seed %s: %v", runID, err)
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]int{"30d": 30, "2w": 14, "48h": 2, "1d": 1, "always": 0, "infinite": 0}
	for input, want := range cases {
		got, err := ParseDuration(input)
		if err != nil {
			t.Fatalf("ParseDuration(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseDuration(%q) = %d, want %d", input, got, want)
		}
	}
	for _, input := range []string{"", "d", "0d", "-1d", "abc", "30y", "30", "99999d"} {
		if _, err := ParseDuration(input); err == nil {
			t.Fatalf("ParseDuration(%q) should have failed", input)
		}
	}
}

func TestPolicyDescribe(t *testing.T) {
	if got := (Policy{}).Describe(); got != "keep runs indefinitely" {
		t.Fatalf("describe = %q", got)
	}
	if got := (Policy{Days: 30}).Describe(); got != "keep 30 days" {
		t.Fatalf("describe = %q", got)
	}
	if got := (Policy{KeepNewestPerProject: 5}).Describe(); got != "keep the newest 5 runs per project" {
		t.Fatalf("describe = %q", got)
	}
	if got := (Policy{Days: 7, KeepNewestPerProject: 2}).Describe(); got != "keep 7 days and the newest 2 runs per project" {
		t.Fatalf("describe = %q", got)
	}
}

func TestPreviewSelectsOnlyAgedFinishedRuns(t *testing.T) {
	database := newTestDatabase(t)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	seed(t, database, "run_old", now.Add(-72*time.Hour), event.TypeRunCompleted, event.StatusCompleted)
	seed(t, database, "run_recent", now.Add(-2*time.Hour), event.TypeRunCompleted, event.StatusCompleted)
	seed(t, database, "run_active", now.Add(-72*time.Hour), event.TypeRunStarted, event.StatusStarted)

	preview, err := Preview(context.Background(), database, Policy{Days: 1}, now)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Runs) != 1 || preview.Runs[0].ID != "run_old" {
		t.Fatalf("preview = %+v", preview.Runs)
	}
	if preview.Events != 1 {
		t.Fatalf("previewed events = %d", preview.Events)
	}
}

func TestPreviewIsANoopWithoutAPolicy(t *testing.T) {
	database := newTestDatabase(t)
	seed(t, database, "run_old", time.Now().UTC().Add(-72*time.Hour), event.TypeRunCompleted, event.StatusCompleted)
	preview, err := Preview(context.Background(), database, Policy{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Runs) != 0 {
		t.Fatalf("a no-op policy selected %+v", preview.Runs)
	}
}

func TestApplyDeletesPreviewedRunsAndRemovesIndexedData(t *testing.T) {
	database := newTestDatabase(t)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	seed(t, database, "run_old", now.Add(-72*time.Hour), event.TypeRunCompleted, event.StatusCompleted)
	seed(t, database, "run_recent", now.Add(-time.Hour), event.TypeRunCompleted, event.StatusCompleted)

	result, err := Apply(context.Background(), database, Policy{Days: 1}, now)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.Runs != 1 || result.Events != 1 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := database.GetRun(context.Background(), "run_old"); err == nil {
		t.Fatal("the aged run was not deleted")
	}
	if _, err := database.GetRun(context.Background(), "run_recent"); err != nil {
		t.Fatalf("the recent run was deleted: %v", err)
	}
	page, err := database.ListRuns(context.Background(), store.RunFilter{Query: "run_old"}, "", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Runs) != 0 {
		t.Fatalf("search entries survived deletion: %+v", page.Runs)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	database := newTestDatabase(t)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	seed(t, database, "run_old", now.Add(-72*time.Hour), event.TypeRunCompleted, event.StatusCompleted)

	first, err := Apply(context.Background(), database, Policy{Days: 1}, now)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	second, err := Apply(context.Background(), database, Policy{Days: 1}, now)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if first.Runs != 1 || second.Runs != 0 {
		t.Fatalf("runs deleted: %d then %d", first.Runs, second.Runs)
	}
}

func TestKeepNewestPerProjectPolicy(t *testing.T) {
	database := newTestDatabase(t)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if err := database.UpsertProject(context.Background(), "project_a", nil, nil); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	for index, runID := range []string{"run_1", "run_2", "run_3"} {
		seed(t, database, runID, now.Add(-time.Duration(3-index)*time.Hour), event.TypeRunCompleted, event.StatusCompleted)
	}
	if _, err := database.InsertEvents(context.Background(), event.Event{
		SchemaVersion: event.SchemaVersion1, EventID: "link", SourceEventID: "link",
		Source: "opencode", RunID: "run_1", OccurredAt: now,
		Type: event.TypeFileChanged, Status: event.StatusCompleted,
		Capture:    map[string]any{"mode": "metadata"},
		Attributes: map[string]any{"project_id": "project_a"},
	}); err != nil {
		t.Fatalf("assign project: %v", err)
	}

	result, err := Apply(context.Background(), database, Policy{KeepNewestPerProject: 1}, now)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.Runs != 0 {
		t.Fatalf("a per-project cap must not delete runs that belong to no project: %+v", result)
	}
	for _, runID := range []string{"run_1", "run_2", "run_3"} {
		if _, err := database.GetRun(context.Background(), runID); err != nil {
			t.Fatalf("%s was deleted: %v", runID, err)
		}
	}
}

func TestKeepNewestPerProjectCapsOnlyProjectRuns(t *testing.T) {
	database := newTestDatabase(t)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if err := database.UpsertProject(context.Background(), "project_a", nil, nil); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	for index, runID := range []string{"run_1", "run_2", "run_3"} {
		seed(t, database, runID, now.Add(-time.Duration(3-index)*time.Hour), event.TypeRunCompleted, event.StatusCompleted)
	}
	if err := database.AssignProject(context.Background(), []string{"run_1", "run_2", "run_3"}, "project_a"); err != nil {
		t.Fatalf("link project: %v", err)
	}
	seed(t, database, "run_unlinked", now.Add(-72*time.Hour), event.TypeRunCompleted, event.StatusCompleted)

	result, err := Apply(context.Background(), database, Policy{KeepNewestPerProject: 1}, now)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.Runs != 2 {
		t.Fatalf("deleted %d runs, want the two oldest project runs", result.Runs)
	}
	if _, err := database.GetRun(context.Background(), "run_3"); err != nil {
		t.Fatalf("the newest project run was deleted: %v", err)
	}
	if _, err := database.GetRun(context.Background(), "run_unlinked"); err != nil {
		t.Fatalf("an unlinked run was deleted: %v", err)
	}
}
