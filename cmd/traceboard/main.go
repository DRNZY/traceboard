package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"

	"traceboard/internal/buildinfo"
	"traceboard/internal/frontend"
)

var embeddedFrontend = frontend.FS()

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) int {
	env := &environment{stdout: os.Stdout, stderr: os.Stderr, stdin: os.Stdin}
	if value := os.Getenv("TRACEBOARD_CONFIG"); value != "" {
		env.configPath = value
	}
	for index, argument := range arguments {
		if argument == "--config" && index+1 < len(arguments) {
			env.configPath = arguments[index+1]
		}
	}
	return dispatch(env, arguments)
}

func dispatch(env *environment, arguments []string) int {
	if len(arguments) == 0 {
		printUsage(env.stderr)
		return 1
	}
	switch arguments[0] {
	case "help", "-h", "--help":
		printUsage(env.stdout)
		return 0
	case "version":
		if len(arguments) != 1 {
			return env.fail("usage: traceboard version")
		}
		if _, err := fs.Stat(embeddedFrontend, "index.html"); err != nil {
			return env.fail("the embedded dashboard is unavailable in this build")
		}
		printVersion(env.stdout)
		return 0
	case "start":
		return runStart(env, arguments[1:])
	case "status":
		return runStatus(env, arguments[1:])
	case "sources":
		return runSources(env, arguments[1:])
	case "configure":
		return runConfigure(env, arguments[1:])
	case "doctor":
		return runDoctor(env, arguments[1:])
	case "auth":
		return runAuth(env, arguments[1:])
	case "export":
		return runExport(env, arguments[1:])
	case "delete":
		return runDelete(env, arguments[1:])
	case "retention":
		return runRetention(env, arguments[1:])
	case "daemon":
		return runDaemon(env, arguments[1:])
	case "hook":
		return runHook(env, arguments[1:])
	default:
		return env.fail("unknown command: %s", arguments[0])
	}
}

func runAuth(env *environment, arguments []string) int {
	if len(arguments) == 0 {
		return env.fail("usage: traceboard auth rotate")
	}
	switch arguments[0] {
	case "rotate":
		return runAuthRotate(env, arguments[1:])
	default:
		return env.fail("unknown auth subcommand: %s", arguments[0])
	}
}

func printVersion(writer io.Writer) {
	printF(writer, "version: %s\ncommit: %s\nbuild_date: %s\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
}

func printF(writer io.Writer, format string, arguments ...any) {
	if writer == nil {
		return
	}
	_, _ = fmt.Fprintf(writer, format, arguments...)
}

func printUsage(writer io.Writer) {
	_, _ = io.WriteString(writer, usage)
}
