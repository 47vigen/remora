package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

type keyJSON struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Prefix    string    `json:"prefix"`
	Allow     []string  `json:"allow"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	Value     *string   `json:"value"`
}

func createKey(t *testing.T, env *testEnv, body string) keyJSON {
	t.Helper()
	code, resp := env.do(t, env.client, http.MethodPost, "/api/keys", body)
	if code != http.StatusCreated {
		t.Fatalf("create key = %d %s", code, resp)
	}
	var k keyJSON
	decode(t, resp, &k)
	return k
}

func TestCreateKeyShowsValueOnce(t *testing.T) {
	env := newTestEnv(t)
	k := createKey(t, env, `{"name":"svc","allow":["fast","antigravity/*"]}`)
	if k.Value == nil || *k.Value == "" || strings.HasPrefix(*k.Value, "sk-bf-") {
		t.Fatalf("created value = %v", k.Value)
	}
	if k.ID == "" || k.Name != "svc" || !k.Enabled || !slices.Equal(k.Allow, []string{"fast", "antigravity/*"}) ||
		k.Prefix == "" || !strings.HasPrefix(*k.Value, k.Prefix) || k.CreatedAt.IsZero() {
		t.Errorf("created key = %+v", k)
	}

	code, body := env.do(t, env.client, http.MethodGet, "/api/keys/"+k.ID, "")
	if code != http.StatusOK || strings.Contains(string(body), `"value"`) || strings.Contains(string(body), *k.Value) {
		t.Errorf("get = %d %s", code, body)
	}
	var got keyJSON
	decode(t, body, &got)
	if got.Prefix != k.Prefix || !got.CreatedAt.Equal(k.CreatedAt) {
		t.Errorf("get = %s, want prefix %q", body, k.Prefix)
	}
	code, body = env.do(t, env.client, http.MethodGet, "/api/keys", "")
	if code != http.StatusOK || strings.Contains(string(body), `"value"`) || strings.Contains(string(body), *k.Value) {
		t.Errorf("list = %d %s", code, body)
	}
	if !strings.Contains(string(body), `"total":1`) {
		t.Errorf("list = %s, want total 1", body)
	}
}

func TestCreateKeyWithValue(t *testing.T) {
	env := newTestEnv(t)
	k := createKey(t, env, `{"name":"svc","allow":["*"],"value":"sk-imported-123"}`)
	if k.Value == nil || *k.Value != "sk-imported-123" {
		t.Errorf("value = %v, want sk-imported-123", k.Value)
	}
	if ok, _ := env.keys.MatchesValue(t.Context(), k.ID, "sk-imported-123"); !ok {
		t.Error("the fake did not store the caller's value")
	}
	if code, body := env.do(t, env.client, http.MethodPost, "/api/keys", `{"name":"x","allow":[],"value":""}`); code != http.StatusBadRequest {
		t.Errorf("empty value = %d %s", code, body)
	}
}

func TestUpdateKey(t *testing.T) {
	env := newTestEnv(t)
	k := createKey(t, env, `{"name":"svc","allow":["fast"]}`)

	code, body := env.do(t, env.client, http.MethodPatch, "/api/keys/"+k.ID, `{"enabled":false}`)
	if code != http.StatusOK {
		t.Fatalf("patch enabled = %d %s", code, body)
	}
	var got keyJSON
	decode(t, body, &got)
	if got.Enabled || got.Name != "svc" || !slices.Equal(got.Allow, []string{"fast"}) {
		t.Errorf("after patch enabled = %s", body)
	}

	code, body = env.do(t, env.client, http.MethodPatch, "/api/keys/"+k.ID, `{"allow":[]}`)
	if code != http.StatusOK || !strings.Contains(string(body), `"allow":[]`) {
		t.Fatalf("patch allow [] = %d %s", code, body)
	}
	stored, _ := env.keys.Get(t.Context(), k.ID)
	if stored.Allow == nil || len(stored.Allow) != 0 || stored.Enabled {
		t.Errorf("stored after patch allow [] = %+v, want empty allow (deny all), disabled", stored)
	}

	if code, body := env.do(t, env.client, http.MethodPatch, "/api/keys/"+k.ID, `{"name":"renamed"}`); code != http.StatusOK || !strings.Contains(string(body), `"name":"renamed"`) {
		t.Errorf("patch name = %d %s", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodPatch, "/api/keys/"+k.ID, `{"name":""}`); code != http.StatusBadRequest {
		t.Errorf("patch empty name = %d %s", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodPatch, "/api/keys/none", `{"enabled":true}`); code != http.StatusNotFound {
		t.Errorf("patch unknown = %d %s", code, body)
	}
}

func TestKeyAllowValidation(t *testing.T) {
	env := newTestEnv(t)
	for _, body := range []string{
		`{"name":"svc","allow":["["]}`,
		`{"name":"svc","allow":["fast",""]}`,
		`{"name":"","allow":["fast"]}`,
	} {
		code, resp := env.do(t, env.client, http.MethodPost, "/api/keys", body)
		if code != http.StatusBadRequest || errCode(t, resp) != "invalid_request" {
			t.Errorf("create %s = %d %s", body, code, resp)
		}
	}
	k := createKey(t, env, `{"name":"svc","allow":["fast"]}`)
	if code, resp := env.do(t, env.client, http.MethodPatch, "/api/keys/"+k.ID, `{"allow":["a[b"]}`); code != http.StatusBadRequest {
		t.Errorf("patch bad glob = %d %s", code, resp)
	}
	stored, _ := env.keys.Get(t.Context(), k.ID)
	if !slices.Equal(stored.Allow, []string{"fast"}) {
		t.Errorf("a refused patch changed allow: %v", stored.Allow)
	}
}

func TestDeleteKey(t *testing.T) {
	env := newTestEnv(t)
	k := createKey(t, env, `{"name":"svc","allow":["fast"]}`)
	if code, body := env.do(t, env.client, http.MethodDelete, "/api/keys/"+k.ID, ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodGet, "/api/keys/"+k.ID, ""); code != http.StatusNotFound {
		t.Errorf("get after delete = %d %s", code, body)
	}
	if code, body := env.do(t, env.client, http.MethodDelete, "/api/keys/"+k.ID, ""); code != http.StatusNotFound {
		t.Errorf("delete again = %d %s", code, body)
	}
}

func TestListKeysSearch(t *testing.T) {
	env := newTestEnv(t)
	createKey(t, env, `{"name":"billing","allow":["fast"]}`)
	createKey(t, env, `{"name":"search","allow":["fast"]}`)
	code, body := env.do(t, env.client, http.MethodGet, "/api/keys?q=BILL", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"name":"billing"`) || strings.Contains(string(body), `"name":"search"`) ||
		!strings.Contains(string(body), `"total":1`) {
		t.Errorf("list ?q=BILL = %d %s", code, body)
	}
}
