package api

import (
	"context"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const specPath = "../../api/openapi.yaml"

// loadSpec loads and validates the management contract, failing the test on any error.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load %s: %v", specPath, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate %s: %v", specPath, err)
	}
	return doc
}

// okSchema returns the JSON schema of an operation's 200 response.
func okSchema(t *testing.T, op *openapi3.Operation) *openapi3.Schema {
	t.Helper()
	resp := op.Responses.Status(200)
	if resp == nil || resp.Value == nil {
		t.Fatalf("%s: no 200 response", op.OperationID)
	}
	mt := resp.Value.Content.Get("application/json")
	if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
		t.Fatalf("%s: 200 response has no application/json schema", op.OperationID)
	}
	return mt.Schema.Value
}

// hasParamRef reports whether op references the shared parameter components/parameters/<name>.
func hasParamRef(op *openapi3.Operation, name string) bool {
	want := "#/components/parameters/" + name
	for _, p := range op.Parameters {
		if p.Ref == want {
			return true
		}
	}
	return false
}

func TestOpenAPIConventions(t *testing.T) {
	doc := loadSpec(t)
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Errorf("openapi = %q, want 3.1.x", doc.OpenAPI)
	}
	public := map[string]bool{"getHealth": true, "getVersion": true, "login": true}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			if op.OperationID == "" {
				t.Errorf("%s %s: no operationId", method, path)
				continue
			}
			id := op.OperationID
			def := op.Responses.Default()
			if def == nil {
				t.Errorf("%s: no default response", id)
			} else if def.Ref != "#/components/responses/Error" {
				t.Errorf("%s: default response is %q, want the shared Error", id, def.Ref)
			}
			if public[id] {
				if op.Security == nil || len(*op.Security) != 0 {
					t.Errorf("%s: want security: [] (public)", id)
				}
			} else if op.Security != nil && len(*op.Security) == 0 {
				t.Errorf("%s: unexpectedly public", id)
			}
			if strings.HasPrefix(id, "list") {
				req := slices.Clone(okSchema(t, op).Required)
				sort.Strings(req)
				if !slices.Equal(req, []string{"items", "total"}) {
					t.Errorf("%s: 200 required = %v, want [items total]", id, req)
				}
				if !hasParamRef(op, "Limit") || !hasParamRef(op, "Offset") {
					t.Errorf("%s: list without the shared Limit and Offset parameters", id)
				}
			}
		}
	}
}

func TestEveryResourceOperationExists(t *testing.T) {
	doc := loadSpec(t)
	want := []string{
		// system
		"getHealth", "getVersion",
		// auth
		"login", "logout", "getSession",
		// providers
		"listProviders", "createProvider", "getProvider", "updateProvider", "deleteProvider", "testProvider",
		// accounts
		"listAccounts", "getAccountPool", "startAccountLogin", "getAccountLogin", "completeAccountLogin", "cancelAccountLogin",
		"updateAccount", "deleteAccount", "resetAccountCooldown", "importAccount", "listAccountStrategies", "setAccountStrategy",
		// aliases
		"listAliases", "createAlias", "getAlias", "updateAlias", "deleteAlias", "getAliasChain", "compileAlias",
		"listCooling",
		// keys
		"listKeys", "createKey", "getKey", "updateKey", "deleteKey", "getKeyUsage",
		// logs
		"listLogs", "getLog", "streamLogs",
		// stats
		"getStats",
		// settings
		"changePassword", "listApiTokens", "createApiToken", "deleteApiToken",
		// config
		"exportConfig", "planConfig", "applyConfig",
	}
	if len(want) != 48 {
		t.Fatalf("expected list has %d ids, want 48", len(want))
	}
	var got []string
	for _, item := range doc.Paths.Map() {
		for _, op := range item.Operations() {
			got = append(got, op.OperationID)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("operationIds differ\n got: %v\nwant: %v", got, want)
	}
}
