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

// Routing strategies, the same six names at alias and account level. At alias level priority and fill-first behave
// the same: cooling already moves a request past a member that stopped answering.
const (
	StrategyPriority   Strategy = "priority"
	StrategyFillFirst  Strategy = "fill-first"
	StrategyRoundRobin Strategy = "round-robin"
	StrategyRandom     Strategy = "random"
	StrategyP2C        Strategy = "p2c"
	StrategyLeastUsed  Strategy = "least-used"
)

// Strategies lists the routing strategies in documentation order.
var Strategies = []Strategy{StrategyPriority, StrategyFillFirst, StrategyRoundRobin, StrategyRandom, StrategyP2C, StrategyLeastUsed}

// Valid reports whether s is one of Strategies; "" is not, callers default it first.
func (s Strategy) Valid() bool {
	switch s {
	case StrategyPriority, StrategyFillFirst, StrategyRoundRobin, StrategyRandom, StrategyP2C, StrategyLeastUsed:
		return true
	}
	return false
}

// Spreads reports a strategy that spreads first tries over the live members.
func (s Strategy) Spreads() bool {
	switch s {
	case StrategyRoundRobin, StrategyRandom, StrategyP2C, StrategyLeastUsed:
		return true
	}
	return false
}

// FirstHeader names the member a spreading alias's request tries first; set only by the proxy.
const FirstHeader = "X-Remora-First"

// Chain is the live chain of one spreading alias as the reconciler last pushed it: distinct member refs
// "<provider>/<model>" (user-facing), declared order, each with its own rule.
type Chain struct {
	Strategy Strategy
	Members  []string
}

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
