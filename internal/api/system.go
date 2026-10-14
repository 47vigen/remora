// Package api serves remora's management API under /api/* and the public /healthz probe.
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/supervise"
)

// StatusSource reports the supervised engines. *supervise.Supervisor implements it.
type StatusSource interface {
	Status() []supervise.EngineStatus
}

// HealthResponse is the body of GET /healthz.
type HealthResponse struct {
	Status string   `json:"status"`         // "ok" | "degraded"
	Down   []string `json:"down,omitempty"` // neutral component names not up ("router", "accounts")
}

// componentNames maps an engine to the neutral name a public endpoint may show. Engine names appear only in
// /api/system/version and the credits.
var componentNames = map[string]string{"bifrost": "router", "cliproxyapi": "accounts"}

// componentName returns the neutral name of an engine, or "engine" for one remora does not know.
func componentName(engine string) string {
	if n, ok := componentNames[engine]; ok {
		return n
	}
	return "engine"
}

// RegisterHealthz mounts GET /healthz (200 ok, 503 degraded) on mux. It needs no auth: the image's health check
// and orchestrators call it.
func RegisterHealthz(mux *http.ServeMux, src StatusSource) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		resp := HealthResponse{Status: "ok"}
		for _, e := range src.Status() {
			if !e.Up {
				resp.Down = append(resp.Down, componentName(e.Name))
			}
		}
		code := http.StatusOK
		if len(resp.Down) > 0 {
			resp.Status = "degraded"
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// GetHealth reports remora's components by neutral name. It always answers 200; status says "degraded" when a
// component is down.
func (s *Server) GetHealth(ctx context.Context, _ gen.GetHealthRequestObject) (gen.GetHealthResponseObject, error) {
	if s.d.System == nil {
		return nil, errNotImplemented
	}
	ok, engines := s.d.System.Health(ctx)
	resp := gen.GetHealth200JSONResponse{Status: gen.HealthStatusOk, Engines: map[string]gen.HealthEngines{}}
	for name, err := range engines {
		state := gen.HealthEngines("ok")
		if err != nil {
			state, ok = "error", false
		}
		resp.Engines[name] = state
	}
	if !ok {
		resp.Status = gen.HealthStatusDegraded
	}
	return resp, nil
}

// GetVersion reports remora's version and each engine's, in start order.
func (s *Server) GetVersion(_ context.Context, _ gen.GetVersionRequestObject) (gen.GetVersionResponseObject, error) {
	if s.d.System == nil {
		return nil, errNotImplemented
	}
	remora, engines := s.d.System.Version()
	resp := gen.GetVersion200JSONResponse{Remora: remora, Engines: make([]gen.EngineVersion, 0, len(engines))}
	for _, e := range engines {
		resp.Engines = append(resp.Engines, gen.EngineVersion{Name: e.Name, Version: e.Version})
	}
	return resp, nil
}
