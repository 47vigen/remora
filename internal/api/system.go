// Package api serves remora's HTTP endpoints.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/47vigen/remora/internal/supervise"
	"github.com/47vigen/remora/internal/version"
)

// StatusSource reports the supervised engines. *supervise.Supervisor implements it.
type StatusSource interface {
	Status() []supervise.EngineStatus
}

// HealthResponse is the body of GET /healthz.
type HealthResponse struct {
	Status string   `json:"status"`         // "ok" | "degraded"
	Down   []string `json:"down,omitempty"` // engine names not up
}

// EngineVersionInfo names one engine and its release tag.
type EngineVersionInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// VersionResponse is the body of GET /api/system/version.
type VersionResponse struct {
	Remora  string              `json:"remora"` // version.Version
	Engines []EngineVersionInfo `json:"engines"`
}

// RegisterSystem mounts GET /healthz (200 ok, 503 degraded) and GET /api/system/version on mux. Neither route needs
// admin auth until the management API takes them over.
func RegisterSystem(mux *http.ServeMux, src StatusSource) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		resp := HealthResponse{Status: "ok"}
		for _, e := range src.Status() {
			if !e.Up {
				resp.Down = append(resp.Down, e.Name)
			}
		}
		code := http.StatusOK
		if len(resp.Down) > 0 {
			resp.Status = "degraded"
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, resp)
	})

	mux.HandleFunc("GET /api/system/version", func(w http.ResponseWriter, _ *http.Request) {
		status := src.Status()
		resp := VersionResponse{Remora: version.Version, Engines: make([]EngineVersionInfo, 0, len(status))}
		for _, e := range status {
			resp.Engines = append(resp.Engines, EngineVersionInfo{Name: e.Name, Version: e.Version})
		}
		writeJSON(w, http.StatusOK, resp)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
