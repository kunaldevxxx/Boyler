package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// execInfo contains validated arguments passed to a runtime operation.
type execInfo struct {
	binaryPath string
	id         string
	bundlePath string
	sigNum     string
	TTE        int
}

type commandDefinition struct {
	usage string
	parse func(binaryPath string, args []string) (*execInfo, error)
	run   func(*execInfo) error
}

type invocation struct {
	definition commandDefinition
	info       *execInfo
}

var containerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

var supportedSignals = map[string]string{
	"SIGKILL": "SIGKILL",
	"9":       "9",
	"SIGTERM": "SIGTERM",
	"15":      "15",
	"SIGINT":  "SIGINT",
	"2":       "2",
}

var commandDefinitions = map[string]commandDefinition{
	"create": {
		usage: "create <container-id> --bundle <path>",
		parse: parseBundleCommand,
		run:   execCreateContainer,
	},
	"init": {
		usage: "init <container-id> --bundle <path>",
		parse: parseBundleCommand,
		run:   execInitContainer,
	},
	"run": {
		usage: "run <container-id>",
		parse: parseIDCommand,
		run:   execRunContainer,
	},
	"state": {
		usage: "state <container-id>",
		parse: parseIDCommand,
		run:   execCheckStateContainer,
	},
	"kill": {
		usage: "kill <container-id> <signal> or kill <container-id> --signal <signal>",
		parse: parseKillCommand,
		run:   execKillContainer,
	},
	"stop": { // Backward-compatible alias for kill.
		usage: "stop <container-id> <signal> or stop <container-id> --signal <signal>",
		parse: parseKillCommand,
		run:   execKillContainer,
	},
	"delete": {
		usage: "delete <container-id>",
		parse: parseIDCommand,
		run:   execDeleteContainerRuntime,
	},
}

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "myrunc: %v\n\n%s\n", err, usage())
		os.Exit(1)
	}
}

func run(args []string) error {
	invocation, err := parseInvocation(args)
	if err != nil {
		return err
	}
	if err := invocation.definition.run(invocation.info); err != nil {
		return fmt.Errorf("command failed: %w", err)
	}
	return nil
}

func parseInvocation(args []string) (invocation, error) {
	if len(args) < 2 {
		return invocation{}, fmt.Errorf("command is required")
	}
	commandName := args[1]
	definition, ok := commandDefinitions[commandName]
	if !ok {
		return invocation{}, fmt.Errorf("unknown command %q", commandName)
	}
	info, err := definition.parse(args[0], args[2:])
	if err != nil {
		return invocation{}, fmt.Errorf("invalid %s arguments: %w; usage: myrunc %s", commandName, err, definition.usage)
	}
	return invocation{definition: definition, info: info}, nil
}

func parseBundleCommand(binaryPath string, args []string) (*execInfo, error) {
	id, flagArgs, err := splitContainerID(args)
	if err != nil {
		return nil, err
	}
	flags := newFlagSet("bundle")
	bundlePath := flags.String("bundle", "", "path to the OCI bundle")
	if err := flags.Parse(flagArgs); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(*bundlePath) == "" {
		return nil, fmt.Errorf("--bundle is required")
	}
	return &execInfo{binaryPath: binaryPath, id: id, bundlePath: *bundlePath}, nil
}

func parseIDCommand(_ string, args []string) (*execInfo, error) {
	id, flagArgs, err := splitContainerID(args)
	if err != nil {
		return nil, err
	}
	flags := newFlagSet("container")
	if err := flags.Parse(flagArgs); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	return &execInfo{id: id}, nil
}

func parseKillCommand(_ string, args []string) (*execInfo, error) {
	id, flagArgs, err := splitContainerID(args)
	if err != nil {
		return nil, err
	}
	flags := newFlagSet("kill")
	signalFlag := flags.String("signal", "", "signal name or number")
	if err := flags.Parse(flagArgs); err != nil {
		return nil, err
	}
	if flags.NArg() > 1 {
		return nil, fmt.Errorf("expected one signal, got %d arguments", flags.NArg())
	}
	signal := strings.TrimSpace(*signalFlag)
	if flags.NArg() == 1 {
		if signal != "" {
			return nil, fmt.Errorf("signal must be provided either positionally or via --signal")
		}
		signal = flags.Arg(0)
	}
	normalizedSignal, ok := normalizeSignal(signal)
	if !ok {
		return nil, fmt.Errorf("unsupported signal %q (allowed: SIGKILL/9, SIGTERM/15, SIGINT/2)", signal)
	}
	return &execInfo{id: id, sigNum: normalizedSignal}, nil
}

func splitContainerID(args []string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf("container ID is required")
	}
	id := args[0]
	if !containerIDPattern.MatchString(id) || id == "." || id == ".." {
		return "", nil, fmt.Errorf("invalid container ID %q", id)
	}
	return id, args[1:], nil
}

func normalizeSignal(signal string) (string, bool) {
	normalized, ok := supportedSignals[strings.ToUpper(signal)]
	return normalized, ok
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func usage() string {
	return "usage: myrunc <command> [arguments]\ncommands: create, run, state, kill, delete"
}
