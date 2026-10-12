# AGENTS.md

## Project Overview

Go/Gin API server for a Wellness Travel MVP. Backend-only — no frontend.
Serves JSON endpoints under `/v1/*` plus `/healthz`.

## Running Locally

```bash
go run .
curl http://localhost:8080/healthz
```

The server defaults to port 8080; set `PORT` to override.

## Base44 Sandbox

- `docker-compose.base44.yml` runs the Go server on port 3000 using `golang:1.26`.
- Source is bind-mounted at `/app`; changes require a container restart or `reload_preview`.
- Go module cache is persisted in the `go-mod` volume.
- Healthcheck probes `GET /healthz`.

## Key Details

- **Supabase is optional.** Without `SUPABASE_URL` + `SUPABASE_SERVICE_ROLE_KEY`, the
  server loads places from `data/places.json` and forecast/seasonal data from other
  `data/*.json` files (embedded via `go:embed`). The local demo is fully deterministic.
- **Timezone:** `Asia/Bangkok` — the golang base image ships tzdata so
  `time.LoadLocation` works out of the box.
- **CORS:** `CORS_ORIGIN` is a comma-separated allowlist. The sandbox compose sets it
  to the preview origin plus localhost fallbacks.
- **Data files** live in `data/` and are embedded at build time via `//go:embed data/*.json`.
  They are also read from disk at runtime when available.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | Health check |
| GET | `/v1/capabilities` | Server capabilities and date policy |
| GET | `/v1/places` | List all places |
| POST | `/v1/recommendations` | Get travel recommendations |
| POST | `/v1/recommendations/detail` | Get detailed recommendation for a place |

## Testing

```bash
GOCACHE=/tmp/wellness-go-cache go test ./...
```
