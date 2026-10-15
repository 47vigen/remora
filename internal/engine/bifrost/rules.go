package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Route is a provider and model. In a rule target, Model "" is sent as null: the incoming model.
type Route struct{ Provider, Model string }

// RoutingRule is a global front-door routing rule with a target and an ordered fallback chain. With Spread, each
// request goes to Target or one of Spread, picked at random with equal weight. The engine refuses two global rules
// with the same Priority, so each rule needs its own; rules whose CEL expressions never match the same request may
// take any distinct priorities.
type RoutingRule struct {
	ID, Name, CEL string
	Target        Route
	Spread        []Route `json:",omitempty"` // omitted, so rules without it keep the reconciler's stored MACs
	Fallbacks     []Route
	Priority      int
}

type ruleTarget struct {
	Provider string  `json:"provider"`
	Model    *string `json:"model"`
	Weight   float64 `json:"weight"`
}

type ruleFallback struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// UnmarshalJSON reads both forms the engine answers: the "provider/model" string and the object.
func (f *ruleFallback) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		f.Provider, f.Model, _ = strings.Cut(s, "/")
		return nil
	}
	type plain ruleFallback
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("bifrost: decode rule fallback: %w", err)
	}
	*f = ruleFallback(p)
	return nil
}

type ruleBody struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Enabled       bool           `json:"enabled"`
	CelExpression string         `json:"cel_expression"`
	Targets       []ruleTarget   `json:"targets"`
	Fallbacks     []ruleFallback `json:"fallbacks"` // always an array: on update, null or absent would keep the old chain
	Scope         string         `json:"scope"`
	Priority      int            `json:"priority"`
}

type ruleAnswer struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	CelExpression string         `json:"cel_expression"`
	Targets       []ruleTarget   `json:"targets"`
	Fallbacks     []ruleFallback `json:"fallbacks"`
	Priority      int            `json:"priority"`
}

func ruleWire(r RoutingRule) ruleBody {
	// The engine wants the weights to sum to 1.
	weight := 1 / float64(1+len(r.Spread))
	targets := make([]ruleTarget, 0, 1+len(r.Spread))
	for _, t := range append([]Route{r.Target}, r.Spread...) {
		target := ruleTarget{Provider: t.Provider, Weight: weight}
		if t.Model != "" {
			model := t.Model
			target.Model = &model
		}
		targets = append(targets, target)
	}
	fallbacks := make([]ruleFallback, 0, len(r.Fallbacks))
	for _, f := range r.Fallbacks {
		fallbacks = append(fallbacks, ruleFallback(f))
	}
	return ruleBody{
		Name:          r.Name,
		Description:   managedDescription,
		Enabled:       true,
		CelExpression: r.CEL,
		Targets:       targets,
		Fallbacks:     fallbacks,
		Scope:         "global",
		Priority:      r.Priority,
	}
}

func (a ruleAnswer) toRule() RoutingRule {
	r := RoutingRule{ID: a.ID, Name: a.Name, CEL: a.CelExpression, Priority: a.Priority}
	for i, t := range a.Targets {
		route := Route{Provider: t.Provider}
		if t.Model != nil {
			route.Model = *t.Model
		}
		if i == 0 {
			r.Target = route
			continue
		}
		r.Spread = append(r.Spread, route)
	}
	for _, f := range a.Fallbacks {
		r.Fallbacks = append(r.Fallbacks, Route(f))
	}
	return r
}

func rulePath(id string) string {
	return "/api/routing/rules/" + url.PathEscape(id)
}

// ListRoutingRules returns every routing rule.
func (c *Client) ListRoutingRules(ctx context.Context) ([]RoutingRule, error) {
	var out struct {
		Rules []ruleAnswer `json:"rules"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/routing/rules", nil, &out); err != nil {
		return nil, err
	}
	rules := make([]RoutingRule, 0, len(out.Rules))
	for _, a := range out.Rules {
		rules = append(rules, a.toRule())
	}
	return rules, nil
}

// CreateRoutingRule adds a routing rule and returns it with the engine's ID.
func (c *Client) CreateRoutingRule(ctx context.Context, r RoutingRule) (RoutingRule, error) {
	var out struct {
		Rule *ruleAnswer `json:"rule"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/routing/rules", ruleWire(r), &out); err != nil {
		return RoutingRule{}, err
	}
	if out.Rule == nil || out.Rule.ID == "" {
		return RoutingRule{}, fmt.Errorf("bifrost: create routing rule: answer has no id")
	}
	return out.Rule.toRule(), nil
}

// UpdateRoutingRule replaces a routing rule's name, expression, target, fallbacks and priority.
func (c *Client) UpdateRoutingRule(ctx context.Context, r RoutingRule) error {
	if err := requireID("routing rule id", r.ID); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, rulePath(r.ID), ruleWire(r), nil)
}

// DeleteRoutingRule removes a routing rule.
func (c *Client) DeleteRoutingRule(ctx context.Context, id string) error {
	if err := requireID("routing rule id", id); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, rulePath(id), nil, nil)
}
