package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareDataDirCreatesLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := prepareDataDir(dir); err != nil {
		t.Fatalf("prepareDataDir: %v", err)
	}
	for _, sub := range []string{"", "bifrost", "cliproxyapi"} {
		p := filepath.Join(dir, sub)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: dir=%v mode=%o, want dir 700", p, fi.IsDir(), fi.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("write probe left files behind: %v", entries)
	}
}

func TestPrepareDataDirRejectsReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a 0500 directory")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // G302: a directory needs its x bit.
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // G302: a directory needs its x bit.

	err := prepareDataDir(dir)
	if err == nil {
		t.Fatal("prepareDataDir succeeded on a read-only directory")
	}
	if !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "not writable") {
		t.Errorf("error %q does not name %s as not writable", err, dir)
	}
}

func TestPrepareDataDirRejectsFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := prepareDataDir(dir)
	if err == nil || !strings.Contains(err.Error(), dir) {
		t.Errorf("err = %v, want an error naming %s", err, dir)
	}
}
