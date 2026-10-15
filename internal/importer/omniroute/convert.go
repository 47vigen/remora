package omniroute

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/47vigen/remora/internal/config"
	"github.com/47vigen/remora/internal/engine/bifrost"
)

// Result is what an OmniRoute snapshot becomes: a remora.yaml document whose secrets are env: references, the
// values of those references, the OAuth credential files to import as accounts, and what could not be carried over.
type Result struct {
	Doc         config.File
	Secrets     config.Secrets
	Credentials []Credential
	Warnings    []string
}

// apiKeyProviders maps OmniRoute's API-key providers that are not front-door builtins to their remora shape. The base
// URLs are the ones OmniRoute calls (its provider registry); every other provider must be a front-door builtin, as
// opencode-go is.
var apiKeyProviders = map[string]config.FileProvider{
	"opencode":    {Kind: "openai-compatible", BaseURL: "https://opencode.ai/zen/v1"},
	"soniox":      {Kind: "soniox"},
}

// compatPrefix starts the id of an OmniRoute custom OpenAI-compatible node (src/app/api/provider-nodes/route.ts).
const compatPrefix = "openai-compatible-"

type converter struct {
	s     Snapshot
	now   time.Time
	nodes map[string]Node
	r     Result
}

// Convert maps a snapshot onto remora. It never drops something silently: what it cannot carry over is a warning,
// and a combo member naming a provider remora does not have is kept verbatim for the server's plan to reject.
func Convert(s Snapshot, now time.Time) Result {
	c := &converter{s: s, now: now, nodes: map[string]Node{}, r: Result{Doc: config.File{Version: 1}, Secrets: config.Secrets{}}}
	for _, n := range s.Nodes {
		c.nodes[n.ID] = n
	}
	c.providers()
	c.aliases()
	c.keys()
	return c.r
}

func (c *converter) warn(format string, args ...any) {
	c.r.Warnings = append(c.r.Warnings, fmt.Sprintf(format, args...))
}

// providerName is the remora provider an OmniRoute provider id becomes: the id itself, or a custom node's prefix
// (so combo members "<prefix>/<model>" keep working).
func (c *converter) providerName(id string) string {
	if n, ok := c.nodes[id]; ok && n.Prefix != "" {
		return n.Prefix
	}
	return id
}

func (c *converter) providers() {
	index := map[string]int{}
	for _, conn := range c.s.Connections {
		if conn.AuthType == "oauth" {
			c.account(conn)
			continue
		}
		label := conn.Name
		if label == "" {
			label = conn.ID
		}
		if !conn.Active {
			c.warn("skipped %s connection %s: inactive", conn.Provider, label)
			continue
		}
		if conn.APIKey == "" {
			c.warn("skipped %s connection %s: no API key", conn.Provider, label)
			continue
		}
		p := config.FileProvider{Name: conn.Provider, Kind: "builtin", Type: conn.Provider}
		if known, ok := apiKeyProviders[conn.Provider]; ok {
			known.Name = conn.Provider
			p = known
		} else if node, ok := c.nodes[conn.Provider]; ok || strings.HasPrefix(conn.Provider, compatPrefix) {
			if !ok || !strings.HasPrefix(node.ID, compatPrefix) || node.Prefix == "" || node.BaseURL == "" {
				c.warn("skipped %s connection %s: not supported (only OpenAI-compatible nodes with a prefix and a base URL are)", conn.Provider, label)
				continue
			}
			p = config.FileProvider{Name: node.Prefix, Kind: "openai-compatible", BaseURL: node.BaseURL}
		} else if !bifrost.IsStandardProvider(conn.Provider) {
			c.warn("skipped %s connection %s: not supported (no front-door builtin of that name)", conn.Provider, label)
			continue
		}
		i, ok := index[p.Name]
		if !ok {
			i = len(c.r.Doc.Providers)
			index[p.Name] = i
			c.r.Doc.Providers = append(c.r.Doc.Providers, p)
		}
		prov := &c.r.Doc.Providers[i]
		key := fmt.Sprintf("key-%d", len(prov.Keys)+1)
		env := config.EnvName(prov.Name, key)
		prov.Keys = append(prov.Keys, config.FileSecretKey{Name: key, Value: "env:" + env})
		c.r.Secrets[env] = conn.APIKey
	}
}

func (c *converter) account(conn Connection) {
	who := conn.Email
	if who == "" {
		who = conn.Name
	}
	if !conn.Active {
		c.warn("skipped %s account %s: inactive", conn.Provider, who)
		return
	}
	var cred Credential
	var err error
	switch conn.Provider {
	case "antigravity":
		cred, err = antigravityFile(conn, c.now)
	case "codex":
		cred, err = codexFile(conn)
	default:
		c.warn("skipped %s account %s: not supported", conn.Provider, who)
		return
	}
	if err != nil {
		c.warn("skipped %s account %s: %v", conn.Provider, who, err)
		return
	}
	c.r.Credentials = append(c.r.Credentials, cred)
}

// comboData is the part of a combo definition the importer understands.
type comboData struct {
	Models        []json.RawMessage `json:"models"`
	Strategy      string            `json:"strategy"`
	ContextLength *float64          `json:"context_length"`
	ContextCamel  *float64          `json:"contextLength"`
	Dimensions    *float64          `json:"dimensions"`
}

type comboEntry struct {
	Kind       string `json:"kind"`
	ComboName  string `json:"comboName"`
	Model      string `json:"model"`
	ProviderID string `json:"providerId"`
}

func (c *converter) aliases() {
	for _, combo := range c.s.Combos {
		var d comboData
		if err := json.Unmarshal(combo.Data, &d); err != nil {
			c.warn("skipped combo %s: its definition is not JSON: %v", combo.Name, err)
			continue
		}
		a := config.FileAlias{Name: combo.Name, Kind: "chat"}
		if d.Dimensions != nil && *d.Dimensions > 0 {
			a.Kind = "embeddings"
		}
		switch {
		case d.ContextLength != nil:
			a.ContextWindow = int(*d.ContextLength)
		case d.ContextCamel != nil:
			a.ContextWindow = int(*d.ContextCamel)
		}
		if d.Strategy != "" && d.Strategy != "priority" {
			c.warn("alias %s: strategy %s becomes ordered fallback", combo.Name, d.Strategy)
		}
		for i, raw := range d.Models {
			m, ok := c.member(raw)
			if !ok {
				c.warn("alias %s: member %d (%s) skipped: not understood", combo.Name, i+1, raw)
				continue
			}
			a.Members = append(a.Members, m)
		}
		c.r.Doc.Aliases = append(c.r.Doc.Aliases, a)
	}
}

// member converts one combo entry: "<provider>/<model>" or an alias name; {kind: combo-ref, comboName}; or
// {model, providerId}.
func (c *converter) member(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, s != ""
	}
	var e comboEntry
	if json.Unmarshal(raw, &e) != nil {
		return "", false
	}
	switch {
	case e.Kind == "combo-ref":
		return e.ComboName, e.ComboName != ""
	case strings.Contains(e.Model, "/"):
		return e.Model, true
	case e.Model != "" && e.ProviderID != "":
		return c.providerName(e.ProviderID) + "/" + e.Model, true
	case e.Model != "":
		return e.Model, true
	}
	return "", false
}

func (c *converter) keys() {
	var combos []string
	for _, cb := range c.s.Combos {
		combos = append(combos, cb.Name)
	}
	used := map[string]bool{}
	for _, k := range c.s.Keys {
		if k.Revoked {
			c.warn("skipped key %s: revoked", k.Name)
			continue
		}
		name := k.Name
		// Two names can share an environment variable ("a-b", "a.b"), which would mix up their values.
		for i := 2; name == "" || used[config.EnvName("", name)]; i++ {
			name = fmt.Sprintf("%s-%d", k.Name, i)
		}
		if name != k.Name {
			c.warn("key %q renamed %s: names must be unique", k.Name, name)
		}
		used[config.EnvName("", name)] = true

		fk := config.FileKey{Name: name, Value: "env:" + config.EnvName("", name), Allow: []string{"*"}}
		if k.AccessMode != "all" {
			fk.Allow = []string{}
			for _, m := range slices.Concat(k.AllowedModels, k.AllowedCombos) {
				add := []string{m}
				if m == allCombos {
					add = combos
				}
				for _, a := range add {
					if !slices.Contains(fk.Allow, a) {
						fk.Allow = append(fk.Allow, a)
					}
				}
			}
		}
		if k.Expired || k.Disabled {
			off := false
			fk.Enabled = &off
		}
		c.r.Doc.Keys = append(c.r.Doc.Keys, fk)
		c.r.Secrets[config.EnvName("", name)] = k.Key
	}
}
