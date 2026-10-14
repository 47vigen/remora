package bifrost

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Values remora always sends for a provider.
const (
	defaultRequestTimeout   = 600 * time.Second
	streamIdleTimeoutSecond = 300
	providerConcurrency     = 1000
	providerBufferSize      = 5000
)

// Provider is one front-door provider. BaseType "" is a standard provider; "openai" or "typesafe" makes a custom
// provider on that base.
type Provider struct {
	Name                string
	BaseType            string
	BaseURL             string
	AllowPrivateNetwork bool
	AllowedRequests     []string      // custom_provider_config.allowed_requests as {"<type>": true}; nil = omitted (all allowed)
	RequestTimeout      time.Duration // network_config.default_request_timeout_in_seconds; 0 = 600 s
}

// Key is one upstream key of a provider. Value is write-only: lists leave it empty, and the engine refuses an update
// without it.
type Key struct {
	ID, Name, Value string
	Enabled         bool
}

type networkConfig struct {
	BaseURL                        string `json:"base_url,omitempty"`
	DefaultRequestTimeoutInSeconds int    `json:"default_request_timeout_in_seconds"`
	MaxRetries                     int    `json:"max_retries"`
	StreamIdleTimeoutInSeconds     int    `json:"stream_idle_timeout_in_seconds"`
	AllowPrivateNetwork            bool   `json:"allow_private_network"`
}

type concurrencyAndBufferSize struct {
	Concurrency int `json:"concurrency"`
	BufferSize  int `json:"buffer_size"`
}

type customProviderConfig struct {
	IsKeyLess        bool            `json:"is_key_less"`
	BaseProviderType string          `json:"base_provider_type"`
	AllowedRequests  map[string]bool `json:"allowed_requests,omitempty"`
}

type providerBody struct {
	Provider                 string                   `json:"provider,omitempty"` // create only
	NetworkConfig            networkConfig            `json:"network_config"`
	ConcurrencyAndBufferSize concurrencyAndBufferSize `json:"concurrency_and_buffer_size"`
	CustomProviderConfig     *customProviderConfig    `json:"custom_provider_config,omitempty"`
}

type providerAnswer struct {
	Name                 string                `json:"name"`
	NetworkConfig        networkConfig         `json:"network_config"`
	CustomProviderConfig *customProviderConfig `json:"custom_provider_config"`
}

func providerWire(p Provider) providerBody {
	timeout := p.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	b := providerBody{
		NetworkConfig: networkConfig{
			BaseURL:                        p.BaseURL,
			DefaultRequestTimeoutInSeconds: int((timeout + time.Second - 1) / time.Second),
			MaxRetries:                     0,
			StreamIdleTimeoutInSeconds:     streamIdleTimeoutSecond,
			AllowPrivateNetwork:            p.AllowPrivateNetwork,
		},
		ConcurrencyAndBufferSize: concurrencyAndBufferSize{Concurrency: providerConcurrency, BufferSize: providerBufferSize},
	}
	if p.BaseType != "" {
		cfg := &customProviderConfig{BaseProviderType: p.BaseType}
		if p.AllowedRequests != nil {
			cfg.AllowedRequests = make(map[string]bool, len(p.AllowedRequests))
			for _, r := range p.AllowedRequests {
				cfg.AllowedRequests[r] = true
			}
		}
		b.CustomProviderConfig = cfg
	}
	return b
}

func providerFromWire(a providerAnswer) Provider {
	p := Provider{
		Name:                a.Name,
		BaseURL:             a.NetworkConfig.BaseURL,
		AllowPrivateNetwork: a.NetworkConfig.AllowPrivateNetwork,
		RequestTimeout:      time.Duration(a.NetworkConfig.DefaultRequestTimeoutInSeconds) * time.Second,
	}
	if cfg := a.CustomProviderConfig; cfg != nil {
		p.BaseType = cfg.BaseProviderType
		if cfg.AllowedRequests != nil {
			p.AllowedRequests = []string{}
			for r, ok := range cfg.AllowedRequests {
				if ok {
					p.AllowedRequests = append(p.AllowedRequests, r)
				}
			}
			sort.Strings(p.AllowedRequests)
		}
	}
	return p
}

func providerPath(name string) string {
	return "/api/providers/" + url.PathEscape(name)
}

// ListProviders returns every provider the engine has configured.
func (c *Client) ListProviders(ctx context.Context) ([]Provider, error) {
	var out struct {
		Providers []providerAnswer `json:"providers"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/providers", nil, &out); err != nil {
		return nil, err
	}
	providers := make([]Provider, 0, len(out.Providers))
	for _, a := range out.Providers {
		providers = append(providers, providerFromWire(a))
	}
	return providers, nil
}

// CreateProvider adds a provider.
func (c *Client) CreateProvider(ctx context.Context, p Provider) error {
	body := providerWire(p)
	body.Provider = p.Name
	return c.do(ctx, http.MethodPost, "/api/providers", body, nil)
}

// UpdateProvider replaces a provider's network, concurrency and (for a custom provider) custom configuration. The
// engine creates the provider when it does not have it.
func (c *Client) UpdateProvider(ctx context.Context, p Provider) error {
	if err := requireID("provider name", p.Name); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, providerPath(p.Name), providerWire(p), nil)
}

// DeleteProvider removes a provider and its keys. The engine answers success for a provider it does not have.
func (c *Client) DeleteProvider(ctx context.Context, name string) error {
	if err := requireID("provider name", name); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, providerPath(name), nil, nil)
}

type keyBody struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Value   string   `json:"value"`
	Models  []string `json:"models"`
	Weight  float64  `json:"weight"`
	Enabled bool     `json:"enabled"`
}

type keyAnswer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled"`
}

// errEngineReference rejects key values the engine would read as a reference instead of a literal: a value
// starting with "env." makes the engine send its own environment variable upstream, "vault." a vault secret.
var errEngineReference = errors.New(`bifrost: key value must not start with "env." or "vault."`)

func keyWire(provider string, k Key) (keyBody, error) {
	if err := requireID("provider name", provider); err != nil {
		return keyBody{}, err
	}
	if strings.HasPrefix(k.Value, "env.") || strings.HasPrefix(k.Value, "vault.") {
		return keyBody{}, errEngineReference
	}
	return keyBody{ID: k.ID, Name: k.Name, Value: k.Value, Models: []string{"*"}, Weight: 1, Enabled: k.Enabled}, nil
}

// ListKeys returns a provider's keys without their values.
func (c *Client) ListKeys(ctx context.Context, provider string) ([]Key, error) {
	var out struct {
		Keys []keyAnswer `json:"keys"`
	}
	if err := requireID("provider name", provider); err != nil {
		return nil, err
	}
	if err := c.do(ctx, http.MethodGet, providerPath(provider)+"/keys", nil, &out); err != nil {
		return nil, err
	}
	keys := make([]Key, 0, len(out.Keys))
	for _, a := range out.Keys {
		keys = append(keys, Key{ID: a.ID, Name: a.Name, Enabled: a.Enabled == nil || *a.Enabled})
	}
	return keys, nil
}

// CreateKey adds a key to a provider. An empty ID lets the engine generate one. The engine wants key IDs and names
// unique across all providers.
func (c *Client) CreateKey(ctx context.Context, provider string, k Key) error {
	body, err := keyWire(provider, k)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, providerPath(provider)+"/keys", body, nil)
}

// UpdateKey replaces a key. The engine refuses an empty Value.
func (c *Client) UpdateKey(ctx context.Context, provider string, k Key) error {
	if err := requireID("key id", k.ID); err != nil {
		return err
	}
	body, err := keyWire(provider, k)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, providerPath(provider)+"/keys/"+url.PathEscape(k.ID), body, nil)
}

// DeleteKey removes a key from a provider.
func (c *Client) DeleteKey(ctx context.Context, provider, id string) error {
	if err := requireID("provider name", provider); err != nil {
		return err
	}
	if err := requireID("key id", id); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, providerPath(provider)+"/keys/"+url.PathEscape(id), nil, nil)
}
