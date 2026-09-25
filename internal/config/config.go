package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"traceboard/internal/event"
)

var randomReader io.Reader = rand.Reader

type Config struct {
	ListenAddress        string                  `json:"listen_address"`
	DatabasePath         string                  `json:"database_path"`
	IngestToken          string                  `json:"ingest_token"`
	DashboardToken       string                  `json:"dashboard_token"`
	SessionSecret        string                  `json:"session_secret"`
	RetentionDays        int                     `json:"retention_days"`
	KeepNewestPerProject int                     `json:"keep_newest_per_project"`
	HeartbeatSeconds     int                     `json:"heartbeat_seconds"`
	Sources              map[string]SourceConfig `json:"sources"`
}

type SourceConfig struct {
	CaptureMode event.CaptureMode `json:"capture_mode"`
}

func (source *SourceConfig) UnmarshalJSON(data []byte) error {
	var value struct {
		CaptureMode json.RawMessage `json:"capture_mode"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if len(value.CaptureMode) == 0 {
		source.CaptureMode = event.CaptureMetadata
		return nil
	}
	return json.Unmarshal(value.CaptureMode, &source.CaptureMode)
}

func Load(path string) (Config, error) {
	directoryInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return Config{}, err
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		return Config{}, fmt.Errorf("config directory mode must be 0700, got %04o", directoryInfo.Mode().Perm())
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Config{}, err
	}
	if info.Mode().Perm() != 0o600 {
		return Config{}, fmt.Errorf("config file mode must be 0600, got %04o", info.Mode().Perm())
	}

	var cfg Config
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("config must contain one JSON object")
		}
		return Config{}, err
	}
	if err := validateListenAddress(cfg.ListenAddress); err != nil {
		return Config{}, err
	}
	for name, source := range cfg.Sources {
		switch source.CaptureMode {
		case event.CaptureOff, event.CaptureMetadata, event.CaptureDetailed:
		default:
			return Config{}, fmt.Errorf("invalid capture mode %q for source %q", source.CaptureMode, name)
		}
		cfg.Sources[name] = source
	}
	return cfg, nil
}

func (Config) Ensure(path string) (Config, error) {
	if path == "" {
		return Config{}, errors.New("config path is required")
	}
	directory := filepath.Dir(path)
	if _, err := os.Stat(path); err == nil {
		if err := os.Chmod(directory, 0o700); err != nil {
			return Config{}, fmt.Errorf("secure config directory: %w", err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return Config{}, fmt.Errorf("secure config file: %w", err)
		}
		return Load(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}

	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Config{}, fmt.Errorf("create config directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return Config{}, fmt.Errorf("secure config directory: %w", err)
	}

	ingestToken, err := randomSecret()
	if err != nil {
		return Config{}, fmt.Errorf("generate ingest token: %w", err)
	}
	dashboardToken, err := randomSecret()
	if err != nil {
		return Config{}, fmt.Errorf("generate dashboard token: %w", err)
	}
	sessionSecret, err := randomSecret()
	if err != nil {
		return Config{}, fmt.Errorf("generate session secret: %w", err)
	}
	cfg := Config{
		ListenAddress:        defaultListenAddress(),
		DatabasePath:         filepath.Join(directory, "traceboard.db"),
		IngestToken:          ingestToken,
		DashboardToken:       dashboardToken,
		SessionSecret:        sessionSecret,
		RetentionDays:        30,
		KeepNewestPerProject: 0,
		HeartbeatSeconds:     30,
		Sources:              map[string]SourceConfig{},
	}
	return writeExclusive(path, cfg)
}

func validateListenAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("listen address must use a loopback IP")
	}
	return nil
}

func randomSecret() (string, error) {
	buffer := make([]byte, 32)
	if _, err := io.ReadFull(randomReader, buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func writeExclusive(path string, cfg Config) (Config, error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".traceboard-config-*")
	if err != nil {
		return Config{}, fmt.Errorf("create temporary config file: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return Config{}, fmt.Errorf("secure temporary config file: %w", err)
	}
	if err := json.NewEncoder(file).Encode(cfg); err != nil {
		_ = file.Close()
		return Config{}, fmt.Errorf("write temporary config file: %w", err)
	}
	if err := file.Close(); err != nil {
		return Config{}, fmt.Errorf("close temporary config file: %w", err)
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Load(path)
		}
		return Config{}, fmt.Errorf("install config file: %w", err)
	}
	return cfg, nil
}

// Ensure loads the configuration at path, creating it with protected
// permissions and generated credentials on first use.
func Ensure(path string) (Config, error) {
	return Config{}.Ensure(path)
}

const defaultAddress = "127.0.0.1:47821"

// defaultListenAddress honours TRACEBOARD_LISTEN so a second collector can run
// beside an existing one. The value is validated by the same loopback rule, so
// the override cannot open the process to another device.
func defaultListenAddress() string {
	override := strings.TrimSpace(os.Getenv("TRACEBOARD_LISTEN"))
	if override == "" {
		return defaultAddress
	}
	if err := validateListenAddress(override); err != nil {
		return defaultAddress
	}
	return override
}

// Save writes the configuration with user-only permissions, creating the parent
// directory if needed. It is used by `traceboard configure` and `retention set`.
func Save(path string, cfg Config) error {
	if path == "" {
		return errors.New("config path is required")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("secure config directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".traceboard-config-*")
	if err != nil {
		return fmt.Errorf("create temporary config file: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary config file: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(cfg); err != nil {
		temporary.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("install config: %w", err)
	}
	return os.Chmod(path, 0o600)
}

// SetSource records a capture mode for one source, creating it if needed.
func (cfg *Config) SetSource(source string, mode event.CaptureMode) {
	if cfg.Sources == nil {
		cfg.Sources = map[string]SourceConfig{}
	}
	cfg.Sources[source] = SourceConfig{CaptureMode: mode}
}
