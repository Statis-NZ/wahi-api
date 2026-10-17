# Project Specification: Wāhi API (`wahi-api`)

## 1. Objective & Architecture Overview

Build a lightweight, open-source, self-hostable New Zealand Address Autosuggest & Geocoding REST API written in **Go (Golang)** backed by **Meilisearch (v1.54+)**.

The system ingests open data from the **Toitū Te Whenua Land Information New Zealand (LINZ) Data Service (LDS)** using the March 2026 unified addressing schema and the NZ Suburbs and Localities dataset. It is designed to run on a low-cost VPS (1GB–2GB RAM) via Docker Compose.

### Core Tech Stack

- **Language:** Go 1.23+
- **HTTP Router & OpenAPI:** `github.com/danielgtaylor/huma/v2` + standard library `net/http` (auto-generates OpenAPI 3.1 / Swagger UI)
- **Search Engine:** Meilisearch `v1.54+` (`getmeili/meilisearch:v1.54`)
- **Meilisearch SDK:** `github.com/meilisearch/meilisearch-go`
- **Geometry / WKT Parser:** `github.com/twpayne/go-geom` (for parsing LINZ WKT geometries)

---

## 2. Project Directory Structure

Create the following repository layout:

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
│   │   ├── handlers.go          # /v1/autosuggest, /v1/addresses/{id}, /v1/reverse
│   │   └── parser.go            # NZ address query pre-parser (unit/number/range extractor)
│   ├── ingest/
│   │   ├── downloader.go        # Streams CSV exports/changesets from LINZ LDS API
│   │   ├── joiner.go            # In-memory lookup maps for Suburbs & Road Sections
│   │   └── indexer.go           # Configures Meilisearch settings, synonyms & batches NDJSON
│   └── models/
│       └── types.go             # Source LINZ structs & target Meilisearch document structs
├── deploy/
│   ├── Dockerfile               # Multi-stage Go build (distroless/alpine runtime)
│   └── docker-compose.yml       # Go API + memory-constrained Meilisearch service
├── .env.example
├── go.mod
└── README.md

```

---

## 3. Data Sources & In-Memory ETL Pipeline (`internal/ingest`)

The ingestion pipeline takes a `LINZ_API_KEY` (or local CSV file paths) and processes three LINZ datasets in order so smaller lookup tables are loaded into memory before streaming the ~2.43 million address rows:

### Step 3.1: Load `NZ Suburbs and Localities` (Layer `113764`) into Memory Map

Read the CSV/WFS export and index into a Go map keyed by `name` (normalized lowercase):

- **Fields to extract:** `id` (int), `name` (string), `additional_name` (string, comma-separated aliases e.g. "Wellington CBD"), `type` (string), `major_name` (string), `major_name_type` (string), `territorial_authority` (string), `population_estimate` (int), `shape` (Polygon EPSG:2193 - calculate centroid or bounding box and convert to WGS84/NZGD2000 lat/lng).
- **Action:**

1. Batch-insert all ~3,000 rows into the Meilisearch `localities` index.
2. Keep a lookup map `map[string]SuburbMeta` in memory (`name` -> `{population_estimate, additional_name}`) to enrich road sections and addresses.

### Step 3.2: Load `NZ Addresses: Road Sections` (Layer `123109`)

Read the Road Sections CSV and aggregate unique streets by `road_id` + `suburb_locality` + `town_city`:

- **Fields to extract:** `road_section_id` (int), `road_id` (int), `full_road_name` (string), `road_name_label` (string, e.g., "Queen St W"), `secondary_road_name` (string, e.g., "State Highway 45"), `tertiary_road_name` (string, e.g., "Surf Highway"), `suburb_locality` (string), `town_city` (string), `territorial_authority` (string), `is_land` (bool), `shape` (LineString EPSG:4167 - extract midpoint `[lat, lng]`).
- **Action:**

1. Enrich each unique street record with `population_estimate` and `suburb_aliases` from the Step 3.1 lookup map.
2. Batch-insert deduplicated streets into the Meilisearch `streets` index.
3. Keep a compact in-memory lookup map `map[int]RoadAliases` keyed by `road_id` -> `{road_name_label, secondary_road_name, tertiary_road_name}`.

### Step 3.3: Stream `NZ Addresses` (Layer `123113`)

Stream the ~2.43 million rows using Go's `encoding/csv` reader (never load the whole file into memory at once) in batches of `50,000`:

- **Fields to extract:**
- `address_id` (int, Primary Key)
- `road_id` (int)
- `full_address_number` (string, e.g., `"1/100A-120A"`)
- `full_road_name` (string, e.g., `"Queen Street West"`)
- `full_address` (string, e.g., `"1/100A-120A, Queen Street West, Auckland Central, Auckland"`)
- `territorial_authority` (string)
- `unit` (string)
- `address_number` (int)
- `address_number_suffix` (string)
- `address_number_high` (int, optional/nullable)
- `road_name` (string)
- `road_name_type` (string)
- `road_name_suffix` (string)
- `suburb_locality` (string)
- `town_city` (string)
- `is_land` (bool)
- `address_lifecycle` (string: `"Current"` or `"Proposed"`)
- `shape` (Point WKT in EPSG:4167 -> parse into `_geo: {"lat": float64, "lng": float64}`)

- **Enrichment:**
- Lookup `road_id` in the Road Sections map to attach `road_name_label` and `secondary_road_name`.
- Lookup `suburb_locality` in the Suburbs map to attach `population_estimate` (int) and `suburb_aliases` (string).

- **Action:** Send batches of 50,000 documents to the Meilisearch `addresses` index using `AddDocumentsInBatches` (or NDJSON payload). Free the in-memory lookup maps once ingestion finishes.

---

## 4. Meilisearch Index Configuration (`internal/ingest/indexer.go`)

Configure the `addresses`, `streets`, and `localities` indexes before document insertion. Do NOT use wildcard `"*"` for searchable or filterable attributes to keep disk and RAM usage minimal.

### 4.1 `addresses` Index Settings

- **Primary Key:** `address_id`
- **Searchable Attributes (Ordered by weight):**

1. `full_address_number`
2. `full_road_name`
3. `road_name_label`
4. `secondary_road_name`
5. `suburb_locality`
6. `suburb_aliases`
7. `town_city`

- **Filterable Attributes:**
  `address_number`, `address_number_high`, `unit`, `suburb_locality`, `town_city`, `territorial_authority`, `is_land`, `address_lifecycle`, `_geo`
- **Sortable Attributes:**
  `population_estimate`, `_geo`
- **Ranking Rules:**

```json
[
  "words",
  "typo",
  "proximity",
  "attributeRank",
  "wordPosition",
  "sort",
  "exactness",
  "population_estimate:desc"
]
```

- **Synonyms Dictionary:**
  Configure NZ road and locality synonyms on all indexes:

```json
{
  "st": ["street"],
  "street": ["st"],
  "rd": ["road"],
  "road": ["rd"],
  "ave": ["avenue"],
  "avenue": ["ave"],
  "dr": ["drive"],
  "drive": ["dr"],
  "pl": ["place"],
  "place": ["pl"],
  "cres": ["crescent"],
  "crescent": ["cres"],
  "tce": ["terrace"],
  "terrace": ["tce"],
  "hwy": ["highway"],
  "sh": ["state highway"],
  "ln": ["lane"],
  "ct": ["court"],
  "mt": ["mount"],
  "pt": ["point"],
  "cbd": ["central"]
}
```

---

## 5. Query Pre-Parser & Search Logic (`internal/api/parser.go`)

Address queries contain structural numbers (`"2/100A Queen St"`, `"110 Queen St"`). Implement a Go regex parser before dispatching to Meilisearch:

### Regex Pattern

Match optional leading unit/flat patterns and street numbers:
`^(?:(?:unit|flat|apt|u|f)\s*([a-zA-Z0-9]+)[,\s/]+|([a-zA-Z0-9]+)\s*/\s*)?(\d+)([a-zA-Z])?(?:\s*-\s*(\d+)[a-zA-Z]?)?\b\s*(.*)$`

### Execution Strategy in `/v1/autosuggest`

1. **Case A: Query starts with a street number (e.g., `q="2/110 Queen St"` or `q="110 Queen St"`):**

- Extracted `unit` = `"2"`, `number` = `110`, remaining text = `"Queen St"`.
- Construct a Meilisearch filter on the `addresses` index that catches both exact street numbers AND range addresses (where `110` falls between `100` and `120`):

```text
(address_number = 110 OR (address_number <= 110 AND address_number_high >= 110))

```

- Pass the full query (or remaining street query + unit) to Meilisearch `addresses` index so exact matches on `full_address_number` rank highest, while range addresses still match via the filter.

2. **Case B: Query has NO leading street number (e.g., `q="Ponsonby Rd"` or `q="Ōtara"`):**

- Execute a single Meilisearch `MultiSearch` call across:

1. `localities` (limit 3)
2. `streets` (limit 5)
3. `addresses` (limit `limit`)

- Merge and deduplicate into a unified response array with `type: "locality" | "street" | "address"`.

3. **Geo-Biasing:**

- If optional query params `lat` and `lon` are provided:
- Add `_geoRadius(lat, lon, 50000)` (or sort by `_geoPoint(lat, lon):asc` when query length >= 3) to prioritize local results.

---

## 6. REST API Endpoints (`internal/api/handlers.go`)

### 1. `GET /v1/autosuggest`

- **Query Parameters:**
- `q` (string, required, minLength: 2): Search string.
- `limit` (int, default: 10, max: 25): Max results.
- `type` (string, optional: `address`, `street`, `locality`, `all` - default `all`).
- `lat` & `lon` (float64, optional): Coordinates for proximity boosting.
- `town_city` (string, optional): Filter to a specific city.
- `include_proposed` (bool, default: `false`): Filter `address_lifecycle = 'Current'` by default.

- **Response (`200 OK`):**

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

- Fetches the full document directly from Meilisearch `addresses` index by primary key `address_id`.
- Returns all decomposed LINZ attributes (`unit`, `address_number`, `address_number_suffix`, `address_number_high`, `road_name`, `road_name_type`, `road_name_suffix`, `suburb_locality`, `town_city`, `territorial_authority`, `is_land`, `address_lifecycle`, `road_id`, `location`).

### 3. `GET /v1/reverse`

- **Query Parameters:** `lat` (float64, required), `lon` (float64, required), `radius_m` (int, default: 100, max: 1000), `limit` (int, default: 5).
- **Logic:** Queries Meilisearch `addresses` index with `Filter: "_geoRadius(lat, lon, radius_m)"` and `Sort: ["_geoPoint(lat, lon):asc"]`.

### 4. `GET /healthz`

- Checks Meilisearch `/health` status and returns document counts for `addresses`, `streets`, and `localities`.

---

## 7. Low-Footprint Docker Compose Configuration (`deploy/docker-compose.yml`)

Configure Meilisearch to strictly cap indexing memory and threads so the stack never triggers an OOM crash on a cheap 1GB–2GB VPS:

```yaml
services:
  meilisearch:
    image: getmeili/meilisearch:v1.54
    container_name: wahi-meili
    restart: unless-stopped
    environment:
      - MEILI_ENV=production
      - MEILI_MASTER_KEY=${MEILI_MASTER_KEY:-change_me_in_production_min_16_bytes}
      - MEILI_NO_ANALYTICS=true
      - MEILI_MAX_INDEXING_MEMORY=512Mb
      - MEILI_MAX_INDEXING_THREADS=1
    ports:
      - "127.0.0.1:7700:7700"
    volumes:
      - meili_data:/meili_data
    deploy:
      resources:
        limits:
          memory: 1200M

  api:
    build:
      context: ..
      dockerfile: deploy/Dockerfile
    container_name: wahi-api
    restart: unless-stopped
    depends_on:
      - meilisearch
    environment:
      - PORT=8080
      - MEILI_HOST=http://meilisearch:7700
      - MEILI_API_KEY=${MEILI_MASTER_KEY:-change_me_in_production_min_16_bytes}
      - LINZ_API_KEY=${LINZ_API_KEY}
    ports:
      - "8080:8080"
    deploy:
      resources:
        limits:
          memory: 256M

volumes:
  meili_data:
```

---

## 8. Step-by-Step Execution Plan for Agent

Execute the implementation in the following phases, running `go test ./...` and verifying builds after each phase:

1. **Phase 1 (Scaffold & Models):** Initialize `go.mod`, configuration loader (`internal/config`), and data structures (`internal/models/types.go`) matching the LINZ Data Dictionaries (`NZ Addresses`, `NZ Road Sections`, `NZ Suburbs and Localities`).

2. **Phase 2 (Address Query Parser & Unit Tests):** Implement `internal/api/parser.go` to parse NZ unit numbers, street numbers, suffixes, and ranges (`1/100A-120A`), plus comprehensive table-driven unit tests in `parser_test.go`.
3. **Phase 3 (Meilisearch Indexer & Streaming ETL):** Implement `internal/ingest/` to configure Meilisearch indexes/synonyms, parse WKT geometries (`EPSG:4167` & `EPSG:2193`), join Suburb populations and Road aliases in memory, and batch-stream CSV rows into Meilisearch. Include a sample fixture CSV set in `testdata/` for testing ingestion locally.
4. **Phase 4 (HTTP API & OpenAPI Docs):** Build the Huma v2 HTTP router and handlers (`/v1/autosuggest`, `/v1/addresses/{id}`, `/v1/reverse`, `/healthz`) in `internal/api/`.
5. **Phase 5 (CLI & Containerization):** Wire up `cmd/server/main.go` with `serve` and `ingest` subcommands, create the multi-stage `Dockerfile`, `docker-compose.yml`, and a comprehensive `README.md` with quickstart instructions.
