package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/alias"
	"github.com/47vigen/remora/internal/api/gen"
	"github.com/47vigen/remora/internal/engine/bifrost"
)

// LogSource reads the front door's request log; *bifrost.Client satisfies it.
type LogSource interface {
	SearchLogs(ctx context.Context, f bifrost.LogFilter) (bifrost.LogPage, error)
	GetLog(ctx context.Context, id string) (bifrost.Log, error)
	LogStats(ctx context.Context, f bifrost.LogFilter) (bifrost.LogStats, error)
	Histogram(ctx context.Context, f bifrost.LogFilter) ([]bifrost.HistogramBucket, error)
	TokenHistogram(ctx context.Context, f bifrost.LogFilter) ([]bifrost.TokenBucket, error)
	CostHistogram(ctx context.Context, f bifrost.LogFilter) ([]bifrost.CostBucket, error)
	LatencyHistogram(ctx context.Context, f bifrost.LogFilter) ([]bifrost.LatencyBucket, error)
	ModelUsage(ctx context.Context, f bifrost.LogFilter) ([]bifrost.ModelUsage, error)
	LogModels(ctx context.Context, query string) ([]string, error)
}

var _ LogSource = (*bifrost.Client)(nil)

// scrubRules rewrite engine names, engine keys and loopback addresses out of text the engines wrote, in order.
var scrubRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)sk-bf-[A-Za-z0-9_-]+`), "[redacted]"},
	{regexp.MustCompile(`(?i)remora-oauth-key`), "[redacted]"},
	// The OAuth engine's front-door provider prefixes its models ("remora-oauth/codex/gpt-x") and names its routing
	// rules ("remora-oauth-codex"); users know these as "codex/gpt-x" and "codex".
	{regexp.MustCompile(`(?i)remora-oauth[/-]`), ""},
	{regexp.MustCompile(`(?i)remora-oauth`), "oauth engine"},
	{regexp.MustCompile(`(?i)cli ?proxy ?api|cliproxy`), "oauth engine"},
	{regexp.MustCompile(`(?i)bifrost`), "gateway"},
	{regexp.MustCompile(`(?i)(?:127\.0\.0\.1|localhost):[0-9]+`), "upstream"},
}

// Scrub removes engine names, engine key values and loopback addresses from s, case-insensitively. Other text,
// including non-ASCII text, is left byte for byte.
func Scrub(s string) string {
	for _, r := range scrubRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// userTarget maps a front-door provider and model to the names a user sees. The OAuth engine's members are served
// as provider "remora-oauth" with model "<name>/<model>"; every other provider keeps its name.
func userTarget(engineProvider, engineModel string) alias.Target {
	if engineProvider != bifrost.OAuthProvider {
		return alias.Target{Provider: engineProvider, Model: engineModel}
	}
	if name, model, ok := strings.Cut(engineModel, "/"); ok && name != "" && model != "" {
		return alias.Target{Provider: name, Model: model}
	}
	return alias.Target{Provider: "oauth", Model: engineModel}
}

// errorMessage reads error_details.error.message.
func errorMessage(details json.RawMessage) string {
	if len(details) == 0 {
		return ""
	}
	var d struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(details, &d) != nil {
		return ""
	}
	return d.Error.Message
}

// ruleAlias names the alias a routing rule belongs to, or "".
func (s *Server) ruleAlias(ctx context.Context, rule *string) string {
	if rule == nil || *rule == "" {
		return ""
	}
	name, _ := s.d.Names.AliasByRule(ctx, *rule)
	return name
}

// toEntry maps a front-door log row to remora's names: the client key by its virtual key, the alias by its routing
// rule (rootAlias, the alias of the request's first attempt, when the row has none), the provider and model as the
// user knows them, and the error text scrubbed of engine names.
func (s *Server) toEntry(ctx context.Context, l bifrost.Log, rootAlias string) gen.LogEntry {
	t := s.d.Names.Target(ctx, l.Provider, l.Model)
	e := gen.LogEntry{
		Id: l.ID, Attempt: l.FallbackIndex, Time: l.Timestamp, Provider: t.Provider, Model: t.Model,
		Status: gen.LogEntryStatus(l.Status), Stream: l.Stream, CostUsd: l.Cost,
	}
	if l.ParentRequestID != nil && *l.ParentRequestID != "" {
		e.ParentId = new(*l.ParentRequestID)
	}
	if l.VirtualKeyID != nil && *l.VirtualKeyID != "" {
		if id, _, ok := s.d.Names.KeyByVK(ctx, *l.VirtualKeyID); ok {
			e.Key = &id
		}
	}
	if a := s.ruleAlias(ctx, l.RoutingRuleID); a != "" {
		e.Alias = &a
	} else if l.RoutingRuleID == nil && rootAlias != "" {
		e.Alias = new(rootAlias)
	}
	if l.Latency != nil {
		e.LatencyMs = new(int(math.Round(*l.Latency)))
	}
	if u := l.TokenUsage; u != nil {
		e.Tokens = gen.TokenUsage{Prompt: int(u.PromptTokens), Completion: int(u.CompletionTokens), Total: int(u.TotalTokens)}
	}
	if msg := errorMessage(l.ErrorDetails); msg != "" {
		e.Error = new(Scrub(msg))
	}
	return e
}

// errNoMatch is a filter naming a key or alias the front door does not know: the answer is empty, not an error.
var errNoMatch = errors.New("no log entry can match the filter")

// scope narrows f to a client key, an alias, a provider and a served model ("<provider>/<model>"), translated to
// the front door's ids. Empty arguments leave f as it is. A key or alias with no front-door id is errNoMatch.
func (s *Server) scope(ctx context.Context, f *bifrost.LogFilter, key, aliasName, provider, model string) error {
	if key != "" {
		vk, ok := s.d.Names.VKByKey(ctx, key)
		if !ok {
			return errNoMatch
		}
		f.VirtualKeyIDs = []string{vk}
	}
	if aliasName != "" {
		rule, ok := s.d.Names.RuleByAlias(ctx, aliasName)
		if !ok {
			return errNoMatch
		}
		f.RoutingRuleIDs = []string{rule}
	}
	if provider != "" || model != "" {
		f.Providers, f.Models = s.d.Names.EngineFilter(ctx, provider, model)
	}
	if provider != "" && model == "" && isSubscription(provider) {
		ms, err := s.subscriptionModels(ctx, provider)
		if err != nil {
			return err
		}
		if len(ms) == 0 {
			return errNoMatch
		}
		f.Models = ms
	}
	return nil
}

// subscriptionModels lists the models a subscription provider has served. The front door logs every subscription
// provider under the OAuth engine's one provider, so the provider alone would match them all; its models, which
// carry its name as their prefix, tell them apart.
func (s *Server) subscriptionModels(ctx context.Context, provider string) ([]string, error) {
	all, err := s.d.Logs.LogModels(ctx, provider+"/")
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(m string) bool { return !strings.HasPrefix(m, provider+"/") }), nil
}

// statsWindow is the default time range: the 24 hours before to.
const statsWindow = 24 * time.Hour

// timeRange resolves the contract's from and to: to defaults to now, from to 24 hours before to.
func (s *Server) timeRange(from, to *time.Time) (time.Time, time.Time, error) {
	end := s.now()
	if to != nil {
		end = *to
	}
	start := end.Add(-statsWindow)
	if from != nil {
		start = *from
	}
	if start.After(end) {
		return time.Time{}, time.Time{}, badRequest("from must not be after to")
	}
	return start, end, nil
}

// toFilter maps listLogs' parameters to a front-door filter: remora names become engine ids, the time range takes
// the contract's defaults, q searches the content. A key or alias the front door does not know is errNoMatch.
func (s *Server) toFilter(ctx context.Context, p gen.ListLogsParams) (bifrost.LogFilter, error) {
	var f bifrost.LogFilter
	start, end, err := s.timeRange(p.From, p.To)
	if err != nil {
		return f, err
	}
	f.Start, f.End = &start, &end
	if err := s.scope(ctx, &f, deref(p.Key), deref(p.Alias), deref(p.Provider), deref(p.Model)); err != nil {
		return f, err
	}
	if p.Status != nil {
		f.Status = []string{string(*p.Status)}
	}
	f.ContentSearch = deref(p.Q)
	return f, nil
}
