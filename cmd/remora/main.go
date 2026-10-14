// Command remora is an LLM gateway that supervises its engines behind one port.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const usage = "usage: remora <serve|apply|import|version|healthcheck>"

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
	case "apply":
		return runApply(ctx, args[1:], getenv, stdout, stderr)
	case "import":
		env := func(k string) (string, bool) { v := getenv(k); return v, v != "" }
		if err := runImport(ctx, args[1:], stdout, env); err != nil {
			_, _ = fmt.Fprintf(stderr, "remora import: %v\n", err)
			return 1
		}
		return 0
	case "version":
		return runVersion(stdout)
	case "healthcheck":
		return runHealthcheck(ctx, args[1:], getenv, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n%s\n", args[0], usage)
		return 2
	}
}
