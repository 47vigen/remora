#!/usr/bin/env bash
# Runs the README quick start verbatim against the fakes, so an edited quick start that no longer works fails CI.
#
# The ```sh blocks between <!-- quickstart:begin --> and <!-- quickstart:end --> in README.md run in order, as one
# script, after a fixed set of literal substitutions that point them at a local image and the fake upstream. A
# literal missing from the README fails the run, so README drift can never reach a real provider.
#
# Env:
#   REMORA_IMAGE      (required) the remora image to test, e.g. remora:dev or ghcr.io/47vigen/remora@sha256:...
#   FAKES_IMAGE       (required) the fakes image, e.g. remora-fakes:dev
#   QS_UPSTREAM_HOST  (optional) the host address the remora container reaches the fakes on; default: the gateway of
#                     Docker's bridge network, else 172.17.0.1 (use host.docker.internal on Docker Desktop)
#
# Prints "quickstart: OK" and exits 0, or exits non-zero. It uses the README's own names: container "remora", volume
# "remora-data" and host port 8080, plus container "qs-fakes" on host port 9090; all are removed on exit.
set -euo pipefail

: "${REMORA_IMAGE:?set REMORA_IMAGE}"
: "${FAKES_IMAGE:?set FAKES_IMAGE}"

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"

cleanup() {
	local rc=$?
	if [ "$rc" -ne 0 ]; then
		echo "quickstart: FAILED (exit $rc); remora logs follow" >&2
		docker logs --tail 200 remora >&2 2>&1 || true
	fi
	docker rm -f remora qs-fakes >/dev/null 2>&1 || true
	docker volume rm -f remora-data >/dev/null 2>&1 || true
	rm -rf "$work"
	exit "$rc"
}
trap cleanup EXIT

docker run -d --name qs-fakes -p 9090:9090 "$FAKES_IMAGE" >/dev/null
for _ in $(seq 60); do
	curl -fsS http://localhost:9090/healthz >/dev/null 2>&1 && break
	sleep 1
done
curl -fsS http://localhost:9090/healthz >/dev/null

host="${QS_UPSTREAM_HOST:-}"
if [ -z "$host" ]; then
	host="$(docker network inspect bridge -f '{{range .IPAM.Config}}{{.Gateway}}{{end}}' 2>/dev/null || true)"
fi
host="${host:-172.17.0.1}"

# Extract the shell blocks of the quick start.
awk '
	/<!-- quickstart:begin -->/ { inside = 1; next }
	/<!-- quickstart:end -->/   { inside = 0; next }
	inside && /^```sh$/         { block = 1; print "echo __quickstart_block__"; next }
	inside && block && /^```$/  { block = 0; print ""; next }
	inside && block             { print }
' "$repo/README.md" >"$work/readme.sh"

# Literal substitutions, README literal first. Every one must be found.
subs=(
	"ghcr.io/47vigen/remora:latest|$REMORA_IMAGE"
	"https://openrouter.ai/api|http://$host:9090/u/quickstart"
	"sk-or-your-key|fake-key"
	"openai/gpt-4o-mini|m1"
	# The fake listens on a private address, which a provider reaches only when it allows private networks.
	'type: "openrouter",|type: "openrouter", allow_private_network: true,'
)
script="$(cat "$work/readme.sh")"
for s in "${subs[@]}"; do
	from="${s%%|*}" to="${s#*|}"
	if [[ "$script" != *"$from"* ]]; then
		echo "quickstart: README quick start no longer contains the literal: $from" >&2
		exit 1
	fi
	script="${script//"$from"/"$to"}"
done
printf '%s\n' "$script" >"$work/run.sh"

out="$(cd "$work" && bash -euo pipefail run.sh)"
# The output of the last block: everything after the last block marker.
last="${out##*__quickstart_block__}"
last="$(printf '%s' "$last" | tr -d '[:space:]')"
if [ -z "$last" ] || [ "$last" = "null" ]; then
	echo "quickstart: the last block printed nothing" >&2
	exit 1
fi
echo "quickstart: answer: ${out##*__quickstart_block__$'\n'}"
echo "quickstart: OK"
