# URL Shortener — System Design Interview Prep

A URL shortener built from scratch as system design interview prep — a learning exercise focused on design decisions and tradeoffs, not just working code.

## Environment

- Deployed against a Docker-hosted Postgres 18 + Redis 7 stack, reached over SSH from a separate dev machine (via local port-forwarded tunnels during development).
- Language: Go, chosen specifically for concurrency/goroutine practice.

## Scope (session 1 — a working end-to-end vertical slice)

1. `POST /short` — takes a long URL, returns a short code
2. `GET /:code` — 302-redirects to the original URL
3. Postgres as source of truth: `links` table (`id`, `code`, `long_url`, `created_at`, `hit_count`)
4. Short code generation via base62-encoded auto-increment ID
5. Redis cache-aside in front of the redirect lookup
6. A rate limiter on `/short`, backed by Redis, per-IP — **not yet built**
7. Load-testing with `autocannon`, comparing throughput with/without cache

## Key design decisions

- **Base62(auto-increment ID) over hashing** for short codes: Postgres's `SERIAL` guarantees uniqueness for free; a truncated hash risks collisions and needs extra handling. Tradeoff accepted: shortening the same URL twice yields two different codes (matches real-world shorteners, allows independent per-link tracking/expiry).
- **Schema**: `id SERIAL PRIMARY KEY`, `code VARCHAR(7) UNIQUE` (6 chars is the true ceiling for the full `int32` range of `SERIAL`, `62^6 > 2^31-1 > 62^5`), `long_url TEXT`, `created_at TIMESTAMP DEFAULT NOW()`, `hit_count BIGINT DEFAULT 0` (plain counter, not `BIGSERIAL`).
- **302 over 301** for the redirect: a 301 can be cached by browsers/CDNs, silently undercounting `hit_count` on repeat visits since the server never gets asked again. 302 guarantees every visit reaches the server.
- **Cache-aside over write-through** for Redis: lazily populating the cache only on an actual `/:code` visit means the cache naturally self-selects for popular links, rather than wasting memory caching links nobody ever visits.
- **pgx/v5 (native `pgxpool`) over `database/sql` + `lib/pq`**: actively maintained, and a connection pool matters once concurrent goroutines are hitting the DB.

## Benchmark results (autocannon, 50 concurrent connections)

| | Req/sec (avg) | Latency (avg) | Latency (p99) |
|---|---|---|---|
| No cache (Postgres only) | 558 | 89ms | 201ms |
| Redis cache hit | 1,105 | 45ms | 87ms |

Roughly 2x improvement from caching — capped at 2x rather than more because `hit_count`'s `UPDATE` still hits Postgres on every request regardless of cache status. Removing that remaining bottleneck (e.g. batching hit-count writes) is the natural next optimization.

## Remaining work

- Per-IP rate limiter on `/short` (token bucket or sliding window, Redis-backed) — not yet implemented.
- Optionally: reduce the `hit_count` write path's dependency on a synchronous Postgres write per request.
