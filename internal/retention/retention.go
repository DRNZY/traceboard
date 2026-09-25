// Package retention selects and deletes runs that have aged out. Active runs
// are exempt from every policy, and a preview always precedes a destructive
// automatic deletion.
package retention

import (
	"context"
	"fmt"
	"time"

	"traceboard/internal/store"
)

type Policy struct {
	// Days is zero to keep runs indefinitely.
	Days int
	// KeepNewestPerProject is zero to disable the per-project cap.
	KeepNewestPerProject int
}

func (policy Policy) IsNoop() bool {
	return policy.Days <= 0 && policy.KeepNewestPerProject <= 0
}

func (policy Policy) Describe() string {
	switch {
	case policy.Days > 0 && policy.KeepNewestPerProject > 0:
		return fmt.Sprintf("keep %d days and the newest %d runs per project", policy.Days, policy.KeepNewestPerProject)
	case policy.Days > 0:
		return fmt.Sprintf("keep %d days", policy.Days)
	case policy.KeepNewestPerProject > 0:
		return fmt.Sprintf("keep the newest %d runs per project", policy.KeepNewestPerProject)
	default:
		return "keep runs indefinitely"
	}
}

type DeletionPreview struct {
	Policy Policy
	Runs   []store.Run
	Events int
}

type DeletionResult struct {
	Policy  Policy
	Runs    int
	Events  int
	Steps   int
	Alerts  int
	Applied time.Time
}

type Store interface {
	SelectRetentionCandidates(ctx context.Context, policy store.RetentionPolicy, now time.Time) ([]store.Run, error)
	DeleteRun(ctx context.Context, runID string) (store.DeleteResult, error)
}

func Preview(ctx context.Context, database Store, policy Policy, now time.Time) (DeletionPreview, error) {
	preview := DeletionPreview{Policy: policy}
	if policy.IsNoop() {
		return preview, nil
	}
	runs, err := database.SelectRetentionCandidates(ctx, store.RetentionPolicy{
		Days:                 policy.Days,
		KeepNewestPerProject: policy.KeepNewestPerProject,
	}, now)
	if err != nil {
		return DeletionPreview{}, err
	}
	preview.Runs = runs
	for _, run := range runs {
		preview.Events += run.EventCount
	}
	return preview, nil
}

func Apply(ctx context.Context, database Store, policy Policy, now time.Time) (DeletionResult, error) {
	result := DeletionResult{Policy: policy, Applied: now}
	preview, err := Preview(ctx, database, policy, now)
	if err != nil {
		return DeletionResult{}, err
	}
	for _, run := range preview.Runs {
		deleted, err := database.DeleteRun(ctx, run.ID)
		if err != nil {
			return result, fmt.Errorf("delete run %s: %w", run.ID, err)
		}
		result.Runs++
		result.Events += deleted.Events
		result.Steps += deleted.Steps
		result.Alerts += deleted.Alerts
	}
	return result, nil
}

func ParseDuration(value string) (int, error) {
	if value == "always" || value == "infinite" || value == "none" {
		return 0, nil
	}
	if len(value) < 2 {
		return 0, fmt.Errorf("retention must look like 30d, 12h, or always")
	}
	unit := value[len(value)-1]
	amount := value[:len(value)-1]
	days, err := parsePositive(amount)
	if err != nil {
		return 0, err
	}
	switch unit {
	case 'd':
		return days, nil
	case 'w':
		return days * 7, nil
	case 'h':
		return max(1, days/24), nil
	default:
		return 0, fmt.Errorf("retention unit must be d, w, or h")
	}
}

func parsePositive(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("retention amount is required")
	}
	total := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("retention amount must be a whole number")
		}
		total = total*10 + int(r-'0')
		if total > 3650 {
			return 0, fmt.Errorf("retention must not exceed 3650 days")
		}
	}
	if total == 0 {
		return 0, fmt.Errorf("retention amount must be greater than zero")
	}
	return total, nil
}
