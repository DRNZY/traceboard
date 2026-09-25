package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const systemdUnitName = "traceboard.service"

func runDaemon(env *environment, arguments []string) int {
	if len(arguments) == 0 {
		return env.fail("usage: traceboard daemon install | uninstall")
	}
	switch arguments[0] {
	case "install":
		return runDaemonInstall(env, arguments[1:])
	case "uninstall":
		return runDaemonUninstall(env, arguments[1:])
	default:
		return env.fail("unknown daemon subcommand: %s", arguments[0])
	}
}

func runDaemonInstall(env *environment, arguments []string) int {
	set := newFlagSet("daemon install", env)
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	unitDirectory, err := systemdUserUnitDirectory()
	if err != nil {
		return env.fail("%v", err)
	}
	binaryPath, err := os.Executable()
	if err != nil {
		return env.fail("could not resolve the binary path: %v", err)
	}
	absoluteBinary, err := filepath.Abs(binaryPath)
	if err != nil {
		return env.fail("could not resolve the binary path: %v", err)
	}
	unitPath := filepath.Join(unitDirectory, systemdUnitName)
	if err := os.MkdirAll(unitDirectory, 0o700); err != nil {
		return env.fail("could not create %s: %v", unitDirectory, err)
	}
	unit := fmt.Sprintf(`[Unit]
Description=Traceboard local agent observability collector
After=network.target

[Service]
Type=simple
ExecStart=%s start --no-browser
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%s

[Install]
WantedBy=default.target
`, absoluteBinary, filepath.Dir(env.resolveConfigPath()))

	if err := os.WriteFile(unitPath, []byte(unit), 0o600); err != nil {
		return env.fail("could not write %s: %v", unitPath, err)
	}
	if err := runUserSystemctl("daemon-reload"); err != nil {
		fmt.Fprintf(env.stderr, "warning: systemctl daemon-reload reported: %v\n", err)
	}
	if err := runUserSystemctl("enable", "--now", systemdUnitName); err != nil {
		return env.fail("could not enable the service: %v\nunit written to %s", err, unitPath)
	}
	fmt.Fprintf(env.stdout, "installed %s\n", unitPath)
	fmt.Fprintln(env.stdout, "the collector now starts with your user session; read the printed sign-in URL from journalctl --user -u traceboard")
	return 0
}

func runDaemonUninstall(env *environment, arguments []string) int {
	set := newFlagSet("daemon uninstall", env)
	if err := set.Parse(arguments); err != nil {
		return 2
	}
	unitDirectory, err := systemdUserUnitDirectory()
	if err != nil {
		return env.fail("%v", err)
	}
	unitPath := filepath.Join(unitDirectory, systemdUnitName)
	if _, err := os.Stat(unitPath); err != nil {
		return env.fail("no Traceboard unit is installed at %s", unitPath)
	}
	_ = runUserSystemctl("disable", "--now", systemdUnitName)
	if err := os.Remove(unitPath); err != nil {
		return env.fail("could not remove %s: %v", unitPath, err)
	}
	if err := runUserSystemctl("daemon-reload"); err != nil {
		fmt.Fprintf(env.stderr, "warning: systemctl daemon-reload reported: %v\n", err)
	}
	fmt.Fprintf(env.stdout, "removed %s\n", unitPath)
	return 0
}

func systemdUserUnitDirectory() (string, error) {
	if override := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); override != "" {
		return filepath.Join(override, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not resolve the home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func runUserSystemctl(arguments ...string) error {
	command := exec.Command("systemctl", append([]string{"--user"}, arguments...)...)
	command.Stdout = nil
	command.Stderr = nil
	return command.Run()
}

func nowUTC() time.Time {
	return time.Now().UTC()
}

var errNoSystemd = errors.New("systemctl --user is unavailable")
