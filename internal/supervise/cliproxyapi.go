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
	Routing       cliproxyRouting    `yaml:"routing"`
	Requests      cliproxyRequests   `yaml:"requests"`
	Upstream      cliproxyUpstream   `yaml:"upstream"`
}

// cliproxyRouting, cliproxyRequests, cliproxyOAuth.Providers and cliproxyUpstream carry the settings
// accounts.Reconcile also writes through the management API (internal/accounts/reconcile.go). They are rendered here
// too because the engine answers HTTP shortly before its config watcher runs, so a management write right after boot
// can be saved but never applied; what is in the file at start is always loaded. force-model-prefix makes each
// credential answer only to "<prefix>/<model>".
type cliproxyRouting struct {
	Strategy         string           `yaml:"strategy"`
	ForceModelPrefix bool             `yaml:"force-model-prefix"`
	Retry            cliproxyRetry    `yaml:"retry"`
	Cooldown         cliproxyCooldown `yaml:"cooldown"`
}

type cliproxyRetry struct {
	RequestRetry     int `yaml:"request-retry"`
	MaxRetryInterval int `yaml:"max-retry-interval"`
}

type cliproxyCooldown struct {
	SaveCooldownStatus            bool `yaml:"save-cooldown-status"`
	TransientErrorCooldownSeconds int  `yaml:"transient-error-cooldown-seconds"`
}

type cliproxyRequests struct {
	Streaming cliproxyStreaming `yaml:"streaming"`
}

type cliproxyStreaming struct {
	BootstrapRetries int `yaml:"bootstrap-retries"`
	KeepaliveSeconds int `yaml:"keepalive-seconds"`
}

type cliproxyUpstream struct {
	Codex cliproxyCodex `yaml:"codex"`
}

type cliproxyCodex struct {
	ModelLevelCooling bool `yaml:"model-level-cooling"`
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
	AuthDir   string                 `yaml:"auth-dir"`
	Providers cliproxyOAuthProviders `yaml:"providers"`
}

type cliproxyOAuthProviders struct {
	Antigravity cliproxyAntigravity `yaml:"antigravity"`
}

type cliproxyAntigravity struct {
	ConnectionPool cliproxyConnectionPool `yaml:"connection-pool"`
}

type cliproxyConnectionPool struct {
	Enabled             bool   `yaml:"enabled"`
	IdleConnTimeout     string `yaml:"idle-conn-timeout"`
	MaxIdleConnsPerHost int    `yaml:"max-idle-conns-per-host"`
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
		Access: cliproxyAccess{APIKeys: []string{c.CLIProxyAPIKey}},
		OAuth: cliproxyOAuth{AuthDir: authDir, Providers: cliproxyOAuthProviders{Antigravity: cliproxyAntigravity{
			ConnectionPool: cliproxyConnectionPool{Enabled: true, IdleConnTimeout: "90s", MaxIdleConnsPerHost: 8},
		}}},
		Plugins: cliproxyPlugins{Enabled: false},
		Routing: cliproxyRouting{
			Strategy:         "round-robin",
			ForceModelPrefix: true,
			Retry:            cliproxyRetry{RequestRetry: 1, MaxRetryInterval: 10},
			Cooldown:         cliproxyCooldown{SaveCooldownStatus: true, TransientErrorCooldownSeconds: 5},
		},
		Requests: cliproxyRequests{Streaming: cliproxyStreaming{BootstrapRetries: 1, KeepaliveSeconds: 15}},
		Upstream: cliproxyUpstream{Codex: cliproxyCodex{ModelLevelCooling: true}},
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
		Env:       append([]string{childPath, "HOME=" + dir, "WRITABLE_PATH=" + dir}, GoMemLimitEnv(cfg.MemoryLimit, MemoryShares.Accounts)...),
		HealthURL: "http://127.0.0.1:" + strconv.Itoa(cfg.Ports.CLIProxyAPI) + "/healthz",
	}, nil
}
