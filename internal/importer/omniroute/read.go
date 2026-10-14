package omniroute

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the "sqlite" database/sql driver
)

// Node is a provider_nodes row: a custom provider such as an OpenAI-compatible endpoint.
type Node struct{ ID, Type, Name, Prefix, BaseURL string }

// Connection is a provider_connections row with its secrets decrypted.
type Connection struct {
	ID, Provider, AuthType, Name, Email, ProjectID string
	APIKey, AccessToken, RefreshToken, IDToken     string
	ExpiresAt                                      string
	ExpiresIn                                      int
	Active                                         bool
	Specific                                       map[string]any // provider_specific_data
}

// APIKey is an api_keys row: a client key, kept in plaintext by OmniRoute.
type APIKey struct {
	ID, Name, Key                string
	AllowedModels, AllowedCombos []string
	AccessMode                   string // "all" | "restricted"
	Revoked, Expired             bool
	Disabled                     bool // is_active = 0 or is_banned = 1
}

// Combo is a combos row; Data is its JSON definition.
type Combo struct {
	Name string
	Data json.RawMessage
}

// Snapshot is what Read finds in an OmniRoute database.
type Snapshot struct {
	Nodes       []Node
	Connections []Connection
	Keys        []APIKey
	Combos      []Combo
}

// allCombos is OmniRoute's allowed_combos rule for "every combo" (src/shared/constants/comboAccess.ts); a NULL
// allowed_combos column means it too.
const allCombos = "combo/*"

// The columns of 001_initial_schema.sql the importer reads; a database without them is not OmniRoute's.
var required = map[string][]string{
	"provider_nodes": {"id", "type", "name", "prefix", "base_url"},
	"provider_connections": {
		"id", "provider", "auth_type", "name", "email", "is_active", "access_token", "refresh_token", "expires_at",
		"project_id", "api_key", "id_token", "provider_specific_data", "expires_in",
	},
	"combos":   {"name", "data"},
	"api_keys": {"id", "name", "key"},
}

type row map[string]any

// Read opens <dataDir>/storage.sqlite read-only and immutable (no write, no journal, no WAL checkpoint), decrypts
// connection secrets with the STORAGE_ENCRYPTION_KEY that LoadSecret finds, and returns every row the importer
// converts. Columns later OmniRoute versions added are optional. now decides which client keys have expired.
func Read(ctx context.Context, dataDir string, env func(string) (string, bool), now time.Time) (Snapshot, error) {
	path, err := filepath.Abs(filepath.Join(dataDir, "storage.sqlite"))
	if err != nil {
		return Snapshot{}, err
	}
	if _, err := os.Stat(path); err != nil {
		return Snapshot{}, fmt.Errorf("OmniRoute database: %w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = db.Close() }()

	tables := map[string][]row{}
	for _, t := range []string{"provider_nodes", "provider_connections", "combos", "api_keys"} {
		rows, cols, err := selectAll(ctx, db, t)
		if err != nil {
			return Snapshot{}, fmt.Errorf("%s is not an OmniRoute database: %w", path, err)
		}
		for _, c := range required[t] {
			if !cols[c] {
				return Snapshot{}, fmt.Errorf("%s is not an OmniRoute database: table %s has no column %s", path, t, c)
			}
		}
		tables[t] = rows
	}

	var s Snapshot
	for _, r := range tables["provider_nodes"] {
		s.Nodes = append(s.Nodes, Node{ID: r.str("id"), Type: r.str("type"), Name: r.str("name"), Prefix: r.str("prefix"), BaseURL: r.str("base_url")})
	}
	if s.Connections, err = connections(tables["provider_connections"], dataDir, env); err != nil {
		return Snapshot{}, err
	}
	for _, r := range tables["api_keys"] {
		s.Keys = append(s.Keys, apiKey(r, now))
	}
	for _, r := range tables["combos"] {
		s.Combos = append(s.Combos, Combo{Name: r.str("name"), Data: json.RawMessage(r.str("data"))})
	}
	return s, nil
}

// secretFields are the only columns OmniRoute encrypts (encryptConnectionFields).
var secretFields = []string{"api_key", "access_token", "refresh_token", "id_token"}

func connections(rows []row, dataDir string, env func(string) (string, bool)) ([]Connection, error) {
	var probe string
	for _, r := range rows {
		for _, f := range secretFields {
			if v := r.str(f); probe == "" && strings.HasPrefix(v, prefix) {
				probe = v
			}
		}
	}
	d := &Decrypter{} // no keys: only plaintext values pass
	if probe != "" {
		cands, err := LoadSecret(dataDir, env)
		if err != nil {
			return nil, err
		}
		if len(cands) == 0 {
			return nil, fmt.Errorf("the OmniRoute database is encrypted: set %s or put it in %s", secretVar, filepath.Join(dataDir, "server.env"))
		}
		if d, err = PickSecret(cands, probe); err != nil {
			return nil, err
		}
	}

	out := make([]Connection, 0, len(rows))
	for _, r := range rows {
		c := Connection{
			ID: r.str("id"), Provider: r.str("provider"), AuthType: r.str("auth_type"), Name: r.str("name"),
			Email: r.str("email"), ProjectID: r.str("project_id"), ExpiresAt: r.str("expires_at"),
			ExpiresIn: int(r.int("expires_in", 0)), Active: r.int("is_active", 1) != 0,
		}
		if raw := r.str("provider_specific_data"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &c.Specific) // not JSON: no provider-specific data
		}
		if p, ok := c.Specific["projectId"].(string); ok && c.ProjectID == "" {
			c.ProjectID = p
		}
		for _, f := range []struct {
			col string
			dst *string
		}{{"api_key", &c.APIKey}, {"access_token", &c.AccessToken}, {"refresh_token", &c.RefreshToken}, {"id_token", &c.IDToken}} {
			v, err := d.Decrypt(r.str(f.col))
			if err != nil {
				return nil, fmt.Errorf("connection %s (%s): %s: %w", c.ID, c.Provider, f.col, err)
			}
			*f.dst = v
		}
		out = append(out, c)
	}
	return out, nil
}

// apiKey mirrors OmniRoute's row parsers (src/lib/db/apiKeys/rowParsers.ts, modelAccessMode.ts).
func apiKey(r row, now time.Time) APIKey {
	k := APIKey{
		ID: r.str("id"), Name: r.str("name"), Key: r.str("key"),
		AllowedModels: stringList(r["allowed_models"]),
		Revoked:       strings.TrimSpace(r.str("revoked_at")) != "",
		Disabled:      r.int("is_active", 1) == 0 || r.int("is_banned", 0) == 1,
	}
	if v, ok := r["allowed_combos"]; ok && v != nil {
		k.AllowedCombos = stringList(v)
	} else {
		k.AllowedCombos = []string{allCombos}
	}
	k.AccessMode = "all"
	if r.str("model_access_mode") == "restricted" || legacyRestriction(r["allowed_models"]) {
		k.AccessMode = "restricted"
	}
	if at, ok := parseTime(r.str("expires_at")); ok && !at.After(now) {
		k.Expired = true
	}
	return k
}

// stringList parses a JSON array of strings; anything else is an empty list.
func stringList(v any) []string {
	s, _ := v.(string)
	var raw []any
	if strings.TrimSpace(s) == "" || json.Unmarshal([]byte(s), &raw) != nil {
		return nil
	}
	var out []string
	for _, e := range raw {
		if str, ok := e.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

// legacyRestriction reports a non-empty allowed_models, which OmniRoute reads as "restricted" whatever the mode.
func legacyRestriction(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" || s == "null" {
		return false
	}
	var parsed any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return true
	}
	if a, ok := parsed.([]any); ok {
		return len(a) > 0
	}
	return parsed != nil
}

func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// selectAll reads every row of a table by column name, so columns a given OmniRoute version lacks are tolerated.
func selectAll(ctx context.Context, db *sql.DB, table string) ([]row, map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+table) //nolint:gosec // G202: table is one of four constants.
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	names, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	cols := map[string]bool{}
	for _, n := range names {
		cols[n] = true
	}
	var out []row
	for rows.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		r := row{}
		for i, n := range names {
			r[n] = vals[i]
		}
		out = append(out, r)
	}
	return out, cols, rows.Err()
}

func (r row) str(col string) string {
	switch v := r[col].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

func (r row) int(col string, def int64) int64 {
	switch v := r[col].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
	case bool:
		if v {
			return 1
		}
		return 0
	}
	return def
}
