package omniroute

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// nodeVector was made once by Node's crypto with the test key "test-key-not-secret=" (never a real key):
//
//	node -e 'const c=require("crypto");const k=c.scryptSync("test-key-not-secret=","omniroute-field-encryption-v1",32);
//	  const iv=c.randomBytes(16);const x=c.createCipheriv("aes-256-gcm",k,iv);let e=x.update("hello سلام‌دنیا","utf8","hex");
//	  e+=x.final("hex");console.log(`enc:v1:${iv.toString("hex")}:${e}:${x.getAuthTag().toString("hex")}`)'
const (
	nodeKey    = "test-key-not-secret="
	nodeVector = "enc:v1:a69c124469c435ef10a89550191d6e60:f7c3a4616eb32a069a0e4bd6131948498f3841a99f04c2241d:b63aab9d96a683e7b9dc96c3256a7f14"
	nodePlain  = "hello سلام\u200cدنیا"
)

func TestDecryptNodeVector(t *testing.T) {
	got, err := NewDecrypter(nodeKey).Decrypt(nodeVector)
	if err != nil {
		t.Fatal(err)
	}
	if got != nodePlain {
		t.Fatalf("got % x, want % x", got, nodePlain)
	}
}

func TestDecryptLegacySalt(t *testing.T) {
	v := legacyEncrypt(t, "legacy-test-key", "legacy سلام\u200cدنیا")
	got, err := NewDecrypter("legacy-test-key").Decrypt(v)
	if err != nil {
		t.Fatal(err)
	}
	if got != "legacy سلام\u200cدنیا" {
		t.Fatalf("got %q", got)
	}
}

func TestDecryptPassthroughAndErrors(t *testing.T) {
	d := NewDecrypter(nodeKey)
	if got, err := d.Decrypt("plain"); err != nil || got != "plain" {
		t.Fatalf("plain: got %q, %v", got, err)
	}
	if _, err := NewDecrypter("another-test-key").Decrypt(nodeVector); !errors.Is(err, ErrUndecryptable) {
		t.Fatalf("wrong key: got %v, want ErrUndecryptable", err)
	}
	for _, v := range []string{"enc:v1:zz", "enc:v1:zz:00:00", "enc:v1:00:00:00", "enc:v1:" + nodeVector[7:] + ":00"} {
		if _, err := d.Decrypt(v); err == nil {
			t.Errorf("%q: want an error", v)
		}
	}
}

func TestLoadSecretTruncation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.env"), []byte("# comment\nSTORAGE_ENCRYPTION_KEY=\"abc=def=\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cands, err := LoadSecret(dir, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) < 2 || cands[0] != "abc=def=" || cands[1] != "abc" {
		t.Fatalf("got %q, want [abc=def= abc ...]", cands)
	}
	// What OmniRoute's own loader derives from a quoted value: split("=", 2)[1] keeps the opening quote.
	if !slices.Contains(cands, `"abc`) {
		t.Fatalf("got %q, want the loader's exact form %q too", cands, `"abc`)
	}

	env := func(k string) (string, bool) {
		if k == "STORAGE_ENCRYPTION_KEY" {
			return "from-env", true
		}
		return "", false
	}
	cands, err = LoadSecret(dir, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) == 0 || cands[0] != "from-env" || !slices.Contains(cands, "abc=def=") {
		t.Fatalf("got %q, want from-env first", cands)
	}

	// .env is read after server.env; an unquoted value has no quote variant.
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, ".env"), []byte("STORAGE_ENCRYPTION_KEY=xyz=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cands, err = LoadSecret(dir2, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cands, []string{"xyz=", "xyz"}) {
		t.Fatalf("got %q", cands)
	}
}

func TestPickSecret(t *testing.T) {
	probe := encrypt(t, "abc", "probe")
	d, err := PickSecret([]string{"abc=def=", "abc"}, probe)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := d.Decrypt(probe); err != nil || got != "probe" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := PickSecret([]string{"nope"}, probe); err == nil || err.Error() != "STORAGE_ENCRYPTION_KEY does not decrypt this database" {
		t.Fatalf("got %v", err)
	}
}
