package accounts

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

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

func wantSettings(t *testing.T, m map[string]any) {
	t.Helper()
	for path, want := range map[string]any{
		"access.api-keys":                       []any{testClientKey},
		"routing.strategy":                      "round-robin",
		"routing.force-model-prefix":            true,
		"routing.cooldown.save-cooldown-status": true,
		"upstream.codex.model-level-cooling":    true,
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
	if err := cliproxyapi.New(e.fake.URL, testSecret, nil).PatchConfig(context.Background(), settingsPatch(testClientKey, false)); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}
	e.reconcile(t)
	e.clock.advance(time.Hour)
	e.reconcile(t)
	if n := len(e.fake.ConfigPatches()); n != 1 {
		t.Errorf("%d patches, want only the test's own", n)
	}
}

func TestReconcileSettingsDrift(t *testing.T) {
	e := newEnv(t)
	e.setClientKey(t)
	c := cliproxyapi.New(e.fake.URL, testSecret, nil)
	if err := c.PatchConfig(context.Background(), settingsPatch(testClientKey, false)); err != nil {
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
