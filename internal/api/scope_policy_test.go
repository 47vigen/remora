package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScopeUnknownOperationRequiresAdmin(t *testing.T) {
	env := newTestEnv(t)
	scopes := operationScopes{}
	h := env.server.d.Auth.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scopes.authorize(w, r, "UnknownOperation") {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	for _, tc := range []struct {
		scope string
		want  int
	}{
		{"read", http.StatusForbidden},
		{"operate", http.StatusForbidden},
		{"admin", http.StatusNoContent},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/unknown", nil)
		r.Header.Set("Authorization", "Bearer "+scopeToken(t, env, tc.scope))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s token: status = %d, want %d", tc.scope, w.Code, tc.want)
		}
	}
}

func TestScopeSpecRejectsMissingOrInvalidAnnotation(t *testing.T) {
	const doc = `openapi: 3.1.0
info: {title: Test, version: "1"}
paths:
  /api/test:
    get:
      operationId: test
      responses: {"200": {description: OK}}
`
	for _, tc := range []struct {
		name, annotation string
	}{
		{"missing", ""},
		{"unknown", "      x-remora-scope: write\n"},
		{"not a string", "      x-remora-scope: false\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadOperationScopes([]byte(doc + tc.annotation)); err == nil || !strings.Contains(err.Error(), "x-remora-scope") {
				t.Errorf("load spec = %v, want an x-remora-scope error", err)
			}
		})
	}
}
