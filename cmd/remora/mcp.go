package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/47vigen/remora/internal/mcp"
)

const mcpUsage = "usage: remora mcp [--url URL] [--token TOKEN]"

// runMCP implements `remora mcp`: remora's MCP tools over stdin and stdout for a local agent, calling the remora at
// --url or REMORA_URL with --token or REMORA_TOKEN. It returns when stdin closes or ctx ends. Nothing but protocol
// frames is written to stdout.
func runMCP(ctx context.Context, args []string, stdin io.ReadCloser, stdout io.WriteCloser, stderr io.Writer, env func(string) (string, bool)) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // stdout carries protocol frames only; a usage error is returned for stderr
	var conn connFlags
	conn.register(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stderr, mcpUsage)
			return errHelp
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(mcpUsage)
	}
	c, err := conn.dial(ctx, env)
	if err != nil {
		return err
	}
	return mcp.New(c).Run(ctx, &sdk.IOTransport{Reader: stdin, Writer: stdout})
}
