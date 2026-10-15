package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/47vigen/remora/internal/alias"
)

// File is the one remora.yaml format: the providers, aliases and client keys `remora apply -f` makes the store
// match. Plan 05 extends it (export, diff, a JSON Schema) without a second set of types.
type File struct {
	Version   int            `yaml:"version,omitempty"` // 1; 0 is read as 1
	Providers []FileProvider `yaml:"providers"`
	Aliases   []FileAlias    `yaml:"aliases"`
	Keys      []FileKey      `yaml:"keys"`
}

// FileProvider is an upstream provider and its keys.
type FileProvider struct {
	Name                string          `yaml:"name"`
	Kind                string          `yaml:"kind"`           // builtin | openai-compatible | typesafe-compatible | soniox
	Type                string          `yaml:"type,omitempty"` // builtin only, e.g. openrouter
	BaseURL             string          `yaml:"base_url,omitempty"`
	AllowPrivateNetwork bool            `yaml:"allow_private_network,omitempty"`
	Keys                []FileSecretKey `yaml:"keys"`
}

// FileSecretKey is one provider key. Value must be "env:NAME": a secret never sits in the file.
type FileSecretKey struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// FileAlias is an alias and its ordered members (aliases or "<provider>/<model>").
type FileAlias struct {
	Name          string   `yaml:"name"`
	Kind          string   `yaml:"kind"`
	Members       []string `yaml:"members"`
	Capabilities  []string `yaml:"capabilities,omitempty"`
	ContextWindow int      `yaml:"context_window,omitempty"`
	Strategy      string   `yaml:"strategy,omitempty"` // "round-robin" spreads requests; omitted tries members in order
}

// FileKey is a client key. Value is "env:NAME"; when it is omitted on a new key, apply generates one and prints it
// once.
type FileKey struct {
	Name    string   `yaml:"name"`
	Value   string   `yaml:"value,omitempty"`
	Allow   []string `yaml:"allow"`
	Enabled *bool    `yaml:"enabled,omitempty"` // default true
}

// Provider kinds a file may name.
var providerKinds = []string{"builtin", "openai-compatible", "typesafe-compatible", "soniox"}

// Alias kinds a file may name.
var aliasKinds = []alias.Kind{alias.KindChat, alias.KindEmbeddings, alias.KindTranscription, alias.KindDecisions}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse decodes a remora.yaml and validates it, against Validate's rules and the published JSON Schema
// (api/remora.schema.json). An unknown field is an error naming its line and field; every other error names the
// field's path, such as "keys[0].value". An empty document is an empty File.
func Parse(b []byte) (File, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var f File
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return File{}, fmt.Errorf("remora.yaml: %w", err)
	}
	// The file is the whole truth, so a second document must not be ignored silently.
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return File{}, errors.New("remora.yaml: more than one YAML document; put everything in one")
	}
	if err := f.Validate(); err != nil {
		return File{}, err
	}
	if err := checkSchema(b); err != nil {
		return File{}, err
	}
	return f, nil
}

// SecretRef returns NAME for a value "env:NAME", and "" with an error naming the expected form otherwise.
func SecretRef(v string) (string, error) {
	name, ok := strings.CutPrefix(v, "env:")
	if !ok {
		return "", errors.New(`want "env:NAME" (secrets are read from the environment, never written in the file)`)
	}
	if !envName.MatchString(name) {
		return "", fmt.Errorf("%q is not an environment variable name", name)
	}
	return name, nil
}

// Validate checks what can be checked without the store or the environment. Each error names the field's path.
func (f File) Validate() error {
	if f.Version != 0 && f.Version != 1 {
		return fmt.Errorf("remora.yaml: version: %d is not supported, want 1", f.Version)
	}
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("remora.yaml: "+format, args...))
	}

	providers := map[string]bool{}
	for i, p := range f.Providers {
		at := fmt.Sprintf("providers[%d]", i)
		switch {
		case p.Name == "":
			fail("%s.name: required", at)
		case providers[p.Name]:
			fail("%s.name: %q is listed twice", at, p.Name)
		}
		providers[p.Name] = true
		switch {
		case !slices.Contains(providerKinds, p.Kind):
			fail("%s.kind: %q is not one of %s", at, p.Kind, strings.Join(providerKinds, ", "))
		case p.Kind == "builtin" && p.Type == "":
			fail("%s.type: required for kind builtin", at)
		case p.Kind != "builtin" && p.Type != "":
			fail("%s.type: only kind builtin takes a type", at)
		}
		keys := map[string]bool{}
		for j, k := range p.Keys {
			kat := fmt.Sprintf("%s.keys[%d]", at, j)
			switch {
			case k.Name == "":
				fail("%s.name: required", kat)
			case keys[k.Name]:
				fail("%s.name: %q is listed twice", kat, k.Name)
			}
			keys[k.Name] = true
			if _, err := SecretRef(k.Value); err != nil {
				fail("%s.value: %w", kat, err)
			}
		}
	}

	aliases := map[string]bool{}
	for i, a := range f.Aliases {
		at := fmt.Sprintf("aliases[%d]", i)
		if err := alias.ValidName(a.Name); err != nil {
			fail("%s.name: %w", at, err)
		} else if aliases[a.Name] {
			fail("%s.name: %q is listed twice", at, a.Name)
		}
		aliases[a.Name] = true
		if !slices.Contains(aliasKinds, alias.Kind(a.Kind)) {
			fail("%s.kind: %q is not one of chat, embeddings, transcription, decisions", at, a.Kind)
		}
		if len(a.Members) == 0 {
			fail("%s.members: at least one member is required", at)
		}
		for j, m := range a.Members {
			if m == "" {
				fail("%s.members[%d]: empty", at, j)
			}
		}
		if a.ContextWindow < 0 {
			fail("%s.context_window: must not be negative", at)
		}
		if !alias.Strategy(a.Strategy).Valid() {
			fail("%s.strategy: %q is not round-robin; omit it for ordered fallback", at, a.Strategy)
		}
	}

	clients := map[string]bool{}
	for i, k := range f.Keys {
		at := fmt.Sprintf("keys[%d]", i)
		switch {
		case k.Name == "":
			fail("%s.name: required", at)
		case clients[k.Name]:
			fail("%s.name: %q is listed twice", at, k.Name)
		}
		clients[k.Name] = true
		if k.Value != "" {
			if _, err := SecretRef(k.Value); err != nil {
				fail("%s.value: %w", at, err)
			}
		}
	}
	return errors.Join(errs...)
}
