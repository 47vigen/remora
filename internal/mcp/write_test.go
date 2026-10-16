package mcp

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestDeleteEachKind(t *testing.T) {
	for _, tc := range []struct{ kind, id, path string }{
		{"provider", "up-a", "/api/providers/up-a"},
		{"alias", "fast", "/api/aliases/fast"},
		{"key", "k1", "/api/keys/k1"},
		{"account", "a1", "/api/accounts/a1"},
	} {
		f := newFakeAPI(t)
		f.on("DELETE "+tc.path, http.StatusNoContent, "")
		res, out := call(t, f.session(t), "remora_delete", map[string]any{"kind": tc.kind, "id": tc.id})
		if res.IsError || out["deleted"] != tc.kind+" "+tc.id {
			t.Errorf("%s: %v %s", tc.kind, out, text(res))
		}
		if r := f.requests(); len(r) != 1 || r[0].Method != http.MethodDelete || r[0].Path != tc.path {
			t.Errorf("%s: requests %+v", tc.kind, r)
		}
	}
}

func TestDeleteUnknownKind(t *testing.T) {
	f := newFakeAPI(t)
	res, _ := call(t, f.session(t), "remora_delete", map[string]any{"kind": "token", "id": "t1"})
	if !res.IsError || len(f.requests()) != 0 {
		t.Errorf("token delete: isError %v, %d requests", res.IsError, len(f.requests()))
	}
}

func TestAccountOps(t *testing.T) {
	for _, tc := range []struct {
		args         map[string]any
		method, path string
		status       int
		answer       string
		body         map[string]any
		want         map[string]any
	}{
		{map[string]any{"op": "enable", "id": "a1"}, "PATCH", "/api/accounts/a1", 200, `{"id":"a1","enabled":true,"models":[]}`, map[string]any{"enabled": true}, map[string]any{"id": "a1"}},
		{map[string]any{"op": "disable", "id": "a1"}, "PATCH", "/api/accounts/a1", 200, `{"id":"a1","enabled":false,"models":[]}`, map[string]any{"enabled": false}, map[string]any{"id": "a1"}},
		{map[string]any{"op": "set_priority", "id": "a1", "priority": 5}, "PATCH", "/api/accounts/a1", 200, `{"id":"a1","enabled":true,"models":[]}`, map[string]any{"priority": float64(5)}, map[string]any{"id": "a1"}},
		{map[string]any{"op": "reset_cooldown", "id": "a1"}, "POST", "/api/accounts/a1/reset-cooldown", 204, "", nil, map[string]any{"ok": true}},
		{map[string]any{"op": "refresh_quota", "provider": "codex"}, "POST", "/api/quota/refresh", 202, `{"queued":["a1"],"skipped":[]}`, map[string]any{"provider": "codex"}, map[string]any{"queued": []any{"a1"}}},
		{map[string]any{"op": "refresh_quota", "id": "a1"}, "POST", "/api/quota/refresh", 202, `{"queued":["a1"],"skipped":[]}`, map[string]any{"account_id": "a1"}, map[string]any{"queued": []any{"a1"}}},
		{map[string]any{"op": "login_start", "provider": "codex"}, "POST", "/api/accounts/logins", 201, `{"state":"st","url":"https://login.example/x"}`, map[string]any{"provider": "codex"}, map[string]any{"url": "https://login.example/x"}},
		{map[string]any{"op": "login_complete", "id": "st", "redirect_url": "http://localhost:1455/cb?code=c"}, "POST", "/api/accounts/logins/st/complete", 202, "", map[string]any{"redirect_url": "http://localhost:1455/cb?code=c"}, map[string]any{"ok": true}},
		{map[string]any{"op": "login_cancel", "id": "st"}, "DELETE", "/api/accounts/logins/st", 204, "", nil, map[string]any{"ok": true}},
		{map[string]any{"op": "import", "provider": "codex", "credential": map[string]any{"token": "x"}}, "POST", "/api/accounts/import", 201, `{"id":"a2","enabled":true,"models":[]}`, map[string]any{"provider": "codex", "credential": map[string]any{"token": "x"}}, map[string]any{"id": "a2"}},
	} {
		f := newFakeAPI(t)
		f.on(tc.method+" "+tc.path, tc.status, tc.answer)
		res, out := call(t, f.session(t), "remora_account", tc.args)
		if res.IsError {
			t.Errorf("%v: %s", tc.args, text(res))
			continue
		}
		r := f.requests()
		if len(r) != 1 || r[0].Method != tc.method || r[0].Path != tc.path {
			t.Errorf("%v: requests %+v", tc.args, r)
			continue
		}
		if tc.body != nil {
			var got map[string]any
			_ = json.Unmarshal([]byte(r[0].Body), &got)
			if !reflect.DeepEqual(got, tc.body) {
				t.Errorf("%v: body %s, want %v", tc.args, r[0].Body, tc.body)
			}
		}
		for k, v := range tc.want {
			if !reflect.DeepEqual(out[k], v) {
				t.Errorf("%v: result %s = %v, want %v", tc.args, k, out[k], v)
			}
		}
	}
}

func TestAccountMissingField(t *testing.T) {
	f := newFakeAPI(t)
	cs := f.session(t)
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"op": "set_priority", "id": "a1"}, "set_priority needs priority."},
		{map[string]any{"op": "enable"}, "enable needs id."},
		{map[string]any{"op": "refresh_quota"}, "refresh_quota needs id or provider."},
		{map[string]any{"op": "import", "provider": "codex"}, "import needs credential."},
		{map[string]any{"op": "fly"}, "op must be one of"},
	} {
		if res, _ := call(t, cs, "remora_account", tc.args); !res.IsError || !strings.Contains(text(res), tc.want) {
			t.Errorf("%v = %s", tc.args, text(res))
		}
	}
	if len(f.requests()) != 0 {
		t.Errorf("%d requests made", len(f.requests()))
	}
}

func TestAccountAPIError(t *testing.T) {
	f := newFakeAPI(t)
	f.on("PATCH /api/accounts/a9", http.StatusNotFound, `{"error":{"message":"No account a9.","type":"not_found_error","code":"not_found"}}`)
	if res, _ := call(t, f.session(t), "remora_account", map[string]any{"op": "enable", "id": "a9"}); !res.IsError || !strings.Contains(text(res), "No account a9.") {
		t.Errorf("api error = %s", text(res))
	}
}
