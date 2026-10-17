package accounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

// Errors of account management. Their texts are shown to users.
var (
	ErrNotFound          = errors.New("account not found")
	ErrCredentialMissing = errors.New("this account's sign-in is missing; delete it and sign in again")
	ErrBadCredential     = errors.New("invalid credential")
)

// statusMissing is an account's Status when the engine has no credential for it.
const (
	statusMissing = "missing"
	statusRefused = "refused"
)

// MsgNoProject is why an account whose provider assigned it no project cannot serve.
const MsgNoProject = "the provider assigned this account no project, so it serves no model; signing in again will not help until the provider restores the account"

// NoProject reports whether c belongs to a provider that serves only through a project and the provider assigned it
// none. The sign-in itself succeeds: the provider refuses the account when the engine asks for its project, for
// example because it suspended the account.
func NoProject(provider string, c cliproxyapi.Credential) bool {
	return provider == "antigravity" && c.ProjectID == ""
}

// Account is a subscription account as users see it: the store row merged with the engine's view of its credential.
type Account struct {
	store.Account
	Status, StatusMessage string // engine status; "missing" when the engine has no such credential
	Unavailable           bool
	NextRetryAfter        *time.Time
	Cooldowns             []cliproxyapi.Cooldown
	Quota                 map[string]string // the engine's quota signals, opaque per provider
	Success, Failed       int64
	Models                []string // user refs, "<provider>/<model>"
}

// accountFrom builds an account from its row and the engine's credential (nil when the engine has none). models are
// model ids as the engine lists them.
func accountFrom(row store.Account, c *cliproxyapi.Credential, models []string) Account {
	a := Account{Account: row}
	if c == nil {
		a.Status = statusMissing
		return a
	}
	a.Status, a.StatusMessage = c.Status, scrub(c.StatusMessage)
	if NoProject(row.Provider, *c) {
		a.Status, a.StatusMessage = statusRefused, MsgNoProject
	}
	a.Unavailable, a.NextRetryAfter = c.Unavailable, c.NextRetryAfter
	a.Cooldowns = slices.Clone(c.Cooldowns)
	a.Quota = maps.Clone(c.Quota.Signals)
	a.Success, a.Failed = c.Success, c.Failed
	if len(models) > 0 {
		a.Models = make([]string, 0, len(models))
		for _, m := range models {
			if !strings.HasPrefix(m, row.Provider+"/") {
				m = row.Provider + "/" + m
			}
			a.Models = append(a.Models, m)
		}
	}
	return a
}

// List returns the accounts of one user-facing provider ("" = all), ordered by provider and id, each merged with
// the engine's view of its credential.
func (s *Service) List(ctx context.Context, provider string) ([]Account, error) {
	if provider != "" {
		if _, ok := cliproxyapi.ProviderByName(provider); !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, provider)
		}
	}
	rows, err := s.store.Accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("accounts: list: %w", err)
	}
	creds, err := s.credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("accounts: list: %w", err)
	}
	out := []Account{}
	for _, row := range rows {
		if provider != "" && row.Provider != provider {
			continue
		}
		c, ok := creds[row.Credential]
		if !ok {
			out = append(out, accountFrom(row, nil, nil))
			continue
		}
		models, err := s.engine.CredentialModels(ctx, row.Credential)
		if err != nil {
			return nil, fmt.Errorf("accounts: list models of account %d: %w", row.ID, err)
		}
		out = append(out, accountFrom(row, &c, models))
	}
	return out, nil
}

// SetEnabled enables or disables an account: the engine first, then the store.
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	row, err := s.row(ctx, id)
	if err != nil {
		return err
	}
	if err := s.engine.SetCredentialDisabled(ctx, row.Credential, !enabled); err != nil {
		if isNotFound(err) {
			return ErrCredentialMissing
		}
		return fmt.Errorf("accounts: set account %d enabled: %w", id, err)
	}
	if err := s.store.SetAccountEnabled(ctx, id, enabled); err != nil {
		return storeErr(err, "set account %d enabled", id)
	}
	return nil
}

// Delete deletes an account: the engine's credential first (already gone counts as deleted), then the row.
func (s *Service) Delete(ctx context.Context, id int64) error {
	row, err := s.row(ctx, id)
	if err != nil {
		return err
	}
	if err := s.engine.DeleteCredential(ctx, row.Credential); err != nil && !isNotFound(err) {
		return fmt.Errorf("accounts: delete account %d: %w", id, err)
	}
	if err := s.store.DeleteAccount(ctx, id); err != nil {
		return storeErr(err, "delete account %d", id)
	}
	s.log.Info("accounts: account deleted", "provider", row.Provider, "id", id)
	return nil
}

// ResetCooldown clears the engine's cooldowns on an account's credential and its models.
func (s *Service) ResetCooldown(ctx context.Context, id int64) error {
	row, err := s.row(ctx, id)
	if err != nil {
		return err
	}
	creds, err := s.credentials(ctx)
	if err != nil {
		return fmt.Errorf("accounts: reset cooldown of account %d: %w", id, err)
	}
	c, ok := creds[row.Credential]
	if !ok || c.AuthIndex == "" {
		return ErrCredentialMissing
	}
	if err := s.engine.ResetCooldown(ctx, c.AuthIndex); err != nil {
		if isNotFound(err) {
			return ErrCredentialMissing
		}
		return fmt.Errorf("accounts: reset cooldown of account %d: %w", id, err)
	}
	return nil
}

// Import adds an account from a credential file's JSON (an object) for the user-facing provider. A missing "type"
// is set to the provider's; another provider's type is refused. The row is stored before the upload so that a
// reconcile pass never deletes the new credential, and is deleted again if the engine refuses it.
func (s *Service) Import(ctx context.Context, provider string, credential []byte) (Account, error) {
	p, ok := cliproxyapi.ProviderByName(provider)
	if !ok {
		return Account{}, fmt.Errorf("%w: %q", ErrUnknownProvider, provider)
	}
	var doc map[string]any
	if err := json.Unmarshal(credential, &doc); err != nil || doc == nil {
		return Account{}, fmt.Errorf("%w: it must be a JSON object", ErrBadCredential)
	}
	switch t, _ := doc["type"].(string); {
	case t == "":
		doc["type"] = p.Engine
	case t != p.Engine:
		return Account{}, fmt.Errorf("%w: it is not a %s credential", ErrBadCredential, p.Name)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return Account{}, fmt.Errorf("accounts: import: %w", err)
	}
	name, err := credentialName(p.Name)
	if err != nil {
		return Account{}, err
	}
	label, _ := doc["email"].(string)

	row, err := s.store.CreateAccount(ctx, store.Account{Provider: p.Name, Label: label, Credential: name, Enabled: true})
	if err != nil {
		return Account{}, fmt.Errorf("accounts: import: %w", err)
	}
	rollback := func(uploaded bool, cause error) (Account, error) {
		// The caller's context may be what failed; the rollback must still run.
		bg := context.WithoutCancel(ctx)
		if uploaded {
			if err := s.engine.DeleteCredential(bg, name); err != nil && !isNotFound(err) {
				s.log.Warn("accounts: import rollback: delete credential", "provider", p.Name, "err", err)
			}
		}
		if err := s.store.DeleteAccount(bg, row.ID); err != nil {
			s.log.Warn("accounts: import rollback: delete row", "provider", p.Name, "id", row.ID, "err", err)
		}
		return Account{}, fmt.Errorf("accounts: import %s credential: %w", p.Name, cause)
	}
	if err := s.engine.UploadCredential(ctx, name, data); err != nil {
		return rollback(false, err)
	}
	if err := s.engine.SetCredentialPrefix(ctx, name, p.Name); err != nil {
		return rollback(true, err)
	}
	s.log.Info("accounts: account imported", "provider", p.Name, "id", row.ID)

	models, err := s.engine.AwaitCredentialModels(ctx, name, p.Name, s.modelWait)
	if err != nil {
		s.log.Warn("accounts: read imported credential's models", "provider", p.Name, "id", row.ID, "err", err)
	} else if len(models) == 0 {
		s.log.Warn("accounts: imported account lists no models yet", "provider", p.Name, "id", row.ID)
	}
	creds, err := s.credentials(ctx)
	if err != nil {
		s.log.Warn("accounts: read imported credential", "provider", p.Name, "err", err)
		return Account{Account: row}, nil
	}
	if c, ok := creds[name]; ok {
		return accountFrom(row, &c, models), nil
	}
	return accountFrom(row, nil, nil), nil
}

// credentialName returns "<provider>-<8 random hex>.json".
func credentialName(provider string) (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("accounts: credential name: %w", err)
	}
	return provider + "-" + hex.EncodeToString(b) + ".json", nil
}

// credentials returns the engine's credentials by name.
func (s *Service) credentials(ctx context.Context) (map[string]cliproxyapi.Credential, error) {
	list, err := s.engine.Credentials(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]cliproxyapi.Credential, len(list))
	for _, c := range list {
		out[c.Name] = c
	}
	return out, nil
}

// row reads an account's row; ErrNotFound when there is none.
func (s *Service) row(ctx context.Context, id int64) (store.Account, error) {
	row, err := s.store.Account(ctx, id)
	if err != nil {
		return store.Account{}, storeErr(err, "account %d", id)
	}
	return row, nil
}

func storeErr(err error, format string, args ...any) error {
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("accounts: "+format+": %w", append(args, err)...)
}

func isNotFound(err error) bool {
	var ee *cliproxyapi.Error
	return errors.As(err, &ee) && ee.StatusCode == http.StatusNotFound
}
