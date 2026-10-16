package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Listen != ":8080" {
		t.Errorf("Listen = %q", c.Listen)
	}
	if c.DataDir != "/data" {
		t.Errorf("DataDir = %q", c.DataDir)
	}
	if c.AdminPassword != "" {
		t.Errorf("AdminPassword = %q", c.AdminPassword)
	}
	if c.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v", c.LogLevel)
	}
	if c.EngineDir != "/usr/local/lib/remora/engines" {
		t.Errorf("EngineDir = %q", c.EngineDir)
	}
	if c.MasterKey != "" {
		t.Errorf("MasterKey = %q", c.MasterKey)
	}
	if want := (Ports{18080, 18317, 18090}); c.Ports != want {
		t.Errorf("Ports = %+v, want %+v", c.Ports, want)
	}
	if got := c.DBPath(); got != "/data/remora.db" {
		t.Errorf("DBPath = %q", got)
	}
}

func TestLoadOverrides(t *testing.T) {
	c, err := Load(envFrom(map[string]string{
		"REMORA_LISTEN":         "127.0.0.1:9000",
		"REMORA_DATA":           "/tmp/x",
		"REMORA_ADMIN_PASSWORD": "s3cret",
		"REMORA_LOG_LEVEL":      "debug",
		"REMORA_ENGINE_DIR":     "/opt/e",
		"REMORA_MASTER_KEY":     "a2V5",
		"REMORA_PORT_ROUTER":    "28080",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{
		Listen:        "127.0.0.1:9000",
		DataDir:       "/tmp/x",
		AdminPassword: "s3cret",
		LogLevel:      slog.LevelDebug,
		EngineDir:     "/opt/e",
		MasterKey:     "a2V5",
		Ports:         Ports{Bifrost: 28080, CLIProxyAPI: 18317, Soniox: 18090},
		Cooling:       Cooling{Count429: 3, Window429: time.Minute, Default: 5 * time.Minute, Poll: time.Second, SubscriptionDefault: 30 * time.Second},

		SonioxAddr:          "127.0.0.1:18090",
		SonioxBaseURL:       "https://api.soniox.com",
		SonioxPollInterval:  time.Second,
		SonioxDeadline:      30 * time.Minute,
		SonioxSweepInterval: 10 * time.Minute,
	}
	if c != want {
		t.Errorf("Load = %+v, want %+v", c, want)
	}
	if got := c.DBPath(); got != "/tmp/x/remora.db" {
		t.Errorf("DBPath = %q", got)
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"bad level", map[string]string{"REMORA_LOG_LEVEL": "loud"}, "REMORA_LOG_LEVEL"},
		{"port not a number", map[string]string{"REMORA_PORT_SPEECH": "x"}, "REMORA_PORT_SPEECH"},
		{"port out of range", map[string]string{"REMORA_PORT_ROUTER": "70000"}, "REMORA_PORT_ROUTER"},
		{"internal ports collide", map[string]string{"REMORA_PORT_SPEECH": "18080"}, "REMORA_PORT_SPEECH"},
		{"listen collides with internal", map[string]string{"REMORA_LISTEN": ":18317"}, "REMORA_LISTEN"},
		{"listen unparsable", map[string]string{"REMORA_LISTEN": "nonsense"}, "REMORA_LISTEN"},
		{"poll interval unparsable", map[string]string{"REMORA_SONIOX_POLL_INTERVAL": "soon"}, "REMORA_SONIOX_POLL_INTERVAL"},
		{"deadline not positive", map[string]string{"REMORA_SONIOX_DEADLINE": "0s"}, "REMORA_SONIOX_DEADLINE"},
		{"sweep interval negative", map[string]string{"REMORA_SONIOX_SWEEP_INTERVAL": "-1m"}, "REMORA_SONIOX_SWEEP_INTERVAL"},
		{"soniox base URL not http", map[string]string{"REMORA_SONIOX_BASE_URL": "ftp://x.test"}, "REMORA_SONIOX_BASE_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(envFrom(tt.env))
			if err == nil {
				t.Fatal("Load: want error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %q, want it to name %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfigSonioxDefaults(t *testing.T) {
	c, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SonioxAddr != "127.0.0.1:18090" {
		t.Errorf("SonioxAddr = %q", c.SonioxAddr)
	}
	if c.SonioxBaseURL != "https://api.soniox.com" {
		t.Errorf("SonioxBaseURL = %q", c.SonioxBaseURL)
	}
	if c.SonioxPollInterval != time.Second {
		t.Errorf("SonioxPollInterval = %v", c.SonioxPollInterval)
	}
	if c.SonioxDeadline != 30*time.Minute {
		t.Errorf("SonioxDeadline = %v", c.SonioxDeadline)
	}
	if c.SonioxSweepInterval != 10*time.Minute {
		t.Errorf("SonioxSweepInterval = %v", c.SonioxSweepInterval)
	}
}

func TestConfigSonioxOverrides(t *testing.T) {
	c, err := Load(envFrom(map[string]string{
		"REMORA_PORT_SPEECH":           "19000",
		"REMORA_SONIOX_DEADLINE":       "1h",
		"REMORA_SONIOX_BASE_URL":       "http://fakes:9090/u/sx",
		"REMORA_SONIOX_POLL_INTERVAL":  "500ms",
		"REMORA_SONIOX_SWEEP_INTERVAL": "2s",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SonioxAddr != "127.0.0.1:19000" {
		t.Errorf("SonioxAddr = %q", c.SonioxAddr)
	}
	if c.SonioxDeadline != time.Hour {
		t.Errorf("SonioxDeadline = %v", c.SonioxDeadline)
	}
	if c.SonioxBaseURL != "http://fakes:9090/u/sx" {
		t.Errorf("SonioxBaseURL = %q", c.SonioxBaseURL)
	}
	if c.SonioxPollInterval != 500*time.Millisecond {
		t.Errorf("SonioxPollInterval = %v", c.SonioxPollInterval)
	}
	if c.SonioxSweepInterval != 2*time.Second {
		t.Errorf("SonioxSweepInterval = %v", c.SonioxSweepInterval)
	}
}

func TestConfigPerformanceSettings(t *testing.T) {
	c, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.PprofAddr != "" || c.LogContent || c.Cooling.SubscriptionDefault != 30*time.Second {
		t.Errorf("defaults: PprofAddr=%q LogContent=%v SubscriptionDefault=%v", c.PprofAddr, c.LogContent,
			c.Cooling.SubscriptionDefault)
	}
	c, err = Load(envFrom(map[string]string{
		EnvPprofAddr: "127.0.0.1:6060", EnvLogContent: "true", EnvCoolingSubscriptionDefault: "10s",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.PprofAddr != "127.0.0.1:6060" || !c.LogContent || c.Cooling.SubscriptionDefault != 10*time.Second {
		t.Errorf("overrides: PprofAddr=%q LogContent=%v SubscriptionDefault=%v", c.PprofAddr, c.LogContent,
			c.Cooling.SubscriptionDefault)
	}
	for name, env := range map[string]map[string]string{
		"pprof on a public address": {EnvPprofAddr: "0.0.0.0:6060"},
		"pprof without a port":      {EnvPprofAddr: "localhost"},
		"log content not a bool":    {EnvLogContent: "maybe"},
	} {
		if _, err := Load(envFrom(env)); err == nil {
			t.Errorf("%s: Load succeeded, want an error", name)
		}
	}
}
