#!/usr/bin/env bash
# Benchmarks the raw throughput of token-script.lua directly against Redis,
# bypassing the Go app entirely (isolates Redis's own capacity).
#
# Run this ON THE DOCKER HOST (wherever the "redis" container lives), from
# the repo root, with ~/infra/secrets/redis.env reachable at the given path.
#
# Usage: ./scripts/benchmark-ratelimiter.sh [requests] [concurrency]

REQUESTS="${1:-50000}"
CONCURRENCY="${2:-100}"
SECRETS_FILE="${REDIS_SECRETS_FILE:-$HOME/infra/secrets/redis.env}"

set -a
# shellcheck disable=SC1090
source "$SECRETS_FILE"
set +a

sudo docker cp token-script.lua redis:/tmp/token-script.lua

sudo docker exec redis sh -c "
  redis-benchmark -a \"\$REDIS_PASSWORD\" -n $REQUESTS -c $CONCURRENCY -r 100000 \
    eval \"\$(cat /tmp/token-script.lua)\" 1 ratelimit:__rand_int__ 100 1 1700000000
"
