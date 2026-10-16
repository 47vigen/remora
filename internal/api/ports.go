package api

import (
	"context"
	"time"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/quota"
)

// Ports: one small interface per resource. Handlers talk only to these, so they are tested with in-memory fakes;
// adapters.go maps them onto the store, the reconciler and the supervisor.

// The provider, alias and client key ports are declared in internal/config, whose Service drives remora.yaml
// export, plan and apply over the same method sets; they are aliased here so handlers and adapters keep these names.

// ProviderKey is config.ProviderKey.
type ProviderKey = config.ProviderKey

// Provider is config.Provider.
type Provider = config.Provider

// ProviderTest is config.ProviderTest.
type ProviderTest = config.ProviderTest

// Providers is config.Providers.
type Providers = config.Providers

// Cooling is config.CoolingMember.
type Cooling = config.CoolingMember

// Aliases is config.Aliases.
type Aliases = config.Aliases

// Key is config.Key.
type Key = config.Key

// KeyPatch is config.KeyPatch.
type KeyPatch = config.KeyPatch

// Keys is config.Keys.
type Keys = config.Keys

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
	Priority                             int
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
	Pool(ctx context.Context) ([]accounts.PoolStatus, error)
	SetPriority(ctx context.Context, id string, priority int) error
	// Strategies lists every subscription provider's strategy, round-robin when none is set.
	Strategies(ctx context.Context) (map[string]alias.Strategy, error)
	// SetStrategy stores a provider's strategy and applies it; inside a Batch it joins the batch's transaction and
	// Reconcile applies it after the commit.
	SetStrategy(ctx context.Context, provider string, st alias.Strategy) error
	Reconcile(ctx context.Context) error
}

// Quotas reads subscription accounts' quota and queues checks. Account ids are decimal strings.
type Quotas interface {
	Overview(ctx context.Context) ([]quota.ProviderView, error)
	Refresh(ctx context.Context, accountID, provider string) (queued, skipped []string, err error)
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
