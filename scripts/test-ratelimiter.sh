#!/usr/bin/env bash
# Fires N rapid requests at POST /short and prints the HTTP status of each.
# With the default bucket capacity (60), expect the first 60 to return 201
# and the rest to return 429 once the token bucket is drained.
#
# Usage: ./scripts/test-ratelimiter.sh [host] [count]

HOST="${1:-http://localhost:8080}"
COUNT="${2:-65}"

for i in $(seq 1 "$COUNT"); do
  code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$HOST/short" \
    -H "Content-Type: application/json" \
    -d '{"url":"https://example.com"}')
  echo -n "$code "
done
echo
