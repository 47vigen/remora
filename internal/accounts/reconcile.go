package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// settleDelay is how long after a settings write a later pass writes the settings once more through a changed file
// (see reconcileSettings). The engine's blind window after it starts answering is about 100 ms.
const settleDelay = 2 * time.Second

// ClientKey returns the key the front door uses with the OAuth engine: the one the supervisor generated and rendered
// into the engine's configuration. It is never generated here.
func (s *Service) ClientKey(ctx context.Context) (string, error) {
	key, ok, err := s.store.EngineValue(ctx, "cliproxyapi", "api_key")
	if err != nil {
		return "", fmt.Errorf("accounts: client key: %w", err)
	}
	if !ok || key == "" {
		return "", errors.New("accounts: client key: not generated yet")
	}
	return key, nil
}

// Reconcile makes the OAuth engine match the store: its settings, each account's enabled state, and no credential
// the store does not know (except a finished login's, before it is adopted). Store rows whose credential is missing
// are only logged. Every step runs; the errors are joined.
func (s *Service) Reconcile(ctx context.Context) error {
	s.recMu.Lock()
	defer s.recMu.Unlock()

	key, err := s.ClientKey(ctx)
	if err != nil {
		return err
	}
	var errs []error
	if err := s.reconcileSettings(ctx, key); err != nil {
		errs = append(errs, err)
	}
	if err := s.reconcileCredentials(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// settingsPatch is the engine configuration remora owns; supervise.CLIProxyAPIEngine renders the same values into the
// file. force-model-prefix makes each credential answer only to "<prefix>/<model>", so a bare model name never crosses
// providers; PATCH replaces lists, so access.api-keys holds only remora's key. twice lists the key twice: the same
// access through a different file. request-retry and bootstrap-retries retry only before the first byte reaches the
// client, absorbing a short capacity spike; the antigravity pool reuses upstream connections instead of a TLS
// handshake per request.
func settingsPatch(key string, twice bool) map[string]any {
	keys := []any{key}
	if twice {
		keys = append(keys, key)
	}
	return map[string]any{
		"access": map[string]any{"api-keys": keys},
		"routing": map[string]any{
			"strategy":           "round-robin",
			"force-model-prefix": true,
			"retry":              map[string]any{"request-retry": 1, "max-retry-interval": 10},
			"cooldown":           map[string]any{"save-cooldown-status": true, "transient-error-cooldown-seconds": 5},
			"session-affinity":   true, "session-affinity-ttl": "1h", "session-affinity-rebind-on": "quota",
		},
		"requests": map[string]any{"streaming": map[string]any{"bootstrap-retries": 1, "keepalive-seconds": 15}},
		"oauth": map[string]any{"providers": map[string]any{"antigravity": map[string]any{"connection-pool": map[string]any{
			"enabled": true, "idle-conn-timeout": "90s", "max-idle-conns-per-host": 8,
		}}}},
		"upstream": map[string]any{"codex": map[string]any{"model-level-cooling": true}},
	}
}

// reconcileSettings writes the settings when the engine's configuration lacks them.
//
// The engine answers HTTP (health included) about 100 ms before its configuration watcher runs. A write in that
// window is saved to the file but never applied, and writing the same settings again is a no-op because the file does
// not change; the management API only shows the file. So after a write, the first pass at least settleDelay later
// writes the settings once more through a different file (the client key listed twice, which grants the same
// access), and then the settings themselves, so the running engine applies them. Settings already in the file when
// this process did not write them were loaded by the engine at start and need nothing.
func (s *Service) reconcileSettings(ctx context.Context, key string) error {
	raw, err := s.engine.Config(ctx)
	if err != nil {
		return fmt.Errorf("accounts: read engine settings: %w", err)
	}
	var cur map[string]any
	if err := json.Unmarshal(raw, &cur); err != nil {
		return fmt.Errorf("accounts: read engine settings: %w", err)
	}
	want := settingsPatch(key, false)
	if !containsAll(cur, jsonRoundTrip(want)) {
		if err := s.engine.PatchConfig(ctx, want); err != nil {
			return fmt.Errorf("accounts: write engine settings: %w", err)
		}
		s.settingsWrittenAt = s.now()
		s.log.Info("accounts: engine settings written")
		return nil
	}
	if s.settingsWrittenAt.IsZero() || s.now().Sub(s.settingsWrittenAt) < settleDelay {
		return nil
	}
	if err := s.engine.PatchConfig(ctx, settingsPatch(key, true)); err != nil {
		return fmt.Errorf("accounts: rewrite engine settings: %w", err)
	}
	if err := s.engine.PatchConfig(ctx, want); err != nil {
		return fmt.Errorf("accounts: rewrite engine settings: %w", err)
	}
	s.settingsWrittenAt = time.Time{}
	return nil
}

// reconcileCredentials applies each row's enabled state and deletes the credentials the store does not know.
//
// The reads run in this order on purpose: credentials, then the pending logins, then the rows. A credential an
// import uploads already has its row, and a login marks itself adopted only after its rows exist, so any credential
// listed here is either in the rows read afterwards or still pending.
func (s *Service) reconcileCredentials(ctx context.Context) error {
	creds, err := s.engine.Credentials(ctx)
	if err != nil {
		return fmt.Errorf("accounts: reconcile: %w", err)
	}
	pendingProviders := s.PendingProviders()
	rows, err := s.store.Accounts(ctx)
	if err != nil {
		return fmt.Errorf("accounts: reconcile: %w", err)
	}

	byName := make(map[string]int, len(creds))
	for i, c := range creds {
		byName[c.Name] = i
	}
	known := make(map[string]bool, len(rows))
	var errs []error
	for _, row := range rows {
		known[row.Credential] = true
		i, ok := byName[row.Credential]
		if !ok {
			s.log.Warn("accounts: account's credential is missing", "provider", row.Provider, "id", row.ID)
			continue
		}
		if creds[i].Disabled == !row.Enabled {
			continue
		}
		if err := s.engine.SetCredentialDisabled(ctx, row.Credential, !row.Enabled); err != nil && !isNotFound(err) {
			errs = append(errs, fmt.Errorf("accounts: reconcile account %d: %w", row.ID, err))
		}
	}
	for _, c := range creds {
		if known[c.Name] || pendingProviders[c.Provider] {
			continue
		}
		if err := s.engine.DeleteCredential(ctx, c.Name); err != nil && !isNotFound(err) {
			errs = append(errs, fmt.Errorf("accounts: reconcile: delete unknown credential: %w", err))
			continue
		}
		s.log.Info("accounts: unknown credential deleted", "provider", c.Provider)
	}
	return errors.Join(errs...)
}

// jsonRoundTrip returns v as encoding/json decodes it, so it compares equal to a decoded answer.
func jsonRoundTrip(v map[string]any) map[string]any {
	raw, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// containsAll reports whether every leaf of want is in got at the same path with an equal value; objects are
// compared key by key, lists and scalars whole.
func containsAll(got, want map[string]any) bool {
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			return false
		}
		if wm, isObj := w.(map[string]any); isObj {
			gm, ok := g.(map[string]any)
			if !ok || !containsAll(gm, wm) {
				return false
			}
			continue
		}
		if !reflect.DeepEqual(g, w) {
			return false
		}
	}
	return true
}
