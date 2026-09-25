package config

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"traceboard/internal/event"
)

func TestEnsureCreatesLoopbackDefaultsAndProtectedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "config.json")

	cfg, err := (Config{}).Ensure(path)
	if err != nil {
		t.Fatalf("ensure config: %v", err)
	}
	if cfg.ListenAddress != "127.0.0.1:47821" {
		t.Fatalf("listen address = %q", cfg.ListenAddress)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat config directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("config directory mode = %04o", got)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("config file mode = %04o", got)
	}

	for name, secret := range map[string]string{
		"ingest token":    cfg.IngestToken,
		"dashboard token": cfg.DashboardToken,
		"session secret":  cfg.SessionSecret,
	} {
		decoded, err := base64.RawURLEncoding.DecodeString(secret)
		if err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if len(decoded) < 32 {
			t.Fatalf("%s has %d random bytes", name, len(decoded))
		}
	}
}

func TestLoadNormalizesOmittedSourceCaptureMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, path, `{"listen_address":"127.0.0.1:47821","sources":{"opencode":{}}}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := cfg.Sources["opencode"].CaptureMode; got != event.CaptureMetadata {
		t.Fatalf("capture mode = %q", got)
	}
}

func TestLoadRejectsInvalidSourceCaptureMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, path, `{"listen_address":"127.0.0.1:47821","sources":{"opencode":{"capture_mode":"verbose"}}}`)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), `source "opencode"`) || !strings.Contains(err.Error(), "capture mode") {
		t.Fatalf("expected invalid source capture mode error, got %v", err)
	}
}

func TestLoadRejectsInsecureConfigFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, path, `{"listen_address":"127.0.0.1:47821"}`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod config file: %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "config file mode") {
		t.Fatalf("expected insecure config file mode error, got %v", err)
	}
}

func TestLoadRejectsInsecureConfigDirectoryMode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	writeTestFile(t, path, `{"listen_address":"127.0.0.1:47821"}`)
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatalf("chmod config directory: %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "config directory mode") {
		t.Fatalf("expected insecure config directory mode error, got %v", err)
	}
}

func TestLoadRejectsEmptyAndNullSourceCaptureModes(t *testing.T) {
	for name, value := range map[string]string{"empty": `""`, "null": `null`} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			writeTestFile(t, path, `{"listen_address":"127.0.0.1:47821","sources":{"opencode":{"capture_mode":`+value+`}}}`)

			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "capture mode") {
				t.Fatalf("expected invalid source capture mode error, got %v", err)
			}
		})
	}
}

func TestLoadRejectsNonLoopbackAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeTestFile(t, path, `{"listen_address":"0.0.0.0:47821"}`)

	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected loopback error, got %v", err)
	}
}

func TestEnsureRepairsExistingConfigPermissions(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	writeTestFile(t, path, `{"listen_address":"127.0.0.1:47821"}`)
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatalf("chmod config directory: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod config file: %v", err)
	}

	if _, err := (Config{}).Ensure(path); err != nil {
		t.Fatalf("ensure existing config: %v", err)
	}
	assertMode(t, directory, 0o700)
	assertMode(t, path, 0o600)
}

func TestEnsureReturnsRandomnessErrorWithoutConfig(t *testing.T) {
	originalReader := randomReader
	randomReader = failingReader{}
	t.Cleanup(func() { randomReader = originalReader })
	path := filepath.Join(t.TempDir(), "config", "config.json")

	if _, err := (Config{}).Ensure(path); err == nil {
		t.Fatal("expected randomness error")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config file exists after randomness failure: %v", err)
	}
}

func TestEnsureConcurrentCallsReturnSameConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "config.json")
	reader := &gatedReader{path: path, blocked: make(chan struct{})}
	originalReader := randomReader
	randomReader = reader
	t.Cleanup(func() { randomReader = originalReader })

	type result struct {
		config Config
		err    error
	}
	results := make(chan result, 2)
	go func() {
		cfg, err := (Config{}).Ensure(path)
		results <- result{config: cfg, err: err}
	}()
	<-reader.blocked
	go func() {
		cfg, err := (Config{}).Ensure(path)
		results <- result{config: cfg, err: err}
	}()

	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent ensure errors: first=%v second=%v", first.err, second.err)
	}
	if !reflect.DeepEqual(first.config, second.config) {
		t.Fatal("concurrent ensure calls returned different configs")
	}
}

func TestEnsureGeneratesDistinctCredentials(t *testing.T) {
	firstPath := filepath.Join(t.TempDir(), "first", "config.json")
	secondPath := filepath.Join(t.TempDir(), "second", "config.json")

	first, err := (Config{}).Ensure(firstPath)
	if err != nil {
		t.Fatalf("ensure first config: %v", err)
	}
	second, err := (Config{}).Ensure(secondPath)
	if err != nil {
		t.Fatalf("ensure second config: %v", err)
	}

	if first.IngestToken == second.IngestToken {
		t.Fatal("ingest tokens must differ")
	}
	if first.DashboardToken == second.DashboardToken {
		t.Fatal("dashboard tokens must differ")
	}
	if first.SessionSecret == second.SessionSecret {
		t.Fatal("session secrets must differ")
	}
}

func TestEnsurePreservesExistingFileWhenDecodingFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"listen_address":"127.0.0.1:47821","unknown":true}`)
	writeTestFile(t, path, string(original))

	if _, err := (Config{}).Ensure(path); err == nil {
		t.Fatal("expected unknown field error")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("config changed after decode failure: %q", got)
	}
}

type gatedReader struct {
	mu      sync.Mutex
	calls   int
	path    string
	blocked chan struct{}
}

func (reader *gatedReader) Read(data []byte) (int, error) {
	reader.mu.Lock()
	reader.calls++
	call := reader.calls
	reader.mu.Unlock()
	if call == 3 {
		close(reader.blocked)
		deadline := time.Now().Add(2 * time.Second)
		for {
			_, err := os.Stat(reader.path)
			if err == nil {
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return 0, err
			}
			if time.Now().After(deadline) {
				return 0, errors.New("config creation timed out")
			}
			time.Sleep(time.Millisecond)
		}
	}
	for index := range data {
		data[index] = byte(call)
	}
	return len(data), nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want.Perm() {
		t.Fatalf("mode for %s = %04o, want %04o", path, got, want.Perm())
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("chmod test directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}
