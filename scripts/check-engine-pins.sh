#!/usr/bin/env bash
# Checks that each engine submodule is checked out at the commit of the release tag named in engines/versions.env.
# An engine with a docs/patches.md row runs on a fork: its submodule must be at the row's fork commit instead, and
# the row's base tag must be the tag in engines/versions.env.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../engines/versions.env
source "$root/engines/versions.env"

status=0

# patch_cell <engine> <column>: the named column of the engine's docs/patches.md row, or nothing.
patch_cell() {
	local engine="$1" column="$2" file="$root/docs/patches.md"
	[[ -f "$file" ]] || return 0
	awk -F'|' -v engine="$engine" -v column="$column" '
		function trim(s) { gsub(/^[ \t`]+|[ \t`]+$/, "", s); return s }
		/^[ \t]*\|/ {
			if (!header) { for (i = 2; i < NF; i++) col[tolower(trim($i))] = i; header = 1; next }
			if (trim($col["engine"]) == engine) { print trim($col[column]); exit }
		}
	' "$file"
}

# check <name> <dir> <url> <tag>
check() {
	local name="$1" dir="$2" url="$3" tag="$4"
	local have want fork base
	have="$(git -C "$root/engines/$dir" rev-parse HEAD)"
	fork="$(patch_cell "$name" "fork commit")"
	if [[ -n "$fork" ]]; then
		base="$(patch_cell "$name" "base tag")"
		if [[ "$base" != "$tag" ]]; then
			echo "mismatch $name: docs/patches.md base tag $base, engines/versions.env $tag"
			status=1
		elif [[ "$have" == "$fork"* ]]; then
			echo "ok $name fork $fork on $tag"
		else
			echo "mismatch $name: submodule $have, docs/patches.md fork commit $fork"
			status=1
		fi
		return
	fi
	want="$(git ls-remote "$url" "refs/tags/${tag}^{}" | awk '{print $1}')"
	if [[ -z "$want" ]]; then
		want="$(git ls-remote "$url" "refs/tags/${tag}" | awk '{print $1}')"
	fi
	if [[ -z "$want" ]]; then
		echo "mismatch $name: submodule $have tag <not found: $tag>"
		status=1
		return
	fi
	if [[ "$have" == "$want" ]]; then
		echo "ok $name $tag"
	else
		echo "mismatch $name: submodule $have tag $want"
		status=1
	fi
}

check bifrost bifrost "$(git -C "$root" config -f .gitmodules submodule.engines/bifrost.url)" "$BIFROST_VERSION"
check cliproxyapi cliproxyapi "$(git -C "$root" config -f .gitmodules submodule.engines/cliproxyapi.url)" "$CLIPROXYAPI_VERSION"

exit "$status"
