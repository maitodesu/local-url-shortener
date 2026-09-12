# local-url-shortener

A URL shortener built from scratch in Go, as system design interview prep. Postgres as source of truth, Redis cache-aside in front of the redirect lookup, base62-encoded auto-increment IDs for short codes.

## Endpoints

- `POST /short` — body `{"url": "https://..."}`, returns `{"code": "..."}` (201).
- `GET /:code` — 302-redirects to the original URL, increments `hit_count`, 404 if the code doesn't exist.
- `GET /ping` — health check.

## Stack

- Go (stdlib `net/http`, no framework)
- Postgres 18 (`pgx/v5` / `pgxpool`)
- Redis 7 (`go-redis/v9`), cache-aside pattern

## Running it

Requires a reachable Postgres and Redis instance. Set these env vars:

```
POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_HOSTNAME, POSTGRES_PORT, POSTGRES_DB
REDIS_HOST, REDIS_PORT, REDIS_PASSWORD
```

Apply `db/schema.sql` to your Postgres database, then:

```
go run .
```

Server listens on `:8080`.

## Design notes & benchmarks

See [`docs/PLAN.md`](docs/PLAN.md) for the design decisions made (base62 vs. hashing, schema choices, 302 vs. 301, cache-aside vs. write-through) and before/after Redis benchmark numbers.

## Status

Core vertical slice complete. Not yet built: per-IP rate limiting on `/short`.
