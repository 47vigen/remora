package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaRejects(t *testing.T) {
	tests := []struct {
		name, doc, want string
	}{
		{"version 2", "version: 2\n", "version"},
		{"alias kind video", "aliases:\n  - {name: v, kind: video, members: [p/m]}\n", "aliases[0].kind"},
		{"provider kind openai", "providers:\n  - {name: p, kind: openai, keys: []}\n", "providers[0].kind"},
		{"builtin without type", "providers:\n  - {name: p, kind: builtin, keys: []}\n", "providers[0]"},
		{"type on a non-builtin", "providers:\n  - {name: p, kind: soniox, type: x, keys: []}\n", "providers[0]"},
		{
			"plaintext provider key",
			"providers:\n  - {name: p, kind: openai-compatible, base_url: http://u, keys: [{name: main, value: plaintext}]}\n",
			"providers[0].keys[0].value",
		},
		{"plaintext client key", "keys:\n  - {name: k, value: sk-plain, allow: ['*']}\n", "keys[0].value"},
		{"unknown top-level field", "accounts: []\n", "accounts"},
		{"unknown nested field", "keys:\n  - {name: k, allow: ['*'], color: red}\n", "color"},
		{"alias without members", "aliases:\n  - {name: a, kind: chat, members: []}\n", "aliases[0].members"},
		{"negative context window", "aliases:\n  - {name: a, kind: chat, members: [p/m], context_window: -1}\n", "aliases[0].context_window"},
		{"not a mapping", "- a\n- b\n", "remora.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.doc))
			if err == nil {
				t.Fatal("Parse accepted the document")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestSchemaAccepts(t *testing.T) {
	for _, doc := range []string{
		"",
		"version: 1\n",
		"version: 1\nproviders: []\naliases: []\nkeys: []\n",
		"providers:\n  - {name: or, kind: builtin, type: openrouter, keys: [{name: main, value: env:OR_KEY}]}\n",
		"keys:\n  - {name: k, allow: ['*'], enabled: false}\n",
	} {
		if _, err := Parse([]byte(doc)); err != nil {
			t.Errorf("Parse(%q): %v", doc, err)
		}
	}
}

func TestPlan02FixtureParses(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "test", "e2e", "testdata", "remora.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(b)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Providers) == 0 || len(f.Aliases) == 0 || len(f.Keys) == 0 {
		t.Errorf("parsed %+v", f)
	}
}

// The schema is published at api/remora.schema.json and embedded from this package's copy.
func TestSchemaCopyMatchesPublished(t *testing.T) {
	published, err := os.ReadFile(filepath.Join("..", "..", "api", "remora.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(published, schemaJSON) {
		t.Error("internal/config/remora.schema.json differs from api/remora.schema.json: copy the published file over")
	}
}
