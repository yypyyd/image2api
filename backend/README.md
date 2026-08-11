# Vivid AI Backend

Go backend for `vivid-ai`, using:

- Gin
- GORM
- PostgreSQL
- Redis

## Current scope

This is an in-progress rewrite. The current skeleton already includes:

- app bootstrap
- PostgreSQL and Redis initialization
- GORM auto-migrations
- session storage in Redis
- opaque, cross-origin API media via `/v1/images/:event/content`
- legacy gallery media and existing-link compatibility via `/images/:user/:name`
- public site endpoint: `/admin/api/site`
- public showcase endpoint: `/admin/api/showcase`
- session-based auth endpoint: `/admin/api/auth/me`
- typed image/video/audio reference uploads for Adobe video generation, with
  model capability checks and bounded multipart requests

## Environment

Set these before running:

```powershell
$env:POSTGRES_DSN="host=127.0.0.1 user=postgres password=postgres dbname=vivid_ai port=5432 sslmode=disable TimeZone=Asia/Shanghai"
$env:REDIS_ADDR="127.0.0.1:6379"
$env:HTTP_ADDR=":6061"
$env:PUBLIC_BASE_URL="https://api.example.com"
```

Optional:

```powershell
$env:APP_ENV="development"
$env:APP_TITLE="Vivid AI"
$env:SESSION_COOKIE_NAME="vivid_session"
$env:CORS_ORIGINS="http://localhost:5173,http://127.0.0.1:5173"
```

## Run

```powershell
go run ./cmd/api
```

## Notes

- Generated media defaults to `../../ai-gateway/data/generated` relative to the backend working directory.
- Image API results use public, CORS-enabled opaque event URLs so downstream
  clients can fetch them without a browser session and without seeing internal
  owner/object keys. RustFS remains private behind the gateway; API CORS accepts
  arbitrary origins without credentials, while user, billing, account, and
  admin API routes retain authentication. Apply edge rate and connection limits
  to `/images/` and `/v1/images/*/content` in public deployments.
- Set `PUBLIC_BASE_URL` to the canonical HTTPS API origin so responses never
  inherit an internal, legacy, or attacker-supplied request host.
- Image generation/edit requests remain synchronous by default for OpenAI client
  compatibility. Send `Prefer: respond-async` (or `?async=true`) to receive an
  immediate `202` task object with `poll_url`; poll it until `completed` or
  `failed`. Async requests always use an idempotency key, generated from the
  request ID when the caller does not provide one.
- Synchronous image requests that run longer than 10 seconds flush JSON-valid
  leading whitespace every 10 seconds. The web proxy disables buffering for
  `/v1/`, so CDN and downstream idle timers see progress while the final OpenAI
  JSON response remains parseable without a client-specific streaming mode.
