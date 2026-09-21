# Guison API

A lightweight, open-source BIN/IIN lookup API with automatic dataset synchronization, Turso (libSQL/SQLite) persistence, a public website, developer docs, and a protected data administration interface.

Send the first 6–8 digits of a payment card and get back the card scheme, funding type, product level, issuing bank and country as JSON. No API key, no SDK, no sign-up.

```bash
curl https://guison-api.onrender.com/45717360
```

> **Guison is a metadata lookup service.** It does **not** verify that a card or account exists, is active, or is valid, and it cannot authorise a payment. See [Data source & credits](#data-source--credits).

> **Storage note (2026).** The persistence layer has been migrated from PostgreSQL (Neon) to Turso/libSQL. Only two environment variables are required for the database — `TURSO_DATABASE_URL` and `TURSO_AUTH_TOKEN` — and a fresh Turso database initializes itself and re-imports the dataset automatically. See [Environment variables](#environment-variables).

---

## Contents

- [Features](#features)
- [Live API](#live-api)
- [Quick start](#quick-start)
- [API endpoints](#api-endpoints)
- [Input rules](#input-rules)
- [Response](#response)
- [Errors](#errors)
- [Local installation](#local-installation)
- [Environment variables](#environment-variables)
- [Docker](#docker)
- [Docker Compose](#docker-compose)
- [Render deployment](#render-deployment)
- [Deploy anywhere](#deploy-anywhere)
- [Administration](#administration)
- [Data import & automatic sync](#data-import--automatic-sync)
- [Project structure](#project-structure)
- [Security](#security)
- [Data source & credits](#data-source--credits)
- [License](#license)
- [Community](#community)
- [Contributing](#contributing)
- [Testing](#testing)

---

## Features

- **6–8 digit BIN/IIN lookup** — query with 6, 7 or 8 digits; the most specific matching range wins
- **Large baseline dataset** — currently around 374K imported BIN records from the configured upstream source
- **Card scheme / network** — Visa, Mastercard, Amex, and others where the dataset provides them
- **Card type** — debit or credit funding type
- **Card level** — product tier such as classic or platinum, when available
- **Country information** — ISO alpha-2/alpha-3, country name, flag emoji, currency, approximate coordinates
- **Issuer / bank information** — bank name plus website, phone and city where known
- **Turso/libSQL persistence** — records stored as integer ranges with indexed, prefix-filtered containment lookups
- **Automatic upstream synchronization** — periodic commit checks against configured GitHub CSV sources
- **Validated transactional imports** — streamed into the database in bounded batches inside one transaction; a failed import never takes live data down
- **Manual CSV import** — merge or replace a manual dataset from the admin page
- **Public homepage** with an interactive BIN tester
- **Developer documentation** at `/docs`
- **Health and readiness endpoints** for uptime monitors and orchestrators
- **Docker support** via a multi-stage distroless build
- **Render compatible**, with a `render.yaml` blueprint
- **Lightweight Go implementation** — standard-library `database/sql` plus the pure-Go libSQL client, no frontend build step

---

## Live API

| | |
|---|---|
| Base URL | <https://guison-api.onrender.com> |
| Homepage | <https://guison-api.onrender.com/> |
| Documentation | <https://guison-api.onrender.com/docs> |
| Example lookup | <https://guison-api.onrender.com/45717360> |

---

## Quick start

```bash
curl https://guison-api.onrender.com/45717360
```

```json
{
  "iin": "45717360",
  "match": {
    "start": "457173",
    "end": "457173",
    "length": 6
  },
  "number": {
    "length": 16,
    "luhn": true
  },
  "scheme": "visa",
  "brand": "Visa/Dankort",
  "type": "debit",
  "level": "",
  "prepaid": null,
  "country": {
    "alpha2": "DK",
    "alpha3": "DNK",
    "name": "Denmark",
    "emoji": "🇩🇰",
    "currency": "DKK",
    "latitude": null,
    "longitude": null
  },
  "bank": {
    "name": "Jyske Bank",
    "url": "www.jyskebank.dk",
    "phone": "+45 89 89 89 89",
    "city": "Silkeborg",
    "logo": ""
  }
}
```

Public routes send permissive CORS headers (`Access-Control-Allow-Origin: *`, methods `GET, OPTIONS`), so browser `fetch` works without a proxy.

---

## API endpoints

| Method & path | Returns | Description |
|---|---|---|
| `GET /{bin}` | JSON | Primary lookup for a 6–8 digit BIN/IIN |
| `GET /v1/bin/{bin}` | JSON | Versioned lookup; identical behaviour and response |
| `GET /health` | `text/plain` | Service-alive check for uptime monitors; returns `OK` |
| `GET /healthz` | JSON | Application health with process uptime |
| `GET /readyz` | JSON | Whether usable BIN data is currently available |
| `GET /` | HTML | Public homepage |
| `GET /docs` | HTML | Developer documentation |

`GET /data` is a **protected administration interface** and is not part of the public API. See [Administration](#administration).

Lookup responses are sent with `Cache-Control: no-store`, so a completed import is visible on the very next request.

```bash
curl https://guison-api.onrender.com/health    # OK
curl https://guison-api.onrender.com/healthz   # {"status":"ok","uptime_seconds":86400}
curl https://guison-api.onrender.com/readyz    # {"status":"ready"}
```

---

## Input rules

- Must be **6 to 8 digits**
- Digits `0-9` only — no spaces, dashes or letters
- Leading zeros are significant; always send the value as a string
- Anything shorter than 6, longer than 8, or non-numeric returns `400 invalid_iin`

The 8-digit ceiling is enforced by the API, which means a full card number can never be submitted. Truncate to the first 6–8 digits on your side, and never log or transmit a full PAN.

**Guison performs BIN/IIN metadata lookup only.** It does not verify that a payment card or account exists, is active, or has funds, and it cannot authorise or validate a transaction.

---

## Response

| Field | Type | Notes |
|---|---|---|
| `iin` | string | The BIN/IIN exactly as sent |
| `match.start` | string | First BIN in the matched range |
| `match.end` | string | Last BIN in the matched range; equals `start` for single-BIN records |
| `match.length` | number | Digit width of the matched range (6, 7 or 8) |
| `number.length` | number \| **null** | Expected full card number length |
| `number.luhn` | boolean \| **null** | Whether the Luhn check digit applies |
| `scheme` | string | Card scheme/network; `""` when unknown |
| `brand` | string | Product brand; `""` when unknown |
| `type` | string | `debit` or `credit`; `""` when unknown |
| `level` | string | Product level; `""` when unknown |
| `prepaid` | boolean \| **null** | Prepaid flag |
| `country.alpha2` | string | ISO 3166-1 alpha-2 code |
| `country.alpha3` | string | ISO 3166-1 alpha-3 code |
| `country.name` | string | Country name |
| `country.emoji` | string | Flag emoji derived from the alpha-2 code |
| `country.currency` | string | Currency code |
| `country.latitude` | number \| **null** | Approximate country latitude |
| `country.longitude` | number \| **null** | Approximate country longitude |
| `bank.name` | string | Issuing bank name |
| `bank.url` | string | Issuer website |
| `bank.phone` | string | Issuer contact phone |
| `bank.city` | string | Issuer city |
| `bank.logo` | string | Issuer logo reference |

### Nullable and empty values

Guison never invents data. Two conventions apply:

- **String fields** return `""` when the dataset has no value
- **Optional typed fields** — `prepaid`, `number.length`, `number.luhn`, `country.latitude`, `country.longitude` — return `null`

Guard against both. In JavaScript, prefer `card.bank?.name || "Unknown"`.

> Public lookup responses contain card metadata only. Guison tracks internally which dataset and commit each record came from — that provenance drives automatic updates and is visible on the admin page — but it is not returned to public clients. Dataset credit is published in [Data source & credits](#data-source--credits).

---

## Errors

Errors use a consistent envelope with a stable machine-readable `code`:

```json
{
  "error": {
    "code": "invalid_iin",
    "message": "IIN must contain 6 to 8 digits"
  }
}
```

| Status | Code | When |
|---|---|---|
| `400` | `invalid_iin` | Not 6–8 characters, or contains a non-digit. Message is `IIN must contain 6 to 8 digits` or `IIN must contain only digits` |
| `404` | `not_found` | Valid input, but no record covers it. Message is `No record covers this BIN/IIN` |
| `500` | `internal_error` | Unexpected server or database failure. Message is `An internal error occurred`; details are logged server-side, never returned |
| `503` | `not_ready` | Returned by `/readyz` when no usable dataset is loaded yet |

Treat `404` as a normal outcome, not a failure — it means the BIN is not in the dataset. Retry `500` and `503` with backoff.

---

## Local installation

**Requirements:** Go 1.24+ and a Turso database (or any libSQL endpoint).

```bash
git clone https://github.com/harshi79/project-17.git
cd project-17

cp .env.example .env
# Edit .env: set TURSO_DATABASE_URL, TURSO_AUTH_TOKEN and a
# DATA_ADMIN_PASSWORD of at least 16 characters.

go mod download
go run ./cmd/binapi
```

The server connects to Turso, applies its embedded schema migrations on startup, registers the configured source, and begins the first import. A completely empty Turso database initializes itself and rebuilds the full (~374K record) dataset from the configured upstream source automatically — no manual table setup is required. Watch readiness with:

```bash
curl http://localhost:8080/healthz
curl -i http://localhost:8080/readyz    # 503 until the first import commits
curl http://localhost:8080/45717360
```

Build a binary instead:

```bash
go build ./cmd/binapi          # or: make build
```

A `Makefile` provides `make test`, `make build`, `make run`, `make fmt`, `make vet`, `make compose-up` and `make compose-down`.

---

## Environment variables

**Required**

| Variable | Purpose |
|---|---|
| `TURSO_DATABASE_URL` | Turso/libSQL database URL (begins `libsql://`) |
| `TURSO_AUTH_TOKEN` | Turso authentication token for that database |
| `DATA_ADMIN_PASSWORD` | Protects `/data`; minimum 16 characters, and it must not be left as the example value |

**Optional**

| Variable | Default | Purpose |
|---|---:|---|
| `HTTP_ADDR` | empty | Explicit listen address; overrides `PORT` |
| `PORT` | `8080` | Hosting-provider port, used when `HTTP_ADDR` is empty |
| `SYNC_ENABLED` | `true` | Enable periodic upstream checks |
| `SYNC_ON_START` | `true` | Check configured sources at startup |
| `SYNC_INTERVAL` | `5m` | Check interval; minimum `30s` |
| `GITHUB_TOKEN` | empty | Optional token to raise the GitHub API rate limit |
| `SOURCES_JSON` | built-in source | Inline source configuration JSON |
| `SOURCES_FILE` | empty | Path to a source configuration JSON file |
| `MAX_DOWNLOAD_BYTES` | `104857600` | Maximum CSV size; must be 1 MiB–1 GiB |
| `MAX_INVALID_RATIO` | `0.05` | Maximum fraction of invalid CSV rows tolerated |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown deadline |
| `TELEGRAM_CHANNEL_URL` | empty | Website: official channel link |
| `TELEGRAM_PRIVATE_URL` | empty | Website: private community invite link |
| `DEVELOPER_TELEGRAM_URL` | empty | Website: developer contact link |

Set only one of `SOURCES_FILE` and `SOURCES_JSON`. Invalid booleans, durations, numbers, addresses, passwords or unknown source fields stop startup with a clear error rather than silently falling back to a default.

Telegram variables must be absolute `http(s)` URLs. While unset, the website renders a disabled button with a setup hint instead of a broken or invented link.

### Source configuration

```json
[
  {
    "id": "primary",
    "repository": "owner/repository",
    "branch": "main",
    "path": "data/bins.csv",
    "format": "binlist",
    "priority": 100,
    "min_records": 100000
  }
]
```

Supported `format` values are `binlist`, `ranges` and `generic`. Source IDs cannot use the reserved ID `manual`. Automatic source priorities must be below `1000`; the manual dataset uses priority `1000` so admin corrections always win when ranges overlap. See [`config/sources.example.json`](config/sources.example.json).

---

## Docker

The repository ships a multi-stage `Dockerfile` producing a distroless, non-root image.

```bash
docker build -t guison-api .

docker run -d --name guison-api --restart unless-stopped -p 8080:8080 \
  -e TURSO_DATABASE_URL='libsql://YOUR-DATABASE.turso.io' \
  -e TURSO_AUTH_TOKEN='YOUR_TURSO_AUTH_TOKEN' \
  -e DATA_ADMIN_PASSWORD='a-unique-random-value-of-at-least-16-characters' \
  guison-api
```

The container is stateless. **All persistent application data lives in Turso**, not on the container filesystem, so containers can be replaced or scaled freely. Only `/tmp` is used, for staging CSV downloads during import.

---

## Docker Compose

Compose builds and runs the API container — useful for local development.

```bash
cp .env.example .env
# Set TURSO_DATABASE_URL, TURSO_AUTH_TOKEN and DATA_ADMIN_PASSWORD;
# Compose refuses to start without them.

docker compose up --build
```

Compose forwards the two Turso variables to the container. You must also provide `DATA_ADMIN_PASSWORD`. Everything else is optional and forwarded from your environment or `.env`, including the three Telegram variables. The volume-less container holds no data itself; all persistence is in Turso.

Stop with `docker compose down`.

---

## Render deployment

The project is Render-ready via the Docker runtime. Provision a Turso database first (`turso db create`, then `turso db show --url` and `turso db tokens create <database>`).

### Option A — Web Service

1. In Render choose **New → Web Service** and connect this repository.
2. Select the **Docker** runtime; Render uses the root `Dockerfile` automatically.
3. Add environment variables:
   - `TURSO_DATABASE_URL`
   - `TURSO_AUTH_TOKEN`
   - `DATA_ADMIN_PASSWORD`
   - optionally `TELEGRAM_CHANNEL_URL`, `TELEGRAM_PRIVATE_URL`, `DEVELOPER_TELEGRAM_URL`
4. Set the health check path to **`/health`**.
5. Deploy.

### Option B — Blueprint

Choose **New → Blueprint** and point Render at [`render.yaml`](render.yaml). It defines one Docker web service, sets `/health` as the health check, and prompts for the secrets. It does **not** provision a database — set `TURSO_DATABASE_URL` and `TURSO_AUTH_TOKEN` yourself.

### Notes

- **Do not set `PORT`.** Render provides it and the application binds to it automatically.
- Use **`/health`** for the platform health check. It is a cheap liveness probe that does not touch the database.
- **Do not use `/readyz` as basic process health.** It reports whether a dataset is loaded and returns `503` during the first import, which would cause a healthy deploy to be marked failed. Use it for your own dataset-readiness checks instead.
- Render terminates HTTPS before forwarding requests, which is what `/data` Basic Auth needs.

After the first deploy, verify:

```bash
curl https://YOUR-SERVICE.onrender.com/health
curl https://YOUR-SERVICE.onrender.com/readyz
curl https://YOUR-SERVICE.onrender.com/45717360
```

---

## Deploy anywhere

There is no vendor lock-in. Guison runs on anything that provides Docker or a Linux Go runtime, a reachable Turso/libSQL database, environment variables, and one HTTP port — a VPS with systemd, a container platform, Kubernetes, or a PaaS.

The recipe is always the same:

1. Build the container (or the Go binary)
2. Set `TURSO_DATABASE_URL` and `TURSO_AUTH_TOKEN`
3. Set `DATA_ADMIN_PASSWORD`
4. Expose the HTTP port — bind with `PORT` or `HTTP_ADDR`
5. Point health checks at `/health`
6. Serve it over HTTPS, especially if you use `/data`

---

## Administration

`GET /data` is a protected administration interface. From it you can:

- inspect dataset and per-source status, including record counts, last import time, current commit and last error
- upload a manual CSV dataset
- choose **merge** (add and update rows) or **replace** (atomically swap the manual dataset)
- trigger an immediate check of all configured sources

It is protected with **HTTP Basic authentication**: username `admin`, password from the server-side `DATA_ADMIN_PASSWORD` environment variable. The password is never rendered into the page HTML and there is no JavaScript on the page.

**Always serve this over HTTPS in production** — Basic Auth credentials must be protected in transit. No credentials are published in this repository; set your own.

Manual imports affect only the manual dataset, which carries priority `1000` so corrections win over automatic sources. Configured automatic sources are untouched by either mode.

---

## Data import & automatic sync

```
configured source
  → check upstream commit (skip if unchanged)
  → download that exact version
  → streaming CSV parse
  → bounded batches of 1,000 rows, deduplicated on the primary key (last row wins)
  → one multi-row upsert per batch, all inside a single transaction
  → validation (invalid-row ratio, minimum record count)
  → commit, or roll back and keep serving the previous dataset
  → lookup
```

When `SYNC_ENABLED=true`, one background loop checks each configured source every `SYNC_INTERVAL`. If the upstream commit is unchanged, nothing is downloaded.

**The import never holds the dataset in memory.** Rows are parsed, deduplicated and installed 1,000 at a time, so peak memory stays at a few megabytes regardless of source size — a full ~375K row import runs in roughly 5 MB of Go heap. Progress is logged every 50,000 rows (`dataset import in progress`), so a long first import is visibly moving rather than looking hung.

**Failed imports preserve previously working data.** If download, parsing or validation fails at any step, the transaction is never committed, the previously imported records keep serving, and the error is recorded and shown on `/data`.

---

## Project structure

```
.
├── cmd/binapi/              # main entrypoint
├── config/                  # example source configuration
├── internal/
│   ├── config/              # environment configuration and validation
│   ├── database/            # Turso/libSQL access, migrations, lookup query
│   │   └── migrations/      # embedded schema
│   ├── httpapi/             # HTTP routes, public website and docs
│   │   └── assets/          # embedded CSS, JS and artwork
│   ├── importer/            # CSV parsing and normalisation
│   ├── model/               # shared response and status types
│   └── syncer/              # GitHub polling and transactional import
├── Dockerfile
├── docker-compose.yml
├── render.yaml
└── Makefile
```

The public website is server-rendered by the Go application. CSS, JavaScript and artwork are embedded into the binary with `go:embed`, so there is no frontend build step, no npm and no separate deployment.

---

## Security

Mechanisms implemented in this repository:

- **Admin password is environment-only** — read from `DATA_ADMIN_PASSWORD`, never committed, never rendered into HTML
- **`/data` uses HTTP Basic Auth** with constant-time credential comparison
- **CSRF protection** — admin forms carry a server-generated token, validated in constant time
- **Secure headers** — `Content-Security-Policy`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy`; admin pages additionally send `Cache-Control: no-store`
- **No secrets in public HTML** — the public site renders no credentials or environment values
- **Public lookup requires no authentication**, and CORS is open only on non-admin routes
- **Full card numbers are rejected** — input above 8 digits returns `400`
- **Upload limits** — request bodies are capped and oversized uploads are rejected
- **HTTPS is required in production** for `/data`, and recommended everywhere

Please report security issues privately via the [developer contact](#community) rather than opening a public issue.

---

## Data source & credits

Guison imports public BIN/IIN metadata from configured upstream sources. It does **not** claim ownership of that data.

The current primary dataset is **[venelinkochev/bin-list-data](https://github.com/venelinkochev/bin-list-data)** by Venelin Kochev.

- **Verified license:** Creative Commons Attribution 4.0 International (**CC BY 4.0**) — confirmed from the upstream repository's `LICENSE` file, its README, and GitHub's license metadata (`spdx_id: CC-BY-4.0`)
- **Attribution requirement:** CC BY 4.0 requires crediting the original author and indicating that changes were made. Guison normalises the upstream CSV into integer range records and serves it as JSON — that is the modification.
- If you redistribute data obtained from Guison, you must carry the same attribution.

How the data is handled:

- Guison automatically checks configured upstream sources for changes
- Imports are streamed into the database in bounded batches and validated inside one transaction before the new data becomes visible
- A failed import leaves the previous dataset serving

> **Accuracy disclaimer.** BIN/IIN information may be incomplete, outdated or inaccurate. This is community-maintained open data, not real-time payment network data. Do not treat Guison as authoritative payment verification, and do not use it as a sole fraud or compliance control.

---

## License

The Guison API **source code** in this repository is released under the [MIT License](LICENSE).

Imported BIN data retains its upstream license and is **not** covered by the MIT license — see [Data source & credits](#data-source--credits). No BIN dataset is committed to this repository.

The website artwork in `internal/httpapi/assets/art/` was created for this project and ships under the same MIT license as the rest of the repository.

---

## Community

- **[Official Telegram Channel](https://t.me/yorifederation)** — dataset updates, new endpoints and status notices
- **[Private Community](https://t.me/+y8EekRvqpnQzNjZl)** — invite link for discussion and support
- **[Contact the Developer](https://t.me/YorichiiPrime)** — integration questions and direct contact

---

## Contributing

Issues and pull requests are welcome.

For code changes:

- keep the implementation lightweight
- include tests for new behaviour
- preserve existing API compatibility unless the change is intentional and described
- avoid adding unnecessary dependencies
- run `gofmt`, `go vet` and the test suite before opening a PR

---

## Testing

```bash
go test ./...              # run the test suite
go test -race ./...        # run with the race detector (also: make test)
go vet ./...               # static analysis
go build ./cmd/binapi      # verify the binary builds
gofmt -l .                 # list unformatted files; empty output means clean
```
