package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"traceboard/internal/event"
)

func TestListRunsPaginatesWithStableCursor(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	seedRuns(t, store, 5)

	first, err := store.ListRuns(context.Background(), RunFilter{}, "", 2)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(first.Runs) != 2 || first.NextCursor == nil {
		t.Fatalf("first page = %d runs, cursor %v", len(first.Runs), first.NextCursor)
	}
	if first.Runs[0].ID != "run_5" || first.Runs[1].ID != "run_4" {
		t.Fatalf("first page order = %s, %s", first.Runs[0].ID, first.Runs[1].ID)
	}

	second, err := store.ListRuns(context.Background(), RunFilter{}, *first.NextCursor, 2)
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second.Runs) != 2 || second.Runs[0].ID != "run_3" {
		t.Fatalf("second page = %+v", second.Runs)
	}

	third, err := store.ListRuns(context.Background(), RunFilter{}, *second.NextCursor, 2)
	if err != nil {
		t.Fatalf("list third page: %v", err)
	}
	if len(third.Runs) != 1 || third.NextCursor != nil {
		t.Fatalf("third page = %+v cursor %v", third.Runs, third.NextCursor)
	}
}

func TestListRunsRejectsInvalidCursor(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()

	if _, err := store.ListRuns(context.Background(), RunFilter{}, "not-base64!!", 10); err == nil {
		t.Fatal("expected invalid cursor rejection")
	}
}

func TestListRunsAppliesEveryFilter(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	if err := store.UpsertProject(ctx, "project_a", ptrString("Project A"), ptrString("/tmp/a")); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	events := []event.Event{
		storeTestEvent("evt_1", "src_1", "run_1", "run.started", event.StatusStarted, started.Format(time.RFC3339)),
		storeTestEvent("evt_2", "src_2", "run_2", "run.completed", event.StatusCompleted, started.Add(time.Minute).Format(time.RFC3339)),
		storeTestEvent("evt_3", "src_3", "run_3", "run.failed", event.StatusFailed, started.Add(2*time.Minute).Format(time.RFC3339)),
	}
	events[0].Attributes = map[string]any{"project_id": "project_a"}
	if _, err := store.InsertEvents(ctx, events...); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE runs SET project_id = 'project_a' WHERE id = 'run_1'`); err != nil {
		t.Fatalf("assign project: %v", err)
	}

	cases := []struct {
		name   string
		filter RunFilter
		want   []string
	}{
		{"source", RunFilter{Source: "opencode"}, []string{"run_3", "run_2", "run_1"}},
		{"unknown source", RunFilter{Source: "claude-code"}, []string{}},
		{"status", RunFilter{Status: string(event.StatusFailed)}, []string{"run_3"}},
		{"project", RunFilter{ProjectID: "project_a"}, []string{"run_1"}},
		{"capture mode", RunFilter{CaptureMode: string(event.CaptureMetadata)}, []string{"run_3", "run_2", "run_1"}},
		{"unmatched capture mode", RunFilter{CaptureMode: string(event.CaptureDetailed)}, []string{}},
		{"no alert", RunFilter{AlertState: "none"}, []string{"run_3", "run_2", "run_1"}},
		{"top level runs only", RunFilter{SubagentOnly: boolPointer(false)}, []string{"run_3", "run_2", "run_1"}},
		{"time range", RunFilter{StartedAfter: ptrTime(started.Add(90 * time.Second))}, []string{"run_3"}},
	}
	for _, testCase := range cases {
		page, err := store.ListRuns(ctx, testCase.filter, "", 10)
		if err != nil {
			t.Fatalf("%s filter: %v", testCase.name, err)
		}
		if got := runIDs(page.Runs); !equalStrings(got, testCase.want) {
			t.Fatalf("%s filter returned %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestListRunsSearchesFullTextIndex(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	match := storeTestEvent("evt_1", "src_1", "run_1", "run.started", event.StatusStarted, "2026-09-25T10:00:00Z")
	match.Attributes = map[string]any{"tool_name": "ripgrep_search"}
	match.Content = map[string]any{"text": "please refactor the ingest pipeline"}
	other := storeTestEvent("evt_2", "src_2", "run_2", "run.started", event.StatusStarted, "2026-09-25T10:00:01Z")
	other.Attributes = map[string]any{"tool_name": "read_file"}
	if _, err := store.InsertEvents(ctx, match, other); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	page, err := store.ListRuns(ctx, RunFilter{Query: "refactor ingest"}, "", 10)
	if err != nil {
		t.Fatalf("search runs: %v", err)
	}
	if got := runIDs(page.Runs); !equalStrings(got, []string{"run_1"}) {
		t.Fatalf("search returned %v", got)
	}

	if _, err := store.ListRuns(ctx, RunFilter{Query: `"unbalanced ( OR`}, "", 10); err != nil {
		t.Fatalf("hostile search query must not error: %v", err)
	}
	page, err = store.ListRuns(ctx, RunFilter{Query: `"unbalanced ( OR`}, "", 10)
	if err != nil {
		t.Fatalf("hostile search query: %v", err)
	}
	if len(page.Runs) != 0 {
		t.Fatalf("hostile search query matched %v", runIDs(page.Runs))
	}
}

func TestListEventsOrdersTimelineDeterministically(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	late := int64(9)
	early := int64(1)
	events := []event.Event{
		storeTestEvent("evt_1", "src_1", "run_1", "run.started", event.StatusStarted, "2026-09-25T10:00:00Z"),
		storeTestEvent("evt_2", "src_2", "run_1", "tool.completed", event.StatusCompleted, "2026-09-25T10:00:05Z"),
		storeTestEvent("evt_3", "src_3", "run_1", "tool.started", event.StatusStarted, "2026-09-25T10:00:05Z"),
		storeTestEvent("evt_4", "src_4", "run_1", "model.completed", event.StatusCompleted, "2026-09-25T10:00:02Z"),
	}
	events[1].SourceSequence = &late
	events[2].SourceSequence = &early
	events[3].SourceSequence = &early
	if _, err := store.InsertEvents(ctx, events...); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	page, err := store.ListEvents(ctx, "run_1", 0, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	want := []string{"evt_1", "evt_4", "evt_3", "evt_2"}
	got := make([]string, 0, len(page.Events))
	for _, record := range page.Events {
		got = append(got, record.EventID)
	}
	if !equalStrings(got, want) {
		t.Fatalf("timeline order = %v, want %v", got, want)
	}
	if page.RunSequence != 5 {
		t.Fatalf("run sequence = %d, want 5", page.RunSequence)
	}
	// The page reached the end of the run, so the cursor is the run's own head:
	// a client holding every event is not told it is missing any.
	if page.NextSequence != page.RunSequence {
		t.Fatalf("resume cursor = %d, want the run head %d", page.NextSequence, page.RunSequence)
	}
	if page.RunStatus != event.StatusStarted {
		t.Fatalf("run status = %s, want started", page.RunStatus)
	}
}

func TestListEventsReportsTheRangeAsIncompleteWhenAPageStopsShort(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	seed := func(id, runID string, at time.Time) {
		if _, err := store.InsertEvents(ctx, event.Event{
			SchemaVersion: event.SchemaVersion1,
			EventID:       id, SourceEventID: id, Source: "opencode", RunID: runID,
			OccurredAt: at, Type: event.TypeToolStarted, Status: event.StatusStarted,
			Capture: map[string]any{"mode": "metadata"}, Attributes: map[string]any{},
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("evt_1", "run_page", time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC))
	seed("evt_2", "run_page", time.Date(2026, 9, 25, 10, 0, 1, 0, time.UTC))
	seed("evt_3", "run_page", time.Date(2026, 9, 25, 10, 0, 2, 0, time.UTC))

	page, err := store.ListEvents(ctx, "run_page", 0, 2)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if !page.HasMore {
		t.Fatal("a truncated page must report that more remain")
	}
	if page.NextSequence != 2 {
		t.Fatalf("resume cursor = %d, want the highest delivered sequence 2", page.NextSequence)
	}
	if page.RunSequence != 4 {
		t.Fatalf("run head = %d, want 4", page.RunSequence)
	}
	// The cursor sits behind the head, which is exactly the signal a client uses
	// to refuse to claim the range is complete.
	if page.NextSequence >= page.RunSequence {
		t.Fatal("a truncated page must leave the cursor behind the run head")
	}
}

func TestListEventsPagesBySequenceWithoutGaps(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	start := storeTestEvent("evt_0", "src_0", "run_1", "run.started", event.StatusStarted, "2026-09-25T10:00:00Z")
	if _, err := store.InsertEvents(ctx, start); err != nil {
		t.Fatalf("insert run start: %v", err)
	}
	for index := 1; index <= 25; index++ {
		input := storeTestEvent(fmt.Sprintf("evt_%d", index), fmt.Sprintf("src_%d", index), "run_1", "tool.started", event.StatusStarted, "2026-09-25T10:00:00Z")
		if _, err := store.InsertEvents(ctx, input); err != nil {
			t.Fatalf("insert event %d: %v", index, err)
		}
	}

	collected := make([]int64, 0, 26)
	cursor := int64(0)
	for {
		page, err := store.ListEvents(ctx, "run_1", cursor, 10)
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		for _, record := range page.Events {
			collected = append(collected, record.Sequence)
		}
		cursor = page.NextSequence
		if !page.HasMore {
			break
		}
	}
	if len(collected) != 26 {
		t.Fatalf("collected %d events, want 26", len(collected))
	}
	for index, sequence := range collected {
		if sequence != int64(index+1) {
			t.Fatalf("sequence at %d = %d", index, sequence)
		}
	}
}

func TestListEventsRejectsUnknownRun(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()

	if _, err := store.ListEvents(context.Background(), "run_missing", 0, 10); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("expected ErrRunNotFound, got %v", err)
	}
}

func TestGetRunDerivesModelsTokensAndDuration(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	events := []event.Event{
		storeTestEvent("evt_1", "src_1", "run_1", "run.started", event.StatusStarted, started.Format(time.RFC3339)),
		storeTestEvent("evt_2", "src_2", "run_1", "model.completed", event.StatusCompleted, started.Add(3*time.Second).Format(time.RFC3339)),
		storeTestEvent("evt_3", "src_3", "run_1", "run.completed", event.StatusCompleted, started.Add(4*time.Second).Format(time.RFC3339)),
	}
	events[1].Attributes = map[string]any{"model": "claude-sonnet-4", "input_tokens": 1200, "output_tokens": 340}
	if _, err := store.InsertEvents(ctx, events...); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	run, err := store.GetRun(ctx, "run_1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.DurationMS == nil || *run.DurationMS != 4000 {
		t.Fatalf("duration = %v", run.DurationMS)
	}
	if len(run.Models) != 1 || run.Models[0] != "claude-sonnet-4" {
		t.Fatalf("models = %v", run.Models)
	}
	if run.InputTokens == nil || *run.InputTokens != 1200 || run.OutputTokens == nil || *run.OutputTokens != 340 {
		t.Fatalf("tokens = %v / %v", run.InputTokens, run.OutputTokens)
	}
}

func TestDeleteRunCascadesEventsStepsAlertsAndSearch(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	seedRuns(t, store, 1)
	input := storeTestEvent("evt_1", "src_1", "run_1", "run.completed", event.StatusCompleted, "2026-09-25T10:00:00Z")
	input.StepID = "step_1"
	input.Content = map[string]any{"text": "deletable searchable prompt"}
	if _, err := store.InsertEvents(ctx, input); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, _, err := store.OpenAlert(ctx, Alert{Type: "run_failed", RunID: ptrString("run_1"), Message: "failed"}); err != nil {
		t.Fatalf("open alert: %v", err)
	}

	result, err := store.DeleteRun(ctx, "run_1")
	if err != nil {
		t.Fatalf("delete run: %v", err)
	}
	if result.Events != 3 || result.Steps != 1 || result.Alerts != 1 || result.SearchEntries != 3 {
		t.Fatalf("delete result = %+v", result)
	}

	for table, want := range map[string]int{"runs": 0, "steps": 0, "events": 0, "events_fts": 0, "alerts": 0} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != want {
			t.Fatalf("%s count = %d, want %d", table, count, want)
		}
	}
	if _, err := store.ListRuns(ctx, RunFilter{Query: "deletable"}, "", 10); err != nil {
		t.Fatalf("search after delete: %v", err)
	}
}

func TestDeleteRunRejectsUnknownRun(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()

	if _, err := store.DeleteRun(context.Background(), "run_missing"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("expected ErrRunNotFound, got %v", err)
	}
}

func TestSelectRetentionCandidatesNeverSelectsActiveRuns(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	events := []event.Event{
		storeTestEvent("evt_1", "src_1", "run_old", "run.completed", event.StatusCompleted, now.Add(-72*time.Hour).Format(time.RFC3339)),
		storeTestEvent("evt_2", "src_2", "run_recent", "run.completed", event.StatusCompleted, now.Add(-2*time.Hour).Format(time.RFC3339)),
		storeTestEvent("evt_3", "src_3", "run_stalled", "run.started", event.StatusStarted, now.Add(-72*time.Hour).Format(time.RFC3339)),
	}
	if _, err := store.InsertEvents(ctx, events...); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	candidates, err := store.SelectRetentionCandidates(ctx, RetentionPolicy{Days: 1}, now)
	if err != nil {
		t.Fatalf("select candidates: %v", err)
	}
	if got := runIDs(candidates); !equalStrings(got, []string{"run_old"}) {
		t.Fatalf("candidates = %v, want only the finished old run", got)
	}
}

func TestSelectRetentionCandidatesKeepsNewestPerProject(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertProject(ctx, "project_a", nil, nil); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	events := []event.Event{
		storeTestEvent("evt_1", "src_1", "run_1", "run.completed", event.StatusCompleted, now.Add(-3*time.Hour).Format(time.RFC3339)),
		storeTestEvent("evt_2", "src_2", "run_2", "run.completed", event.StatusCompleted, now.Add(-2*time.Hour).Format(time.RFC3339)),
		storeTestEvent("evt_3", "src_3", "run_3", "run.completed", event.StatusCompleted, now.Add(-1*time.Hour).Format(time.RFC3339)),
	}
	if _, err := store.InsertEvents(ctx, events...); err != nil {
		t.Fatalf("insert events: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE runs SET project_id = 'project_a'`); err != nil {
		t.Fatalf("assign project: %v", err)
	}

	candidates, err := store.SelectRetentionCandidates(ctx, RetentionPolicy{KeepNewestPerProject: 2}, now)
	if err != nil {
		t.Fatalf("select candidates: %v", err)
	}
	if got := runIDs(candidates); !equalStrings(got, []string{"run_1"}) {
		t.Fatalf("candidates = %v, want run_1", got)
	}
}

func TestListStepsReportsParentRelationships(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	parent := storeTestEvent("evt_1", "src_1", "run_1", "subagent.started", event.StatusStarted, "2026-09-25T10:00:00Z")
	parent.StepID = "step_parent"
	child := storeTestEvent("evt_2", "src_2", "run_1", "tool.started", event.StatusStarted, "2026-09-25T10:00:01Z")
	child.StepID = "step_child"
	child.ParentStepID = "step_parent"
	if _, err := store.InsertEvents(ctx, parent, child); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	steps, err := store.ListSteps(ctx, "run_1")
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("steps = %+v", steps)
	}
	var childStep *Step
	for index := range steps {
		if steps[index].ID == "step_child" {
			childStep = &steps[index]
		}
	}
	if childStep == nil || childStep.ParentStepID == nil || *childStep.ParentStepID != "step_parent" {
		t.Fatalf("child step parent = %+v", childStep)
	}
}

func TestSessionLifecycleRejectsExpiredAndConsumedTokens(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	now := time.Now().UTC()
	if err := store.CreateSession(ctx, "session_1", "token_1", now.Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.SessionValid(ctx, "session_1", now); err != nil {
		t.Fatalf("validate session: %v", err)
	}
	if _, err := store.ConsumeSession(ctx, "token_1", now); err != nil {
		t.Fatalf("consume session: %v", err)
	}
	if _, err := store.ConsumeSession(ctx, "token_1", now); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("second consume = %v, want ErrSessionNotFound", err)
	}
	if _, err := store.ConsumeSession(ctx, "wrong", now); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("wrong token = %v, want ErrSessionNotFound", err)
	}
	if err := store.CreateSession(ctx, "session_2", "token_2", now.Add(-time.Hour)); err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	if err := store.SessionValid(ctx, "session_2", now); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expired session = %v, want ErrSessionNotFound", err)
	}
}

func TestSessionStoreNeverPersistsRawToken(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	if err := store.CreateSession(ctx, "session_1", "super-secret-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	var stored string
	if err := store.db.QueryRow("SELECT token_hash FROM sessions WHERE id = 'session_1'").Scan(&stored); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("query session hash: %v", err)
	}
	if stored == "super-secret-token" || stored == "" {
		t.Fatalf("session token stored in the clear: %q", stored)
	}
}

func TestOpenAlertIsIdempotentAndResolvable(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()
	seedRuns(t, store, 1)

	first, _, err := store.OpenAlert(ctx, Alert{Type: "run_failed", RunID: ptrString("run_1"), Message: "run failed"})
	if err != nil {
		t.Fatalf("open alert: %v", err)
	}
	second, _, err := store.OpenAlert(ctx, Alert{Type: "run_failed", RunID: ptrString("run_1"), Message: "run failed again"})
	if err != nil {
		t.Fatalf("reopen alert: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate alert created: %s then %s", first.ID, second.ID)
	}

	acknowledged, err := store.AcknowledgeAlert(ctx, first.ID)
	if err != nil {
		t.Fatalf("acknowledge alert: %v", err)
	}
	if acknowledged.AcknowledgedAt == nil || acknowledged.State != "acknowledged" {
		t.Fatalf("acknowledged alert = %+v", acknowledged)
	}

	resolved, err := store.ResolveAlerts(ctx, "run_failed", ptrString("run_1"), nil, time.Now())
	if err != nil {
		t.Fatalf("resolve alerts: %v", err)
	}
	if resolved != 1 {
		t.Fatalf("resolved %d alerts, want 1", resolved)
	}
	open, err := store.ListAlerts(ctx, AlertFilter{State: "open"})
	if err != nil {
		t.Fatalf("list open alerts: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open alerts after resolve = %+v", open)
	}
}

func TestListSourcesTracksHeartbeatAndQuarantine(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()
	ctx := context.Background()

	now := time.Now().UTC()
	if err := store.RecordHeartbeat(ctx, "opencode", now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	if err := store.RecordHeartbeat(ctx, "codex", now); err != nil {
		t.Fatalf("record heartbeat: %v", err)
	}
	invalid := storeTestEvent("evt_bad", "src_bad", "run_bad", "not-a-type", event.StatusStarted, "2026-09-25T10:00:00Z")
	invalid.SchemaVersion = 9
	if _, err := store.InsertEvents(ctx, invalid); err != nil {
		t.Fatalf("quarantine invalid event: %v", err)
	}

	sources, err := store.ListSources(ctx)
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %+v", sources)
	}
	byName := map[string]Source{}
	for _, source := range sources {
		byName[source.Name] = source
	}
	if !byName["opencode"].Connected || byName["opencode"].LastHeartbeatAt == nil {
		t.Fatalf("opencode source = %+v", byName["opencode"])
	}

	entries, err := store.ListQuarantine(ctx, 10)
	if err != nil {
		t.Fatalf("list quarantine: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("quarantine entries = %+v", entries)
	}
	if count, err := store.QuarantineCount(ctx); err != nil || count != 1 {
		t.Fatalf("quarantine count = %d (%v)", count, err)
	}
}

func seedRuns(t *testing.T, store *Store, count int) {
	t.Helper()
	events := make([]event.Event, 0, count)
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	for index := 1; index <= count; index++ {
		runID := "run_" + string(rune('0'+index))
		events = append(events,
			storeTestEvent("evt_"+runID+"_1", "src_"+runID+"_1", runID, "run.started", event.StatusStarted, base.Add(time.Duration(index)*time.Minute).Format(time.RFC3339)),
			storeTestEvent("evt_"+runID+"_2", "src_"+runID+"_2", runID, "run.completed", event.StatusCompleted, base.Add(time.Duration(index)*time.Minute+time.Second).Format(time.RFC3339)),
		)
	}
	if _, err := store.InsertEvents(context.Background(), events...); err != nil {
		t.Fatalf("seed runs: %v", err)
	}
}

func runIDs(runs []Run) []string {
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	return ids
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestStoreCreatesUserOnlyFiles(t *testing.T) {
	path := privateTempDatabase(t)
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	info, err := statFileMode(path)
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if info != 0o600 {
		t.Fatalf("database mode = %04o, want 0600", info)
	}
}

func ptrString(value string) *string {
	return &value
}

func boolPointer(value bool) *bool {
	return &value
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func statFileMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}
