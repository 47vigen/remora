#!/usr/bin/env bash
# make bench: run the scenario matrix against the built images (REMORA_IMAGE, FAKES_IMAGE) and write the results to
# bench-results/<version>.txt. Each scenario reports latency and throughput from tools/loadgen plus remora's peak CPU
# and memory from docker stats. The fake answers at once, so every figure is gateway cost, not model time.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
dc=(docker compose -p remora-bench -f "$root/tools/bench/compose.yaml")
key=rk-bench-0123456789abcdef
dur=${BENCH_DURATION:-15s}
out="$root/bench-results/$(git -C "$root" describe --tags --always --dirty).txt"
mkdir -p "$root/bench-results"

cleanup() { "${dc[@]}" down -v --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT

"${dc[@]}" up -d --wait --wait-timeout 180 >/dev/null
"${dc[@]}" exec -T remora remora apply -f /bench/remora.yaml --yes >/dev/null
remora="http://$("${dc[@]}" port remora 8080)"
fakes="http://$("${dc[@]}" port fakes 9090)"
cid=$("${dc[@]}" ps -q remora)
until curl -fsS -o /dev/null -H "Authorization: Bearer $key" "$remora/v1/models"; do sleep 1; done

loadgen="$root/bench-results/.loadgen"
go build -o "$loadgen" "$root/tools/loadgen"

reset() { curl -fsS -X POST "$fakes/_control/reset" >/dev/null; }

# stream_script makes the fake answer every chat request with 40 chunks 25 ms apart (about 1 s per stream).
stream_script() {
	local sse="" i
	for i in $(seq 0 39); do
		sse+="\"{\\\"id\\\":\\\"c\\\",\\\"object\\\":\\\"chat.completion.chunk\\\",\\\"created\\\":1,\\\"model\\\":\\\"m1\\\",\\\"choices\\\":[{\\\"index\\\":0,\\\"delta\\\":{\\\"content\\\":\\\"token$i \\\"},\\\"finish_reason\\\":null}]}\","
	done
	sse+="\"{\\\"id\\\":\\\"c\\\",\\\"object\\\":\\\"chat.completion.chunk\\\",\\\"created\\\":1,\\\"model\\\":\\\"m1\\\",\\\"choices\\\":[{\\\"index\\\":0,\\\"delta\\\":{},\\\"finish_reason\\\":\\\"stop\\\"}]}\""
	curl -fsS -X PUT -H 'Content-Type: application/json' "$fakes/_control/upstreams/a/routes/openai.chat" \
		-d "{\"steps\":[{\"sse\":[$sse],\"interval_ms\":25}],\"repeat\":true}" >/dev/null
}

# peak samples remora's CPU% and memory every second until the file $1 exists, then prints the peaks.
peak() {
	local stop=$1 cpu=0 mem=0 c m
	while [ ! -e "$stop" ]; do
		read -r c m < <(docker stats --no-stream --format '{{.CPUPerc}} {{.MemUsage}}' "$cid" |
			awk '{gsub("%","",$1); v=$2; u=v; gsub(/[0-9.]/,"",u); gsub(/[A-Za-z]/,"",v);
				f=(u=="GiB")?1024:(u=="KiB")?1/1024:1; printf "%s %.0f\n", $1, v*f}')
		cpu=$(awk -v a="$cpu" -v b="$c" 'BEGIN{print (b>a)?b:a}')
		mem=$(awk -v a="$mem" -v b="$m" 'BEGIN{print (b>a)?b:a}')
	done
	echo "peak_cpu=${cpu}% peak_rss=${mem}MiB"
}

run() {
	local label=$1; shift
	local stop; stop=$(mktemp -u)
	peak "$stop" >"$stop.peak" &
	local line
	line=$("$loadgen" -key "$key" -d "$dur" "$@" || true)
	touch "$stop"; wait
	printf '%-28s %s %s\n' "$label" "$line" "$(cat "$stop.peak")" | tee -a "$out"
	rm -f "$stop" "$stop.peak"
}

{
	echo "# remora bench $(date -u +%FT%TZ) $(git -C "$root" describe --tags --always --dirty)"
	echo "# image ${REMORA_IMAGE:-remora:dev}, remora limited to 4 CPUs and 8 GiB, duration $dur per scenario"
} | tee "$out"

reset
run "models c=10" -url "$remora/v1/models" -get -c 10
for c in 1 10 100; do
	reset
	run "chat c=$c" -url "$remora/v1/chat/completions" -model fast -c "$c"
done
reset
run "chat 256KiB c=50" -url "$remora/v1/chat/completions" -model fast -c 50 -body-kb 256
for c in 100 500; do
	reset; stream_script
	run "stream c=$c" -url "$remora/v1/chat/completions" -model fast -c "$c" -stream
done
echo "wrote $out"
