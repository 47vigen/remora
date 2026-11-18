package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/auth"
	"github.com/47vigen/remora/internal/store"
)

// ListAudit returns the audit trail without request or response bodies.
func (s *Server) ListAudit(ctx context.Context, req gen.ListAuditRequestObject) (gen.ListAuditResponseObject, error) {
	if s.d.Audit == nil {
		return nil, errNotImplemented
	}
	limit := deref(req.Params.Limit)
	if limit < 1 {
		limit = defaultLimit
	}
	entries, total, err := s.d.Audit.ListAudit(ctx, deref(req.Params.Operation), min(limit, maxLimit), max(0, deref(req.Params.Offset)))
	if err != nil {
		return nil, err
	}
	items := make([]gen.AuditEntry, 0, len(entries))
	for _, e := range entries {
		item := gen.AuditEntry{Id: e.ID, At: e.At, Principal: gen.AuditEntryPrincipal(e.Principal), OperationId: e.OperationID, Diff: e.Diff}
		if e.Principal == "token" {
			item.TokenId, item.TokenName = &e.TokenID, &e.TokenName
		}
		items = append(items, item)
	}
	return gen.ListAudit200JSONResponse{Items: items, Total: total}, nil
}

// The strict response can carry a non-2xx status even when the handler returns nil error.
func (s *Server) auditOperations(scopes operationScopes) gen.StrictMiddlewareFunc {
	return func(next gen.StrictHandlerFunc, id string) gen.StrictHandlerFunc {
		if op := strings.ToLower(id); scopes[op] < scopeOperate || op == "getaccountlogin" {
			return next
		}
		operation := strings.ToLower(id[:1]) + id[1:]
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			response, err := next(ctx, w, r, request)
			p, ok := auth.PrincipalFrom(ctx)
			if ok && err == nil && response != nil {
				w.(*auditWriter).record = func() {
					ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					e := store.AuditEntry{
						At: s.now(), Principal: p.Via, TokenID: p.TokenID, TokenName: p.TokenName,
						OperationID: operation, Diff: auditChanges(request, response),
					}
					if err := s.d.Audit.RecordAudit(ctx, e); err != nil {
						s.log().ErrorContext(ctx, "cannot record management audit entry", "operation", operation, "err", err)
					}
				}
			}
			return response, err
		}
	}
}

func auditResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&auditWriter{ResponseWriter: w}, r)
	})
}

type auditWriter struct {
	http.ResponseWriter
	record  func()
	started bool
}

func (w *auditWriter) WriteHeader(status int) {
	if !w.started && status >= 200 {
		w.started = true
		if status < 300 && w.record != nil {
			w.record()
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *auditWriter) Write(b []byte) (int, error) {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

const auditSet = "[set]"

// auditChanges projects known fields, so new or opaque request fields cannot accidentally log credentials.
func auditChanges(request, response any) map[string]any {
	d := map[string]any{}
	switch r := request.(type) {
	case gen.CreateProviderRequestObject:
		auditProvider(d, r.Body)
	case gen.UpdateProviderRequestObject:
		auditProvider(d, r.Body)
	case gen.DeleteProviderRequestObject:
		d["name"] = r.Name
	case gen.CreateAliasRequestObject:
		auditAlias(d, r.Body)
	case gen.UpdateAliasRequestObject:
		auditAlias(d, r.Body)
	case gen.DeleteAliasRequestObject:
		d["name"] = r.Name
	case gen.CreateKeyRequestObject:
		d["name"], d["allow"], d["value"] = r.Body.Name, r.Body.Allow, auditSet
	case gen.UpdateKeyRequestObject:
		d["id"] = r.Id
		auditOptional(d, "name", r.Body.Name)
		auditOptional(d, "allow", r.Body.Allow)
		auditOptional(d, "enabled", r.Body.Enabled)
	case gen.DeleteKeyRequestObject:
		d["id"] = r.Id
	case gen.SetAccountStrategyRequestObject:
		d["provider"], d["strategy"] = r.Provider, r.Body.Strategy
	case gen.RefreshQuotaRequestObject:
		auditOptional(d, "account_id", r.Body.AccountId)
		auditOptional(d, "provider", r.Body.Provider)
	case gen.StartAccountLoginRequestObject:
		d["provider"] = r.Body.Provider
	case gen.CancelAccountLoginRequestObject:
		d["state"] = auditSet
	case gen.CompleteAccountLoginRequestObject:
		d["state"], d["redirect_url"] = auditSet, auditSet
	case gen.ImportAccountRequestObject:
		d["provider"], d["credential"] = r.Body.Provider, auditSet
	case gen.UpdateAccountRequestObject:
		d["id"] = r.Id
		auditOptional(d, "enabled", r.Body.Enabled)
		auditOptional(d, "priority", r.Body.Priority)
	case gen.DeleteAccountRequestObject:
		d["id"] = r.Id
	case gen.ResetAccountCooldownRequestObject:
		d["id"], d["cooldown_until"] = r.Id, nil
	case gen.ChangePasswordRequestObject:
		d["current"], d["new"] = auditSet, auditSet
	case gen.CreateApiTokenRequestObject:
		d["name"], d["value"] = r.Body.Name, auditSet
		auditOptional(d, "scope", r.Body.Scope)
		auditOptional(d, "expires_at", r.Body.ExpiresAt)
	case gen.DeleteApiTokenRequestObject:
		d["id"] = r.Id
	case gen.ApplyConfigRequestObject:
		d["document"], d["prune"] = auditSet, r.Body.Prune == nil || *r.Body.Prune
		if r.Body.Secrets != nil {
			d["secrets"] = auditSet
		}
		if applied, ok := response.(gen.ApplyConfig200JSONResponse); ok {
			sample := applied.Changes[:min(len(applied.Changes), 5)]
			changes := make([]gen.Change, 0, len(sample))
			for _, c := range sample {
				changes = append(changes, gen.Change{Op: c.Op, Kind: c.Kind, Name: c.Name, Fields: c.Fields})
			}
			d["changes"], d["change_count"] = changes, len(applied.Changes)
		}
	}
	for field, value := range d {
		if b, err := json.Marshal(value); err != nil || len(b) > 1024 {
			d[field] = "[omitted]"
		}
	}
	return d
}

func auditOptional[T any](d map[string]any, field string, value *T) {
	if value != nil {
		d[field] = *value
	}
}

func auditProvider(d map[string]any, p *gen.Provider) {
	d["name"], d["kind"] = p.Name, p.Kind
	auditOptional(d, "type", p.Type)
	auditOptional(d, "allow_private_network", p.AllowPrivateNetwork)
	if p.BaseUrl != nil {
		d["base_url"] = auditSet
		// Credentials can be embedded anywhere after the origin, not just in userinfo.
		if u, err := url.Parse(*p.BaseUrl); err == nil && u.Host != "" {
			d["base_url"] = (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
		}
	}
	if p.Keys != nil {
		d["keys"] = auditSet
	}
}

func auditAlias(d map[string]any, a *gen.Alias) {
	d["name"], d["kind"], d["members"], d["capabilities"] = a.Name, a.Kind, a.Members, a.Capabilities
	auditOptional(d, "context_window", a.ContextWindow)
	auditOptional(d, "strategy", a.Strategy)
}
