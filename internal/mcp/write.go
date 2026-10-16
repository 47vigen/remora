package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/47vigen/remora/internal/api/gen"
)

// DeleteIn is remora_delete's input.
type DeleteIn struct {
	Kind string `json:"kind" jsonschema:"provider|alias|key|account"`
	ID   string `json:"id" jsonschema:"provider or alias name, key id, account id"`
}

// DeleteOut is remora_delete's answer, such as "alias fast".
type DeleteOut struct {
	Deleted string `json:"deleted"`
}

// AccountIn is remora_account's input.
type AccountIn struct {
	Op          string         `json:"op" jsonschema:"enable|disable|set_priority|reset_cooldown|refresh_quota|login_start|login_complete|login_cancel|import"`
	ID          string         `json:"id,omitempty" jsonschema:"account id; for login_complete and login_cancel the login state"`
	Provider    string         `json:"provider,omitempty" jsonschema:"login_start, import, refresh_quota for a whole provider"`
	Priority    *int           `json:"priority,omitempty" jsonschema:"set_priority; higher is tried first"`
	RedirectURL string         `json:"redirect_url,omitempty" jsonschema:"login_complete: the address the browser ended on"`
	Credential  map[string]any `json:"credential,omitempty" jsonschema:"import: the provider's credential JSON"`
}

const accountOps = "enable, disable, set_priority, reset_cooldown, refresh_quota, login_start, login_complete, login_cancel, import"

// accountNeeds is the input each op requires, checked before any call.
var accountNeeds = map[string][]string{
	"enable": {"id"}, "disable": {"id"}, "reset_cooldown": {"id"}, "set_priority": {"id", "priority"},
	"refresh_quota": {"id or provider"}, "login_start": {"provider"}, "login_complete": {"id", "redirect_url"},
	"login_cancel": {"id"}, "import": {"provider", "credential"},
}

func addWrite(s *sdk.Server, c gen.ClientWithResponsesInterface) {
	add(s, &sdk.Tool{
		Name:        "remora_delete",
		Description: "Delete one provider, alias, client key or subscription account. Cannot be undone.",
		Annotations: write(true),
	}, func(ctx context.Context, in DeleteIn) (DeleteOut, error) {
		var err error
		switch in.Kind {
		case "provider":
			err = done(c.DeleteProviderWithResponse(ctx, in.ID))
		case "alias":
			err = done(c.DeleteAliasWithResponse(ctx, in.ID))
		case "key":
			err = done(c.DeleteKeyWithResponse(ctx, in.ID))
		case "account":
			err = done(c.DeleteAccountWithResponse(ctx, in.ID))
		default:
			return DeleteOut{}, fmt.Errorf("kind must be one of provider, alias, key, account")
		}
		if err != nil {
			return DeleteOut{}, err
		}
		return DeleteOut{Deleted: in.Kind + " " + in.ID}, nil
	})

	add(s, &sdk.Tool{
		Name: "remora_account",
		Description: "Operate a subscription account: enable, disable, set_priority, reset_cooldown, refresh_quota, " +
			"login_start (returns a URL for the user to open), login_complete (the address the browser ended on), " +
			"login_cancel, import (a credential JSON).",
		Annotations: &sdk.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(true)},
	}, func(ctx context.Context, in AccountIn) (any, error) { return accountOp(ctx, c, in) })
}

func missing(in AccountIn, field string) bool {
	switch field {
	case "id":
		return in.ID == ""
	case "priority":
		return in.Priority == nil
	case "id or provider":
		return in.ID == "" && in.Provider == ""
	case "provider":
		return in.Provider == ""
	case "redirect_url":
		return in.RedirectURL == ""
	default: // credential
		return len(in.Credential) == 0
	}
}

var okBody = map[string]any{"ok": true}

func accountOp(ctx context.Context, c gen.ClientWithResponsesInterface, in AccountIn) (any, error) {
	needs, known := accountNeeds[in.Op]
	if !known {
		return nil, fmt.Errorf("op must be one of %s", accountOps)
	}
	for _, f := range needs {
		if missing(in, f) {
			return nil, toolError(fmt.Sprintf("%s needs %s.", in.Op, f))
		}
	}
	switch in.Op {
	case "enable", "disable":
		return ok200(c.UpdateAccountWithResponse(ctx, in.ID, gen.AccountUpdate{Enabled: new(in.Op == "enable")}))
	case "set_priority":
		return ok200(c.UpdateAccountWithResponse(ctx, in.ID, gen.AccountUpdate{Priority: in.Priority}))
	case "reset_cooldown":
		return okBody, done(c.ResetAccountCooldownWithResponse(ctx, in.ID))
	case "refresh_quota":
		r, err := c.RefreshQuotaWithResponse(ctx, gen.QuotaRefresh{AccountId: opt(in.ID), Provider: opt(in.Provider)})
		if err != nil {
			return nil, err
		}
		return check(r.StatusCode(), r.JSON202, r.JSONDefault, nil)
	case "login_start":
		r, err := c.StartAccountLoginWithResponse(ctx, gen.AccountLoginStart{Provider: in.Provider})
		if err != nil {
			return nil, err
		}
		return check(r.StatusCode(), r.JSON201, r.JSONDefault, nil)
	case "login_complete":
		return okBody, done(c.CompleteAccountLoginWithResponse(ctx, in.ID, gen.AccountLoginComplete{RedirectUrl: in.RedirectURL}))
	case "login_cancel":
		return okBody, done(c.CancelAccountLoginWithResponse(ctx, in.ID))
	default: // import
		r, err := c.ImportAccountWithResponse(ctx, gen.AccountImport{Provider: in.Provider, Credential: in.Credential})
		if err != nil {
			return nil, err
		}
		return check(r.StatusCode(), r.JSON201, r.JSONDefault, nil)
	}
}
