# wellness-travel-api

Go API for Wellness Travel MVP1. The server owns validation, scoring, provenance, and the 30-day search policy. `SUPABASE_URL` and `SUPABASE_SERVICE_ROLE_KEY` are server-only settings; checked-in Open-Meteo/CAMS snapshots and compact Seasonal data make the local demo deterministic without exposing credentials.

## Vercel

This repository is configured with the Vercel **Go Gin Framework Preset** (`vercel.json`). Vercel builds the root `main.go` Gin server and supplies its `PORT` environment variable. Deploy from this directory with `vercel --prod`, then set `CORS_ORIGIN` to the web project's production origin. Keep `SUPABASE_SERVICE_ROLE_KEY` server-only. The public API keeps the same paths (`/healthz` and `/v1/*`).

## Run

```bash
go run .
curl http://localhost:8080/healthz
```

ตรวจชุด API handler และ validation ด้วย `GOCACHE=/tmp/wellness-go-cache go test ./...` ก่อนเชื่อมหน้าเว็บ

For a Supabase-backed run, set `SUPABASE_URL` and `SUPABASE_SERVICE_ROLE_KEY`. The API reads the `travel.place` catalog through PostgREST and keeps the key out of the browser. The scoring datasets are `data/forecast.json`, `data/air-forecast.json`, `data/seasonal_month.json`, and `data/seas5.json`; each carries source/version metadata. The older `data/pilot-summary.json` is retained only as history and is not loaded by the server.

กำหนด `CORS_ORIGIN` เป็น origin ของเว็บแต่ละ environment (local ตัวอย่างคือ `http://127.0.0.1:3000`) และไม่ใช้ wildcard เมื่อเปิด endpoint จริง

## Endpoints

- `GET /healthz`
- `GET /v1/capabilities`
- `GET /v1/places`
- `POST /v1/recommendations`
- `POST /v1/recommendations/detail`
