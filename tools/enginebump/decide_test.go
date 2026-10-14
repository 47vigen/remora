package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var (
	bifrost     = engines[0]
	cliproxyapi = engines[1]
)

func sortedTags(tags []Tag) []Tag {
	out := append([]Tag(nil), tags...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func TestParseLsRemotePeeledWins(t *testing.T) {
	out := "aaa\trefs/tags/v8.0.27\nbbb\trefs/tags/v8.0.28\nccc\trefs/tags/v8.0.28^{}\n"
	got := sortedTags(ParseLsRemote(out))
	want := []Tag{{"v8.0.27", "aaa"}, {"v8.0.28", "ccc"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseLsRemote = %v, want %v", got, want)
	}

	// The peeled line may come first; it still wins.
	got = sortedTags(ParseLsRemote("ccc\trefs/tags/v8.0.28^{}\nbbb\trefs/tags/v8.0.28\n\nnot a line\n"))
	want = []Tag{{"v8.0.28", "ccc"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseLsRemote (peeled first) = %v, want %v", got, want)
	}
}

func TestLatest(t *testing.T) {
	bifrostTags := []Tag{
		{"transports/v2.2.6", "1"},
		{"transports/v2.3.0", "2"},
		{"transports/v2.10.0", "3"},
		{"transports/v2.11.0-prerelease1", "4"},
		{"transports/v3.0.0", "5"},
		{"core/v9.0.0", "6"},
		{"transports/vX", "7"},
	}
	cliTags := []Tag{{"v8.0.28", "a"}, {"v8.0.3", "b"}, {"v9.0.0", "c"}, {"v7.9.9", "d"}}
	cases := []struct {
		name   string
		tags   []Tag
		prefix string
		major  int
		want   string
		ok     bool
	}{
		{"numeric not lexical, prerelease and v3 excluded", bifrostTags, "transports/v", 2, "transports/v2.10.0", true},
		{"no tag of that major", bifrostTags, "transports/v", 4, "", false},
		{"new major excluded", cliTags, "v", 8, "v8.0.28", true},
		{"prefix v ignores prefixed tags", bifrostTags, "v", 2, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Latest(tc.tags, tc.prefix, tc.major)
			if ok != tc.ok || got.Name != tc.want {
				t.Fatalf("Latest = %v, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestCurrentMatchesPeeledCommit(t *testing.T) {
	tags := ParseLsRemote("aaa\trefs/tags/v8.0.27\nbbb\trefs/tags/v8.0.28\nccc\trefs/tags/v8.0.28^{}\n")
	got, ok := Current(tags, "v", "ccc")
	if !ok || got.Name != "v8.0.28" {
		t.Fatalf("Current(ccc) = %v, %v; want v8.0.28, true", got, ok)
	}
	if got, ok := Current(tags, "v", "bbb"); ok {
		t.Fatalf("Current(tag object sha) = %v, true; want false", got)
	}
	if got, ok := Current(tags, "v", "zzz"); ok {
		t.Fatalf("Current(unknown) = %v, true; want false", got)
	}
}

func TestBetween(t *testing.T) {
	tags := []Tag{
		{"v8.0.28", "c"},
		{"v8.0.26", "a"},
		{"v8.0.27", "b"},
		{"v8.0.29", "d"},
		{"v8.0.28-rc.1", "e"},
		{"v9.0.0", "f"},
	}
	got := Between(tags, "v", Tag{"v8.0.26", "a"}, Tag{"v8.0.28", "c"})
	want := []Tag{{"v8.0.27", "b"}, {"v8.0.28", "c"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Between = %v, want %v", got, want)
	}
}

const patchesMD = "# Patches\n\nThe fork rule.\n\n" +
	"| engine | file | change | why | fork commit | upstream PR | base tag | remove when |\n" +
	"|---|---|---|---|---|---|---|---|\n" +
	"| bifrost | core/schemas/transcriptions.go | add speaker | segments carry speakers | abc123 | https://github.com/maximhq/bifrost/pull/123 | transports/v2.3.0 | in a release |\n" +
	"\nTrailing text.\n"

func TestParsePatches(t *testing.T) {
	got, err := ParsePatches(patchesMD)
	if err != nil {
		t.Fatal(err)
	}
	want := []Patch{{"bifrost", "https://github.com/maximhq/bifrost/pull/123", "transports/v2.3.0"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePatches = %v, want %v", got, want)
	}

	reordered := "| Base Tag | Upstream PR | Engine |\n| --- | --- | --- |\n" +
		"| v8.0.28 | https://github.com/router-for-me/CLIProxyAPI/pull/9 | cliproxyapi |\n"
	got, err = ParsePatches(reordered)
	if err != nil {
		t.Fatal(err)
	}
	want = []Patch{{"cliproxyapi", "https://github.com/router-for-me/CLIProxyAPI/pull/9", "v8.0.28"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePatches (reordered) = %v, want %v", got, want)
	}

	got, err = ParsePatches("# Patches\n\nNone yet.\n")
	if err != nil || len(got) != 0 {
		t.Fatalf("ParsePatches (no table) = %v, %v; want empty, nil", got, err)
	}

	empty := "| engine | upstream PR | base tag |\n|---|---|---|\n"
	got, err = ParsePatches(empty)
	if err != nil || len(got) != 0 {
		t.Fatalf("ParsePatches (header only) = %v, %v; want empty, nil", got, err)
	}

	for name, md := range map[string]string{
		"non-URL PR":        "| engine | upstream PR | base tag |\n|---|---|---|\n| bifrost | #123 | transports/v2.3.0 |\n",
		"missing base tag":  "| engine | upstream PR |\n|---|---|\n| bifrost | https://github.com/maximhq/bifrost/pull/123 |\n",
		"unknown engine":    "| engine | upstream PR | base tag |\n|---|---|---|\n| other | https://github.com/maximhq/bifrost/pull/1 | v1.0.0 |\n",
		"PR not a pull URL": "| engine | upstream PR | base tag |\n|---|---|---|\n| bifrost | https://github.com/maximhq/bifrost/issues/1 | transports/v2.3.0 |\n",
	} {
		if _, err := ParsePatches(md); err == nil {
			t.Errorf("ParsePatches (%s): want error", name)
		}
	}
}

func TestWithoutPatch(t *testing.T) {
	got, err := WithoutPatch(patchesMD, "bifrost")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "pull/123") {
		t.Fatalf("row not removed:\n%s", got)
	}
	for _, keep := range []string{"# Patches", "The fork rule.", "| engine | file |", "|---|", "Trailing text."} {
		if !strings.Contains(got, keep) {
			t.Fatalf("lost %q:\n%s", keep, got)
		}
	}
	rows, err := ParsePatches(got)
	if err != nil || len(rows) != 0 {
		t.Fatalf("ParsePatches after removal = %v, %v", rows, err)
	}
	if _, err := WithoutPatch(patchesMD, "cliproxyapi"); err == nil {
		t.Fatal("WithoutPatch for an engine without a row: want error")
	}
}

func TestDecide(t *testing.T) {
	ctx := context.Background()
	cliTags := []Tag{{"v8.0.26", "s26"}, {"v8.0.27", "s27"}, {"v8.0.28", "s28"}}
	withMajor := append(append([]Tag(nil), cliTags...), Tag{"v9.0.0", "s90"})
	bifTags := []Tag{
		{"transports/v2.3.0", "b230"},
		{"transports/v2.3.1", "b231"},
		{"transports/v2.4.0", "b240"},
		{"transports/v2.5.0-prerelease1", "b25p"},
		{"transports/v3.0.0", "b300"},
	}
	fork := "https://github.com/example/bifrost-fork.git"
	patch := &Patch{"bifrost", "https://github.com/maximhq/bifrost/pull/123", "transports/v2.3.0"}
	never := func(context.Context, string, Tag) (bool, error) {
		t.Fatal("merged called for an upstream engine")
		return false, nil
	}
	mergedIn := func(want string) func(context.Context, string, Tag) (bool, error) {
		return func(_ context.Context, pr string, tag Tag) (bool, error) {
			if pr != patch.PR {
				t.Fatalf("merged called with PR %q", pr)
			}
			return tag.Name == want, nil
		}
	}
	errBoom := errors.New("boom")

	cases := []struct {
		name     string
		e        Engine
		url      string
		gitlink  string
		tags     []Tag
		patch    *Patch
		merged   func(context.Context, string, Tag) (bool, error)
		kind     Kind
		from, to string
		note     string
		err      string
		errIs    error
	}{
		{name: "at latest", e: cliproxyapi, url: cliproxyapi.Upstream, gitlink: "s28", tags: cliTags, merged: never, kind: KindNone, from: "v8.0.28", to: "v8.0.28"},
		{name: "upstream URL with .git and slash", e: cliproxyapi, url: "https://GitHub.com/router-for-me/CLIProxyAPI.git/", gitlink: "s28", tags: cliTags, merged: never, kind: KindNone, from: "v8.0.28", to: "v8.0.28"},
		{name: "behind", e: cliproxyapi, url: cliproxyapi.Upstream, gitlink: "s26", tags: cliTags, merged: never, kind: KindBump, from: "v8.0.26", to: "v8.0.28"},
		{name: "behind with a new major too", e: cliproxyapi, url: cliproxyapi.Upstream, gitlink: "s26", tags: withMajor, merged: never, kind: KindBump, from: "v8.0.26", to: "v8.0.28", note: "v9.0.0"},
		{name: "major", e: cliproxyapi, url: cliproxyapi.Upstream, gitlink: "s28", tags: withMajor, merged: never, kind: KindMajor, from: "v8.0.28", note: "v9.0.0"},
		{name: "untagged", e: cliproxyapi, url: cliproxyapi.Upstream, gitlink: "dead", tags: cliTags, merged: never, err: "pin engines/cliproxyapi to a release tag by hand"},
		{name: "fork held", e: bifrost, url: fork, gitlink: "forkhead", tags: bifTags, patch: patch, merged: mergedIn("none"), kind: KindHold, note: patch.PR},
		{name: "fork unfork", e: bifrost, url: fork, gitlink: "forkhead", tags: bifTags, patch: patch, merged: mergedIn("transports/v2.4.0"), kind: KindUnfork, to: "transports/v2.4.0"},
		{name: "fork without row", e: bifrost, url: fork, gitlink: "forkhead", tags: bifTags, merged: never, err: "engines/bifrost points at a fork with no docs/patches.md row"},
		{name: "merged errors", e: bifrost, url: fork, gitlink: "forkhead", tags: bifTags, patch: patch, merged: func(context.Context, string, Tag) (bool, error) { return false, errBoom }, errIs: errBoom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := Decide(ctx, tc.e, tc.url, tc.gitlink, tc.tags, tc.patch, tc.merged)
			if tc.err != "" || tc.errIs != nil {
				if err == nil {
					t.Fatalf("Decide = %+v, want error", a)
				}
				if tc.err != "" && !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error %q does not contain %q", err, tc.err)
				}
				if tc.errIs != nil && !errors.Is(err, tc.errIs) {
					t.Fatalf("error %v does not wrap %v", err, tc.errIs)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if a.Kind != tc.kind {
				t.Fatalf("Kind = %q, want %q (note %q)", a.Kind, tc.kind, a.Note)
			}
			if tc.from != "" && a.From.Name != tc.from {
				t.Fatalf("From = %v, want %s", a.From, tc.from)
			}
			if tc.to != "" && a.To.Name != tc.to {
				t.Fatalf("To = %v, want %s", a.To, tc.to)
			}
			if a.Engine.Name != tc.e.Name {
				t.Fatalf("Engine = %v", a.Engine)
			}
			if a.Note == "" || strings.Contains(a.Note, "\n") {
				t.Fatalf("Note must be one non-empty line, got %q", a.Note)
			}
			if tc.note != "" && !strings.Contains(a.Note, tc.note) {
				t.Fatalf("Note %q does not name %q", a.Note, tc.note)
			}
		})
	}
}

func TestPRTitle(t *testing.T) {
	bump := Action{Kind: KindBump, Engine: bifrost, To: Tag{"transports/v2.3.1", "x"}}
	if got, want := PRTitle(bump), "deps(engines): bump bifrost to transports/v2.3.1"; got != want {
		t.Fatalf("PRTitle(bump) = %q, want %q", got, want)
	}
	unfork := Action{Kind: KindUnfork, Engine: bifrost, To: Tag{"transports/v2.4.0", "x"}}
	if got, want := PRTitle(unfork), "deps(engines): return bifrost to upstream transports/v2.4.0"; got != want {
		t.Fatalf("PRTitle(unfork) = %q, want %q", got, want)
	}
}

func TestPRBodyLinksEveryRelease(t *testing.T) {
	a := Action{Kind: KindBump, Engine: bifrost, From: Tag{"transports/v2.3.0", "a"}, To: Tag{"transports/v2.4.0", "c"}}
	between := []Tag{{"transports/v2.3.1", "b"}, {"transports/v2.4.0", "c"}}
	body := PRBody(a, between)
	for _, tag := range []string{"transports%2Fv2.3.1", "transports%2Fv2.4.0"} {
		link := "https://github.com/maximhq/bifrost/releases/tag/" + tag
		if !strings.Contains(body, link) {
			t.Fatalf("body lacks %s:\n%s", link, body)
		}
	}
	if strings.Contains(body, "releases/tag/transports%2Fv2.3.0") {
		t.Fatalf("body links the current release:\n%s", body)
	}
}

func TestWithVersion(t *testing.T) {
	env := "# pins\nBIFROST_VERSION=transports/v2.3.0\nCLIPROXYAPI_VERSION=v8.0.28\n"
	got, err := WithVersion(env, "CLIPROXYAPI_VERSION", "v8.1.0")
	if err != nil {
		t.Fatal(err)
	}
	want := "# pins\nBIFROST_VERSION=transports/v2.3.0\nCLIPROXYAPI_VERSION=v8.1.0\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := WithVersion(env, "OTHER_VERSION", "v1"); err == nil {
		t.Error("missing line: want an error")
	}
}

func TestEveryEngineHasAVersionLine(t *testing.T) {
	env, err := os.ReadFile("../../engines/versions.env")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range engines {
		if _, err := WithVersion(string(env), e.VersionVar, "x"); err != nil {
			t.Errorf("%s: %v", e.Name, err)
		}
	}
}
