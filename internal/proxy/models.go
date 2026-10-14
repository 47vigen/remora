package proxy

import (
	"log/slog"
	"net/http"
	"sort"
	"strconv"

	"github.com/47vigen/remora/internal/store"
)

// modelEntry is one alias in the /v1/models list.
type modelEntry struct {
	ID                  string   `json:"id"`
	Object              string   `json:"object"`
	Created             int64    `json:"created"`
	OwnedBy             string   `json:"owned_by"`
	ContextLength       int      `json:"context_length,omitempty"`
	SupportedParameters []string `json:"supported_parameters"`
}

type modelList struct {
	Object string       `json:"object"`
	Data   []modelEntry `json:"data"`
}

// serveModels lists the aliases key may use, sorted by id. Provider models never appear, even when allowed: the
// list is remora's own catalogue, not a provider's.
func (p *Proxy) serveModels(w http.ResponseWriter, r *http.Request, key store.ClientKey) {
	aliases, err := p.store.Aliases(r.Context())
	if err != nil {
		p.log.ErrorContext(r.Context(), "proxy: list aliases", slog.Any("err", err))
		errInternal.write(w, shapeOpenAI)
		return
	}
	list := modelList{Object: "list", Data: []modelEntry{}}
	for name, a := range aliases {
		if !Allowed(key.Allow, name) {
			continue
		}
		params := append([]string{}, a.Capabilities...)
		list.Data = append(list.Data, modelEntry{
			ID: name, Object: "model", OwnedBy: "remora",
			ContextLength: a.ContextWindow, SupportedParameters: params,
		})
	}
	sort.Slice(list.Data, func(i, j int) bool { return list.Data[i].ID < list.Data[j].ID })

	b := marshal(list)
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b) //nolint:gosec // a JSON body remora encodes, served as application/json
}
