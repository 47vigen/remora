package supervise

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLoadVersions(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		content *string
		want    map[string]EngineVersion
		wantErr error
	}{
		{
			name:    "both entries",
			content: ptr(`{"bifrost":{"version":"transports/v2.3.0","sha256":"ab"},"cliproxyapi":{"version":"v8.0.28","sha256":"cd"}}`),
			want: map[string]EngineVersion{
				"bifrost":     {Version: "transports/v2.3.0", SHA256: "ab"},
				"cliproxyapi": {Version: "v8.0.28", SHA256: "cd"},
			},
		},
		{name: "missing file", wantErr: fs.ErrNotExist},
		{name: "invalid json", content: ptr(`{"bifrost":`)},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, "versions"+strconv.Itoa(i)+".json")
			if tt.content != nil {
				if err := os.WriteFile(path, []byte(*tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := LoadVersions(path)
			if tt.want == nil {
				if err == nil {
					t.Fatalf("LoadVersions = %v, want error", got)
				}
				if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadVersions: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Fatalf("got[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestVerifyBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "engine")
	content := []byte("engine binary")
	if err := os.WriteFile(bin, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	good := hex.EncodeToString(sum[:])

	tests := []struct {
		name    string
		bin     string
		sha     string
		wantErr error
	}{
		{name: "missing path", bin: filepath.Join(dir, "nope"), sha: good, wantErr: ErrEngineMissing},
		{name: "directory", bin: dir, sha: good, wantErr: ErrEngineMissing},
		{name: "wrong sha", bin: bin, sha: "00", wantErr: ErrVersionMismatch},
		{name: "empty sha", bin: bin, sha: "", wantErr: ErrVersionMismatch},
		{name: "matching sha", bin: bin, sha: good},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyBinary(tt.bin, EngineVersion{Version: "v1", SHA256: tt.sha})
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("VerifyBinary: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
