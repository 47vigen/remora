package quota

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

// versionCache knows the Antigravity client version the engine reports: read from the public manifest at most once
// per antigravityVersionTTL, the last good one on failure, else the floor.
type versionCache struct {
	get func(ctx context.Context) (string, error)
	now func() time.Time

	mu      sync.Mutex
	version string
	read    time.Time
	tried   bool
}

func newVersionCache(hc *http.Client, manifestURL string) *versionCache {
	return &versionCache{now: time.Now, get: func(ctx context.Context) (string, error) {
		return readManifest(ctx, hc, manifestURL)
	}}
}

func (v *versionCache) userAgent(ctx context.Context) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if now := v.now(); !v.tried || now.Sub(v.read) >= antigravityVersionTTL {
		v.tried, v.read = true, now
		if got, err := v.get(ctx); err == nil {
			v.version = got
		}
	}
	version := v.version
	if version == "" {
		version = antigravityVersionFloor
	}
	return "antigravity/hub/" + version + " " + antigravityPlatform
}

func readManifest(ctx context.Context, hc *http.Client, manifestURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("manifest: %d", resp.StatusCode)
	}
	var m struct {
		Version string `yaml:"version"`
	}
	if err := yaml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return "", err
	}
	if m.Version == "" {
		return "", fmt.Errorf("manifest: no version")
	}
	return m.Version, nil
}
