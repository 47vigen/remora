package reconcile

import (
	"time"

	"github.com/47vigen/remora/internal/engine/bifrost"
	"github.com/47vigen/remora/internal/store"
)

// SonioxProviderName is the only name a provider of kind "soniox" may take: clients ask for "soniox/<model>".
const SonioxProviderName = "soniox"

// defaultSonioxDeadline is the adapter's deadline when the Reconciler is given none.
const defaultSonioxDeadline = 30 * time.Minute

// sonioxGrace is how much longer than the adapter's deadline the front door waits for its answer, so the adapter's
// own timeout error reaches the client first.
const sonioxGrace = 60 * time.Second

// sonioxProvider is the front-door provider that sends transcriptions to the speech adapter. The front door appends
// "/v1/audio/transcriptions" to adapterURL and forwards the provider key as the bearer token; only transcription is
// allowed, so list-models, chat and streaming are off for it.
func sonioxProvider(adapterURL string, deadline time.Duration) bifrost.Provider {
	return bifrost.Provider{
		Name:                SonioxProviderName,
		BaseType:            "openai",
		BaseURL:             adapterURL,
		AllowPrivateNetwork: true,
		AllowedRequests:     []string{"transcription"},
		RequestTimeout:      deadline + sonioxGrace,
	}
}

// engineSonioxProvider validates a store provider of kind "soniox" and maps it to sonioxProvider. Its upstream is
// the adapter's setting, never the store's.
func (r *Reconciler) engineSonioxProvider(p store.Provider) (bifrost.Provider, error) {
	switch {
	case p.Name != SonioxProviderName:
		return bifrost.Provider{}, invalid("provider %q of kind soniox must be named %q", p.Name, SonioxProviderName)
	case p.Type != "":
		return bifrost.Provider{}, invalid("provider %q of kind soniox takes no type", p.Name)
	case p.BaseURL != "":
		return bifrost.Provider{}, invalid("provider %q of kind soniox takes no base URL (set REMORA_SONIOX_BASE_URL)", p.Name)
	case r.SonioxURL == "":
		return bifrost.Provider{}, invalid("provider %q: the speech adapter is not configured", p.Name)
	}
	deadline := r.SonioxDeadline
	if deadline <= 0 {
		deadline = defaultSonioxDeadline
	}
	return sonioxProvider(r.SonioxURL, deadline), nil
}
