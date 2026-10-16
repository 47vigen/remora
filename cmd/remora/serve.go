package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/api"
	"github.com/47vigen/remora/internal/auth"
	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/cooling"
	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/proxy"
	"github.com/47vigen/remora/internal/reconcile"
	"github.com/47vigen/remora/internal/soniox"
	"github.com/47vigen/remora/internal/store"
	"github.com/47vigen/remora/internal/supervise"
	"github.com/47vigen/remora/internal/web"
)

const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second // closes a client's idle keep-alive connection
	shutdownTimeout   = 15 * time.Second
	engineStopTimeout = 45 * time.Second // covers both engines' 15s SIGTERM grace
	// sweepMargin keeps the speech sweeper off a job a live request still waits on.
	sweepMargin = 10 * time.Minute
)

// runServe implements `remora serve`.
func runServe(ctx context.Context, getenv func(string) string, stderr io.Writer) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	logger := newServeLogger(stderr, cfg.LogLevel)
	if err := serve(ctx, cfg, logger); err != nil {
		logger.Error("serve failed", "err", err)
		return 1
	}
	return 0
}

// newServeLogger logs JSON records to w through the same scrubber as /v1, so engine names and keys in an error
// chain (a failed reconcile, say) never reach the operator's log. slog writes each record in one Write.
func newServeLogger(w io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(slog.NewJSONHandler(scrubWriter{w}, &slog.HandlerOptions{Level: level}))
}

// serve prepares the data directory, the store and the engine credentials, starts the engines and then serves the
// public listener until ctx ends. It shuts down the listener, then the engines, then the store.
func serve(ctx context.Context, cfg config.Config, logger *slog.Logger) (err error) {
	cfg.MemoryLimit = supervise.MemoryLimit(os.ReadFile)
	if n := selfMemoryLimit(cfg.MemoryLimit, os.Getenv); n > 0 {
		debug.SetMemoryLimit(n)
	}
	if err := prepareDataDir(cfg.DataDir); err != nil {
		return fmt.Errorf("prepare data dir: %w", err)
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

	pw, err := auth.EnsureAdmin(ctx, st, cfg.AdminPassword)
	if err != nil {
		return fmt.Errorf("ensure admin: %w", err)
	}
	if pw != "" {
		logger.Log(ctx, passwordLogLevel(ctx, logger), "admin password generated; it is shown only this once", "password", pw)
	}

	// Secrets at rest: load (or create) the master key before anything else needs the sealer.
	if err := setSealer(st, cfg); err != nil {
		return fmt.Errorf("master key: %w", err)
	}

	creds, err := supervise.EnsureCredentials(ctx, st)
	if err != nil {
		return fmt.Errorf("ensure engine credentials: %w", err)
	}
	cliproxy, err := supervise.CLIProxyAPIEngine(cfg, creds)
	if err != nil {
		return fmt.Errorf("render engine config: %w", err)
	}
	frontDoorEngine, err := supervise.BifrostEngine(cfg, creds)
	if err != nil {
		return fmt.Errorf("render engine config: %w", err)
	}

	versions, err := supervise.LoadVersions(filepath.Join(cfg.EngineDir, "versions.json"))
	if err != nil {
		return fmt.Errorf("load engine versions: %w", err)
	}

	// The speech adapter listens before the engines start, since the front door sends transcriptions to it. It
	// stops after the engines, and its sweeper before the store closes.
	stopSpeech, err := startSpeech(ctx, cfg, st, logger)
	if err != nil {
		return err
	}
	defer stopSpeech()

	sup := supervise.New(logger, versions, supervise.Options{})
	defer func() {
		// ctx is already done here; the engines get their own deadline.
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), engineStopTimeout)
		defer cancel()
		if serr := sup.Stop(sctx); serr != nil {
			err = errors.Join(err, fmt.Errorf("stop engines: %w", serr))
		}
	}()
	// The OAuth engine starts first because the front door calls it; it stops last.
	if err := sup.Start(ctx, cliproxy, frontDoorEngine); err != nil {
		return fmt.Errorf("start engines: %w", err)
	}

	// Reconcile the front door now that it is healthy, before the listener opens, so /v1 never meets an engine that
	// lags the store (a fresh or wiped engine data directory). A failure is logged, not fatal: the timer retries,
	// and /v1 stays closed (503) until a pass succeeds.
	// The OAuth engine is reconciled in the same pass, before the front door: its settings and accounts first.
	// Cooling leaves failing alias members out of the compiled rules; a change recompiles through Sync.
	accts := accounts.New(accountsEngine(cfg, creds), st, logger)
	fd := frontDoor(cfg, creds)
	rec := newReconciler(cfg, st, fd, logger)
	withOAuth(rec, cfg, creds.CLIProxyAPIKey)
	rec.Also = []func(context.Context) error{accts.Reconcile}
	cooler := cooling.New(cooling.Config{
		Threshold: cfg.Cooling.Count429, Window: cfg.Cooling.Window429, Default: cfg.Cooling.Default,
		SubscriptionDefault: cfg.Cooling.SubscriptionDefault,
	}, rec.Sync, nil, logger)
	rec.Filter = cooler.Filter
	if err := rec.SyncAll(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logger.Error("initial reconcile failed; retrying on the timer", "err", err)
	}
	// Then reconcile on a timer and follow the front door's log for cooling. These loops end before the engines stop
	// and the store closes. Cooling is started before the listener opens: it skips log rows older than its start,
	// which the tail replays from before a restart.
	rctx, rcancel := context.WithCancel(ctx)
	var loops sync.WaitGroup
	loops.Go(func() { rec.Run(rctx) })
	// Cooling only uses failures, so its tail asks for error rows, plus the requests still running so one that outlives
	// the lookback and then fails is still followed; reading every row re-read the whole window, bodies included,
	// every poll. Live log viewers get their own tail, which polls only while one is open.
	coolTail := bifrost.NewTail(fd, cfg.Cooling.Poll, []string{"error", "processing"}, logger)
	liveTail := bifrost.NewTail(fd, cfg.Cooling.Poll, nil, logger)
	loops.Go(func() { coolTail.Run(rctx) })
	loops.Go(func() { liveTail.Run(rctx) })
	loops.Go(func() { runCooling(rctx, cooler, coolTail, fd, logger) })
	defer func() {
		rcancel()
		loops.Wait()
	}()

	upstream, err := url.Parse(frontDoorURL(cfg))
	if err != nil {
		return fmt.Errorf("front door url: %w", err)
	}
	mux := http.NewServeMux()
	api.RegisterHealthz(mux, sup)
	mgmt := api.New(api.Deps{
		Providers: api.NewProviders(st, rec, fd, soniox.NewClient(cfg.SonioxBaseURL, &http.Client{})),
		Accounts:  api.NewAccounts(accts),
		Aliases:   api.NewAliases(st, rec, cooler.Snapshot),
		Keys:      api.NewKeys(st, rec),
		Batch:     rec.Batch,
		System:    api.NewSystem(sup),
		Names:     api.NewNames(st),
		Logs:      fd,
		Tail:      liveTail,
		Auth:      auth.NewService(st, nil),
		Log:       logger,
	})
	mux.Handle("/api/", mgmt.Handler())
	// /v1 answers 503 until a Sync has completed, so a failed first pass never serves a stale front door; the
	// management API and health stay reachable meanwhile.
	px := proxy.New(upstream, st, logger)
	px.Ready = rec.Synced
	// The OAuth engine's members are served as "remora-oauth/<name>/<model>"; clients see "<name>/<model>".
	px.OnResponse(proxy.ServedByHook(cliproxyapi.UserRef))
	mux.Handle("/v1/", px)
	// Everything else is the web console.
	mux.Handle("/", web.Handler())
	stopPprof, _, err := startPprof(ctx, cfg.PprofAddr, logger)
	if err != nil {
		return err
	}
	defer stopPprof()
	srv := newServer(ctx, cfg.Listen, mux)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}
	logger.Info("remora listening", "addr", ln.Addr().String())

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shut down http: %w", err)
	}
	return nil
}

// selfMemoryLimit is the soft memory limit remora sets for itself: its share of the container's limit, or 0 when
// there is no limit or the operator set GOMEMLIMIT, which the runtime already applied.
func selfMemoryLimit(limit int64, getenv func(string) string) int64 {
	if limit <= 0 || getenv("GOMEMLIMIT") != "" {
		return 0
	}
	return int64(float64(limit) * supervise.MemoryShares.Self)
}

// newReconciler is serve's reconciler: the store, the front door and the speech adapter it registers.
func newReconciler(cfg config.Config, st *store.Store, engine reconcile.Engine, logger *slog.Logger) *reconcile.Reconciler {
	return &reconcile.Reconciler{
		Store:          st,
		Engine:         engine,
		Log:            logger,
		SonioxURL:      "http://" + cfg.SonioxAddr,
		SonioxDeadline: cfg.SonioxDeadline,
	}
}

// startSpeech serves the speech-to-text adapter on its loopback address and runs the sweeper of leftover upstream
// files and transcriptions. The returned stop shuts the listener down (waiting up to shutdownTimeout for running
// jobs, then closing it) and waits for the sweeper to end.
func startSpeech(ctx context.Context, cfg config.Config, st *store.Store, logger *slog.Logger) (stop func(), err error) {
	log := logger.With("component", "speech")
	srv := newServer(ctx, cfg.SonioxAddr, soniox.New(soniox.Config{
		BaseURL:      cfg.SonioxBaseURL,
		PollInterval: cfg.SonioxPollInterval,
		Deadline:     cfg.SonioxDeadline,
		Logger:       log,
	}))
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.SonioxAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", cfg.SonioxAddr, err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("speech adapter stopped", "err", err)
		}
	}()

	sweeper := &soniox.Sweeper{
		Client: soniox.NewClient(cfg.SonioxBaseURL, &http.Client{}),
		Keys: func(ctx context.Context) ([]string, error) {
			return st.ProviderKeysByKind(ctx, store.KindSoniox)
		},
		MaxAge: max(soniox.DefaultMaxAge, cfg.SonioxDeadline+sweepMargin),
		Logger: log,
	}
	sctx, scancel := context.WithCancel(ctx)
	swept := make(chan struct{})
	go func() {
		defer close(swept)
		sweeper.Run(sctx, cfg.SonioxSweepInterval)
	}()

	return func() {
		scancel()
		<-swept
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(dctx); err != nil {
			log.Warn("speech adapter did not drain; closing it", "err", err)
			_ = srv.Close()
		}
		<-served
	}, nil
}

const (
	// coolingFeedBuffer is how many log rows the cooling subscription's channel holds.
	coolingFeedBuffer = 1024
	// coolingFeedBacklog is how many more rows the tail keeps for the cooling subscription before it drops it, so a
	// burst of failures waits for the cooler instead of being lost.
	coolingFeedBacklog = 64 * 1024
)

// runCooling feeds the front door's log rows to the cooler until ctx ends, subscribing again when the tail drops a
// subscription that fell behind.
func runCooling(ctx context.Context, cooler *cooling.Cooler, tail *bifrost.Tail, fd *bifrost.Client, logger *slog.Logger) {
	for {
		rows, cancel := tail.Subscribe(coolingFeedBuffer, coolingFeedBacklog)
		err := cooler.Run(ctx, rows, fd.GetLog, 0)
		cancel()
		if ctx.Err() != nil {
			return
		}
		logger.Warn("cooling: log feed ended; subscribing again", "err", err)
	}
}

// passwordLogLevel is the level of the one-time admin password line: warn, raised to error when the configured
// level would hide a warning, so the only copy of the password is never filtered out.
func passwordLogLevel(ctx context.Context, logger *slog.Logger) slog.Level {
	if logger.Enabled(ctx, slog.LevelWarn) {
		return slog.LevelWarn
	}
	return slog.LevelError
}

// newServer is the public listener's server. It has no ReadTimeout or WriteTimeout: /v1 streams answers for as
// long as the upstream takes, and uploads may be large. ReadHeaderTimeout still bounds a slow client's headers.
func newServer(ctx context.Context, addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
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

// accountsURL is the OAuth engine's loopback base URL.
func accountsURL(cfg config.Config) string {
	return "http://127.0.0.1:" + strconv.Itoa(cfg.Ports.CLIProxyAPI)
}

// withOAuth registers the OAuth engine behind the front door: its internal provider with clientKey, one routing
// rule per subscription provider, and the subscription provider names as reserved alias member providers.
func withOAuth(r *reconcile.Reconciler, cfg config.Config, clientKey string) {
	r.Internal = append(r.Internal, reconcile.OAuthProvider(clientKey, accountsURL(cfg)))
	r.InternalRules = reconcile.OAuthRules()
	r.Reserved = reconcile.OAuthReserved()
}

// managementTimeout bounds one management call. A reconcile pass holds the store's write lock, so a stuck engine
// must not hold it for long.
const managementTimeout = 30 * time.Second

// frontDoor is the front door's management client. Its transport ignores proxy environment variables: the engine is
// on loopback.
func frontDoor(cfg config.Config, c supervise.Credentials) *bifrost.Client {
	return bifrost.New(frontDoorURL(cfg), c.BifrostAdminUser, c.BifrostAdminPassword, loopbackClient())
}

// accountsEngine is the OAuth engine's management client, on loopback like frontDoor.
func accountsEngine(cfg config.Config, c supervise.Credentials) *cliproxyapi.Client {
	return cliproxyapi.New(accountsURL(cfg), c.CLIProxyManagementSecret, loopbackClient())
}

// loopbackClient is an HTTP client for an engine's management API: it ignores proxy environment variables and
// bounds each call by managementTimeout.
func loopbackClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &http.Client{Transport: tr, Timeout: managementTimeout}
}

// scrubWriter replaces engine names and keys in everything written through it.
type scrubWriter struct{ w io.Writer }

func (s scrubWriter) Write(p []byte) (int, error) {
	if _, err := s.w.Write(proxy.Scrub(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}
