package api

import (
	"context"
	"time"

	"github.com/47vigen/remora/internal/alias"
)

// Ports: one small interface per resource. Handlers talk only to these, so they are tested with in-memory fakes;
// adapters.go maps them onto the store, the reconciler and the supervisor.

// ProviderKey is one credential of a provider. Value is never serialised by handlers.
type ProviderKey struct{ Name, Value string }

// Provider is an upstream reached with API keys.
type Provider struct {
	Name, Kind, Type, BaseURL string
	AllowPrivateNetwork       bool
	Keys                      []ProviderKey
}

// ProviderTest is the result of a provider connectivity test.
type ProviderTest struct {
	OK      bool
	Latency time.Duration
	Message string
}

// Providers manages providers. Engine failures roll the store back and are returned.
type Providers interface {
	List(ctx context.Context) ([]Provider, error)
	Get(ctx context.Context, name string) (Provider, error)
	Create(ctx context.Context, p Provider) (Provider, error)
	Update(ctx context.Context, p Provider) (Provider, error) // empty Keys = keep stored keys
	Delete(ctx context.Context, name string) error
	Test(ctx context.Context, name string) (ProviderTest, error)
}

// Quota is an account's remaining quota for one model.
type Quota struct {
	Model             string
	RemainingFraction *float64
	ResetAt           *time.Time
}

// Account is a subscription account signed in with OAuth.
type Account struct {
	ID, Provider, Email, Status, Message string
	Enabled                              bool
	CooldownUntil                        *time.Time
	Quota                                []Quota
	Models                               []string
}

// Login is a started OAuth login: a URL to open, or a device code to enter.
type Login struct{ State, URL, UserCode string }

// LoginStatus is the state of a login: "wait" | "ok" | "error".
type LoginStatus struct{ Status, Message string }

// Accounts manages subscription accounts (plan 03's accounts.Service).
type Accounts interface {
	List(ctx context.Context, provider string) ([]Account, error)
	StartLogin(ctx context.Context, provider string) (Login, error)
	LoginStatus(ctx context.Context, state string) (LoginStatus, error)
	CompleteLogin(ctx context.Context, state, redirectURL string) error
	CancelLogin(ctx context.Context, state string) error
	SetEnabled(ctx context.Context, id string, enabled bool) error
	Delete(ctx context.Context, id string) error
	ResetCooldown(ctx context.Context, id string) error
	Import(ctx context.Context, provider string, credential []byte) (Account, error)
}

// Cooling is a target that is cooling down after failures.
type Cooling struct {
	Target alias.Target
	Until  time.Time
	Reason string
}

// Aliases manages aliases. alias.ErrCycle, ErrDepth and ErrUnknownMember pass through.
type Aliases interface {
	List(ctx context.Context) ([]alias.Alias, error)
	Get(ctx context.Context, name string) (alias.Alias, error)
	Put(ctx context.Context, a alias.Alias, create bool) (alias.Alias, error)
	Delete(ctx context.Context, name string) error
	Chain(ctx context.Context, name string) ([]alias.Target, error)     // compiled, before cooling
	Compile(ctx context.Context, a alias.Alias) ([]alias.Target, error) // an unsaved alias against the stored ones; stores nothing
	Cooling(ctx context.Context) ([]Cooling, error)
}

// Key is a client key for /v1. Its value is shown once, at creation.
type Key struct {
	ID, Name, Prefix string
	Allow            []string
	Enabled          bool
	CreatedAt        time.Time
}

// KeyPatch changes the fields that are set.
type KeyPatch struct {
	Name    *string
	Allow   *[]string
	Enabled *bool
}

// Keys manages client keys.
type Keys interface {
	List(ctx context.Context) ([]Key, error)
	Get(ctx context.Context, id string) (Key, error)
	Create(ctx context.Context, name string, allow []string, value string) (Key, string, error) // value "" → generated; returns the value
	Update(ctx context.Context, id string, p KeyPatch) (Key, error)
	SetValue(ctx context.Context, id, value string) error // apply/import replacing a key's value
	MatchesValue(ctx context.Context, id, value string) (bool, error)
	Delete(ctx context.Context, id string) error
}

// EngineVersion names one engine and its release tag.
type EngineVersion struct{ Name, Version string }

// System reports health and versions. Health names components neutrally; Version lists engines by their own
// names, in start order (engine names appear only there and in the credits).
type System interface {
	Health(ctx context.Context) (ok bool, engines map[string]error)
	Version() (remora string, engines []EngineVersion)
}

// EngineNames translates between remora names and the front door's ids (Task 8 uses it for logs and stats).
type EngineNames interface {
	KeyByVK(ctx context.Context, vkID string) (keyID, keyName string, ok bool)
	VKByKey(ctx context.Context, keyID string) (vkID string, ok bool)
	AliasByRule(ctx context.Context, ruleID string) (string, bool)
	RuleByAlias(ctx context.Context, name string) (string, bool)
	Target(ctx context.Context, engineProvider, engineModel string) alias.Target
	EngineFilter(ctx context.Context, provider, model string) (engineProviders, engineModels []string)
}
