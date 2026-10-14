package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

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
}

// dialImport connects to the running remora. Plan 05 Task 14's API client (newClient, with --url and the admin
// password login) replaces importClient here once it exists.
func dialImport(_ context.Context, env func(string) (string, bool)) (importTarget, error) {
	c, err := importClient(env)
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
	doc, err := yaml.Marshal(res.Doc)
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
	printImportChanges(stdout, changes)
	printAccounts(stdout, res.Credentials)
	if !yes {
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
	return nil
}

// printImportChanges prints one line per change: "+ provider openrouter", "~ alias fast (members)", "- key old".
func printImportChanges(w io.Writer, cs []gen.Change) {
	for _, c := range cs {
		sign := map[gen.ChangeOp]string{gen.Create: "+", gen.Update: "~", gen.Delete: "-"}[c.Op]
		line := fmt.Sprintf("%s %s %s", sign, c.Kind, c.Name)
		if len(c.Fields) > 0 {
			line += " (" + strings.Join(c.Fields, ", ") + ")"
		}
		_, _ = fmt.Fprintln(w, line)
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

// importClient builds a management API client from the environment: REMORA_URL, else 127.0.0.1 on the port of
// REMORA_LISTEN (default :8080), with REMORA_TOKEN as the bearer token.
func importClient(env func(string) (string, bool)) (*gen.ClientWithResponses, error) {
	url, ok := env("REMORA_URL")
	if !ok || url == "" {
		listen, ok := env(config.EnvListen)
		if !ok || listen == "" {
			listen = config.DefaultListen
		}
		_, port, err := net.SplitHostPort(listen)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", config.EnvListen, err)
		}
		url = "http://127.0.0.1:" + port
	}
	token, ok := env("REMORA_TOKEN")
	if !ok || token == "" {
		return nil, errors.New("set REMORA_TOKEN (Settings → API tokens)")
	}
	return gen.NewClientWithResponses(url, gen.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+token)
		return nil
	}))
}

// apiTarget is importTarget over the generated management API client.
type apiTarget struct{ c *gen.ClientWithResponses }

func configRequest(doc []byte, sec config.Secrets) gen.ConfigRequest {
	prune := false
	m := map[string]string(sec)
	return gen.ConfigRequest{Document: string(doc), Prune: &prune, Secrets: &m}
}

func (t apiTarget) Plan(ctx context.Context, doc []byte, sec config.Secrets) ([]gen.Change, error) {
	resp, err := t.c.PlanConfigWithResponse(ctx, configRequest(doc, sec))
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, apiError("plan", resp.HTTPResponse, resp.JSONDefault)
	}
	return resp.JSON200.Changes, nil
}

func (t apiTarget) Apply(ctx context.Context, doc []byte, sec config.Secrets) ([]gen.Change, error) {
	resp, err := t.c.ApplyConfigWithResponse(ctx, configRequest(doc, sec))
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, apiError("apply", resp.HTTPResponse, resp.JSONDefault)
	}
	return resp.JSON200.Changes, nil
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

func apiError(what string, r *http.Response, e *gen.Error) error {
	if e != nil && e.Error.Message != "" {
		return fmt.Errorf("%s: %s", what, e.Error.Message)
	}
	status := "no response"
	if r != nil {
		status = r.Status
	}
	return fmt.Errorf("%s: %s", what, status)
}
