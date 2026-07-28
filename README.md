# Open BIN API

A fast, keyless, open-source BIN/IIN lookup service backed entirely by community-maintained GitHub datasets.

- **No API keys and no per-user rate limiter.** Lookup endpoints do not contain a request counter, quota, or account system.
- **Near-real-time data.** A conditional commit poll detects upstream changes every five minutes by default. Signed GitHub webhooks can trigger an immediate sync.
- **Atomic updates.** A changed CSV is downloaded, parsed into a PostgreSQL staging table, checked, and swapped inside one transaction. Readers see either the complete old version or the complete new version.
- **Fast lookups.** PostgreSQL GiST range indexes support both six- and eight-digit IINs. A bounded in-process cache and HTTP cache headers absorb repeat traffic.
- **Horizontally safe.** PostgreSQL advisory locks prevent concurrent imports, and `LISTEN`/`NOTIFY` invalidates caches on every replica after a commit.
- **Source-transparent.** Every response identifies the repository and exact commit from which it came.
- **Privacy-conscious.** The API accepts only 6–8 digits. It rejects full card numbers and does not log lookup paths.

> Community BIN data is not an official ISO/IIN register and can be incomplete or wrong. Do not use it as the only control for authorizing payments, identifying fraud, or making eligibility decisions.

## Run it

Docker Compose starts PostgreSQL, applies the schema, downloads the default community CSV, and serves the API:

```bash
cp .env.example .env                    # optional: add a GITHUB_TOKEN
docker compose up --build
```

The initial 27 MB import can take a minute. `/healthz` reports process liveness immediately; `/readyz` returns `503` until at least one source has imported successfully.

```bash
curl http://localhost:8080/v1/bin/45717360
curl http://localhost:8080/45717360       # binlist.net-compatible route
curl -X POST http://localhost:8080/v1/bins:lookup \
  -H 'content-type: application/json' \
  -d '{"bins":["45717360","542523"]}'
curl http://localhost:8080/v1/stats
```

Interactive tooling can consume the OpenAPI 3.1 document at [`/openapi.yaml`](http://localhost:8080/openapi.yaml).

### Example response

```json
{
  "iin": "45717360",
  "match": { "start": "457173", "end": "457173", "length": 6 },
  "number": { "length": 16, "luhn": true },
  "scheme": "visa",
  "brand": "",
  "type": "debit",
  "level": "classic",
  "prepaid": false,
  "country": {
    "alpha2": "DK",
    "alpha3": "DNK",
    "name": "Denmark",
    "emoji": "🇩🇰",
    "currency": "",
    "latitude": 56,
    "longitude": 10
  },
  "bank": {
    "name": "Jyske Bank",
    "url": "https://www.jyskebank.dk",
    "phone": "+4589893300",
    "city": "",
    "logo": ""
  },
  "source": {
    "id": "bin-list-data",
    "repository": "venelinkochev/bin-list-data",
    "commit": "023a4f68ab1b5d7edd45f03acb132f88a49db96a",
    "synced_at": "2026-07-28T12:00:00Z"
  }
}
```

Unknown properties are represented by empty strings or `null`; they are never guessed.

## How synchronization works

```text
GitHub commit poll ─┐
                    ├─> deduplicated queue ─> download exact commit ─> CSV parser
signed push webhook ┘                                              │
                                                                  ▼
API replicas <─ PostgreSQL NOTIFY <─ atomic transaction <─ quality gates + staging
     │
     └─ bounded local cache ─> GiST range lookup ─> PostgreSQL
```

1. The poller calls GitHub's commits API for each configured repository and file. It downloads data only when the file's latest commit SHA differs from the imported SHA.
2. The file is pinned to that immutable commit, downloaded to a temporary file with a strict size limit, and streamed into a temporary PostgreSQL table with `COPY`.
3. An import must meet `min_records` and `MAX_INVALID_RATIO`. A truncated, malformed, or unexpectedly empty upstream file cannot replace good data.
4. A single transaction replaces that source's records and advances its commit. Existing requests continue to see the old snapshot until commit.
5. PostgreSQL sends `bin_data_changed` after commit. Every API replica purges its local cache immediately.
6. When sources overlap, the narrowest matching range wins, followed by source priority.

Sync errors leave the last good dataset online. Inspect freshness, record counts, skipped rows, and errors through `GET /v1/stats`.

### Polling versus webhooks

Polling works with any public repository and is the default. Set `GITHUB_TOKEN` on the **server** to obtain GitHub's normal authenticated API allowance; this token is never exposed to lookup clients.

A webhook is instantaneous but GitHub only lets repository administrators install one. If you own a configured source repository, create a push webhook targeting:

```text
https://your-api.example/webhooks/github
```

Set the same random value as its webhook secret and `GITHUB_WEBHOOK_SECRET`. The handler requires GitHub's HMAC-SHA256 signature, accepts only bounded request bodies, filters configured branch/path changes, and still resolves the latest commit itself. For third-party repositories, the five-minute poll is the reliable near-real-time path. `SYNC_INTERVAL` can be lowered to `30s` when an authenticated token and the number of sources make that practical.

## Configure data sources

The built-in default is [`venelinkochev/bin-list-data`](https://github.com/venelinkochev/bin-list-data), a CC BY 4.0 community dataset. The repository named `milesj/bin-list` is not hard-coded and appears not to exist today; any current GitHub source can be configured without changing code.

Set `SOURCES_FILE` to a JSON file (see [`config/sources.example.json`](config/sources.example.json)), or put the JSON array directly in `SOURCES_JSON`:

```json
[
  {
    "id": "primary",
    "repository": "community-owner/bin-data",
    "branch": "main",
    "path": "data/bins.csv",
    "format": "binlist",
    "priority": 100,
    "min_records": 100000
  }
]
```

Only GitHub `owner/repository` coordinates are accepted, rather than arbitrary download URLs. This avoids turning configuration mistakes into server-side request forgery.

Supported parser formats:

| Format | Required field | Recognized shape |
|---|---|---|
| `binlist` | `BIN` or `IIN` | `Brand, Type, Category, Issuer, IssuerPhone, IssuerUrl, isoCode2, ...` |
| `ranges` | `iin_start` | Optional `iin_end`, plus `scheme, brand, type, prepaid, country, bank_*` |
| `generic` | `bin`, `iin`, `prefix`, or `range_start` | Header aliases used by both formats |

Prefixes and ranges from 1–8 digits can be imported, while public queries require 6–8 digits. Six-digit ranges are normalized internally so an eight-digit query gets the most specific covering record. Add aliases in `internal/importer/csv.go` when a source uses a genuinely different schema.

If a source is removed from configuration it is disabled, not immediately deleted, so adding it back does not require another import when the commit is unchanged.

## Configuration

| Variable | Default | Purpose |
|---|---:|---|
| `DATABASE_URL` | required | PostgreSQL connection URL |
| `HTTP_ADDR` | `:8080` | Listen address |
| `SOURCES_FILE` / `SOURCES_JSON` | built-in source | Source configuration; set only one |
| `GITHUB_TOKEN` | empty | Optional server-side GitHub token |
| `SYNC_ENABLED` | `true` | Run the background poll/import worker |
| `SYNC_ON_START` | `true` | Check every source at startup |
| `SYNC_INTERVAL` | `5m` | Time between source checks; minimum `30s` |
| `GITHUB_WEBHOOK_SECRET` | empty | Enables signed push webhooks |
| `SYNC_ADMIN_TOKEN` | empty | Enables authenticated `POST /internal/sync` |
| `MAX_DOWNLOAD_BYTES` | `104857600` | Maximum source file size |
| `MAX_INVALID_RATIO` | `0.05` | Maximum fraction of skipped CSV rows |
| `CACHE_TTL` | `5m` | Local positive/negative cache lifetime |
| `CACHE_MAX_ENTRIES` | `100000` | Hard local cache cardinality bound |
| `DATABASE_MIN_CONNS` | `2` | Per-process PostgreSQL pool minimum |
| `DATABASE_MAX_CONNS` | `20` | Per-process PostgreSQL pool maximum |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown deadline |

The admin sync endpoint accepts `{"source":"source-id"}` or `{}` for all sources and requires `Authorization: Bearer $SYNC_ADMIN_TOKEN`. Leave the token empty to keep that endpoint unavailable.

## Scaling without quotas

The application intentionally has no user identity or per-user rate limiter. Its safety bounds are request-size and resource-cardinality bounds, not time-based quotas:

- individual lookups accept only 6–8 digits;
- batch bodies contain at most 1,000 identifiers and execute with bounded concurrency;
- source downloads and webhook bodies are bounded;
- cache cardinality and database pools are bounded.

For a public deployment:

1. Put a CDN in front of `GET /v1/bin/*`. Successful lookups send `Cache-Control: public, max-age=300, stale-while-revalidate=86400` and an `ETag`; misses are cacheable for 60 seconds.
2. Run multiple stateless API replicas against managed PostgreSQL. Reserve one connection per replica for cache invalidation when sizing `DATABASE_MAX_CONNS`.
3. Enable sync on one replica and set `SYNC_ENABLED=false` on the others to minimize GitHub calls. It is safe to enable it everywhere—advisory locks prevent double commits—but replicas may redundantly download a changed file.
4. Keep PostgreSQL private, use TLS, take backups, monitor `/metrics`, and alert on stale `/v1/stats` timestamps.
5. If an upstream or infrastructure provider imposes its own abuse controls, document those separately; the application itself does not issue `429` responses.

Prometheus-format process metrics are exposed at `/metrics`. Production ingress should restrict `/internal/*` and `/webhooks/*` as appropriate, while leaving lookup routes public.

## Development

Requires Go 1.24 and PostgreSQL 14 or newer.

```bash
go test -race ./...
go vet ./...
go build ./cmd/binapi
```

Migrations are embedded and protected by a PostgreSQL advisory lock, so replicas can start concurrently. Schema changes belong in a new ordered file under `internal/database/migrations`.

## Data licensing and attribution

The service code is MIT licensed. Imported records retain their upstream data license; the default source is CC BY 4.0. Every lookup includes source repository and commit attribution, and `/v1/stats` lists active repositories. Before adding a source, confirm that its license permits redistribution and comply with attribution/share-alike requirements. No community dataset is vendored into this repository.
