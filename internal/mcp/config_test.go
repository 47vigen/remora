package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const changesBody = `{"changes":[{"op":"update","kind":"alias","name":"fast","fields":["members"]}],"plan_hash":"h1"}`

func sentBody(t *testing.T, f *fakeAPI) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(f.requests()[0].Body), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestConfigPlanSendsPruneFalse(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /api/config/plan", http.StatusOK, changesBody)
	res, out := call(t, f.session(t), "remora_config_plan", map[string]any{"document": "aliases: []\n"})
	if res.IsError {
		t.Fatal(text(res))
	}
	body := sentBody(t, f)
	if body["prune"] != false || body["secrets"] == nil || len(body["secrets"].(map[string]any)) != 0 || body["document"] != "aliases: []\n" {
		t.Errorf("sent %v", body)
	}
	if out["plan_hash"] != "h1" || len(out["changes"].([]any)) != 1 {
		t.Errorf("result %v", out)
	}
}

func TestConfigApplySendsHash(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /api/config/apply", http.StatusOK, `{"changes":[{"op":"create","kind":"key","name":"k","fields":[],"value":"sk-new"}]}`)
	res, out := call(t, f.session(t), "remora_config_apply", map[string]any{"document": "keys: []\n", "plan_hash": "h1", "prune": true})
	if res.IsError {
		t.Fatal(text(res))
	}
	if body := sentBody(t, f); body["plan_hash"] != "h1" || body["prune"] != true {
		t.Errorf("sent %v", body)
	}
	if out["applied"] != true || len(out["changes"].([]any)) != 1 {
		t.Errorf("result %v", out)
	}
}

func TestConfigApplyStaleHash(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /api/config/apply", http.StatusConflict, strings.Replace(changesBody, "h1", "h2", 1))
	res, out := call(t, f.session(t), "remora_config_apply", map[string]any{"document": "aliases: []\n", "plan_hash": "h1"})
	if res.IsError {
		t.Fatalf("isError: %s", text(res))
	}
	if out["applied"] != false || out["plan_hash"] != "h2" || len(out["changes"].([]any)) != 1 || !strings.Contains(out["message"].(string), "Nothing was applied") {
		t.Errorf("result %v", out)
	}
}

func TestConfigApplyRequiresHash(t *testing.T) {
	f := newFakeAPI(t)
	res, _ := call(t, f.session(t), "remora_config_apply", map[string]any{"document": "aliases: []\n"})
	if !res.IsError || len(f.requests()) != 0 {
		t.Errorf("without plan_hash: isError %v, %d requests", res.IsError, len(f.requests()))
	}
}

func TestConfigApplyFailedChange(t *testing.T) {
	f := newFakeAPI(t)
	msg := "update alias fast failed: Unknown member nowhere. Nothing was changed."
	f.on("POST /api/config/apply", http.StatusUnprocessableEntity, `{"error":{"message":"`+msg+`","type":"invalid_request_error","code":"invalid_request"}}`)
	res, _ := call(t, f.session(t), "remora_config_apply", map[string]any{"document": "aliases: []\n", "plan_hash": "h1"})
	if !res.IsError || !strings.Contains(text(res), msg) {
		t.Errorf("failed change = %s", text(res))
	}
}
