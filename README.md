# Open BIN API

A small Go + PostgreSQL service for looking up BIN/IIN information and keeping the dataset up to date.

## What it does

- Public BIN lookup: `GET /{bin}`
- Alternative lookup route: `GET /v1/bin/{bin}`
- Lightweight uptime endpoint: `GET /health`
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
curl http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
```

## Deploy with Docker and external PostgreSQL

Only two application settings are required:

```env
DATABASE_URL=postgresql://USER:PASSWORD@HOST:5432/DATABASE?sslmode=require
DATA_ADMIN_PASSWORD=use-a-unique-random-value-of-at-least-16-characters
```

The PostgreSQL user must be able to create tables and indexes in its database and read/write those tables. Use the exact TLS options supplied by your database provider; some private databases use a different `sslmode`.

Build and run:

```bash
docker build -t open-bin-api .

cat > production.env <<'EOF'
DATABASE_URL=postgresql://USER:PASSWORD@HOST:5432/DATABASE?sslmode=require
DATA_ADMIN_PASSWORD=use-a-unique-random-value-of-at-least-16-characters
EOF

docker run -d \
  --name open-bin-api \
  --restart unless-stopped \
  --env-file production.env \
  -p 8080:8080 \
  open-bin-api
```

For a Docker-compatible hosting provider, deploy the repository's `Dockerfile` and set the same two environment variables in the provider's secret settings. The application listens on `HTTP_ADDR` when set, otherwise on the provider's `PORT`, otherwise on port `8080`.

### First boot

On a fresh database the application:

1. Connects to PostgreSQL and applies its embedded schema migrations.
2. Registers the built-in GitHub CSV source.
3. Starts the HTTP server and the startup import check.
4. Downloads and validates the source CSV in temporary storage.
5. Commits all imported records in one PostgreSQL transaction.
6. Changes `/readyz` from `503` to `200`; lookups are then available.

`/healthz` returns `200` once the HTTP process is running. `/readyz` returns `503` until at least one usable dataset has committed. A GitHub failure leaves the process running and readiness false on a fresh database; the error is visible in logs and on `/data`. When the database already contains usable records, a later GitHub failure leaves those records online and readiness stays true.

PostgreSQL connection or migration failures stop startup with a non-zero exit and a clear log message. This is intentional: the API never pretends to be healthy without its database.

## Deploy on Render

The repository is ready for a Render **Web Service** using the Docker runtime. Render builds the Go binary inside Docker; no Go installation or build command is needed on Render.

1. Push or select this GitHub repository and branch.
2. In Render, choose **New → Web Service**.
3. Connect the repository.
4. Select **Docker** as the runtime. Render uses the root `Dockerfile` automatically.
5. Add these environment variables:

   ```text
   DATABASE_URL=<your hosted PostgreSQL connection URL>
   DATA_ADMIN_PASSWORD=<a unique random value of at least 16 characters>
   ```

6. Set the health check path to `/health` under the service's advanced settings.
7. Deploy. Do not set `PORT`; Render provides it and the application binds to `0.0.0.0:$PORT` automatically.
8. Open `https://YOUR-SERVICE.onrender.com/health` and expect `OK` with HTTP `200`.
9. Wait for `https://YOUR-SERVICE.onrender.com/readyz` to return HTTP `200` after the first import.
10. Test `https://YOUR-SERVICE.onrender.com/45717360`.
11. Open `https://YOUR-SERVICE.onrender.com/data` and sign in with username `admin` and `DATA_ADMIN_PASSWORD`.

A minimal [`render.yaml`](render.yaml) is included for users who prefer **New → Blueprint**. It creates one Docker web service, sets `/health` as Render's health check, and prompts for the two required secrets. It does not provision a database; set `DATABASE_URL` to your external or Render PostgreSQL connection string.

Render terminates public HTTPS before forwarding requests to the container. `/data` uses same-origin form actions and standard Basic Auth, so it does not need a Render-specific hostname or proxy configuration.

### UptimeRobot

Create an **HTTP(S)** monitor with this URL:

```text
https://YOUR-SERVICE.onrender.com/health
```

Expect HTTP `200`. This endpoint returns only `OK`; it does not query PostgreSQL, contact GitHub, check dataset readiness, trigger imports, or require authentication. Continue using `/readyz` separately when you need to know whether BIN data is available.

Health endpoint meanings:

- `/health`: minimal process-liveness response for Render and UptimeRobot.
- `/healthz`: application process health with uptime information; no database query.
- `/readyz`: queries PostgreSQL and returns `200` only when at least one usable dataset exists.

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
- Password: the server-side `DATA_ADMIN_PASSWORD` environment variable (required, at least 16 characters)

The password is never included in page HTML or frontend JavaScript. There is no JavaScript on the page. Always serve the application over HTTPS in production because Basic authentication credentials must be protected in transit.

The page also uses a server-generated CSRF token, disables caching, blocks framing, and applies a restrictive Content Security Policy. Its forms and redirects use same-origin `/data/...` paths and contain no localhost or hardcoded domain. If a hosting proxy filters headers, configure it to forward the standard `Authorization` header used by Basic Auth.

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
| `DATA_ADMIN_PASSWORD` | required | Protects `/data`; minimum 16 characters |
| `HTTP_ADDR` | empty | Explicit HTTP listen address; overrides `PORT` |
| `PORT` | `8080` | Hosting-provider port used when `HTTP_ADDR` is empty |
| `SYNC_ENABLED` | `true` | Enable periodic GitHub checks |
| `SYNC_ON_START` | `true` | Check configured sources at startup |
| `SYNC_INTERVAL` | `5m` | Check interval; minimum `30s` |
| `GITHUB_TOKEN` | empty | Optional server-side token for GitHub API allowance |
| `SOURCES_FILE` | empty | Path to source configuration JSON |
| `SOURCES_JSON` | built-in source | Inline source configuration JSON |
| `MAX_DOWNLOAD_BYTES` | `104857600` | Maximum automatic or manual CSV size; 1 MiB–1 GiB |
| `MAX_INVALID_RATIO` | `0.05` | Maximum fraction of invalid CSV rows |
| `DATABASE_MAX_CONNS` | `10` | PostgreSQL pool size |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown deadline |

Only one of `SOURCES_FILE` and `SOURCES_JSON` may be set. Docker Compose forwards `SOURCES_JSON`; use `SOURCES_FILE` only when that file is available inside the running container. Invalid booleans, durations, numbers, addresses, passwords, or unknown source fields stop startup with a clear error instead of silently using a default.

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

`repository` is a GitHub `owner/repository`. Source IDs cannot use the reserved ID `manual`. Automatic source priorities must be below `1000`; the manual source uses priority `1000` so admin corrections always win when records overlap.

## Development

Requires Go 1.24 and PostgreSQL 14 or newer.

```bash
go mod tidy
go test -race ./...
go vet ./...
go build ./cmd/binapi
```

The code is MIT licensed. Imported data retains its upstream license. No BIN dataset is committed to this repository.
