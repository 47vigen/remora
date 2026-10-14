package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// prepareDataDir creates dir, dir/bifrost and dir/cliproxyapi (0700) and checks that each one is writable, so a
// volume the process cannot write fails at once instead of crash-looping the engines. Errors name the path.
func prepareDataDir(dir string) error {
	for _, p := range []string{dir, filepath.Join(dir, "bifrost"), filepath.Join(dir, "cliproxyapi")} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return fmt.Errorf("data directory %s is not writable: %w", p, err)
		}
		if err := probeWrite(p); err != nil {
			return fmt.Errorf("data directory %s is not writable: %w", p, err)
		}
	}
	return nil
}

// probeWrite creates and removes a temp file in dir.
func probeWrite(dir string) error {
	f, err := os.CreateTemp(dir, ".remora-write-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}
