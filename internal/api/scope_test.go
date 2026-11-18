package api

import (
	"slices"
	"strings"
	"testing"
)

func TestScopeOperations(t *testing.T) {
	for path, item := range loadSpec(t).Paths.Map() {
		for method, op := range item.Operations() {
			id := op.OperationID
			var want string
			switch id {
			case "getHealth", "getVersion", "login", "logout", "getSession":
				want = "none"
			case "streamLogs", "compileAlias", "planConfig", "testProvider":
				want = "read"
			case "updateAccount", "resetAccountCooldown", "refreshQuota", "startAccountLogin", "getAccountLogin",
				"cancelAccountLogin", "completeAccountLogin":
				want = "operate"
			default:
				want = "admin"
				if strings.HasPrefix(id, "get") || strings.HasPrefix(id, "list") {
					want = "read"
				}
			}
			if got, _ := op.Extensions["x-remora-scope"].(string); got != want {
				t.Errorf("%s %s (%s): x-remora-scope = %q, want %q", method, path, id, got, want)
			}
		}
	}
}

func TestScopeTokenSchemas(t *testing.T) {
	doc := loadSpec(t)
	for _, name := range []string{"ApiToken", "ApiTokenCreate"} {
		t.Run(name, func(t *testing.T) {
			schema := doc.Components.Schemas[name].Value
			scope := schema.Properties["scope"]
			if scope == nil {
				t.Error("missing scope")
			} else if s := scope.Value; s.Default != "admin" || len(s.Enum) != 3 ||
				!slices.Contains(s.Enum, "read") || !slices.Contains(s.Enum, "operate") || !slices.Contains(s.Enum, "admin") {
				t.Errorf("scope enum = %v, default = %v; want read, operate, admin with default admin", s.Enum, s.Default)
			}
			expiry := schema.Properties["expires_at"]
			if expiry == nil || expiry.Value.Format != "date-time" || slices.Contains(schema.Required, "expires_at") {
				t.Error("expires_at must be an optional date-time")
			}
		})
	}
}
