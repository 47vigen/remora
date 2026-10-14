package config

import (
	"log/slog"
	"strings"
	"testing"
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
