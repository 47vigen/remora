package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/reconcile"
	"github.com/47vigen/remora/internal/store"
	"github.com/47vigen/remora/internal/supervise"
)

const applyUsage = "usage: remora apply -f <remora.yaml>"

// runApply implements `remora apply -f <file>`: it makes the store match the file and reconciles the front door. It
// runs as a second process next to `remora serve`, on the same data directory and engines.
func runApply(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("f", "", "the remora.yaml to apply")
	if err := fs.Parse(args); err != nil || *path == "" || fs.NArg() != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, applyUsage)
		}
		return 2
	}

	cfg, err := config.Load(getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if err := apply(ctx, cfg, *path, getenv, stdout, logger); err != nil {
		_, _ = fmt.Fprintf(stderr, "remora apply: %v\n", err)
		return 1
	}
	return 0
}

func apply(ctx context.Context, cfg config.Config, path string, getenv func(string) string, out io.Writer, logger *slog.Logger) (err error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: the operator names the file to apply.
	if err != nil {
		return err
	}
	f, err := config.Parse(b)
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, cfg.DBPath())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close store: %w", cerr))
		}
	}()
	// The engine credentials are made by `remora serve`; apply only reads them.
	user, err := engineValue(ctx, st, "admin_username")
	if err != nil {
		return err
	}
	password, err := engineValue(ctx, st, "admin_password")
	if err != nil {
		return err
	}
	if err := setSealer(st, cfg); err != nil {
		return err
	}
	r := &reconcile.Reconciler{
		Store:  st,
		Engine: frontDoor(cfg, supervise.Credentials{BifrostAdminUser: user, BifrostAdminPassword: password}),
		Log:    logger,
	}
	return applyFile(ctx, r, f, getenv, out)
}

func engineValue(ctx context.Context, st *store.Store, key string) (string, error) {
	v, ok, err := st.EngineValue(ctx, "bifrost", key)
	if err != nil {
		return "", err
	}
	if !ok || v == "" {
		return "", errors.New("no engine credentials in the data directory: start `remora serve` first")
	}
	return v, nil
}

// setSealer loads (or creates) the master key and gives the store its sealer.
func setSealer(st *store.Store, cfg config.Config) error {
	master, err := store.LoadMasterKey(cfg.DataDir, cfg.MasterKey)
	if err != nil {
		return err
	}
	sl, err := store.NewSealer(master)
	if err != nil {
		return err
	}
	st.SetSealer(sl)
	return nil
}

// frontDoorURL is the front door's loopback base URL.
func frontDoorURL(cfg config.Config) string {
	return "http://127.0.0.1:" + strconv.Itoa(cfg.Ports.Bifrost)
}

// managementTimeout bounds one management call. A reconcile pass holds the store's write lock, so a stuck engine
// must not hold it for long.
const managementTimeout = 30 * time.Second

// frontDoor is the front door's management client. Its transport ignores proxy environment variables: the engine is
// on loopback.
func frontDoor(cfg config.Config, c supervise.Credentials) *bifrost.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return bifrost.New(frontDoorURL(cfg), c.BifrostAdminUser, c.BifrostAdminPassword,
		&http.Client{Transport: tr, Timeout: managementTimeout})
}

// applyFile makes the store match f in one reconcile.Change, so the engine matches too or nothing is written.
// Provider keys are matched by provider and name, client keys by name; what the file does not name is deleted. A
// new client key without a value gets a generated one, printed to out as "<name>: <value>" once the change has
// committed. Secrets are resolved through getenv before anything is written.
func applyFile(ctx context.Context, r *reconcile.Reconciler, f config.File, getenv func(string) string, out io.Writer) error {
	if err := f.Validate(); err != nil {
		return err
	}
	providerSecrets := make([][]string, len(f.Providers))
	for i, p := range f.Providers {
		providerSecrets[i] = make([]string, len(p.Keys))
		for j, k := range p.Keys {
			v, err := resolveSecret(getenv, fmt.Sprintf("providers[%d].keys[%d].value", i, j), k.Value)
			if err != nil {
				return err
			}
			providerSecrets[i][j] = v
		}
	}
	clientValues := make([]string, len(f.Keys))
	for i, k := range f.Keys {
		if k.Value == "" {
			continue
		}
		v, err := resolveSecret(getenv, fmt.Sprintf("keys[%d].value", i), k.Value)
		if err != nil {
			return err
		}
		clientValues[i] = v
	}

	var generated []string // "<name>: <value>" lines, printed after the commit
	err := r.Change(ctx, func(ctx context.Context, tx *store.Tx) error {
		generated = generated[:0]
		if err := removeUnlisted(ctx, tx, f); err != nil {
			return err
		}
		if err := putProviders(ctx, tx, f, providerSecrets); err != nil {
			return err
		}
		for _, a := range f.Aliases {
			members := make([]alias.Member, len(a.Members))
			for i, m := range a.Members {
				members[i] = alias.Member{Ref: m}
			}
			if err := tx.PutAlias(ctx, alias.Alias{
				Name: a.Name, Kind: alias.Kind(a.Kind), Members: members,
				Capabilities: a.Capabilities, ContextWindow: a.ContextWindow,
			}); err != nil {
				return err
			}
		}
		existing, err := tx.ClientKeys(ctx)
		if err != nil {
			return err
		}
		ids := map[string]string{}
		for _, k := range existing {
			ids[k.Name] = k.ID
		}
		for i, k := range f.Keys {
			value := clientValues[i]
			id := ids[k.Name]
			if id == "" && value == "" {
				value = store.GenerateKeyValue()
				generated = append(generated, k.Name+": "+value)
			}
			enabled := k.Enabled == nil || *k.Enabled
			if _, err := tx.PutClientKey(ctx, store.ClientKey{ID: id, Name: k.Name, Allow: k.Allow, Enabled: enabled}, value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, line := range generated {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

// resolveSecret reads an "env:NAME" reference through getenv. Errors name the field.
func resolveSecret(getenv func(string) string, field, ref string) (string, error) {
	name, err := config.SecretRef(ref)
	if err != nil {
		return "", fmt.Errorf("%s: %w", field, err)
	}
	v := getenv(name)
	if v == "" {
		return "", fmt.Errorf("%s: environment variable %s is not set", field, name)
	}
	return v, nil
}

// removeUnlisted deletes the aliases, client keys, provider keys and providers the file does not name.
func removeUnlisted(ctx context.Context, tx *store.Tx, f config.File) error {
	wantAlias := map[string]bool{}
	for _, a := range f.Aliases {
		wantAlias[a.Name] = true
	}
	aliases, err := tx.Aliases(ctx)
	if err != nil {
		return err
	}
	for name := range aliases {
		if !wantAlias[name] {
			if err := tx.DeleteAlias(ctx, name); err != nil {
				return err
			}
		}
	}

	wantClient := map[string]bool{}
	for _, k := range f.Keys {
		wantClient[k.Name] = true
	}
	clients, err := tx.ClientKeys(ctx)
	if err != nil {
		return err
	}
	for _, k := range clients {
		if !wantClient[k.Name] {
			if err := tx.DeleteClientKey(ctx, k.ID); err != nil {
				return err
			}
		}
	}

	wantKey := map[[2]string]bool{}
	wantProvider := map[string]bool{}
	for _, p := range f.Providers {
		wantProvider[p.Name] = true
		for _, k := range p.Keys {
			wantKey[[2]string{p.Name, k.Name}] = true
		}
	}
	keys, err := tx.ProviderKeys(ctx)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if !wantKey[[2]string{k.Provider, k.Name}] {
			if err := tx.DeleteProviderKey(ctx, k.ID); err != nil {
				return err
			}
		}
	}
	providers, err := tx.Providers(ctx)
	if err != nil {
		return err
	}
	for _, p := range providers {
		if !wantProvider[p.Name] {
			if err := tx.DeleteProvider(ctx, p.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// putProviders upserts the file's providers and their keys, keeping the id of a key already stored under the same
// provider and name.
func putProviders(ctx context.Context, tx *store.Tx, f config.File, secrets [][]string) error {
	keys, err := tx.ProviderKeys(ctx)
	if err != nil {
		return err
	}
	ids := map[[2]string]string{}
	for _, k := range keys {
		ids[[2]string{k.Provider, k.Name}] = k.ID
	}
	for i, p := range f.Providers {
		if err := tx.PutProvider(ctx, store.Provider{
			Name: p.Name, Kind: store.ProviderKind(p.Kind), Type: p.Type,
			BaseURL: p.BaseURL, AllowPrivateNetwork: p.AllowPrivateNetwork,
		}); err != nil {
			return err
		}
		for j, k := range p.Keys {
			if err := tx.PutProviderKey(ctx, store.ProviderKey{
				ID: ids[[2]string{p.Name, k.Name}], Provider: p.Name, Name: k.Name,
				Secret: secrets[i][j], Enabled: true,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
