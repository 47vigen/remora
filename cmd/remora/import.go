package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/importer/omniroute"
)

const importUsage = "usage: remora import omniroute <data-dir> [--yes]"

const codexWarning = "Codex refresh tokens are single-use: stop the old gateway before remora refreshes them."

// importTarget is what `remora import` needs from a running remora.
type importTarget interface {
	Plan(ctx context.Context, doc []byte, sec config.Secrets) ([]gen.Change, error)
	Apply(ctx context.Context, doc []byte, sec config.Secrets) ([]gen.Change, error)
	ImportAccount(ctx context.Context, c omniroute.Credential) error
	Models(ctx context.Context, provider string) ([]string, error)
}

// dialImport connects to the running remora the way export and apply do, from the environment: REMORA_URL (else
// the port of REMORA_LISTEN) and REMORA_TOKEN, else REMORA_ADMIN_PASSWORD.
func dialImport(ctx context.Context, env func(string) (string, bool)) (importTarget, error) {
	c, err := connFlags{}.dial(ctx, env)
	if err != nil {
		return nil, err
	}
	return apiTarget{c}, nil
}

// runImport implements `remora import omniroute <data-dir> [--yes]`: it reads OmniRoute's database read-only,
// converts it, and shows what the running remora would change. With --yes it applies the document (never pruning)
// and imports the OAuth accounts.
func runImport(ctx context.Context, args []string, stdout io.Writer, env func(string) (string, bool)) error {
	if len(args) == 0 || args[0] != "omniroute" {
		return errors.New(importUsage)
	}
	var dir string
	yes := false
	for _, a := range args[1:] {
		switch {
		case a == "--yes" || a == "-yes":
			yes = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %s\n%s", a, importUsage)
		case dir == "":
			dir = a
		default:
			return errors.New(importUsage)
		}
	}
	if dir == "" {
		return errors.New(importUsage)
	}

	snap, err := omniroute.Read(ctx, dir, env, time.Now())
	if err != nil {
		return err
	}
	res := omniroute.Convert(snap, time.Now())
	for _, w := range res.Warnings {
		_, _ = fmt.Fprintf(stdout, "warning: %s\n", w)
	}
	doc, err := config.Marshal(res.Doc)
	if err != nil {
		return err
	}

	target, err := dialImport(ctx, env)
	if err != nil {
		return err
	}
	changes, err := target.Plan(ctx, doc, res.Secrets)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		_, _ = fmt.Fprintln(stdout, "No changes to providers, aliases or keys.")
	}
	printChanges(stdout, changes)
	printAccounts(stdout, res.Credentials)
	if !yes {
		warnUnserved(ctx, stdout, target, res)
		_, _ = fmt.Fprintln(stdout, "Run again with --yes to import.")
		return nil
	}

	if len(changes) > 0 {
		applied, err := target.Apply(ctx, doc, res.Secrets)
		_, _ = fmt.Fprintf(stdout, "Applied %d changes.\n", len(applied))
		if err != nil {
			return err
		}
	}
	for _, c := range res.Credentials {
		if err := target.ImportAccount(ctx, c); err != nil {
			return fmt.Errorf("import %s account %s: %w", c.Provider, accountName(c), err)
		}
		_, _ = fmt.Fprintf(stdout, "Imported %s account %s.\n", c.Provider, accountName(c))
	}
	warnUnserved(ctx, stdout, target, res)
	return nil
}

// warnUnserved warns about each alias member on an imported subscription provider whose accounts do not serve its
// model, which the engine refuses at request time. A provider whose accounts list no model is not checked.
func warnUnserved(ctx context.Context, w io.Writer, t importTarget, res omniroute.Result) {
	served := map[string][]string{}
	for _, c := range res.Credentials {
		if _, ok := served[c.Provider]; ok {
			continue
		}
		models, err := t.Models(ctx, c.Provider)
		if err != nil {
			_, _ = fmt.Fprintf(w, "warning: could not list the models of the %s accounts: %v\n", c.Provider, err)
		}
		served[c.Provider] = models
	}
	for _, a := range res.Doc.Aliases {
		for _, m := range a.Members {
			p, model, ok := strings.Cut(m, "/")
			base, _, _ := strings.Cut(model, "(") // a thinking budget suffix: model(8192)
			if !ok || len(served[p]) == 0 || slices.Contains(served[p], base) {
				continue
			}
			hint := ""
			if i := strings.LastIndex(base, "-"); i > 0 {
				if j := slices.IndexFunc(served[p], func(s string) bool { return strings.HasPrefix(s, base[:i+1]) }); j >= 0 {
					hint = fmt.Sprintf(", e.g. %s/%s(8192)", p, served[p][j])
				}
			}
			_, _ = fmt.Fprintf(w, "warning: alias %s: member %s: no %s account serves %s; point it at a served model%s\n",
				a.Name, m, p, base, hint)
		}
	}
}

func printAccounts(w io.Writer, creds []omniroute.Credential) {
	if len(creds) == 0 {
		return
	}
	names := make([]string, 0, len(creds))
	codex := false
	for _, c := range creds {
		names = append(names, c.Provider+" "+accountName(c))
		codex = codex || c.Provider == "codex"
	}
	if codex {
		_, _ = fmt.Fprintln(w, codexWarning)
	}
	_, _ = fmt.Fprintf(w, "%d accounts: %s\n", len(creds), strings.Join(names, ", "))
}

// accountName is the account's email, as its credential file is named "<provider>-<email>.json".
func accountName(c omniroute.Credential) string {
	n := strings.TrimSuffix(strings.TrimPrefix(c.FileName, c.Provider+"-"), ".json")
	if n == c.Provider {
		return "(no email)"
	}
	return n
}

// apiTarget is importTarget over the generated management API client.
type apiTarget struct{ c *gen.ClientWithResponses }

// Plan and Apply never prune: an import adds to what remora has.
func (t apiTarget) Plan(ctx context.Context, doc []byte, sec config.Secrets) ([]gen.Change, error) {
	return planConfig(ctx, t.c, configRequest(doc, sec, false))
}

func (t apiTarget) Apply(ctx context.Context, doc []byte, sec config.Secrets) ([]gen.Change, error) {
	return applyConfig(ctx, t.c, configRequest(doc, sec, false))
}

// Models returns the models the provider's accounts serve. shortcut: reads the first 200 accounts only, enough for
// an import; page through them if a provider ever has more.
func (t apiTarget) Models(ctx context.Context, provider string) ([]string, error) {
	limit := 200
	resp, err := t.c.ListAccountsWithResponse(ctx, &gen.ListAccountsParams{Provider: &provider, Limit: &limit})
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, apiError("account list", resp.HTTPResponse, resp.JSONDefault)
	}
	var models []string
	for _, a := range resp.JSON200.Items {
		for _, m := range a.Models {
			models = append(models, strings.TrimPrefix(m, provider+"/"))
		}
	}
	return models, nil
}

func (t apiTarget) ImportAccount(ctx context.Context, c omniroute.Credential) error {
	var cred map[string]any
	if err := json.Unmarshal(c.JSON, &cred); err != nil {
		return err
	}
	resp, err := t.c.ImportAccountWithResponse(ctx, gen.AccountImport{Provider: c.Provider, Credential: cred})
	if err != nil {
		return err
	}
	if resp.JSON201 == nil {
		return apiError("account import", resp.HTTPResponse, resp.JSONDefault)
	}
	return nil
}
