package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/auth"
)

type scopeLevel uint8

const (
	scopeUnknown scopeLevel = iota
	scopeNone
	scopeRead
	scopeOperate
	scopeAdmin
)

func scopeLevelOf(scope string) scopeLevel {
	switch scope {
	case "none":
		return scopeNone
	case string(gen.Read):
		return scopeRead
	case string(gen.Operate):
		return scopeOperate
	case string(gen.Admin):
		return scopeAdmin
	default:
		return scopeUnknown
	}
}

type operationScopes map[string]scopeLevel

func loadOperationScopes(spec []byte) (operationScopes, error) {
	doc, err := openapi3.NewLoader().LoadFromData(spec)
	if err != nil {
		return nil, fmt.Errorf("load management OpenAPI spec: %w", err)
	}
	scopes := make(operationScopes)
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			scope, _ := op.Extensions["x-remora-scope"].(string)
			level := scopeLevelOf(scope)
			if level == scopeUnknown {
				return nil, fmt.Errorf("%s %s (%s): missing or invalid x-remora-scope", method, path, op.OperationID)
			}
			// The generator capitalizes operation IDs for Go; reject collisions before matching case-insensitively.
			id := strings.ToLower(op.OperationID)
			if id == "" || scopes[id] != scopeUnknown {
				return nil, fmt.Errorf("%s %s: missing or duplicate operationId %q", method, path, op.OperationID)
			}
			scopes[id] = level
		}
	}
	return scopes, nil
}

func (scopes operationScopes) authorize(w http.ResponseWriter, r *http.Request, id string) bool {
	need := scopes[strings.ToLower(id)]
	if need == scopeUnknown {
		need = scopeAdmin
	}
	if need == scopeNone {
		return true
	}
	if p, ok := auth.PrincipalFrom(r.Context()); ok && scopeLevelOf(p.Scope) >= need {
		return true
	}
	auth.WriteError(w, http.StatusForbidden, typePermission, "forbidden", "This operation requires a higher scope.")
	return false
}

func (scopes operationScopes) middleware(next gen.StrictHandlerFunc, id string) gen.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		if !scopes.authorize(w, r, id) {
			return nil, nil
		}
		return next(ctx, w, r, request)
	}
}
