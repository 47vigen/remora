package soniox

import (
	"encoding/json"
	"net/http"

	"github.com/47vigen/remora/test/fakes/control"
)

// controlRoutes adds the control API:
//
//	PUT  /_control/soniox/script  body Script
//	POST /_control/soniox/seed    body Object
//	GET  /_control/soniox/state   answers State
//	POST /_control/soniox/reset
func (f *Fake) controlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /_control/soniox/script", func(w http.ResponseWriter, r *http.Request) {
		var s Script
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&s); err != nil {
			writeSonioxError(w, http.StatusBadRequest, "invalid_request", "fake: decode script: "+err.Error())
			return
		}
		f.SetScript(s)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /_control/soniox/seed", func(w http.ResponseWriter, r *http.Request) {
		var o Object
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&o); err != nil {
			writeSonioxError(w, http.StatusBadRequest, "invalid_request", "fake: decode object: "+err.Error())
			return
		}
		if o.Kind != KindFile && o.Kind != KindTranscription {
			writeSonioxError(w, http.StatusBadRequest, "invalid_request", `fake: kind must be "file" or "transcription"`)
			return
		}
		f.Seed(o)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /_control/soniox/state", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, f.State())
	})
	mux.HandleFunc("POST /_control/soniox/reset", func(w http.ResponseWriter, _ *http.Request) {
		f.Reset()
		w.WriteHeader(http.StatusNoContent)
	})
}

// sonioxRoutes are the control-server routes the fake answers by default.
var sonioxRoutes = []string{
	control.RouteSonioxFileCreate,
	control.RouteSonioxFileList,
	control.RouteSonioxFileDelete,
	control.RouteSonioxTxCreate,
	control.RouteSonioxTxList,
	control.RouteSonioxTxGet,
	control.RouteSonioxTxDelete,
	control.RouteSonioxTranscript,
}

// Defaults returns the default answer of every Soniox route of the control
// server (served under /u/{upstream}), all backed by this fake. Merge it into
// control.Defaults() so a route without a scripted step falls through to the
// fake while scripted steps still win.
func (f *Fake) Defaults() map[string]http.HandlerFunc {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/u/"+r.PathValue("upstream"), f).ServeHTTP(w, r)
	})
	out := make(map[string]http.HandlerFunc, len(sonioxRoutes))
	for _, route := range sonioxRoutes {
		out[route] = h
	}
	return out
}
