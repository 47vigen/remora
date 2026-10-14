package config

import (
	"bytes"
	"cmp"
	"context"
	"slices"

	"go.yaml.in/yaml/v3"
)

// exportHeader opens every exported document.
const exportHeader = "# remora.yaml, exported by remora. Secrets are env:NAME references: set them in the environment of\n" +
	"# `remora apply`. An unset variable keeps the secret remora already has.\n"

// Marshal writes f as YAML in a stable order: every list sorted by name (provider keys by name within their
// provider); alias members keep their order, which is the fallback order. f itself is not changed.
func Marshal(f File) ([]byte, error) {
	out := File{Version: 1}
	for _, p := range f.Providers {
		p.Keys = slices.SortedFunc(slices.Values(p.Keys), func(a, b FileSecretKey) int { return cmp.Compare(a.Name, b.Name) })
		out.Providers = append(out.Providers, p)
	}
	slices.SortFunc(out.Providers, func(a, b FileProvider) int { return cmp.Compare(a.Name, b.Name) })
	out.Aliases = slices.SortedFunc(slices.Values(f.Aliases), func(a, b FileAlias) int { return cmp.Compare(a.Name, b.Name) })
	out.Keys = slices.SortedFunc(slices.Values(f.Keys), func(a, b FileKey) int { return cmp.Compare(a.Name, b.Name) })

	var buf bytes.Buffer
	buf.WriteString(exportHeader)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Export returns the store as a document: every provider with its keys as env:NAME references (EnvName), every
// alias, and every client key without its value. A provider whose kind a document cannot hold is left out.
func (s *Service) Export(ctx context.Context) (File, error) {
	f := File{Version: 1, Providers: []FileProvider{}, Aliases: []FileAlias{}, Keys: []FileKey{}}
	providers, err := s.p.List(ctx)
	if err != nil {
		return File{}, err
	}
	for _, p := range providers {
		if !slices.Contains(providerKinds, p.Kind) {
			continue
		}
		fp := FileProvider{
			Name: p.Name, Kind: p.Kind, Type: p.Type, BaseURL: p.BaseURL, AllowPrivateNetwork: p.AllowPrivateNetwork,
			Keys: []FileSecretKey{},
		}
		for _, k := range p.Keys {
			fp.Keys = append(fp.Keys, FileSecretKey{Name: k.Name, Value: "env:" + EnvName(p.Name, k.Name)})
		}
		f.Providers = append(f.Providers, fp)
	}

	aliases, err := s.a.List(ctx)
	if err != nil {
		return File{}, err
	}
	for _, a := range aliases {
		fa := FileAlias{Name: a.Name, Kind: string(a.Kind), Members: make([]string, len(a.Members)), ContextWindow: a.ContextWindow}
		for i, m := range a.Members {
			fa.Members[i] = m.Ref
		}
		if len(a.Capabilities) > 0 {
			fa.Capabilities = slices.Clone(a.Capabilities)
		}
		f.Aliases = append(f.Aliases, fa)
	}

	keys, err := s.k.List(ctx)
	if err != nil {
		return File{}, err
	}
	for _, k := range keys {
		fk := FileKey{Name: k.Name, Allow: slices.Clone(k.Allow)}
		if fk.Allow == nil {
			fk.Allow = []string{}
		}
		if !k.Enabled {
			off := false
			fk.Enabled = &off
		}
		f.Keys = append(f.Keys, fk)
	}
	return f, nil
}
