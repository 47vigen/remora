// Package alias holds remora's model aliases and compiles them into a flat chain of provider targets.
package alias

// Kind is the kind of request an alias serves.
type Kind string

// Alias kinds.
const (
	KindChat          Kind = "chat"
	KindEmbeddings    Kind = "embeddings"
	KindTranscription Kind = "transcription"
	KindDecisions     Kind = "decisions"
)

// Member is one entry of an alias: an alias name, or "<provider>/<model>".
type Member struct{ Ref string }

// Alias is a named, ordered list of members that clients call as a model.
type Alias struct {
	Name          string
	Kind          Kind
	Members       []Member
	Capabilities  []string // advertised supported_parameters, e.g. "structured_outputs", "tools"
	ContextWindow int      // 0 = unset
}

// Target is a provider model an alias resolves to.
type Target struct{ Provider, Model string }
