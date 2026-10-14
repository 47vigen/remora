package supervise

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/47vigen/remora/internal/config"
)

// The front door's config.json, limited to the keys remora sets.
type bifrostConfig struct {
	EncryptionKey string            `json:"encryption_key"`
	ConfigStore   bifrostStore      `json:"config_store"`
	LogsStore     bifrostStore      `json:"logs_store"`
	Governance    bifrostGovernance `json:"governance"`
}

type bifrostStore struct {
	Enabled bool               `json:"enabled"`
	Type    string             `json:"type"`
	Config  bifrostStoreConfig `json:"config"`
}

type bifrostStoreConfig struct {
	Path string `json:"path"`
}

type bifrostGovernance struct {
	AuthConfig bifrostAuthConfig `json:"auth_config"`
}

type bifrostAuthConfig struct {
	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"`
	IsEnabled     bool   `json:"is_enabled"`
}

// BifrostEngine renders $DATA/bifrost/config.json (0600) and returns the front door's Engine. It renders on every
// boot: the file is a pure function of cfg and c.
func BifrostEngine(cfg config.Config, c Credentials) (Engine, error) {
	appDir := filepath.Join(cfg.DataDir, "bifrost")
	doc := bifrostConfig{
		EncryptionKey: c.BifrostEncryptionKey,
		ConfigStore:   sqliteStore(filepath.Join(appDir, "config.db")),
		LogsStore:     sqliteStore(filepath.Join(appDir, "logs.db")),
		Governance: bifrostGovernance{AuthConfig: bifrostAuthConfig{
			AdminUsername: c.BifrostAdminUser,
			AdminPassword: c.BifrostAdminPassword,
			IsEnabled:     true,
		}},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return Engine{}, fmt.Errorf("supervise: encode bifrost config: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(appDir, "config.json"), append(data, '\n')); err != nil {
		return Engine{}, fmt.Errorf("supervise: render bifrost config: %w", err)
	}

	port := strconv.Itoa(cfg.Ports.Bifrost)
	return Engine{
		Name: "bifrost",
		Bin:  filepath.Join(cfg.EngineDir, "bifrost"),
		Args: []string{
			"-app-dir", appDir,
			"-host", "127.0.0.1",
			"-port", port,
			"-log-style", "json",
			"-log-level", levelName(cfg.LogLevel),
		},
		Env:       []string{childPath, "HOME=" + appDir},
		HealthURL: "http://127.0.0.1:" + port + "/health",
	}, nil
}

func sqliteStore(path string) bifrostStore {
	return bifrostStore{Enabled: true, Type: "sqlite", Config: bifrostStoreConfig{Path: path}}
}
