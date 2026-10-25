# Wāhi API (`wahi-api`)

A lightweight, open-source, self-hostable **New Zealand Address Autosuggest & Geocoding REST API** written in Go and backed by **Meilisearch (v1.54+)**.

Wāhi API ingests open data from the **Toitū Te Whenua Land Information New Zealand (LINZ) Data Service (LDS)** using the March 2026 unified addressing schema and the NZ Suburbs and Localities dataset. It is engineered to run comfortably on a low-cost 1GB–2GB RAM VPS via Docker Compose.

---

## Features

- **Unified March 2026 LINZ Schema:** Streams and joins:
  1. `NZ Suburbs and Localities` (Layer `113764`) — population estimates, suburb aliases, and NZTM2000 (`EPSG:2193`) polygon centroids.
  2. `NZ Addresses: Road Sections` (Layer `123109`) — abbreviated road labels (`Queen St W`), secondary/tertiary route names (`State Highway 45`, `Surf Highway`), and `EPSG:4167` midpoints.
  3. `NZ Addresses` (Layer `123113`) — ~2.43 million street addresses streamed in batches of 50,000 without loading the full dataset into memory.
- **NZ Address Query Pre-Parser:** Extracts units/flats (`2/100A`, `Unit 4B`, `Flat 2`), street numbers, suffixes, and ranged addresses (`1/100A-120A`), generating Meilisearch range filters so queries like `110 Queen St` match both `110 Queen St` and ranged properties like `100-120 Queen St`.
- **Typo-Tolerant & Macron-Aware Search:** Pre-configured with NZ road/locality synonyms (`st` ↔ `street`, `rd` ↔ `road`, `sh` ↔ `state highway`, `cbd` ↔ `central`, etc.) and `population_estimate:desc` tie-breaking.
- **Proximity Geo-Biasing & Reverse Geocoding:** Supports `lat`/`lon` proximity boosting on `/v1/autosuggest` and radius-based reverse geocoding on `/v1/reverse`.
- **OpenAPI 3.1 & Interactive Docs:** Powered by [Huma v2](https://huma.rocks/), automatically serving `/openapi.json`, `/openapi.yaml`, and interactive API documentation at `/docs`.

---

## Project Layout

```text
wahi-api/
├── cmd/
│   └── server/
│       └── main.go              # CLI entrypoint (`serve` and `ingest` subcommands)
├── internal/
│   ├── config/
│   │   └── config.go            # Env var configuration loader
│   ├── api/
│   │   ├── router.go            # Huma API setup, CORS, rate-limiting middleware
│   │   ├── handlers.go          # /v1/autosuggest, /v1/addresses/{id}, /v1/reverse, /healthz
│   │   └── parser.go            # NZ address query pre-parser (unit/number/range extractor)
│   ├── ingest/
│   │   ├── downloader.go        # Streams CSV exports/changesets from LINZ LDS API
│   │   ├── joiner.go            # In-memory lookup maps for Suburbs & Road Sections + WKT parser
│   │   └── indexer.go           # Configures Meilisearch settings, synonyms & batches NDJSON
│   └── models/
│       └── types.go             # Source LINZ structs & target Meilisearch document structs
├── testdata/                    # Sample fixture CSVs (suburbs, roads, addresses)
├── demo/
│   ├── index.html               # Standalone local demo & playground page (excluded from Docker image)
│   ├── styles.css               # Demo page styles
│   └── app.js                   # Demo page client-side logic
├── deploy/
│   ├── Dockerfile               # Multi-stage Go build (distroless runtime)
│   └── docker-compose.yml       # Go API + memory-constrained Meilisearch service
├── .dockerignore
├── .env.example
├── go.mod
└── README.md
```

---

## Quickstart

### Option 1: Docker Compose (Recommended for Production / VPS)

1. Copy `.env.example` to `.env` and set your `MEILI_MASTER_KEY` and `LINZ_API_KEY` (generate a free key at [data.linz.govt.nz/my/api/](https://data.linz.govt.nz/my/api/)):

   ```bash
   cp .env.example .env
   ```

2. Start Meilisearch and the Wāhi API server:

   ```bash
   docker compose -f deploy/docker-compose.yml --env-file .env up -d --build
   ```

3. Run initial data ingestion:

   - **Using sample fixtures (instant local test):**
     ```bash
     docker compose -f deploy/docker-compose.yml exec api /app/wahi-api ingest --fixtures
     ```

   - **Full LINZ Data Service streaming ingestion (~2.43M addresses):**
     ```bash
     docker compose -f deploy/docker-compose.yml exec api /app/wahi-api ingest
     ```

### Option 2: Local Go Development

1. Start a local Meilisearch v1.54 instance:

   ```bash
   docker run -d --rm -p 7700:7700 \
     -e MEILI_MASTER_KEY=change_me_in_production_min_16_bytes \
     getmeili/meilisearch:v1.54
   ```

2. Ingest the sample CSV fixtures (or local CSV exports):

   ```bash
   go run ./cmd/server ingest --fixtures
   ```

   Or specify custom local CSV files:

   ```bash
   go run ./cmd/server ingest \
     --suburbs-csv ./path/to/suburbs.csv \
     --roads-csv ./path/to/road_sections.csv \
     --addresses-csv ./path/to/addresses.csv
   ```

3. Run the API server:

   ```bash
   go run ./cmd/server serve --port 8080
   ```

4. Open `http://localhost:8080/docs` for the interactive OpenAPI 3.1 explorer, or open `demo/index.html` in your browser (or via `python3 -m http.server 3000 -d demo`) to test autosuggest, address lookup, and map-based reverse geocoding locally.

---

## REST API Endpoints

### 1. `GET /v1/autosuggest`

Typeahead search across NZ addresses, streets, and localities.

**Query Parameters:**
- `q` (string, required, min 2 chars): Search query (e.g. `1/100A Queen St`, `Ponsonby Rd`, `Ōtara`).
- `limit` (int, default `10`, max `25`): Maximum number of results.
- `type` (string, default `all`): Filter by `address`, `street`, `locality`, or `all`.
- `lat` & `lon` (float64, optional): Coordinates for 50km proximity boosting and sorting.
- `town_city` (string, optional): Filter to a specific town/city (e.g. `Auckland`).
- `include_proposed` (bool, default `false`): Include `Proposed` lifecycle addresses in addition to `Current`.

**Example:**
```bash
curl "http://localhost:8080/v1/autosuggest?q=1/110+Queen+St&lat=-36.8485&lon=174.7633"
```

```json
{
  "results": [
    {
      "id": 3000000,
      "type": "address",
      "full_address": "1/100A-120A, Queen Street West, Auckland Central, Auckland",
      "full_address_number": "1/100A-120A",
      "full_road_name": "Queen Street West",
      "suburb_locality": "Auckland Central",
      "town_city": "Auckland",
      "location": { "lat": -36.8485, "lon": 174.7633 }
    }
  ],
  "query_time_ms": 3
}
```

### 2. `GET /v1/addresses/{address_id}`

Fetch the full decomposed LINZ address document by primary key.

```bash
curl "http://localhost:8080/v1/addresses/3000000"
```

### 3. `GET /v1/reverse`

Reverse geocode a coordinate to the nearest NZ addresses.

**Query Parameters:**
- `lat` (float64, required)
- `lon` (float64, required)
- `radius_m` (int, default `100`, max `1000`)
- `limit` (int, default `5`, max `25`)

```bash
curl "http://localhost:8080/v1/reverse?lat=-36.8485&lon=174.7633&radius_m=150"
```

### 4. `GET /healthz`

Returns Meilisearch health status and indexed document counts for `addresses`, `streets`, and `localities`.

```bash
curl "http://localhost:8080/healthz"
```

---

## Access Control (CORS & Rotatable Access Tokens)

Deployers can configure who can access the service using environment variables in `.env`:

### 1. Frontend CORS Configuration (`CORS_ORIGINS` & `CORS_STRICT`)

- `CORS_ORIGINS`: Comma-separated list of allowed origins. Supports `*` (default), exact origins (`https://example.co.nz`), and wildcard subdomains (`https://*.example.co.nz`).
- `CORS_STRICT`: Set to `true` to actively reject requests carrying a non-matching `Origin` header with `403 Forbidden`.

```env
CORS_ORIGINS=https://example.co.nz,https://*.example.co.nz
CORS_STRICT=true
```

### 2. Rotatable Access Tokens (`API_ACCESS_TOKENS`)

- `API_ACCESS_TOKENS` (or `API_ACCESS_TOKEN`): Comma-separated list of valid access tokens. When empty (default), endpoints are publicly accessible.
- When set, requests to `/v1/*` must present a valid token via any of:
  - `Authorization: Bearer <token>` header
  - `X-API-Key: <token>` header
  - `?api_key=<token>` query parameter
- **Zero-Downtime Token Rotation:**
  1. Add the new token alongside the existing token: `API_ACCESS_TOKENS=tok_new_v2,tok_old_v1`
  2. Reload in-place by sending `SIGHUP` (`kill -HUP <pid>` or `docker kill -s HUP wahi-api`) or restarting the container.
  3. Update client applications to use `tok_new_v2`, then remove `tok_old_v1` (`API_ACCESS_TOKENS=tok_new_v2`).

---

## Testing

Run the full unit and integration test suite:

```bash
go test -v ./...
```

---

## License

This project is licensed under the [MIT License](file:///Users/cameron/Sites/statis/wahi-api/LICENSE.md) © 2026 Cameron Prebble.

Contains data sourced from the [Toitū Te Whenua Land Information New Zealand (LINZ) Data Service](https://data.linz.govt.nz) licensed under [CC-BY 4.0](https://creativecommons.org/licenses/by/4.0/).

