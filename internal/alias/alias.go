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

// Strategy is how an alias picks the first member a request tries.
type Strategy string

// Alias strategies.
const (
	// StrategyFallback tries the members in order: the default, written as no strategy.
	StrategyFallback Strategy = ""
	// StrategyRoundRobin spreads requests over the members, each picked at random with equal weight, and falls back
	// through the members in order when the picked one fails.
	StrategyRoundRobin Strategy = "round-robin"
)

// Valid reports whether s is one of the alias strategies.
func (s Strategy) Valid() bool { return s == StrategyFallback || s == StrategyRoundRobin }

// Member is one entry of an alias: an alias name, or "<provider>/<model>".
type Member struct{ Ref string }

// Alias is a named, ordered list of members that clients call as a model.
type Alias struct {
	Name          string
	Kind          Kind
	Members       []Member
	Capabilities  []string // advertised supported_parameters, e.g. "structured_outputs", "tools"
	ContextWindow int      // 0 = unset
	Strategy      Strategy // how the alias's own compiled chain is used; a nested alias is flattened in order
}

// Target is a provider model an alias resolves to.
type Target struct{ Provider, Model string }
