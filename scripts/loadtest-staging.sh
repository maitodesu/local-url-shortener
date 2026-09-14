#!/usr/bin/env bash
# Fires N concurrent, distinct-URL shorten requests at a staging backend,
# each tagged with a unique X-Forwarded-For so the per-IP rate limiter
# treats them as N different users (mirrors the "how many users" question
# this test exists to answer) instead of exhausting one shared bucket.
# Requires the target backend to run with TRUST_PROXY_HEADERS=true.
#
# Usage: ./scripts/loadtest-staging.sh [host] [count] [concurrency]

HOST="${1:-http://127.0.0.1:8081}"
COUNT="${2:-50000}"
CONCURRENCY="${3:-50}"
RESULTS="$(mktemp)"

fire_one() {
  local i="$1"
  local ip="10.$(( (i / 65536) % 256 )).$(( (i / 256) % 256 )).$(( i % 256 ))"
  local url="https://example.com/loadtest/${i}-${RANDOM}"
  local t0 t1
  t0=$(date +%s%3N)
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$HOST/short" \
    -H "Content-Type: application/json" \
    -H "X-Forwarded-For: $ip" \
    -d "{\"url\":\"$url\"}")
  t1=$(date +%s%3N)
  echo "$code $((t1 - t0))"
}
export -f fire_one
export HOST

echo "Firing $COUNT requests at $HOST with concurrency $CONCURRENCY..."
start=$(date +%s)
seq 1 "$COUNT" | xargs -P "$CONCURRENCY" -I{} bash -c 'fire_one "$@"' _ {} > "$RESULTS"
end=$(date +%s)

echo "Duration: $((end - start))s"
echo "Total requests: $(wc -l < "$RESULTS")"
echo "Status code breakdown:"
awk '{print $1}' "$RESULTS" | sort | uniq -c | sort -rn
echo "Latency (ms) -- min/avg/max:"
awk '{sum+=$2; if(min==""||$2<min) min=$2; if($2>max) max=$2} END {printf "%d / %.1f / %d\n", min, sum/NR, max}' "$RESULTS"

rm -f "$RESULTS"
