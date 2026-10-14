// Command enginebump decides whether an engine submodule follows a newer upstream release and, unless -dry-run is
// set, stages the change. It prints `kind=<k>` and `title=<t>` lines for $GITHUB_OUTPUT on stdout and one note line
// for the job summary on stderr.
//
//	go run ./tools/enginebump -engine bifrost -body pr-body.md
//	go run ./tools/enginebump -engine cliproxyapi -dry-run
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

const patchesFile = "docs/patches.md"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		slog.Error("enginebump failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("enginebump", flag.ContinueOnError)
	name := fl.String("engine", "", "engine to check: bifrost or cliproxyapi")
	body := fl.String("body", "", "write the pull request body to this file")
	dryRun := fl.Bool("dry-run", false, "decide and print, change nothing")
	verbose := fl.Bool("v", false, "log diagnostics to stderr")
	if err := fl.Parse(args); err != nil {
		return err
	}
	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	e, ok := engineByName(*name)
	if !ok {
		return fmt.Errorf("-engine must be one of bifrost, cliproxyapi; got %q", *name)
	}

	gitmodulesURL, err := command(ctx, "git", "config", "-f", ".gitmodules", "--get", "submodule."+e.Path+".url")
	if err != nil {
		return err
	}
	gitlink, err := readGitlink(ctx, e.Path)
	if err != nil {
		return err
	}
	lsRemote, err := command(ctx, "git", "ls-remote", "--tags", e.Upstream)
	if err != nil {
		return err
	}
	tags := ParseLsRemote(lsRemote)
	patchesMD, patch, err := readPatch(e.Name)
	if err != nil {
		return err
	}
	slog.Debug("inputs", "engine", e.Name, "url", gitmodulesURL, "gitlink", gitlink, "tags", len(tags),
		"patch", patch != nil)

	a, err := Decide(ctx, e, gitmodulesURL, gitlink, tags, patch, merged)
	if err != nil {
		return err
	}
	fmt.Printf("kind=%s\ntitle=%s\n", a.Kind, PRTitle(a))
	fmt.Fprintln(os.Stderr, a.Note)

	if a.Kind != KindBump && a.Kind != KindUnfork {
		return nil
	}
	between := Between(tags, e.TagPrefix, a.From, a.To)
	if *body != "" {
		if err := os.WriteFile(*body, []byte(PRBody(a, between)), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", *body, err)
		}
	}
	if *dryRun {
		slog.Debug("dry run: nothing staged")
		return nil
	}
	return apply(ctx, a, patchesMD)
}

// apply stages the decision: the gitlink always, and for unfork the upstream URL and the patch table row.
func apply(ctx context.Context, a Action, patchesMD string) error {
	e := a.Engine
	if _, err := command(ctx, "git", "update-index", "--cacheinfo", "160000,"+a.To.SHA+","+e.Path); err != nil {
		return err
	}
	if a.Kind != KindUnfork {
		return nil
	}
	if _, err := command(ctx, "git", "config", "-f", ".gitmodules", "submodule."+e.Path+".url",
		e.Upstream); err != nil {
		return err
	}
	md, err := WithoutPatch(patchesMD, e.Name)
	if err != nil {
		return err
	}
	if err := os.WriteFile(patchesFile, []byte(md), 0o644); err != nil { //nolint:gosec // a tracked doc, world-readable like the rest of the checkout
		return fmt.Errorf("write %s: %w", patchesFile, err)
	}
	return nil
}

func engineByName(name string) (Engine, bool) {
	for _, e := range engines {
		if e.Name == name {
			return e, true
		}
	}
	return Engine{}, false
}

// readGitlink reads the commit the superproject pins at path, without cloning the submodule.
func readGitlink(ctx context.Context, path string) (string, error) {
	out, err := command(ctx, "git", "ls-tree", "HEAD", path)
	if err != nil {
		return "", err
	}
	// "160000 commit <sha>\t<path>"
	meta, _, _ := strings.Cut(out, "\t")
	f := strings.Fields(meta)
	if len(f) != 3 || f[1] != "commit" {
		return "", fmt.Errorf("git ls-tree HEAD %s: %q is not a submodule gitlink", path, out)
	}
	return f[2], nil
}

// readPatch returns docs/patches.md (empty when absent) and the engine's row, if any.
func readPatch(engine string) (string, *Patch, error) {
	b, err := os.ReadFile(patchesFile)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", patchesFile, err)
	}
	patches, err := ParsePatches(string(b))
	if err != nil {
		return "", nil, err
	}
	for i := range patches {
		if patches[i].Engine == engine {
			return string(b), &patches[i], nil
		}
	}
	return string(b), nil, nil
}

// merged reports whether the pull request is merged and its merge commit is contained in tag.
func merged(ctx context.Context, pr string, tag Tag) (bool, error) {
	u, err := url.Parse(pr)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", pr, err)
	}
	// /<owner>/<repo>/pull/<n>
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return false, fmt.Errorf("%s is not a pull request URL", pr)
	}
	repo := "repos/" + parts[0] + "/" + parts[1]
	out, err := command(ctx, "gh", "api", repo+"/pulls/"+parts[3], "--jq", ".merged,.merge_commit_sha")
	if err != nil {
		return false, err
	}
	f := strings.Fields(out)
	if len(f) == 0 || f[0] != "true" {
		return false, nil
	}
	if len(f) != 2 {
		return false, fmt.Errorf("%s is merged but has no merge commit: %q", pr, out)
	}
	// Compare against the tag's peeled commit: same answer as the tag name, without escaping "transports/v...".
	status, err := command(ctx, "gh", "api", repo+"/compare/"+f[1]+"..."+tag.SHA, "--jq", ".status")
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(status) {
	case "ahead", "identical":
		return true, nil
	default:
		return false, nil
	}
}

// command runs a program and returns its trimmed stdout. Errors name the command line and carry stderr.
func command(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed programs (git, gh); arguments are passed without a shell
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	line := strings.Join(append([]string{name}, args...), " ")
	slog.Debug("exec", "cmd", line)
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", line, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
