package supervise

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/47vigen/remora/internal/config"
)

// The OAuth engine's config.yaml in its v8 layout, limited to the keys remora sets.
type cliproxyConfig struct {
	ConfigVersion int                `yaml:"config-version"`
	Server        cliproxyServer     `yaml:"server"`
	Management    cliproxyManagement `yaml:"management"`
	Access        cliproxyAccess     `yaml:"access"`
	OAuth         cliproxyOAuth      `yaml:"oauth"`
	Plugins       cliproxyPlugins    `yaml:"plugins"`
}

type cliproxyServer struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type cliproxyManagement struct {
	AllowRemote            bool   `yaml:"allow-remote"`
	SecretKey              string `yaml:"secret-key"`
	DisableControlPanel    bool   `yaml:"disable-control-panel"`
	DisableAutoUpdatePanel bool   `yaml:"disable-auto-update-panel"`
}

type cliproxyAccess struct {
	APIKeys []string `yaml:"api-keys"`
}

type cliproxyOAuth struct {
	AuthDir string `yaml:"auth-dir"`
}

type cliproxyPlugins struct {
	Enabled bool `yaml:"enabled"`
}

// CLIProxyAPIEngine renders $DATA/cliproxyapi/config.yaml (0600) and returns the OAuth engine's Engine. It renders
// on every boot: the engine has no port flag, so its port lives only in this file. The engine rewrites the
// plaintext secret-key as a bcrypt hash on load; rendering the plaintext again is harmless.
func CLIProxyAPIEngine(cfg config.Config, c Credentials) (Engine, error) {
	dir := filepath.Join(cfg.DataDir, "cliproxyapi")
	authDir := filepath.Join(dir, "auths")
	path := filepath.Join(dir, "config.yaml")
	doc := cliproxyConfig{
		ConfigVersion: 8,
		Server:        cliproxyServer{Host: "127.0.0.1", Port: cfg.Ports.CLIProxyAPI},
		Management: cliproxyManagement{
			AllowRemote:            false,
			SecretKey:              c.CLIProxyManagementSecret,
			DisableControlPanel:    true,
			DisableAutoUpdatePanel: true,
		},
		Access:  cliproxyAccess{APIKeys: []string{c.CLIProxyAPIKey}},
		OAuth:   cliproxyOAuth{AuthDir: authDir},
		Plugins: cliproxyPlugins{Enabled: false},
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return Engine{}, fmt.Errorf("supervise: encode cliproxyapi config: %w", err)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return Engine{}, fmt.Errorf("supervise: render cliproxyapi config: %w", err)
	}
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		return Engine{}, fmt.Errorf("supervise: create %s: %w", authDir, err)
	}

	return Engine{
		Name:      "cliproxyapi",
		Bin:       filepath.Join(cfg.EngineDir, "cliproxyapi"),
		Args:      []string{"-config", path},
		Env:       []string{childPath, "HOME=" + dir, "WRITABLE_PATH=" + dir},
		HealthURL: "http://127.0.0.1:" + strconv.Itoa(cfg.Ports.CLIProxyAPI) + "/healthz",
	}, nil
}
