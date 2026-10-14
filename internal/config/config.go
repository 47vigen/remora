// Package config loads remora's runtime settings from the environment.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"time"
)

// Environment variables read by Load.
const (
	EnvListen        = "REMORA_LISTEN"
	EnvData          = "REMORA_DATA"
	EnvAdminPassword = "REMORA_ADMIN_PASSWORD" //nolint:gosec // G101: a variable name, not a credential.
	EnvLogLevel      = "REMORA_LOG_LEVEL"
	EnvEngineDir     = "REMORA_ENGINE_DIR"
	EnvMasterKey     = "REMORA_MASTER_KEY"
	EnvPortRouter    = "REMORA_PORT_ROUTER"
	EnvPortAccounts  = "REMORA_PORT_ACCOUNTS"
	EnvPortSpeech    = "REMORA_PORT_SPEECH"

	EnvSonioxBaseURL       = "REMORA_SONIOX_BASE_URL"
	EnvSonioxPollInterval  = "REMORA_SONIOX_POLL_INTERVAL"
	EnvSonioxDeadline      = "REMORA_SONIOX_DEADLINE"
	EnvSonioxSweepInterval = "REMORA_SONIOX_SWEEP_INTERVAL"
)

// Defaults applied when a variable is unset or empty.
const (
	DefaultListen       = ":8080"
	DefaultDataDir      = "/data"
	DefaultEngineDir    = "/usr/local/lib/remora/engines"
	DefaultPortRouter   = 18080
	DefaultPortAccounts = 18317
	DefaultPortSpeech   = 18090

	DefaultSonioxBaseURL       = "https://api.soniox.com"
	DefaultSonioxPollInterval  = time.Second
	DefaultSonioxDeadline      = 30 * time.Minute
	DefaultSonioxSweepInterval = 10 * time.Minute
)

// Ports are the loopback ports of the internal processes. The Go fields name
// the engines; the variables that set them are neutral.
type Ports struct {
	Bifrost     int // REMORA_PORT_ROUTER
	CLIProxyAPI int // REMORA_PORT_ACCOUNTS
	Soniox      int // REMORA_PORT_SPEECH
}

// Config is remora's runtime configuration.
type Config struct {
	Listen        string     // REMORA_LISTEN, default ":8080"
	DataDir       string     // REMORA_DATA, default "/data"
	AdminPassword string     // REMORA_ADMIN_PASSWORD, "" = generate at first boot
	LogLevel      slog.Level // REMORA_LOG_LEVEL, default info
	EngineDir     string     // REMORA_ENGINE_DIR, default "/usr/local/lib/remora/engines"
	MasterKey     string     // REMORA_MASTER_KEY, "" = $REMORA_DATA/master.key
	Ports         Ports      // REMORA_PORT_ROUTER, REMORA_PORT_ACCOUNTS, REMORA_PORT_SPEECH

	// The speech-to-text adapter.
	SonioxAddr          string        // "127.0.0.1:" + Ports.Soniox
	SonioxBaseURL       string        // REMORA_SONIOX_BASE_URL, default "https://api.soniox.com"
	SonioxPollInterval  time.Duration // REMORA_SONIOX_POLL_INTERVAL, default 1s
	SonioxDeadline      time.Duration // REMORA_SONIOX_DEADLINE, default 30m
	SonioxSweepInterval time.Duration // REMORA_SONIOX_SWEEP_INTERVAL, default 10m
}

// Load reads the configuration through getenv. Every error names its variable.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Listen:        orDefault(getenv(EnvListen), DefaultListen),
		DataDir:       orDefault(getenv(EnvData), DefaultDataDir),
		AdminPassword: getenv(EnvAdminPassword),
		LogLevel:      slog.LevelInfo,
		EngineDir:     orDefault(getenv(EnvEngineDir), DefaultEngineDir),
		MasterKey:     getenv(EnvMasterKey),
		SonioxBaseURL: orDefault(getenv(EnvSonioxBaseURL), DefaultSonioxBaseURL),
	}

	if v := getenv(EnvLogLevel); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return Config{}, fmt.Errorf("config: %s=%q: want debug, info, warn or error: %w", EnvLogLevel, v, err)
		}
	}

	var err error
	if c.Ports.Bifrost, err = port(getenv, EnvPortRouter, DefaultPortRouter); err != nil {
		return Config{}, err
	}
	if c.Ports.CLIProxyAPI, err = port(getenv, EnvPortAccounts, DefaultPortAccounts); err != nil {
		return Config{}, err
	}
	if c.Ports.Soniox, err = port(getenv, EnvPortSpeech, DefaultPortSpeech); err != nil {
		return Config{}, err
	}

	if err := c.checkPorts(); err != nil {
		return Config{}, err
	}
	c.SonioxAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(c.Ports.Soniox))

	if u, perr := url.Parse(c.SonioxBaseURL); perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, fmt.Errorf("config: %s=%q: want an http or https URL", EnvSonioxBaseURL, c.SonioxBaseURL)
	}
	if c.SonioxPollInterval, err = duration(getenv, EnvSonioxPollInterval, DefaultSonioxPollInterval); err != nil {
		return Config{}, err
	}
	if c.SonioxDeadline, err = duration(getenv, EnvSonioxDeadline, DefaultSonioxDeadline); err != nil {
		return Config{}, err
	}
	if c.SonioxSweepInterval, err = duration(getenv, EnvSonioxSweepInterval, DefaultSonioxSweepInterval); err != nil {
		return Config{}, err
	}
	return c, nil
}

// duration reads a positive Go duration such as "500ms" or "1h".
func duration(getenv func(string) string, env string, def time.Duration) (time.Duration, error) {
	v := getenv(env)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s=%q: want a duration such as 500ms or 1h: %w", env, v, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("config: %s=%q: must be positive", env, v)
	}
	return d, nil
}

// DBPath is the path of the SQLite store.
func (c Config) DBPath() string {
	return filepath.Join(c.DataDir, "remora.db")
}

// checkPorts rejects an unparsable listen address and any port used twice.
func (c Config) checkPorts() error {
	_, p, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("config: %s=%q: %w", EnvListen, c.Listen, err)
	}
	listenPort, err := parsePort(p)
	if err != nil {
		return fmt.Errorf("config: %s=%q: %w", EnvListen, c.Listen, err)
	}

	internal := []struct {
		env  string
		port int
	}{
		{EnvPortRouter, c.Ports.Bifrost},
		{EnvPortAccounts, c.Ports.CLIProxyAPI},
		{EnvPortSpeech, c.Ports.Soniox},
	}
	for i, a := range internal {
		for _, b := range internal[:i] {
			if a.port == b.port {
				return fmt.Errorf("config: %s=%d collides with %s", a.env, a.port, b.env)
			}
		}
		if a.port == listenPort {
			return fmt.Errorf("config: %s=%q collides with %s=%d", EnvListen, c.Listen, a.env, a.port)
		}
	}
	return nil
}

func port(getenv func(string) string, env string, def int) (int, error) {
	v := getenv(env)
	if v == "" {
		return def, nil
	}
	p, err := parsePort(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s=%q: %w", env, v, err)
	}
	return p, nil
}

func parsePort(v string) (int, error) {
	p, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("port is not a number: %w", err)
	}
	if p < 1 || p > 65535 {
		return 0, fmt.Errorf("port %d is out of range 1-65535", p)
	}
	return p, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
