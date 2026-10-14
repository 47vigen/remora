package main

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
)

// Engine is one engine submodule and the upstream repository it follows.
type Engine struct {
	Name      string // "bifrost" | "cliproxyapi"
	Path      string // "engines/bifrost"
	Upstream  string // "https://github.com/maximhq/bifrost"
	TagPrefix string // "transports/v" | "v"
	// VersionVar is the engine's line in engines/versions.env, which must name the tag the gitlink points at.
	VersionVar string // "BIFROST_VERSION" | "CLIPROXYAPI_VERSION"
}

var engines = []Engine{
	{"bifrost", "engines/bifrost", "https://github.com/maximhq/bifrost", "transports/v", "BIFROST_VERSION"},
	{"cliproxyapi", "engines/cliproxyapi", "https://github.com/router-for-me/CLIProxyAPI", "v", "CLIPROXYAPI_VERSION"},
}

// WithVersion returns versions.env with the line for name set to value, keeping every other line as it is.
func WithVersion(env, name, value string) (string, error) {
	lines := strings.Split(env, "\n")
	found := false
	for i, line := range lines {
		k, _, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == name {
			lines[i] = name + "=" + value
			found = true
		}
	}
	if !found {
		return "", fmt.Errorf("%s has no %s line", versionsFile, name)
	}
	return strings.Join(lines, "\n"), nil
}

// Tag is an upstream release tag. SHA is the commit (peeled) SHA, never the tag object's.
type Tag struct{ Name, SHA string }

// ParseLsRemote reads `git ls-remote --tags` output. For an annotated tag the peeled `^{}` line names the commit
// and wins over the tag object line, whatever their order.
func ParseLsRemote(out string) []Tag {
	shas := map[string]string{}
	peeled := map[string]bool{}
	var order []string
	for line := range strings.SplitSeq(out, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		name, ok := strings.CutPrefix(ref, "refs/tags/")
		if !ok || sha == "" {
			continue
		}
		isPeeled := false
		if base, ok := strings.CutSuffix(name, "^{}"); ok {
			name, isPeeled = base, true
		}
		if _, seen := shas[name]; !seen {
			order = append(order, name)
		}
		if isPeeled || !peeled[name] {
			shas[name] = sha
			peeled[name] = peeled[name] || isPeeled
		}
	}
	tags := make([]Tag, 0, len(order))
	for _, name := range order {
		tags = append(tags, Tag{name, shas[name]})
	}
	return tags
}

// version maps a tag to its semver form ("transports/v2.3.0" with prefix "transports/v" is "v2.3.0").
func version(name, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return "", false
	}
	v := "v" + rest
	if !semver.IsValid(v) || semver.Build(v) != "" {
		return "", false
	}
	return v, true
}

func stable(name, prefix string) (string, bool) {
	v, ok := version(name, prefix)
	if !ok || semver.Prerelease(v) != "" {
		return "", false
	}
	return v, true
}

func majorOf(v string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(semver.Major(v), "v"))
	if err != nil {
		return -1
	}
	return n
}

// Latest returns the highest stable semver tag with prefix and the given major; prereleases excluded.
func Latest(tags []Tag, prefix string, major int) (Tag, bool) {
	var best Tag
	bestV := ""
	for _, t := range tags {
		v, ok := stable(t.Name, prefix)
		if !ok || majorOf(v) != major {
			continue
		}
		if bestV == "" || semver.Compare(v, bestV) > 0 {
			best, bestV = t, v
		}
	}
	return best, bestV != ""
}

// Current returns the release tag (stable or prerelease) whose commit is the submodule gitlink. When several tags
// point at the commit, the highest wins.
func Current(tags []Tag, prefix, gitlink string) (Tag, bool) {
	var best Tag
	bestV := ""
	for _, t := range tags {
		if t.SHA != gitlink {
			continue
		}
		v, ok := version(t.Name, prefix)
		if !ok {
			continue
		}
		if bestV == "" || semver.Compare(v, bestV) > 0 {
			best, bestV = t, v
		}
	}
	return best, bestV != ""
}

// Between returns the stable tags in (from, to], ascending.
func Between(tags []Tag, prefix string, from, to Tag) []Tag {
	fromV, okFrom := version(from.Name, prefix)
	toV, okTo := version(to.Name, prefix)
	if !okFrom || !okTo {
		return nil
	}
	type tv struct {
		t Tag
		v string
	}
	var in []tv
	for _, t := range tags {
		v, ok := stable(t.Name, prefix)
		if ok && semver.Compare(v, fromV) > 0 && semver.Compare(v, toV) <= 0 {
			in = append(in, tv{t, v})
		}
	}
	sort.Slice(in, func(i, j int) bool { return semver.Compare(in[i].v, in[j].v) < 0 })
	out := make([]Tag, len(in))
	for i, x := range in {
		out[i] = x.t
	}
	return out
}

// newestMajorAbove returns the highest stable tag whose major is above major.
func newestMajorAbove(tags []Tag, prefix string, major int) (Tag, bool) {
	var best Tag
	bestV := ""
	for _, t := range tags {
		v, ok := stable(t.Name, prefix)
		if !ok || majorOf(v) <= major {
			continue
		}
		if bestV == "" || semver.Compare(v, bestV) > 0 {
			best, bestV = t, v
		}
	}
	return best, bestV != ""
}

// Patch is one row of docs/patches.md: an engine on a fork, the upstream pull request that carries the patch, and
// the upstream release tag the fork branch starts from.
type Patch struct{ Engine, PR, BaseTag string }

var prURL = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[0-9]+$`)

func isTableLine(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "|") }

func cells(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

var separatorCell = regexp.MustCompile(`^:?-+:?$`)

func isSeparator(line string) bool {
	if !isTableLine(line) {
		return false
	}
	for _, c := range cells(line) {
		if !separatorCell.MatchString(c) {
			return false
		}
	}
	return true
}

// patchTable locates the first Markdown table: the header line index, the column index of each required column,
// and the line indexes of its rows. found is false when md has no table.
type patchTable struct {
	header int
	col    map[string]int
	rows   []int
}

func findTable(lines []string) (patchTable, bool, error) {
	for i := 0; i+1 < len(lines); i++ {
		if !isTableLine(lines[i]) || !isSeparator(lines[i+1]) {
			continue
		}
		tbl := patchTable{header: i, col: map[string]int{}}
		for j, name := range cells(lines[i]) {
			tbl.col[strings.ToLower(name)] = j
		}
		for _, req := range []string{"engine", "upstream pr", "base tag"} {
			if _, ok := tbl.col[req]; !ok {
				return patchTable{}, false, fmt.Errorf("docs/patches.md: the table has no %q column", req)
			}
		}
		for j := i + 2; j < len(lines) && isTableLine(lines[j]); j++ {
			tbl.rows = append(tbl.rows, j)
		}
		return tbl, true, nil
	}
	return patchTable{}, false, nil
}

func knownEngine(name string) bool {
	for _, e := range engines {
		if e.Name == name {
			return true
		}
	}
	return false
}

// ParsePatches reads the patch table of docs/patches.md. Columns are found by header name, case-insensitive, in
// any order. No table means no patches.
func ParsePatches(md string) ([]Patch, error) {
	lines := strings.Split(md, "\n")
	tbl, found, err := findTable(lines)
	if err != nil || !found {
		return []Patch{}, err
	}
	patches := []Patch{}
	for _, i := range tbl.rows {
		c := cells(lines[i])
		get := func(col string) string {
			if j := tbl.col[col]; j < len(c) {
				return c[j]
			}
			return ""
		}
		p := Patch{Engine: get("engine"), PR: get("upstream pr"), BaseTag: get("base tag")}
		switch {
		case !knownEngine(p.Engine):
			return nil, fmt.Errorf("docs/patches.md line %d: unknown engine %q", i+1, p.Engine)
		case !prURL.MatchString(p.PR):
			return nil, fmt.Errorf("docs/patches.md line %d: upstream PR %q is not a pull request URL", i+1, p.PR)
		case p.BaseTag == "":
			return nil, fmt.Errorf("docs/patches.md line %d: empty base tag", i+1)
		}
		patches = append(patches, p)
	}
	return patches, nil
}

// WithoutPatch returns md with that engine's rows removed; everything else is kept byte for byte.
func WithoutPatch(md, engine string) (string, error) {
	lines := strings.Split(md, "\n")
	tbl, found, err := findTable(lines)
	if err != nil {
		return "", err
	}
	drop := map[int]bool{}
	if found {
		j := tbl.col["engine"]
		for _, i := range tbl.rows {
			if c := cells(lines[i]); j < len(c) && c[j] == engine {
				drop[i] = true
			}
		}
	}
	if len(drop) == 0 {
		return "", fmt.Errorf("docs/patches.md has no row for %s", engine)
	}
	kept := make([]string, 0, len(lines)-len(drop))
	for i, l := range lines {
		if !drop[i] {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n"), nil
}

// Kind is what the bump job does for one engine.
type Kind string

const (
	KindNone   Kind = "none"   // the submodule is at the latest release of its major
	KindBump   Kind = "bump"   // move the gitlink to a newer release of the same major
	KindUnfork Kind = "unfork" // the patch shipped upstream: point the submodule back at upstream
	KindHold   Kind = "hold"   // on a fork, the patch has not shipped yet
	KindMajor  Kind = "major"  // only a new major is newer: a maintainer bumps by hand
)

// Action is the decision for one engine.
type Action struct {
	Kind     Kind
	Engine   Engine
	From, To Tag
	Note     string // one line for the job summary
}

func normURL(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	for {
		trimmed := strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
		if trimmed == u {
			return u
		}
		u = trimmed
	}
}

// Decide chooses what to do for one engine. gitmodulesURL is the submodule URL in .gitmodules, gitlink the commit
// the superproject pins, tags the upstream tags, patch the engine's docs/patches.md row (nil when none).
// merged reports whether the patch's PR is merged and its merge commit is an ancestor of tag.
func Decide(ctx context.Context, e Engine, gitmodulesURL, gitlink string, tags []Tag, patch *Patch,
	merged func(ctx context.Context, pr string, tag Tag) (bool, error),
) (Action, error) {
	a := Action{Engine: e}
	if normURL(gitmodulesURL) != normURL(e.Upstream) {
		return decideFork(ctx, a, gitlink, tags, patch, merged)
	}

	cur, ok := Current(tags, e.TagPrefix, gitlink)
	if !ok {
		return a, fmt.Errorf("%s is at %s, which no upstream release tag names: pin %s to a release tag by hand",
			e.Path, gitlink, e.Path)
	}
	curV, _ := version(cur.Name, e.TagPrefix)
	major := majorOf(curV)
	a.From, a.To = cur, cur

	majorNote := ""
	if next, ok := newestMajorAbove(tags, e.TagPrefix, major); ok {
		majorNote = fmt.Sprintf("%s: new major %s upstream needs a hand-made pull request", e.Name, next.Name)
	}

	latest, ok := Latest(tags, e.TagPrefix, major)
	if ok {
		latestV, _ := version(latest.Name, e.TagPrefix)
		if semver.Compare(latestV, curV) > 0 {
			a.Kind, a.To = KindBump, latest
			a.Note = fmt.Sprintf("%s: bump %s to %s", e.Name, cur.Name, latest.Name)
			if majorNote != "" {
				a.Note += "; " + strings.TrimPrefix(majorNote, e.Name+": ")
			}
			return a, nil
		}
	}
	if majorNote != "" {
		a.Kind, a.Note = KindMajor, majorNote+fmt.Sprintf(" (at %s, the latest of its major)", cur.Name)
		return a, nil
	}
	a.Kind, a.Note = KindNone, fmt.Sprintf("%s: at %s, the latest release", e.Name, cur.Name)
	return a, nil
}

func decideFork(ctx context.Context, a Action, gitlink string, tags []Tag, patch *Patch,
	merged func(ctx context.Context, pr string, tag Tag) (bool, error),
) (Action, error) {
	e := a.Engine
	if patch == nil || patch.Engine != e.Name {
		return a, fmt.Errorf("%s points at a fork with no docs/patches.md row", e.Path)
	}
	baseV, ok := version(patch.BaseTag, e.TagPrefix)
	if !ok {
		return a, fmt.Errorf("docs/patches.md: base tag %q of %s is not a %s release tag", patch.BaseTag, e.Name,
			e.TagPrefix+"X.Y.Z")
	}
	a.From = Tag{Name: patch.BaseTag, SHA: gitlink}
	latest, ok := Latest(tags, e.TagPrefix, majorOf(baseV))
	if !ok {
		return a, fmt.Errorf("%s: no upstream release of major %s", e.Name, semver.Major(baseV))
	}
	a.To = latest
	in, err := merged(ctx, patch.PR, latest)
	if err != nil {
		return a, fmt.Errorf("%s: check %s against %s: %w", e.Name, patch.PR, latest.Name, err)
	}
	if !in {
		a.Kind = KindHold
		a.Note = fmt.Sprintf("%s: on a fork until %s is merged and released (latest %s)", e.Name, patch.PR,
			latest.Name)
		return a, nil
	}
	a.Kind = KindUnfork
	a.Note = fmt.Sprintf("%s: %s shipped in %s, returning to upstream", e.Name, patch.PR, latest.Name)
	return a, nil
}

// PRTitle is the pull request title and squash commit message.
func PRTitle(a Action) string {
	switch a.Kind {
	case KindBump:
		return fmt.Sprintf("deps(engines): bump %s to %s", a.Engine.Name, a.To.Name)
	case KindUnfork:
		return fmt.Sprintf("deps(engines): return %s to upstream %s", a.Engine.Name, a.To.Name)
	default:
		return ""
	}
}

func releaseURL(e Engine, t Tag) string {
	return e.Upstream + "/releases/tag/" + url.PathEscape(t.Name)
}

// PRBody is the pull request body: what moves, and a release-notes link per tag in between.
func PRBody(a Action, between []Tag) string {
	var b strings.Builder
	switch a.Kind {
	case KindUnfork:
		fmt.Fprintf(&b, "Points `%s` back at upstream `%s` at `%s` and removes its row from `docs/patches.md`: "+
			"the patch it carried has shipped upstream.\n", a.Engine.Path, a.Engine.Upstream, a.To.Name)
	default:
		fmt.Fprintf(&b, "Moves `%s` from `%s` to `%s`.\n", a.Engine.Path, a.From.Name, a.To.Name)
	}
	if len(between) > 0 {
		b.WriteString("\nUpstream release notes:\n\n")
		for _, t := range between {
			fmt.Fprintf(&b, "- [%s](%s)\n", t.Name, releaseURL(a.Engine, t))
		}
	}
	b.WriteString("\nOpened by the engine bump workflow (`tools/enginebump`). It merges automatically once every " +
		"required check, including the end-to-end suite, is green.\n")
	return b.String()
}
