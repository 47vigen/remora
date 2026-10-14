package store

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustSealer(t *testing.T, master []byte) *Sealer {
	t.Helper()
	sl, err := NewSealer(master)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return sl
}

func TestSealer(t *testing.T) {
	sl := mustSealer(t, bytes.Repeat([]byte{1}, 32))
	ct, err := sl.Seal("test-upstream-key", "provider_key:k1")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(ct, []byte("test-upstream-key")) {
		t.Error("sealed value contains the plaintext")
	}
	got, err := sl.Open(ct, "provider_key:k1")
	if got != "test-upstream-key" || err != nil {
		t.Errorf("Open = (%q, %v), want (test-upstream-key, nil)", got, err)
	}
	if _, err := sl.Open(ct, "provider_key:k2"); err == nil {
		t.Error("Open with another row's AAD succeeded")
	}
	if _, err := mustSealer(t, bytes.Repeat([]byte{2}, 32)).Open(ct, "provider_key:k1"); err == nil {
		t.Error("Open with another master key succeeded")
	}
	if _, err := sl.Open(ct[:5], "provider_key:k1"); err == nil {
		t.Error("Open of a truncated value succeeded")
	}
	ct2, err := sl.Seal("test-upstream-key", "provider_key:k1")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(ct, ct2) {
		t.Error("two seals of the same value are equal; the nonce is not random")
	}

	m1, m2 := sl.MAC([]byte("a")), sl.MAC([]byte("a"))
	if !bytes.Equal(m1, m2) || len(m1) != 32 {
		t.Errorf("MAC not stable or not 32 bytes: %x %x", m1, m2)
	}
	if other := mustSealer(t, bytes.Repeat([]byte{2}, 32)).MAC([]byte("a")); bytes.Equal(m1, other) {
		t.Error("MAC equal under two master keys")
	}

	if _, err := NewSealer(make([]byte, 16)); err == nil {
		t.Error("NewSealer accepted a 16-byte master key")
	}
}

func TestLoadMasterKey(t *testing.T) {
	t.Run("generated file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "data")
		k1, err := LoadMasterKey(dir, "")
		if err != nil {
			t.Fatalf("LoadMasterKey: %v", err)
		}
		if len(k1) != 32 {
			t.Errorf("len = %d, want 32", len(k1))
		}
		fi, err := os.Stat(filepath.Join(dir, "master.key"))
		if err != nil {
			t.Fatalf("stat master.key: %v", err)
		}
		if fi.Mode().Perm() != 0o600 || fi.Size() != 32 {
			t.Errorf("master.key mode %v size %d, want 0600 and 32", fi.Mode().Perm(), fi.Size())
		}
		k2, err := LoadMasterKey(dir, "")
		if err != nil || !bytes.Equal(k1, k2) {
			t.Errorf("second LoadMasterKey = (%x, %v), want the same key", k2, err)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 {
			t.Errorf("data dir has %d entries, want only master.key", len(entries))
		}
	})
	t.Run("env wins", func(t *testing.T) {
		dir := t.TempDir()
		want := bytes.Repeat([]byte{7}, 32)
		got, err := LoadMasterKey(dir, base64.StdEncoding.EncodeToString(want))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("LoadMasterKey = (%x, %v), want (%x, nil)", got, err, want)
		}
		if _, err := os.Stat(filepath.Join(dir, "master.key")); !os.IsNotExist(err) {
			t.Errorf("master.key created although the env value was set (stat err %v)", err)
		}
	})
	t.Run("env wrong length", func(t *testing.T) {
		_, err := LoadMasterKey(t.TempDir(), base64.StdEncoding.EncodeToString(make([]byte, 16)))
		if err == nil || !strings.Contains(err.Error(), "REMORA_MASTER_KEY") {
			t.Errorf("err = %v, want an error naming REMORA_MASTER_KEY", err)
		}
	})
	t.Run("file wrong length", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "master.key"), []byte("short"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadMasterKey(dir, ""); err == nil {
			t.Error("LoadMasterKey accepted a short master.key")
		}
	})
}
