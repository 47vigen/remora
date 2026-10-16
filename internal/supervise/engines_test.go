package supervise

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/47vigen/remora/internal/config"
)

var testCreds = Credentials{
	BifrostAdminUser:         "remora",
	BifrostAdminPassword:     "bifrost-admin-password",
	BifrostEncryptionKey:     "bifrost-encryption-key",
	CLIProxyManagementSecret: "management-secret",
	CLIProxyAPIKey:           "client-api-key",
}

func testConfig(dir string) config.Config {
	return config.Config{
		DataDir:   dir,
		EngineDir: "/e",
		LogLevel:  slog.LevelWarn,
		Ports:     config.Ports{Bifrost: 18080, CLIProxyAPI: 18317, Soniox: 18090},
	}
}

func checkFileMode(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("%s mode = %o, want 600", path, got)
	}
}

// checkGoMemLimit: want "" means no GOMEMLIMIT entry at all.
func checkGoMemLimit(t *testing.T, env []string, want string) {
	t.Helper()
	var got []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOMEMLIMIT=") {
			got = append(got, kv)
		}
	}
	if want == "" && len(got) != 0 || want != "" && !slices.Equal(got, []string{want}) {
		t.Errorf("GOMEMLIMIT entries = %q, want %q", got, want)
	}
}

func checkNoRemoraEnv(t *testing.T, env []string) {
	t.Helper()
	for _, kv := range env {
		if strings.HasPrefix(kv, "REMORA_") {
			t.Errorf("child env leaks %q", kv)
		}
	}
}

func TestBifrostEngine(t *testing.T) {
	dir := t.TempDir()
	e, err := BifrostEngine(testConfig(dir), testCreds)
	if err != nil {
		t.Fatalf("BifrostEngine: %v", err)
	}
	if e.Name != "bifrost" {
		t.Errorf("Name = %q", e.Name)
	}
	if e.Bin != "/e/bifrost" {
		t.Errorf("Bin = %q", e.Bin)
	}
	if e.HealthURL != "http://127.0.0.1:18080/health" {
		t.Errorf("HealthURL = %q", e.HealthURL)
	}
	wantArgs := []string{"-app-dir", dir + "/bifrost", "-host", "127.0.0.1", "-port", "18080", "-log-style", "json", "-log-level", "warn"}
	if !slices.Equal(e.Args, wantArgs) {
		t.Errorf("Args = %q, want %q", e.Args, wantArgs)
	}
	if !slices.Contains(e.Env, "HOME="+dir+"/bifrost") {
		t.Errorf("Env = %q, want HOME", e.Env)
	}
	checkNoRemoraEnv(t, e.Env)
	checkGoMemLimit(t, e.Env, "")

	path := filepath.Join(dir, "bifrost", "config.json")
	checkFileMode(t, path)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	type store struct {
		Enabled bool   `json:"enabled"`
		Type    string `json:"type"`
		Config  struct {
			Path string `json:"path"`
		} `json:"config"`
	}
	var doc struct {
		EncryptionKey string `json:"encryption_key"`
		ConfigStore   store  `json:"config_store"`
		LogsStore     store  `json:"logs_store"`
		Governance    struct {
			AuthConfig struct {
				AdminUsername string `json:"admin_username"`
				AdminPassword string `json:"admin_password"`
				IsEnabled     bool   `json:"is_enabled"`
			} `json:"auth_config"`
		} `json:"governance"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode config.json: %v", err)
	}
	if doc.EncryptionKey != testCreds.BifrostEncryptionKey {
		t.Errorf("encryption_key = %q", doc.EncryptionKey)
	}
	for name, st := range map[string]struct {
		got  store
		path string
	}{
		"config_store": {doc.ConfigStore, dir + "/bifrost/config.db"},
		"logs_store":   {doc.LogsStore, dir + "/bifrost/logs.db"},
	} {
		if !st.got.Enabled || st.got.Type != "sqlite" || st.got.Config.Path != st.path {
			t.Errorf("%s = %+v, want enabled sqlite at %s", name, st.got, st.path)
		}
	}
	ac := doc.Governance.AuthConfig
	if ac.AdminUsername != "remora" || ac.AdminPassword != testCreds.BifrostAdminPassword || !ac.IsEnabled {
		t.Errorf("governance.auth_config = %+v", ac)
	}

	cfg := testConfig(dir)
	cfg.MemoryLimit = 4 << 30
	if e, err = BifrostEngine(cfg, testCreds); err != nil {
		t.Fatalf("BifrostEngine: %v", err)
	}
	checkGoMemLimit(t, e.Env, "GOMEMLIMIT=2147483648")
}

type cliproxyDoc struct {
	ConfigVersion int `yaml:"config-version"`
	Server        struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"server"`
	Management struct {
		AllowRemote            bool   `yaml:"allow-remote"`
		SecretKey              string `yaml:"secret-key"`
		DisableControlPanel    bool   `yaml:"disable-control-panel"`
		DisableAutoUpdatePanel bool   `yaml:"disable-auto-update-panel"`
	} `yaml:"management"`
	Access struct {
		APIKeys []string `yaml:"api-keys"`
	} `yaml:"access"`
	OAuth struct {
		AuthDir string `yaml:"auth-dir"`
	} `yaml:"oauth"`
	Plugins struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"plugins"`
}

func readCLIProxyConfig(t *testing.T, path string) cliproxyDoc {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	var doc cliproxyDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode config.yaml: %v", err)
	}
	return doc
}

func TestCLIProxyAPIEngine(t *testing.T) {
	dir := t.TempDir()
	e, err := CLIProxyAPIEngine(testConfig(dir), testCreds)
	if err != nil {
		t.Fatalf("CLIProxyAPIEngine: %v", err)
	}
	if e.Name != "cliproxyapi" {
		t.Errorf("Name = %q", e.Name)
	}
	if e.Bin != "/e/cliproxyapi" {
		t.Errorf("Bin = %q", e.Bin)
	}
	path := dir + "/cliproxyapi/config.yaml"
	if want := []string{"-config", path}; !slices.Equal(e.Args, want) {
		t.Errorf("Args = %q, want %q", e.Args, want)
	}
	if e.HealthURL != "http://127.0.0.1:18317/healthz" {
		t.Errorf("HealthURL = %q", e.HealthURL)
	}
	for _, want := range []string{"WRITABLE_PATH=" + dir + "/cliproxyapi", "HOME=" + dir + "/cliproxyapi"} {
		if !slices.Contains(e.Env, want) {
			t.Errorf("Env = %q, want %q", e.Env, want)
		}
	}
	checkNoRemoraEnv(t, e.Env)
	checkGoMemLimit(t, e.Env, "")

	checkFileMode(t, path)
	doc := readCLIProxyConfig(t, path)
	if doc.ConfigVersion != 8 {
		t.Errorf("config-version = %d", doc.ConfigVersion)
	}
	if doc.Server.Host != "127.0.0.1" || doc.Server.Port != 18317 {
		t.Errorf("server = %+v", doc.Server)
	}
	m := doc.Management
	if m.AllowRemote || !m.DisableControlPanel || !m.DisableAutoUpdatePanel || m.SecretKey != testCreds.CLIProxyManagementSecret {
		t.Errorf("management = %+v", m)
	}
	if !slices.Equal(doc.Access.APIKeys, []string{testCreds.CLIProxyAPIKey}) {
		t.Errorf("access.api-keys = %q", doc.Access.APIKeys)
	}
	if doc.OAuth.AuthDir != dir+"/cliproxyapi/auths" {
		t.Errorf("oauth.auth-dir = %q", doc.OAuth.AuthDir)
	}
	if doc.Plugins.Enabled {
		t.Error("plugins.enabled = true")
	}

	cfg := testConfig(dir)
	cfg.MemoryLimit = 4 << 30
	if e, err = CLIProxyAPIEngine(cfg, testCreds); err != nil {
		t.Fatalf("CLIProxyAPIEngine: %v", err)
	}
	checkGoMemLimit(t, e.Env, "GOMEMLIMIT=644245094")
}

func TestRenderOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir)
	if _, err := CLIProxyAPIEngine(cfg, testCreds); err != nil {
		t.Fatal(err)
	}
	cfg.Ports.CLIProxyAPI = 28317
	if _, err := CLIProxyAPIEngine(cfg, testCreds); err != nil {
		t.Fatal(err)
	}
	if got := readCLIProxyConfig(t, dir+"/cliproxyapi/config.yaml").Server.Port; got != 28317 {
		t.Errorf("server.port = %d, want 28317", got)
	}
	entries, err := os.ReadDir(dir + "/cliproxyapi")
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), ".tmp") || strings.Contains(en.Name(), ".tmp") {
			t.Errorf("leftover temp file %s", en.Name())
		}
	}
}
