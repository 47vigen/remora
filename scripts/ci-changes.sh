#!/usr/bin/env bash
# Decides which CI jobs a change needs. Reads the changed file names on stdin, one per line, and prints one
# `name=true|false` line per job group, ready to append to $GITHUB_OUTPUT. `--all` ignores stdin and turns
# everything on (pushes to main, the nightly run, a manual run).
#
#   go         lint, unit tests with the race detector, OpenAPI drift
#   web        the console: client drift, lint, build, typecheck, unit tests
#   engine     the engine build, its contract tests, the engine pin check
#   docs       the tests that read README.md and docs/ (go test ./test/docs)
#   stack      the images, the e2e suite, the console smoke tests, the README quick start
#   workflows  actionlint
#
# BASE and HEAD (default HEAD^1 and HEAD) are only used to compare the README quick start block.
set -euo pipefail

if [[ "${1:-}" == "--all" ]]; then
	go=true web=true engine=true docs=true stack=true workflows=true
else
	files="$(cat)"
	base="${BASE:-HEAD^1}"
	head="${HEAD:-HEAD}"
	go=false web=false engine=false docs=false stack=false workflows=false

	has() { grep -Eq "$1" <<<"$files"; }
	# The quick start block of README.md at a revision; empty when the revision has none.
	quickstart() { git show "$1:README.md" 2>/dev/null | sed -n '/<!-- quickstart:begin -->/,/<!-- quickstart:end -->/p'; }

	has '\.go$|^go\.(mod|sum)$|^api/openapi\.yaml$|^\.golangci\.yml$' && go=true
	has '^web/|^api/openapi\.yaml$' && web=true
	has '^engines/|^internal/(engine|quota)/|^go\.(mod|sum)$|^docs/patches\.md$|^scripts/check-engine-pins\.sh$' && engine=true
	has '^(README\.md|docs/)' && docs=true
	has '^\.github/' && workflows=true

	# The images hold the shipped code only: a change to a _test.go file under cmd/ or internal/ cannot alter them.
	if has '^(Dockerfile|\.dockerignore|Makefile|api/|deploy/|engines/|web/|test/fakes/|test/e2e/|scripts/quickstart-smoke\.sh|go\.(mod|sum))' ||
		grep -E '^(cmd|internal)/' <<<"$files" | grep -Evq '_test\.go$'; then
		stack=true
	elif has '^README\.md$' && [[ "$(quickstart "$base")" != "$(quickstart "$head")" ]]; then
		stack=true
	fi

	# A change to the pipeline itself is checked by the whole pipeline.
	if has '^(\.github/workflows/ci\.yml|scripts/ci-changes\.sh)$'; then
		go=true web=true engine=true docs=true stack=true workflows=true
	fi
fi

printf '%s=%s\n' go "$go" web "$web" engine "$engine" docs "$docs" stack "$stack" workflows "$workflows"
