package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: boyler-shim <command> [flags]")
		fmt.Fprintln(os.Stderr, "commands: start")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "start":
		if err := runStart(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "boyler-shim start: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "boyler-shim: unknown command %q\n", os.Args[1])
		os.Exit(1)
	}
}

func runStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	id := fs.String("id", "", "container ID (required)")
	bundle := fs.String("bundle", "", "absolute path to OCI bundle (required)")
	myrunc := fs.String("myrunc", "", "path to myrunc binary (required)")
	stateDir := fs.String("state-dir", "/var/run/boyler/shims", "root directory for shim state files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" || *bundle == "" || *myrunc == "" {
		return fmt.Errorf("--id, --bundle, and --myrunc are required")
	}

	mgr := newManager(*id, *bundle, *myrunc, *stateDir)
	srv := newServer(mgr)
	return srv.run()
}
