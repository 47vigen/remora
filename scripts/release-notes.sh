#!/usr/bin/env bash
# Prints the Markdown that release.yml appends to a GitHub release: the component versions from the image's
# versions.json, the image digest, and how to verify its signature. The first line is a marker so a re-run can cut
# the previous block and append a fresh one.
#
# Usage: scripts/release-notes.sh <versions.json> <image-ref> <digest>
set -euo pipefail

if [[ $# -ne 3 ]]; then
	echo "usage: $0 <versions.json> <image-ref> <digest>" >&2
	exit 2
fi
versions="$1" image="$2" digest="$3"

echo '<!-- remora-release-assets -->'
echo
echo '## Components'
echo
echo '| component | version |'
echo '|---|---|'
# A value is a version string, or an object with a "version" field.
jq -r 'to_entries[] | "| \(.key) | \(if (.value | type) == "object" then .value.version else .value end) |"' "$versions"
echo
echo '## Image'
echo
echo '```sh'
echo "docker pull ${image}@${digest}"
echo '```'
echo
echo 'Multi-arch (linux/amd64, linux/arm64). The SPDX SBOM of each platform is attached to this release.'
echo
echo '## Verify'
echo
echo '```sh'
echo "cosign verify ${image}@${digest} \\"
echo "  --certificate-identity-regexp '^https://github\\.com/47vigen/remora/\\.github/workflows/release\\.yml@refs/' \\"
echo '  --certificate-oidc-issuer https://token.actions.githubusercontent.com'
echo '```'
