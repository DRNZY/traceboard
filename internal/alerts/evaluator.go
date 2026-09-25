// Package alerts evaluates run and source health on a fixed cadence. Evaluation
// is idempotent: one open alert exists per stable (type, target) key, so a
// reconnect or a repeated event never produces a duplicate.
package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"traceboard/internal/event"
	"traceboard/internal/store"
)

// Stable alert types.
const (
	TypeRunFailed     = "run_failed"
	TypeRunStalled    = "run_stalled"
	TypeSourceOffline = "source_disconnected"
	TypeRunIncomplete = "run_incomplete"
)

type Config struct {
	StalledAfter      time.Duration
	HeartbeatInterval time.Duration
	MissedHeartbeats  int
	Notifiable        bool
}

func DefaultConfig() Config {
	return Config{
		StalledAfter:      15 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		MissedHeartbeats:  3,
		Notifiable:        true,
	}
}

type Notifier interface {
	Notify(ctx context.Context, alert store.Alert) error
}

type Evaluator struct {
	store    *store.Store
	config   Config
	notifier Notifier
	now      func() time.Time
}

func NewEvaluator(database *store.Store, config Config, notifier Notifier) *Evaluator {
	if config.StalledAfter <= 0 {
		config.StalledAfter = 15 * time.Minute
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 30 * time.Second
	}
	if config.MissedHeartbeats <= 0 {
		config.MissedHeartbeats = 3
	}
	return &Evaluator{store: database, config: config, notifier: notifier, now: func() time.Time { return time.Now().UTC() }}
}

func (evaluator *Evaluator) SetClock(now func() time.Time) {
	if now != nil {
		evaluator.now = now
	}
}

func (evaluator *Evaluator) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// A failed evaluation must never take the server down.
			_ = evaluator.Evaluate(ctx, evaluator.now())
		}
	}
}

// Evaluate opens an alert for every source-reported failure, stalled active
// run, and silent source, and resolves alerts whose condition has cleared.
func (evaluator *Evaluator) Evaluate(ctx context.Context, now time.Time) error {
	if err := evaluator.evaluateRuns(ctx, now); err != nil {
		return err
	}
	return evaluator.evaluateSources(ctx, now)
}

func (evaluator *Evaluator) evaluateRuns(ctx context.Context, now time.Time) error {
	terminal := []event.Status{event.StatusCompleted, event.StatusFailed, event.StatusCancelled, event.StatusIncomplete}
	for _, status := range terminal {
		page, err := evaluator.store.ListRuns(ctx, store.RunFilter{Status: string(status)}, "", 500)
		if err != nil {
			return fmt.Errorf("list %s runs: %w", status, err)
		}
		for _, run := range page.Runs {
			if status == event.StatusFailed {
				alert := store.Alert{
					Type:    TypeRunFailed,
					RunID:   stringPointer(run.ID),
					Message: failureMessage(run, now),
				}
				opened, created, err := evaluator.store.OpenAlert(ctx, alert)
				if err != nil {
					return err
				}
				evaluator.notify(ctx, opened, created)
			}
			// A run that reached a terminal state is no longer stalled.
			if _, err := evaluator.store.ResolveAlerts(ctx, TypeRunStalled, stringPointer(run.ID), nil, now); err != nil {
				return err
			}
			if status != event.StatusFailed {
				if _, err := evaluator.store.ResolveAlerts(ctx, TypeRunIncomplete, stringPointer(run.ID), nil, now); err != nil {
					return err
				}
			}
		}
	}
	return evaluator.evaluateStalled(ctx, now)
}

func (evaluator *Evaluator) evaluateStalled(ctx context.Context, now time.Time) error {
	page, err := evaluator.store.ListRuns(ctx, store.RunFilter{Status: string(event.StatusStarted)}, "", 500)
	if err != nil {
		return fmt.Errorf("list active runs: %w", err)
	}
	cutoff := now.Add(-evaluator.config.StalledAfter).UnixNano()
	for _, run := range page.Runs {
		if run.LastEventAt == nil {
			continue
		}
		if run.LastEventAt.UnixNano() > cutoff {
			continue
		}
		alert := store.Alert{
			Type:    TypeRunStalled,
			RunID:   stringPointer(run.ID),
			Message: fmt.Sprintf("run %s has received no event since %s", run.ID, run.LastEventAt.Format(time.RFC3339)),
		}
		opened, created, err := evaluator.store.OpenAlert(ctx, alert)
		if err != nil {
			return err
		}
		evaluator.notify(ctx, opened, created)
	}
	return nil
}

func (evaluator *Evaluator) evaluateSources(ctx context.Context, now time.Time) error {
	sources, err := evaluator.store.ListSources(ctx)
	if err != nil {
		return fmt.Errorf("list sources: %w", err)
	}
	deadline := now.Add(-time.Duration(evaluator.config.MissedHeartbeats) * evaluator.config.HeartbeatInterval)
	for _, source := range sources {
		if source.CaptureMode == event.CaptureOff {
			continue
		}
		if source.LastHeartbeatAt == nil {
			continue
		}
		if source.LastHeartbeatAt.After(deadline) {
			if _, err := evaluator.store.ResolveAlerts(ctx, TypeSourceOffline, nil, stringPointer(source.Name), now); err != nil {
				return err
			}
			continue
		}
		alert := store.Alert{
			Type:    TypeSourceOffline,
			Source:  stringPointer(source.Name),
			Message: fmt.Sprintf("source %s has missed %d heartbeats", source.Name, evaluator.config.MissedHeartbeats),
		}
		opened, created, err := evaluator.store.OpenAlert(ctx, alert)
		if err != nil {
			return err
		}
		evaluator.notify(ctx, opened, created)
	}
	return nil
}

// notify sends a desktop notification only for an alert this pass created, so a
// repeated evaluation never re-notifies and an acknowledged alert stays quiet.
func (evaluator *Evaluator) notify(ctx context.Context, alert store.Alert, created bool) {
	if !created || evaluator.notifier == nil || !evaluator.config.Notifiable || alert.AcknowledgedAt != nil {
		return
	}
	_ = evaluator.notifier.Notify(ctx, alert)
}

func failureMessage(run store.Run, now time.Time) string {
	if run.Title != nil && *run.Title != "" {
		return fmt.Sprintf("run %s failed: %s", run.ID, truncate(*run.Title, 80))
	}
	return fmt.Sprintf("run %s failed at %s", run.ID, now.Format(time.RFC3339))
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func stringPointer(value string) *string {
	return &value
}

var ErrNoStore = errors.New("an evaluator requires a store")
