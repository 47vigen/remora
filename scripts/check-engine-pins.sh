#!/usr/bin/env bash
# Checks that each engine submodule is checked out at the commit of the release tag named in engines/versions.env.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=../engines/versions.env
source "$root/engines/versions.env"

status=0

# check <name> <dir> <url> <tag>
check() {
	local name="$1" dir="$2" url="$3" tag="$4"
	local have want
	have="$(git -C "$root/engines/$dir" rev-parse HEAD)"
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
