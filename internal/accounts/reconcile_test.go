package accounts

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/supervise"
)

const testClientKey = "client-key-1"

func (e *env) setClientKey(t *testing.T) {
	t.Helper()
	if err := e.store.SetEngineValue(context.Background(), "cliproxyapi", "api_key", testClientKey); err != nil {
		t.Fatalf("SetEngineValue: %v", err)
	}
}

func (e *env) reconcile(t *testing.T) {
	t.Helper()
	if err := e.svc.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
}

func decodePatch(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode patch %s: %v", raw, err)
	}
	return m
}

// at returns the value at a dotted path of a decoded JSON object.
func at(m map[string]any, path string) any {
	var v any = m
	for _, k := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = obj[k]
	}
	return v
}

// oldSettings is what remora wrote before the request-retry, streaming and connection-pool keys existed.
func oldSettings() map[string]any {
	return map[string]any{
		"access": map[string]any{"api-keys": []any{testClientKey}},
		"routing": map[string]any{
			"strategy":           "round-robin",
			"force-model-prefix": true,
			"cooldown":           map[string]any{"save-cooldown-status": true},
		},
		"upstream": map[string]any{"codex": map[string]any{"model-level-cooling": true}},
	}
}

func wantSettings(t *testing.T, m map[string]any) {
	t.Helper()
	for path, want := range map[string]any{
		"access.api-keys":                                   []any{testClientKey},
		"routing.strategy":                                  "round-robin",
		"routing.force-model-prefix":                        true,
		"routing.cooldown.save-cooldown-status":             true,
		"routing.cooldown.transient-error-cooldown-seconds": 5.0,
		"routing.session-affinity":                          true,
		"routing.session-affinity-ttl":                      "1h",
		"routing.session-affinity-rebind-on":                "quota",
		"routing.retry.request-retry":                       1.0,
		"routing.retry.max-retry-interval":                  10.0,
		"requests.streaming.bootstrap-retries":              1.0,
		"requests.streaming.keepalive-seconds":              15.0,
		"oauth.providers.antigravity.connection-pool":       map[string]any{"enabled": true, "idle-conn-timeout": "90s", "max-idle-conns-per-host": 8.0},
		"upstream.codex.model-level-cooling":                true,
		"routing.provider-strategies":                       jsonRoundTrip(map[string]any{"x": EngineStrategies(nil)})["x"],
	} {
		if got := at(m, path); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", path, got, want)
		}
	}
}

func TestReconcilePatchesSettings(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	e.reconcile(t)
	patches := e.fake.ConfigPatches()
	if len(patches) == 0 {
		t.Fatal("no PATCH /config")
	}
	wantSettings(t, decodePatch(t, patches[len(patches)-1]))
}

func TestReconcileReappliesSettingsWithChange(t *testing.T) {
	// The engine answers before its config watcher runs, and a configuration write in that window is saved but not
	// applied; writing the same settings again changes nothing on disk. So remora writes them once more, through a
	// different file, on a pass well after the first write.
	e := newEnv(t)
	e.setClientKey(t)
	e.reconcile(t)
	if n := len(e.fake.ConfigPatches()); n != 1 {
		t.Fatalf("first pass: %d patches, want 1", n)
	}
	e.reconcile(t)
	if n := len(e.fake.ConfigPatches()); n != 1 {
		t.Fatalf("pass right after the write: %d patches, want still 1", n)
	}

	e.clock.advance(settleDelay)
	e.reconcile(t)
	patches := e.fake.ConfigPatches()
	if len(patches) != 3 {
		t.Fatalf("settling pass: %d patches, want 3", len(patches))
	}
	if string(patches[1]) == string(patches[2]) {
		t.Error("the settling pass wrote the same settings twice; the file would not change")
	}
	if keys := at(decodePatch(t, patches[1]), "access.api-keys"); !reflect.DeepEqual(keys, []any{testClientKey, testClientKey}) {
		t.Errorf("interim access.api-keys = %#v, want the client key twice (the same access, a different file)", keys)
	}
	wantSettings(t, decodePatch(t, patches[2]))
	raw, err := cliproxyapi.New(e.fake.URL, testSecret, nil).Config(context.Background())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	wantSettings(t, decodePatch(t, raw))

	e.clock.advance(time.Hour)
	e.reconcile(t)
	if n := len(e.fake.ConfigPatches()); n != 3 {
		t.Errorf("settled pass: %d patches, want still 3", n)
	}
}

func TestReconcileSettingsPresentAtStart(t *testing.T) {
	// Settings the engine loaded from its file at start are applied; there is nothing to write.
	e := newEnv(t)
	e.setClientKey(t)
	if err := cliproxyapi.New(e.fake.URL, testSecret, nil).PatchConfig(context.Background(), settingsPatch(testClientKey, false, EngineStrategies(nil))); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}
	e.reconcile(t)
	e.clock.advance(time.Hour)
	e.reconcile(t)
	if n := len(e.fake.ConfigPatches()); n != 1 {
		t.Errorf("%d patches, want only the test's own", n)
	}
}

// TestReconcileAddsNewSettings: a node upgraded from a remora that wrote only the old settings gets the new ones
// through the management API, without an engine restart.
func TestReconcileAddsNewSettings(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	if err := cliproxyapi.New(e.fake.URL, testSecret, nil).PatchConfig(context.Background(), oldSettings()); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}
	e.reconcile(t)
	patches := e.fake.ConfigPatches()
	if len(patches) != 2 {
		t.Fatalf("%d patches, want the test's own and one from Reconcile", len(patches))
	}
	wantSettings(t, decodePatch(t, patches[1]))
}

// TestSettingsPatchMatchesRenderedFile: the runtime patch and the file the engine boots with hold the same settings,
// so neither can drift from the other.
func TestSettingsPatchMatchesRenderedFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := supervise.CLIProxyAPIEngine(config.Config{DataDir: dir, EngineDir: "/e"}, supervise.Credentials{CLIProxyAPIKey: testClientKey}, EngineStrategies(nil)); err != nil {
		t.Fatalf("CLIProxyAPIEngine: %v", err)
	}
	raw, err := os.ReadFile(dir + "/cliproxyapi/config.yaml") //nolint:gosec // G304: a path under t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err := yaml.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decode config.yaml: %v", err)
	}
	file = jsonRoundTrip(file)
	patch := jsonRoundTrip(settingsPatch(testClientKey, false, EngineStrategies(nil)))
	if !containsAll(file, patch) {
		t.Errorf("rendered file lacks part of the patch:\nfile  %v\npatch %v", file, patch)
	}
	for _, section := range []string{"routing", "requests", "upstream"} {
		if !reflect.DeepEqual(file[section], patch[section]) {
			t.Errorf("%s: file %v, patch %v", section, file[section], patch[section])
		}
	}
	if got, want := at(file, "oauth.providers"), at(patch, "oauth.providers"); !reflect.DeepEqual(got, want) {
		t.Errorf("oauth.providers: file %v, patch %v", got, want)
	}
}

func TestReconcileSettingsDrift(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	c := cliproxyapi.New(e.fake.URL, testSecret, nil)
	if err := c.PatchConfig(context.Background(), settingsPatch(testClientKey, false, EngineStrategies(nil))); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}
	if err := c.PatchConfig(context.Background(), map[string]any{
		"access":  map[string]any{"api-keys": []any{testClientKey, "stranger"}},
		"routing": map[string]any{"strategy": "fill-first"},
	}); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}
	e.reconcile(t)
	patches := e.fake.ConfigPatches()
	if len(patches) != 3 {
		t.Fatalf("%d patches, want the store's settings written once more", len(patches))
	}
	wantSettings(t, decodePatch(t, patches[2]))
}

func TestClientKeyStable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	creds, err := supervise.EnsureCredentials(ctx, e.store)
	if err != nil {
		t.Fatalf("EnsureCredentials: %v", err)
	}
	a, err := e.svc.ClientKey(ctx)
	if err != nil {
		t.Fatalf("ClientKey: %v", err)
	}
	b, err := e.svc.ClientKey(ctx)
	if err != nil {
		t.Fatalf("ClientKey: %v", err)
	}
	if a == "" || a != b || a != creds.CLIProxyAPIKey {
		t.Errorf("ClientKey = %q then %q, want %q both times", a, b, creds.CLIProxyAPIKey)
	}
}

func TestClientKeyMissing(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.ClientKey(context.Background()); err == nil {
		t.Error("ClientKey succeeded without a stored key")
	}
	if err := e.svc.Reconcile(context.Background()); err == nil {
		t.Error("Reconcile succeeded without a client key")
	}
	if n := len(e.fake.ConfigPatches()); n != 0 {
		t.Errorf("%d patches, want none without a client key", n)
	}
}

func TestReconcileStoreWinsEnabled(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex"})
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-b.json", Provider: "codex", Disabled: true})
	e.addRow(t, "codex", "a", "codex-a.json", false)
	e.addRow(t, "codex", "b", "codex-b.json", true)
	e.reconcile(t)
	if c, _, _ := e.fake.Credential("codex-a.json"); !c.Disabled {
		t.Error("codex-a.json enabled in the engine, disabled in the store: want disabled")
	}
	if c, _, _ := e.fake.Credential("codex-b.json"); c.Disabled {
		t.Error("codex-b.json disabled in the engine, enabled in the store: want enabled")
	}
	before := e.fake.Calls("/credentials/status")
	e.reconcile(t)
	if n := e.fake.Calls("/credentials/status"); n != before {
		t.Errorf("an agreeing pass sent %d status changes, want 0", n-before)
	}
}

func TestReconcileDeletesUnknown(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-stray.json", Provider: "codex"})
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-a.json", Provider: "codex"})
	e.addRow(t, "codex", "a", "codex-a.json", true)
	e.reconcile(t)
	if _, _, ok := e.fake.Credential("codex-stray.json"); ok {
		t.Error("credential not in the store survived")
	}
	if _, _, ok := e.fake.Credential("codex-a.json"); !ok {
		t.Error("credential in the store was deleted")
	}
}

func TestReconcileKeepsPendingLoginCredential(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	l := e.start(t, "codex")
	e.fake.FinishLogin(l.State, cliproxyapi.Credential{Name: "codex-new.json", Provider: "codex", Email: "n@x"})
	e.fake.AddCredential(cliproxyapi.Credential{Name: "xai-stray.json", Provider: "xai"})
	e.reconcile(t)
	if _, _, ok := e.fake.Credential("codex-new.json"); !ok {
		t.Fatal("the finished login's credential was deleted before it was adopted")
	}
	if _, _, ok := e.fake.Credential("xai-stray.json"); ok {
		t.Error("a stray credential of a provider with no login survived")
	}

	st, err := e.svc.LoginStatus(context.Background(), l.State)
	if err != nil || st.Status != "ok" {
		t.Fatalf("LoginStatus = %+v, %v", st, err)
	}
	e.reconcile(t)
	if _, _, ok := e.fake.Credential("codex-new.json"); !ok {
		t.Error("the adopted credential was deleted")
	}
}

func TestReconcileKeepsMissingRow(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	row := e.addRow(t, "codex", "a", "codex-gone.json", true)
	e.reconcile(t)
	got, err := e.store.Account(context.Background(), row.ID)
	if err != nil || got != row {
		t.Errorf("row = %+v, %v; want kept unchanged", got, err)
	}
	if n := e.fake.Calls("/credentials/status"); n != 0 {
		t.Errorf("status calls = %d, want 0 for a missing credential", n)
	}
}

func TestReconcileContinuesAfterEngineError(t *testing.T) {
	e := newFaultyEnv(t, "PATCH /config")
	e.setClientKey(t)
	e.fake.AddCredential(cliproxyapi.Credential{Name: "codex-stray.json", Provider: "codex"})
	if err := e.svc.Reconcile(context.Background()); err == nil {
		t.Error("Reconcile succeeded although the settings write failed")
	}
	if _, _, ok := e.fake.Credential("codex-stray.json"); ok {
		t.Error("a failed settings write stopped the credential pass")
	}
}
