# Changelog

## 2026-09-12 — Session 1: vertical slice, cache-aside, load testing

### Built
- **Base62 short code generation**
  - Chose auto-increment ID + base62 encoding over hash-truncation — Postgres's `SERIAL` gives uniqueness for free; a truncated hash risks collisions and needs extra handling.
  - `Encode(id int64) string`, unit-tested with table-driven Go tests.
- **Schema**: `links` table — `id SERIAL PRIMARY KEY`, `code VARCHAR(7) UNIQUE`, `long_url TEXT`, `created_at TIMESTAMP DEFAULT NOW()`, `hit_count BIGINT DEFAULT 0`.
- **`POST /short`**: decode JSON → insert row → encode id → set code → respond `{"code": "..."}` (201).
- **`GET /:code`**: look up by code → increment `hit_count` → 302-redirect. 404 on unknown code.
- **`GET /ping`**: health check.
- **Redis cache-aside** in front of the `/:code` lookup: check cache → miss → Postgres → populate cache (TTL: none, relying on the existing `allkeys-lru` eviction policy since code→URL mappings never change).
- Public GitHub repo initialized, with an internal-only plan (real infra details) kept separate from the public-facing one.

### Broke (bugs found and fixed, in order encountered)
- Base62 digits came out in reversed order (didn't reverse after building via mod/divide) — accepted as-is since it's still injective (no collisions) and no decode is needed.
- `log.Fatal(pool)` called unconditionally right after connecting — killed the server immediately on every startup, before it ever reached `ListenAndServe`.
- Variable shadowing: `pool, err := pgxpool.New(...)` inside `main()` created a new *local* `pool` instead of assigning the package-level one — handlers would have used a permanently `nil` pool.
- Wrong HTTP status codes: a DB failure returned `400 Bad Request` (client's fault) instead of `500 Internal Server Error` (server's fault) — same mistake in two different handlers.
- Inverted error-branch logic when distinguishing `pgx.ErrNoRows` — the "not found" and "real error" cases were swapped.
- `net/http` `ServeMux` panic: `/short` and `/ping` were registered without method restrictions, conflicting with the `GET /{code}` wildcard pattern — fixed by scoping every route to its actual HTTP method.
- Redis cache-aside branches initially got swapped (`if`/`else` inverted) — `hit_count` only incremented on cache hits, and `Set` only ran on hits instead of misses.
- **Found via load testing, not inspection**: 25 orphaned rows (`code IS NULL`) appeared under a concurrent `autocannon` run against `/short`. Root cause — `INSERT` and the code-assigning `UPDATE` were two separate, non-atomic statements; when a client disconnected mid-request (autocannon hitting its deadline), the request's context got cancelled between the two calls, leaving a row with a `long_url` but no `code`, forever. Fixed by wrapping both statements in a single Postgres transaction (`pool.Begin` → `tx.Exec`/`tx.QueryRow` → `tx.Commit`, with `defer tx.Rollback` as a safety net).

### Went well
- Chose **302 over 301** for the redirect specifically to keep `hit_count` accurate — a 301 would let browsers/CDNs cache the redirect and skip the server on repeat visits, silently undercounting clicks.
- Caught two separate over-engineering instincts before building them: sizing `VARCHAR`/id type for a billions-of-rows scale this project will never hit, and briefly considering `BIGSERIAL` for `hit_count` (would have auto-generated values instead of starting at 0).
- Measured, not guessed, at every load-bearing decision:
  - No-cache baseline: **558 req/sec avg, 89ms avg latency** (`GET /:code` via Postgres only).
  - With Redis cache hit: **~1,100-1,160 req/sec avg, ~43-45ms avg latency** — roughly 2x, capped there because `hit_count`'s `UPDATE` still hits Postgres on every request regardless of cache status.
  - Transaction safety on `/short`: **551 → 336 req/sec avg (-40%), 90ms → 148ms avg latency (+65%)** — a real, measured cost of atomicity, judged worth paying since `/short` isn't the hot path and the alternative was permanently orphaned data.

### Not yet built
- Per-IP rate limiter on `/short` (token bucket or sliding window, Redis-backed).
- Reducing `hit_count`'s remaining synchronous Postgres write per request (candidates: async goroutine, or batching increments in Redis with a periodic flush).
- A deliberately multi-instance (N app copies + replicated/sharded Postgres + Redis) version, to be built and bottlenecked as its own exercise later.
