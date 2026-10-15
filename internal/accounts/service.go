// Package accounts owns remora's subscription accounts: OAuth logins, the accounts table and the OAuth engine's
// credentials, kept in step by reconciliation. Nothing it returns to a caller names the engine.
package accounts

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

// Errors a caller can match with errors.Is. Their texts are shown to users.
var (
	ErrUnknownProvider = errors.New("unknown OAuth provider")
	ErrLoginNotFound   = errors.New("login not found") // state never started here
	ErrLoginExpired    = errors.New("login expired, start again")
	ErrBadRedirect     = errors.New("this is not the sign-in address for this login")
	ErrNotPasteFlow    = errors.New("this login completes on its own")
)

// sessionTTL is how long the OAuth engine keeps a login session, finished or not.
const sessionTTL = 30 * time.Minute

// Service combines the OAuth engine's management client with the accounts table.
type Service struct {
	engine *cliproxyapi.Client
	store  *store.Store
	log    *slog.Logger
	now    func() time.Time

	modelWait time.Duration // how long an imported credential's models are awaited per round

	mu      sync.Mutex
	pending map[string]*pending // by login state

	recMu             sync.Mutex // serializes Reconcile
	settingsWrittenAt time.Time  // under recMu: when this process last wrote the settings, zero once they settled
}

// pending is a login started here. done is set once its credential was adopted.
type pending struct {
	provider cliproxyapi.Provider
	mode     string
	until    time.Time // the engine forgets the session after this
	done     *Account
}

// New returns a Service. A nil log discards.
func New(engine *cliproxyapi.Client, st *store.Store, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{engine: engine, store: st, log: log, now: time.Now, modelWait: 2 * time.Second, pending: map[string]*pending{}}
}
