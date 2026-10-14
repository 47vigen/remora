package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/47vigen/remora/internal/api/gen"
)

const exportUsage = "usage: remora export [-o FILE] [--url URL] [--token TOKEN]"

// runExport implements `remora export [-o FILE]`: it writes the running remora's configuration as remora.yaml, to
// FILE or stdout. Secrets are env:NAME references, never values.
func runExport(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), exportUsage)
		fs.PrintDefaults()
	}
	out := fs.String("o", "", "write the document to FILE instead of stdout")
	var conn connFlags
	conn.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(exportUsage)
	}

	c, err := conn.dial(ctx, os.LookupEnv)
	if err != nil {
		return err
	}
	resp, err := c.ExportConfig(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e gen.Error
		_ = json.Unmarshal(body, &e)
		return apiError("export", resp, &e)
	}
	if *out == "" {
		_, err := stdout.Write(body)
		return err
	}
	return writeFileAtomic(*out, body)
}

// writeFileAtomic writes b to path through a temporary file in the same directory, so a failed export never
// leaves a half-written remora.yaml.
func writeFileAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".remora-export-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
