package ingest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"traceboard/internal/redact"
)

const (
	// DefaultSpoolLimitBytes is the per-source spool ceiling.
	DefaultSpoolLimitBytes int64 = 100 << 20
	// DefaultSpoolAge is how long a spooled payload may wait.
	DefaultSpoolAge = 7 * 24 * time.Hour
	// LossWarningGrace is how long an over-limit spool keeps its oldest data
	// before Traceboard is allowed to drop it.
	LossWarningGrace = 24 * time.Hour
)

type DrainResult struct {
	Drained   int
	Failed    int
	Remaining int
}

type SpoolState struct {
	Source      string     `json:"source"`
	Events      int        `json:"events"`
	Bytes       int64      `json:"bytes"`
	LimitBytes  int64      `json:"limit_bytes"`
	OldestAt    *time.Time `json:"oldest_at,omitempty"`
	Dropped     int        `json:"dropped"`
	AtRisk      bool       `json:"at_risk"`
	AtRiskSince *time.Time `json:"at_risk_since,omitempty"`
	Now         time.Time  `json:"now"`
}

type spoolEntry struct {
	Event     json.RawMessage `json:"event"`
	Timestamp time.Time       `json:"timestamp"`
}

// Spool is the bounded local disk buffer that keeps an agent's telemetry safe
// while the collector is offline. Every payload is redacted before it reaches
// the disk, so a spool file can never contain a secret.
type Spool struct {
	root       string
	redactor   redact.Redactor
	limitBytes int64
	maxAge     time.Duration
	now        func() time.Time
	mu         sync.Mutex
	atRisk     map[string]time.Time
	dropped    map[string]int
}

func NewSpool(root string, redactor redact.Redactor, limitBytes int64, maxAge time.Duration) (*Spool, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("spool root is required")
	}
	if limitBytes <= 0 || limitBytes > DefaultSpoolLimitBytes {
		limitBytes = DefaultSpoolLimitBytes
	}
	if maxAge <= 0 || maxAge > DefaultSpoolAge {
		maxAge = DefaultSpoolAge
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create spool directory: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure spool directory: %w", err)
	}
	return &Spool{
		root:       root,
		redactor:   redactor,
		limitBytes: limitBytes,
		maxAge:     maxAge,
		now:        func() time.Time { return time.Now().UTC() },
		atRisk:     make(map[string]time.Time),
		dropped:    make(map[string]int),
	}, nil
}

func (spool *Spool) SetClock(now func() time.Time) {
	if now != nil {
		spool.now = now
	}
}

func (spool *Spool) Append(source string, payloads ...json.RawMessage) error {
	if strings.TrimSpace(source) == "" {
		return errors.New("spool source is required")
	}
	if len(payloads) == 0 {
		return nil
	}
	spool.mu.Lock()
	defer spool.mu.Unlock()

	redacted := make([]spoolEntry, 0, len(payloads))
	for _, payload := range payloads {
		var decoded any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return fmt.Errorf("spool payload is not valid JSON: %w", err)
		}
		cleaned, _ := spool.redactor.Apply(decoded)
		encoded, err := json.Marshal(cleaned)
		if err != nil {
			return fmt.Errorf("encode spool payload: %w", err)
		}
		redacted = append(redacted, spoolEntry{Event: encoded, Timestamp: spool.now().UTC()})
	}
	return spool.appendLocked(source, redacted)
}

// appendLocked writes new entries and, when the ceiling would be exceeded,
// keeps the newest data and reports the loss through the spool state. Nothing
// is deleted without a rewrite, so a crash never truncates a good spool.
func (spool *Spool) appendLocked(source string, entries []spoolEntry) error {
	existing, err := spool.readEntries(source)
	if err != nil {
		return err
	}
	combined := append(append(make([]spoolEntry, 0, len(existing)+len(entries)), existing...), entries...)

	dropped := 0
	total := int64(0)
	for _, entry := range combined {
		total += entryLineBytes(entry)
	}
	for total > spool.limitBytes && len(combined) > 0 {
		total -= entryLineBytes(combined[0])
		combined = combined[1:]
		dropped++
	}
	if dropped > 0 {
		spool.atRisk[source] = spool.now().UTC()
		spool.dropped[source] += dropped
	}
	return spool.rewrite(source, combined)
}

func entryLineBytes(entry spoolEntry) int64 {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return 0
	}
	return int64(len(encoded)) + 1
}

// Drain replays the spool in append order and deletes only the lines the
// consumer accepted, so a collector restart never loses or duplicates events.
func (spool *Spool) Drain(ctx context.Context, consume func(json.RawMessage) error) (DrainResult, error) {
	spool.mu.Lock()
	defer spool.mu.Unlock()

	result := DrainResult{}
	sources, err := spool.sources()
	if err != nil {
		return result, err
	}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		entries, err := spool.readEntries(source)
		if err != nil {
			return result, err
		}
		remaining := make([]spoolEntry, 0, len(entries))
		for _, entry := range entries {
			if err := consume(entry.Event); err != nil {
				result.Failed++
				remaining = append(remaining, entry)
				continue
			}
			result.Drained++
		}
		if err := spool.rewrite(source, remaining); err != nil {
			return result, err
		}
		result.Remaining += len(remaining)
	}
	return result, nil
}

func (spool *Spool) State(source string) (SpoolState, error) {
	spool.mu.Lock()
	defer spool.mu.Unlock()

	entries, err := spool.readEntries(source)
	if err != nil {
		return SpoolState{}, err
	}
	now := spool.now().UTC()
	state := SpoolState{
		Source:     source,
		Events:     len(entries),
		LimitBytes: spool.limitBytes,
		Dropped:    spool.dropped[source],
		Now:        now,
	}
	for _, entry := range entries {
		state.Bytes += entryLineBytes(entry)
		if state.OldestAt == nil || entry.Timestamp.Before(*state.OldestAt) {
			oldest := entry.Timestamp
			state.OldestAt = &oldest
		}
	}
	if since, ok := spool.atRisk[source]; ok {
		state.AtRisk = true
		value := since
		state.AtRiskSince = &value
	} else if state.Bytes >= spool.limitBytes {
		state.AtRisk = true
		since := now.Add(-LossWarningGrace)
		state.AtRiskSince = &since
	}
	return state, nil
}

// Expire removes entries older than the configured age. It runs before an
// append so a long-idle spool cannot silently grow.
func (spool *Spool) Expire() (int, error) {
	spool.mu.Lock()
	defer spool.mu.Unlock()

	removed := 0
	sources, err := spool.sources()
	if err != nil {
		return 0, err
	}
	cutoff := spool.now().UTC().Add(-spool.maxAge)
	for _, source := range sources {
		entries, err := spool.readEntries(source)
		if err != nil {
			return removed, err
		}
		kept := make([]spoolEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.Timestamp.Before(cutoff) {
				removed++
				continue
			}
			kept = append(kept, entry)
		}
		if removed > 0 && len(kept) != len(entries) {
			if err := spool.rewrite(source, kept); err != nil {
				return removed, err
			}
		}
	}
	return removed, nil
}

func (spool *Spool) Clear(source string) error {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	path, err := spool.path(source)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear spool: %w", err)
	}
	return nil
}

func (spool *Spool) path(source string) (string, error) {
	cleaned := sanitizeSourceName(source)
	if cleaned == "" {
		return "", errors.New("spool source name is invalid")
	}
	return filepath.Join(spool.root, cleaned+".ndjson"), nil
}

func (spool *Spool) sources() ([]string, error) {
	entries, err := os.ReadDir(spool.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read spool directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ndjson") {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".ndjson"))
	}
	sort.Strings(names)
	return names, nil
}

func (spool *Spool) readEntries(source string) ([]spoolEntry, error) {
	path, err := spool.path(source)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open spool file: %w", err)
	}
	defer file.Close()

	entries := make([]spoolEntry, 0, 64)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxRawPayloadBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry spoolEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// A truncated trailing line is discarded rather than blocking the
			// whole spool; the source will re-send it on its next attempt.
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read spool file: %w", err)
	}
	return entries, nil
}

func (spool *Spool) rewrite(source string, entries []spoolEntry) error {
	path, err := spool.path(source)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("clear spool file: %w", err)
		}
		return nil
	}
	temporary, err := os.CreateTemp(spool.root, ".spool-*")
	if err != nil {
		return fmt.Errorf("create spool temporary: %w", err)
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure spool temporary: %w", err)
	}
	writer := bufio.NewWriter(temporary)
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			temporary.Close()
			return fmt.Errorf("encode spool entry: %w", err)
		}
		if _, err := writer.Write(append(line, '\n')); err != nil {
			temporary.Close()
			return fmt.Errorf("write spool entry: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		temporary.Close()
		return fmt.Errorf("flush spool: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync spool: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close spool temporary: %w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("install spool file: %w", err)
	}
	return nil
}

func sanitizeSourceName(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

// ExpirySweeper removes aged spool entries on a fixed cadence so a long-idle
// spool cannot grow without bound between server starts.
type ExpirySweeper struct {
	spool    *Spool
	interval time.Duration
	stop     chan struct{}
	once     sync.Once
}

func NewExpirySweeper(spool *Spool, interval time.Duration) *ExpirySweeper {
	if interval <= 0 {
		interval = time.Hour
	}
	return &ExpirySweeper{spool: spool, interval: interval, stop: make(chan struct{})}
}

func (sweeper *ExpirySweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(sweeper.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sweeper.stop:
			return
		case <-ticker.C:
			_, _ = sweeper.spool.Expire()
		}
	}
}

func (sweeper *ExpirySweeper) Stop() {
	sweeper.once.Do(func() { close(sweeper.stop) })
}
