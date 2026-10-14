// Package supervise starts the engines as child processes, checks them against versions.json, waits for their
// health endpoints, restarts them when they crash and stops them on shutdown.
package supervise

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// Errors returned when an engine binary cannot be verified or started.
var (
	ErrEngineMissing   = errors.New("supervise: engine binary missing")
	ErrVersionMismatch = errors.New("supervise: engine binary does not match versions.json")
	ErrUnhealthy       = errors.New("supervise: engine did not become healthy")
)

// EngineVersion is one entry of versions.json: the release an engine binary was built from and its checksum.
type EngineVersion struct {
	Version string `json:"version"` // the release tag, e.g. "transports/v2.3.0"
	SHA256  string `json:"sha256"`  // hex of the binary built into the image
}

// LoadVersions reads versions.json, a JSON object keyed by engine name.
func LoadVersions(path string) (map[string]EngineVersion, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- path is the operator-configured engine directory
	if err != nil {
		return nil, fmt.Errorf("supervise: read versions: %w", err)
	}
	var v map[string]EngineVersion
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("supervise: parse versions %s: %w", path, err)
	}
	return v, nil
}

// VerifyBinary checks that bin exists and that its sha256 equals want.SHA256. It returns an error wrapping
// ErrEngineMissing or ErrVersionMismatch.
func VerifyBinary(bin string, want EngineVersion) error {
	f, err := os.Open(bin) // #nosec G304 -- path is the operator-configured engine directory
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s: %w", ErrEngineMissing, bin, err)
		}
		return fmt.Errorf("supervise: open %s: %w", bin, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("supervise: stat %s: %w", bin, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrEngineMissing, bin)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("supervise: hash %s: %w", bin, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if want.SHA256 == "" || !strings.EqualFold(got, want.SHA256) {
		return fmt.Errorf("%w: %s has sha256 %s, want %q (%s)", ErrVersionMismatch, bin, got, want.SHA256, want.Version)
	}
	return nil
}
