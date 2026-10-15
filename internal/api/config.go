package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/config"
)

// The remora.yaml endpoints: export, plan and apply over config.Service, which drives the provider, alias and key
// ports. Subscription accounts are not part of the document.

const exportDisposition = "attachment; filename=remora.yaml"

// config returns the remora.yaml service, or errNotImplemented while a port it needs is not wired.
func (s *Server) config() (*config.Service, error) {
	if s.cfg == nil {
		return nil, errNotImplemented
	}
	return s.cfg, nil
}

// ExportConfig answers the whole configuration as remora.yaml, secrets as env:NAME references.
func (s *Server) ExportConfig(ctx context.Context, _ gen.ExportConfigRequestObject) (gen.ExportConfigResponseObject, error) {
	svc, err := s.config()
	if err != nil {
		return nil, err
	}
	f, err := svc.Export(ctx)
	if err != nil {
		return nil, err
	}
	b, err := config.Marshal(f)
	if err != nil {
		return nil, err
	}
	disposition := exportDisposition
	return gen.ExportConfig200ApplicationyamlResponse{
		Body: bytes.NewReader(b), ContentLength: int64(len(b)),
		Headers: gen.ExportConfig200ResponseHeaders{ContentDisposition: &disposition},
	}, nil
}

// configRequest parses a plan or apply request: the document (400 with the failing path), its secrets and prune
// (default true).
func configRequest(body *gen.ConfigRequest) (config.File, config.Secrets, bool, error) {
	if body == nil {
		return config.File{}, nil, false, badRequest("A body with the remora.yaml document is required.")
	}
	f, err := config.Parse([]byte(body.Document))
	if err != nil {
		return config.File{}, nil, false, badRequest(err.Error())
	}
	var sec config.Secrets
	if body.Secrets != nil {
		sec = *body.Secrets
	}
	prune := body.Prune == nil || *body.Prune
	return f, sec, prune, nil
}

// PlanConfig answers the changes a document would make; nothing is changed.
func (s *Server) PlanConfig(ctx context.Context, req gen.PlanConfigRequestObject) (gen.PlanConfigResponseObject, error) {
	svc, err := s.config()
	if err != nil {
		return nil, err
	}
	f, sec, prune, err := configRequest(req.Body)
	if err != nil {
		return nil, err
	}
	cs, err := svc.Plan(ctx, f, sec, prune)
	if err != nil {
		return nil, err
	}
	return gen.PlanConfig200JSONResponse{Changes: toChanges(cs)}, nil
}

// ApplyConfig makes the store match a document and answers the changes made. When a change fails, nothing is
// changed and the error names the failed change.
func (s *Server) ApplyConfig(ctx context.Context, req gen.ApplyConfigRequestObject) (gen.ApplyConfigResponseObject, error) {
	svc, err := s.config()
	if err != nil {
		return nil, err
	}
	f, sec, prune, err := configRequest(req.Body)
	if err != nil {
		return nil, err
	}
	applied, err := svc.Apply(ctx, f, sec, prune)
	if err == nil {
		return gen.ApplyConfig200JSONResponse{Changes: toChanges(applied)}, nil
	}
	var ae *config.ApplyError
	if !errors.As(err, &ae) {
		return nil, err
	}
	e := toAPIError(err)
	if e.status >= 500 {
		s.log().ErrorContext(ctx, "remora.yaml apply failed", "change", ae.Change.Op+" "+ae.Change.Kind+" "+ae.Change.Name, "err", err)
	}
	msg := fmt.Sprintf("%s %s %s failed: %s Nothing was changed.", ae.Change.Op, ae.Change.Kind, ae.Change.Name, e.msg)
	detail := gen.ErrorDetail{Type: e.typ, Code: e.code, Message: msg}
	return gen.ApplyConfigdefaultJSONResponse{StatusCode: e.status, Body: gen.Error{Error: detail}}, nil
}

func toChanges(cs []config.Change) []gen.Change {
	out := make([]gen.Change, len(cs))
	for i, c := range cs {
		fields := c.Fields
		if fields == nil {
			fields = []string{}
		}
		out[i] = gen.Change{Op: gen.ChangeOp(c.Op), Kind: gen.ChangeKind(c.Kind), Name: c.Name, Fields: fields}
		if c.Value != "" {
			v := c.Value
			out[i].Value = &v
		}
	}
	return out
}
