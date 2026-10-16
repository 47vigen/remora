package config

import (
	"context"
	"time"

	"github.com/47vigen/remora/internal/alias"
)

// Ports: the provider, alias and client key services Service drives. They are the management API's ports
// (internal/api aliases them), declared here so this package does not import internal/api.

// Batch runs fn so that the port writes made under the context it gets are committed together or not at all.
type Batch func(ctx context.Context, fn func(ctx context.Context) error) error

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

// CoolingMember is a target that is cooling down after failures.
type CoolingMember struct {
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
	Cooling(ctx context.Context) ([]CoolingMember, error)
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
	Create(ctx context.Context, name string, allow []string, value string, enabled bool) (Key, string, error) // value "" → generated; returns the value
	Update(ctx context.Context, id string, p KeyPatch) (Key, error)
	SetValue(ctx context.Context, id, value string) error // apply/import replacing a key's value
	MatchesValue(ctx context.Context, id, value string) (bool, error)
	Delete(ctx context.Context, id string) error
}

// AccountStrategies reads and sets each subscription provider's account strategy. SetStrategy called under a Batch
// context writes on the batch's transaction; Reconcile then applies the committed strategies to the engine.
type AccountStrategies interface {
	Strategies(ctx context.Context) (map[string]alias.Strategy, error) // every subscription provider, round-robin by default
	SetStrategy(ctx context.Context, provider string, st alias.Strategy) error
	Reconcile(ctx context.Context) error
}
