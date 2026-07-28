# Open BIN API

A small Go + PostgreSQL service for looking up BIN/IIN information and keeping the dataset up to date.

## What it does

- Public BIN lookup: `GET /{bin}`
- Alternative lookup route: `GET /v1/bin/{bin}`
- Password-protected data page: `GET /data`
- Manual CSV upload with merge or replace mode
- Periodic import from configured GitHub CSV files
- Safe updates: the current dataset keeps serving until a complete replacement import commits

The API accepts 6 to 8 digits. It intentionally rejects full card numbers.

## Run locally

```bash
cp .env.example .env
# Edit .env and set a long, random DATA_ADMIN_PASSWORD.
docker compose up --build
```

The initial automatic import may take a minute. Check readiness with:

```bash
curl http://localhost:8080/readyz
```

## Lookup API

```bash
curl http://localhost:8080/45717360
# or
curl http://localhost:8080/v1/bin/45717360
```

A successful response includes the matching range, scheme, card type, country, bank, and data source when those fields exist. Unknown fields are empty or `null`; the service does not guess them.

Lookup responses use `Cache-Control: no-store`, so a successful admin import is visible to the next API request.

## Data admin page

Open:

```text
https://your-host/data
```

The page uses HTTP Basic authentication:

- Username: `admin`
- Password: the server-side `DATA_ADMIN_PASSWORD` environment variable

The password is never included in page HTML or frontend JavaScript. There is no JavaScript on the page. Always serve the application over HTTPS in production because Basic authentication credentials must be protected in transit.

The page also uses a server-generated CSRF token, disables caching, blocks framing, and applies a restrictive Content Security Policy.

### Manual CSV import

From `/data`:

1. Choose a CSV file.
2. Choose its format.
3. Choose **Merge** or **Replace**.
4. Click **Import data**.

Formats:

- `binlist`: headers such as `BIN, Brand, Type, Category, Issuer, IssuerPhone, IssuerUrl, isoCode2, isoCode3, CountryName`
- `ranges`: headers such as `iin_start, iin_end, scheme, brand, type, country, bank_name`
- `generic`: accepts the header aliases supported by both formats

**Merge** adds new rows to the manual dataset and updates matching ranges. **Replace** atomically replaces only the manual dataset. Configured automatic sources are unaffected by either mode.

The CSV is parsed into a temporary PostgreSQL table first. Invalid, empty, truncated, or oversized imports fail without deleting the currently working data. When an import succeeds, the transaction commits once and the lookup API immediately reads the new records.

The manual source has higher lookup priority than automatic sources, so it can be used for corrections.

### Check automatic sources now

The **Check for updates** button on `/data` checks all configured GitHub sources immediately. Unchanged files are not downloaded again.

## Automatic importing

When `SYNC_ENABLED=true`, one background loop checks each configured GitHub CSV every `SYNC_INTERVAL` (five minutes by default):

1. Read the latest commit for the configured file.
2. Compare it with the commit stored in PostgreSQL.
3. If unchanged, do nothing.
4. If changed, download that exact version of the CSV.
5. Parse and validate it in a temporary table.
6. Replace that source's records in one transaction.

If download, parsing, or validation fails, the previous imported records continue serving normally. Import status and errors are shown on `/data`.

The built-in source is the CC BY 4.0 dataset at [`venelinkochev/bin-list-data`](https://github.com/venelinkochev/bin-list-data). Configure different sources with `SOURCES_FILE` or `SOURCES_JSON`; see [`config/sources.example.json`](config/sources.example.json).

## Environment variables

| Variable | Default | Purpose |
|---|---:|---|
| `DATABASE_URL` | required | PostgreSQL connection URL |
| `DATA_ADMIN_PASSWORD` | empty | Enables and protects `/data` |
| `HTTP_ADDR` | `:8080` | HTTP listen address |
| `SYNC_ENABLED` | `true` | Enable periodic GitHub checks |
| `SYNC_ON_START` | `true` | Check configured sources at startup |
| `SYNC_INTERVAL` | `5m` | Check interval; minimum `30s` |
| `GITHUB_TOKEN` | empty | Optional server-side token for GitHub API allowance |
| `SOURCES_FILE` | empty | Path to source configuration JSON |
| `SOURCES_JSON` | built-in source | Inline source configuration JSON |
| `MAX_DOWNLOAD_BYTES` | `104857600` | Maximum automatic or manual CSV size |
| `MAX_INVALID_RATIO` | `0.05` | Maximum fraction of invalid CSV rows |
| `DATABASE_MAX_CONNS` | `10` | PostgreSQL pool size |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown deadline |

Only one of `SOURCES_FILE` and `SOURCES_JSON` may be set.

## Source configuration

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

`repository` is a GitHub `owner/repository`. Source IDs cannot use the reserved ID `manual`.

## Development

Requires Go 1.24 and PostgreSQL 14 or newer.

```bash
go mod tidy
go test -race ./...
go vet ./...
go build ./cmd/binapi
```

The code is MIT licensed. Imported data retains its upstream license. No BIN dataset is committed to this repository.
