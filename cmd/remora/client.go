package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/config"
)

// Environment variables the API commands (export, apply, import) read.
const (
	envURL   = "REMORA_URL"
	envToken = "REMORA_TOKEN" //nolint:gosec // G101: a variable name, not a credential.
)

// errHelp is returned when a command's -h or --help printed its usage; the command exits 0.
var errHelp = flag.ErrHelp

// clientTimeout bounds one management API call.
const clientTimeout = 2 * time.Minute

// newClient returns a management API client for the remora at url. A token is sent as the bearer token; without
// one, password signs in as the admin and the session cookie is kept in a jar; with neither it is an error.
func newClient(ctx context.Context, url, token, password string) (*gen.ClientWithResponses, error) {
	url = strings.TrimSuffix(url, "/")
	httpc := &http.Client{Timeout: clientTimeout}
	switch {
	case token != "":
		return gen.NewClientWithResponses(url, gen.WithHTTPClient(httpc), gen.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+token)
			return nil
		}))
	case password != "":
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		httpc.Jar = jar
		c, err := gen.NewClientWithResponses(url, gen.WithHTTPClient(httpc))
		if err != nil {
			return nil, err
		}
		resp, err := c.LoginWithResponse(ctx, gen.LoginJSONRequestBody{Password: password})
		if err != nil {
			return nil, fmt.Errorf("sign in: %w", err)
		}
		if resp.StatusCode() != http.StatusNoContent {
			return nil, apiError("sign in with "+config.EnvAdminPassword, resp.HTTPResponse, resp.JSONDefault)
		}
		return c, nil
	default:
		return nil, errors.New("set REMORA_TOKEN (Settings → API tokens)")
	}
}

// clientURL is the remora to talk to: the --url flag, else REMORA_URL, else 127.0.0.1 on the port of
// REMORA_LISTEN (default :8080).
func clientURL(flagURL string, env func(string) (string, bool)) (string, error) {
	if flagURL != "" {
		return flagURL, nil
	}
	if u, ok := env(envURL); ok && u != "" {
		return u, nil
	}
	listen, ok := env(config.EnvListen)
	if !ok || listen == "" {
		listen = config.DefaultListen
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("%s: %w", config.EnvListen, err)
	}
	return "http://127.0.0.1:" + port, nil
}

// connFlags are the connection flags every API command takes.
type connFlags struct{ url, token string }

func (c *connFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.url, "url", "", "remora's base URL (default $REMORA_URL, else http://127.0.0.1 on the port of $REMORA_LISTEN)")
	fs.StringVar(&c.token, "token", "", "an API token (default $REMORA_TOKEN; else $REMORA_ADMIN_PASSWORD signs in)")
}

// dial connects to remora with the flags, falling back to the environment.
func (c connFlags) dial(ctx context.Context, env func(string) (string, bool)) (*gen.ClientWithResponses, error) {
	url, err := clientURL(c.url, env)
	if err != nil {
		return nil, err
	}
	token := c.token
	if token == "" {
		token, _ = env(envToken)
	}
	password, _ := env(config.EnvAdminPassword)
	return newClient(ctx, url, token, password)
}

// printChanges prints one line per change: "+ provider openrouter", "~ alias fast (members)", "- key old".
func printChanges(w io.Writer, cs []gen.Change) {
	for _, c := range cs {
		sign := map[gen.ChangeOp]string{gen.Create: "+", gen.Update: "~", gen.Delete: "-"}[c.Op]
		line := fmt.Sprintf("%s %s %s", sign, c.Kind, c.Name)
		if len(c.Fields) > 0 {
			line += " (" + strings.Join(c.Fields, ", ") + ")"
		}
		_, _ = fmt.Fprintln(w, line)
	}
}

// printGenerated prints each client key value apply generated, which is shown only this once.
func printGenerated(w io.Writer, cs []gen.Change) {
	for _, c := range cs {
		if c.Kind == gen.ChangeKindKey && c.Value != nil && *c.Value != "" {
			_, _ = fmt.Fprintf(w, "%s: %s\n", c.Name, *c.Value)
		}
	}
}

// configRequest is a plan or apply request body.
func configRequest(doc []byte, sec config.Secrets, prune bool) gen.ConfigRequest {
	m := map[string]string(sec)
	if m == nil {
		m = map[string]string{}
	}
	return gen.ConfigRequest{Document: string(doc), Prune: &prune, Secrets: &m}
}

func planConfig(ctx context.Context, c *gen.ClientWithResponses, req gen.ConfigRequest) ([]gen.Change, error) {
	resp, err := c.PlanConfigWithResponse(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, apiError("plan", resp.HTTPResponse, resp.JSONDefault)
	}
	return resp.JSON200.Changes, nil
}

// applyConfig applies a document. A failed apply changes nothing; an earlier remora that applied change by change
// lists the changes made before the failure, their generated key values included, and those are returned with the
// error.
func applyConfig(ctx context.Context, c *gen.ClientWithResponses, req gen.ConfigRequest) ([]gen.Change, error) {
	resp, err := c.ApplyConfigWithResponse(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		var applied []gen.Change
		if resp.JSONDefault != nil && resp.JSONDefault.Error.Applied != nil {
			applied = *resp.JSONDefault.Error.Applied
		}
		return applied, apiError("apply", resp.HTTPResponse, resp.JSONDefault)
	}
	return resp.JSON200.Changes, nil
}

// apiError reports a failed API call with remora's error message, else the HTTP status.
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
