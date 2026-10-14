package control

import "net/http"

// Route names, matched under /u/{upstream}.
const (
	RouteOpenAIChat           = "openai.chat"                  // POST /v1/chat/completions
	RouteOpenAIEmbeddings     = "openai.embeddings"            // POST /v1/embeddings
	RouteOpenAITranscriptions = "openai.transcriptions"        // POST /v1/audio/transcriptions
	RouteOpenAIDecisions      = "openai.decisions"             // POST /v1/decisions
	RouteModels               = "models"                       // GET  /v1/models
	RouteSonioxFileCreate     = "soniox.files.create"          // POST /v1/files
	RouteSonioxFileDelete     = "soniox.files.delete"          // DELETE /v1/files/{id}
	RouteSonioxTxCreate       = "soniox.transcriptions.create" // POST /v1/transcriptions
	RouteSonioxTxGet          = "soniox.transcriptions.get"    // GET  /v1/transcriptions/{id}
	RouteSonioxTxDelete       = "soniox.transcriptions.delete" // DELETE /v1/transcriptions/{id}
	RouteSonioxTranscript     = "soniox.transcript.get"        // GET  /v1/transcriptions/{id}/transcript
	RouteTypeSafeSystemOne    = "typesafe.systemone"           // POST /v1/systemone
)

// routeSpec binds a route name to its method and path below /u/{upstream}.
type routeSpec struct {
	name   string
	method string
	path   string
}

// routeTable lists every fake route. Each later plan adds default behaviour
// for its routes through New; the table itself is the full contract.
func routeTable() []routeSpec {
	return []routeSpec{
		{RouteOpenAIChat, http.MethodPost, "/v1/chat/completions"},
		{RouteOpenAIEmbeddings, http.MethodPost, "/v1/embeddings"},
		{RouteOpenAITranscriptions, http.MethodPost, "/v1/audio/transcriptions"},
		{RouteOpenAIDecisions, http.MethodPost, "/v1/decisions"},
		{RouteModels, http.MethodGet, "/v1/models"},
		{RouteSonioxFileCreate, http.MethodPost, "/v1/files"},
		{RouteSonioxFileDelete, http.MethodDelete, "/v1/files/{id}"},
		{RouteSonioxTxCreate, http.MethodPost, "/v1/transcriptions"},
		{RouteSonioxTxGet, http.MethodGet, "/v1/transcriptions/{id}"},
		{RouteSonioxTxDelete, http.MethodDelete, "/v1/transcriptions/{id}"},
		{RouteSonioxTranscript, http.MethodGet, "/v1/transcriptions/{id}/transcript"},
		{RouteTypeSafeSystemOne, http.MethodPost, "/v1/systemone"},
	}
}
