// Command remora is an LLM gateway that supervises its engines behind one port.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const usage = "usage: remora <serve|export|apply|import|mcp|version|healthcheck>"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run dispatches a subcommand and returns the process exit code.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return runServe(ctx, getenv, stderr)
	case "export":
		return exitCode(stderr, "export", runExport(ctx, args[1:], stdout))
	case "apply":
		return exitCode(stderr, "apply", runApply(ctx, args[1:], stdout, lookup(getenv)))
	case "import":
		return exitCode(stderr, "import", runImport(ctx, args[1:], stdout, lookup(getenv)))
	case "mcp":
		return exitCode(stderr, "mcp", runMCP(ctx, args[1:], os.Stdin, os.Stdout, lookup(getenv)))
	case "version":
		return runVersion(stdout)
	case "healthcheck":
		return runHealthcheck(ctx, args[1:], getenv, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n%s\n", args[0], usage)
		return 2
	}
}

// lookup adapts getenv to the commands' env lookup: an empty variable counts as unset.
func lookup(getenv func(string) string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v := getenv(k)
		return v, v != ""
	}
}

// exitCode reports a command's error on stderr and returns the exit code: 0 after -h or --help, else 1. The error
// can carry an engine's own words; the operator sees remora's.
func exitCode(stderr io.Writer, cmd string, err error) int {
	switch {
	case err == nil, errors.Is(err, errHelp):
		return 0
	default:
		_, _ = fmt.Fprintf(scrubWriter{stderr}, "remora %s: %v\n", cmd, err)
		return 1
	}
}
