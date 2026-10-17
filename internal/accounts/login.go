package accounts

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

// Login is a started login. Mode "paste": the user opens URL and pastes the address the browser ends on into
// CompleteLogin. Mode "device": the user opens URL and enters UserCode; the login completes on its own.
type Login struct {
	State, Provider, URL, Mode, UserCode string
	ExpiresAt                            time.Time
}

// LoginStatus is a login's progress. Status is "wait", "ok" (Account set) or "error" (Message set).
type LoginStatus struct {
	Status, Message string
	Account         *Account
}

// engineExpired is the engine's status error for a state it does not know (never started, expired, or forgotten in
// a restart).
const engineExpired = "unknown or expired state"

// StartLogin starts a login for the user-facing provider name.
func (s *Service) StartLogin(ctx context.Context, provider string) (Login, error) {
	p, ok := cliproxyapi.ProviderByName(provider)
	if !ok {
		return Login{}, fmt.Errorf("%w: %q", ErrUnknownProvider, provider)
	}
	// No is_webui: that would start a callback listener inside the container, which a remote browser cannot reach.
	start, err := s.engine.StartOAuth(ctx, p.Engine)
	if err != nil {
		return Login{}, fmt.Errorf("accounts: start %s login: %w", p.Name, err)
	}
	if start.State == "" {
		return Login{}, fmt.Errorf("accounts: start %s login: no state in the answer", p.Name)
	}
	now := s.now()
	l := Login{State: start.State, Provider: p.Name, URL: start.URL, Mode: "paste", ExpiresAt: now.Add(sessionTTL)}
	if start.Flow == "device" {
		l.Mode, l.UserCode = "device", start.UserCode
	}
	if start.ExpiresIn > 0 {
		l.ExpiresAt = now.Add(time.Duration(start.ExpiresIn) * time.Second)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	s.pending[l.State] = &pending{provider: p, mode: l.Mode, until: now.Add(sessionTTL)}
	return l, nil
}

// CompleteLogin hands the engine the address the browser ended on after a paste-flow login. The address must carry
// a code or an error, and either no state or this login's state; otherwise ErrBadRedirect and the engine is not
// called.
func (s *Service) CompleteLogin(ctx context.Context, state, redirectURL string) error {
	p, err := s.lookup(state)
	if err != nil {
		return err
	}
	if p.mode != "paste" {
		return ErrNotPasteFlow
	}
	redirectURL = strings.TrimSpace(redirectURL)
	if !validRedirect(redirectURL, state) {
		return ErrBadRedirect
	}
	if err := s.engine.SubmitOAuthCallback(ctx, state, redirectURL); err != nil {
		if isNotFound(err) {
			s.drop(state)
			return ErrLoginExpired
		}
		return fmt.Errorf("accounts: complete %s login: %w", p.provider.Name, err)
	}
	return nil
}

// validRedirect reports whether raw is an address a sign-in for state ends on.
func validRedirect(raw, state string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	q := u.Query()
	if q.Get("code") == "" && q.Get("error") == "" {
		return false
	}
	got := q.Get("state")
	return got == "" || got == state
}

// LoginStatus reports a login's progress. On "ok" it adopts the provider's new credentials into the store; the
// result stays readable, with the same account, until the engine's session TTL.
func (s *Service) LoginStatus(ctx context.Context, state string) (LoginStatus, error) {
	p, err := s.lookup(state)
	if err != nil {
		return LoginStatus{}, err
	}
	if p.done != nil {
		return LoginStatus{Status: "ok", Account: p.done}, nil
	}
	st, err := s.engine.OAuthStatus(ctx, state)
	if err != nil {
		return LoginStatus{}, fmt.Errorf("accounts: %s login status: %w", p.provider.Name, err)
	}
	switch st.Status {
	case "ok":
		return s.adopt(ctx, state)
	case "error":
		s.drop(state)
		if st.Error == engineExpired {
			return LoginStatus{}, ErrLoginExpired
		}
		msg := scrub(st.Error)
		if msg == "" {
			msg = "sign-in failed"
		}
		return LoginStatus{Status: "error", Message: msg}, nil
	default:
		return LoginStatus{Status: "wait"}, nil
	}
}

// adopt stores every credential of the login's provider that the store does not have yet, with the provider's name
// as the credential's model prefix, and records the login as done. It returns the provider's most recently written
// credential: a sign-in to an account remora already has rewrites that account's file.
func (s *Service) adopt(ctx context.Context, state string) (LoginStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[state]
	if !ok {
		return LoginStatus{}, ErrLoginExpired
	}
	if p.done != nil {
		return LoginStatus{Status: "ok", Account: p.done}, nil
	}
	prov := p.provider

	creds, err := s.engine.Credentials(ctx)
	if err != nil {
		return LoginStatus{}, fmt.Errorf("accounts: adopt %s login: %w", prov.Name, err)
	}
	rows, err := s.store.Accounts(ctx)
	if err != nil {
		return LoginStatus{}, fmt.Errorf("accounts: adopt %s login: %w", prov.Name, err)
	}
	byCred := make(map[string]store.Account, len(rows))
	for _, r := range rows {
		byCred[r.Credential] = r
	}

	var newest, newestAdopted *cliproxyapi.Credential
	for i := range creds {
		c := &creds[i]
		if c.Provider != prov.Engine {
			continue
		}
		if _, known := byCred[c.Name]; !known {
			row, err := s.adoptOne(ctx, prov, *c)
			if err != nil {
				return LoginStatus{}, err
			}
			byCred[c.Name] = row
			if newestAdopted == nil || c.UpdatedAt.After(newestAdopted.UpdatedAt) {
				newestAdopted = c
			}
		}
		if newest == nil || c.UpdatedAt.After(newest.UpdatedAt) {
			newest = c
		}
	}
	pick := newestAdopted
	if pick == nil {
		pick = newest
	}
	if pick == nil {
		s.log.Warn("accounts: login finished without a credential", "provider", prov.Name)
		delete(s.pending, state)
		return LoginStatus{Status: "error", Message: "the sign-in finished but no account was found; start again"}, nil
	}
	if pick != newestAdopted {
		// A sign-in again rewrote an existing file; make sure it still answers to the provider's prefix.
		if err := s.engine.SetCredentialPrefix(ctx, pick.Name, prov.Name); err != nil {
			return LoginStatus{}, fmt.Errorf("accounts: adopt %s login: %w", prov.Name, err)
		}
	}
	if NoProject(prov.Name, *pick) {
		// The account stays, marked refused, so it can be seen and deleted; the sign-in reports the failure.
		s.log.Warn("accounts: login finished without a project", "provider", prov.Name, "id", byCred[pick.Name].ID)
		delete(s.pending, state)
		return LoginStatus{Status: "error", Message: "signed in, but " + MsgNoProject}, nil
	}
	acct := accountFrom(byCred[pick.Name], pick, nil)
	p.done = &acct
	return LoginStatus{Status: "ok", Account: p.done}, nil
}

// adoptOne prefixes one engine credential and stores it as an enabled account.
func (s *Service) adoptOne(ctx context.Context, prov cliproxyapi.Provider, c cliproxyapi.Credential) (store.Account, error) {
	if err := s.engine.SetCredentialPrefix(ctx, c.Name, prov.Name); err != nil {
		return store.Account{}, fmt.Errorf("accounts: adopt %s credential: %w", prov.Name, err)
	}
	label := c.Email
	if label == "" {
		label = c.Label
	}
	row, err := s.store.CreateAccount(ctx, store.Account{Provider: prov.Name, Label: label, Credential: c.Name, Enabled: true})
	if err != nil {
		return store.Account{}, fmt.Errorf("accounts: adopt %s credential: %w", prov.Name, err)
	}
	s.log.Info("accounts: account added", "provider", prov.Name, "id", row.ID)
	return row, nil
}

// CancelLogin cancels a login started here.
func (s *Service) CancelLogin(ctx context.Context, state string) error {
	p, err := s.lookup(state)
	if err != nil {
		return err
	}
	if err := s.engine.CancelOAuth(ctx, state); err != nil {
		return fmt.Errorf("accounts: cancel %s login: %w", p.provider.Name, err)
	}
	s.drop(state)
	return nil
}

// PendingProviders returns the engine provider ids of the logins started here that are neither adopted nor over.
// Reconciliation leaves those providers' unknown credentials alone: a finished login's file may not be adopted yet.
func (s *Service) PendingProviders() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := map[string]bool{}
	for _, p := range s.pending {
		if p.done == nil && now.Before(p.until) {
			out[p.provider.Engine] = true
		}
	}
	return out
}

// lookup returns a copy of a pending login. ErrLoginNotFound for a state never started here; ErrLoginExpired once
// the engine's session TTL has passed.
func (s *Service) lookup(state string) (pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[state]
	if !ok {
		return pending{}, ErrLoginNotFound
	}
	if !s.now().Before(p.until) {
		delete(s.pending, state)
		return pending{}, ErrLoginExpired
	}
	return *p, nil
}

func (s *Service) drop(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, state)
}

// pruneLocked forgets logins well past their TTL; until then a status call still answers ErrLoginExpired.
func (s *Service) pruneLocked(now time.Time) {
	for state, p := range s.pending {
		if now.After(p.until.Add(sessionTTL)) {
			delete(s.pending, state)
		}
	}
}

// engineName matches the OAuth engine's product name, so it never reaches a user through a passed-on message.
var engineName = regexp.MustCompile(`(?i)cli[-_ ]?proxy(?:[-_ ]?api)?`)

func scrub(msg string) string {
	return strings.TrimSpace(engineName.ReplaceAllString(msg, "engine"))
}
