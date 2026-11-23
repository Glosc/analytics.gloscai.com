# Project guide

Aptabase is a privacy-first analytics platform for mobile, desktop, and web apps.
A Go executable serves the React/TypeScript dashboard and SDK HTTP APIs.
The backend module and its tests live in `backend/`. The Vite application lives in
`src/`, with its dashboard components in `src/webapp/`.

## Development

Start PostgreSQL, ClickHouse and Mailcatcher with `docker compose -f docker-compose.dev.yml up -d`.
Install frontend dependencies with `npm ci --prefix src`, then run `make dev`. Vite listens on
port 3000 and proxies API, webhook and upload requests to the local HTTPS Go server on port 5251.
Run `go test ./...` from `backend/` for Go tests. The HTTP compatibility suite has its own
instructions in `backend/compat/README.md` and requires a disposable service for write checks.

## Frontend

- React 18 with TypeScript
- Vite for bundling and dev server
- TailwindCSS for styling
- Jotai for state management
- React Router for routing
- Source code in `src/webapp/`

### Frontend Structure (`src/webapp/`)

- `features/` - Feature-based React components (analytics, apps, auth, billing, etc.)
- `components/` - Shared UI components
- `atoms/` - Jotai state atoms
- `hooks/` - Custom React hooks
- `fns/` - Utility functions
- `layout/` - Layout components
- `router.tsx` - React Router configuration
- `main.tsx` - Application entry point

**Path Aliases (configured in vite.config.ts):**
- `@components` → `webapp/components`
- `@features` → `webapp/features`
- `@hooks` → `webapp/hooks`
- `@fns` → `webapp/fns`

## Backend changes

`main.go` starts the databases, HTTP listener and cancellable background workers. Register
HTTP routes with the feature's `register…` method. Keep JSON fields, status codes, cookie
behavior and SDK headers compatible with the dashboard and external SDKs.

PostgreSQL stores users, credentials, apps, shares, invitations, flags, usage metadata and
blobs. Add a numbered SQL file under `backend/migrations/` for schema changes. At startup
the runner locks and updates the same `migration_history` table used by earlier versions.
ClickHouse SQL and Tinybird pipes live under `etc/`. Update both analytics backends when
changing the query contract. The city database under `etc/geoip/` powers offline geolocation.

## Runtime configuration

Use `BASE_URL`, `DATABASE_URL`, and `AUTH_SECRET` for the application URL, PostgreSQL
connection string, and session secret. Set `CLICKHOUSE_URL` for self-hosted analytics or
`TINYBIRD_BASE_URL` and `TINYBIRD_TOKEN` for managed analytics. `.env.example` lists the
optional SMTP, GitHub, and Google OAuth settings for self-hosting.

The SDK writes to separate event and error buffers. Workers flush them to the configured
analytics service and drain them on shutdown. Background jobs also purge old privacy salts
and, in managed regions, process billing alerts, trial expiration, and monthly quota resets.
The default per-IP limits are four registrations per hour, ten sign-ins per five minutes,
1,000 stats requests per hour, twenty event submissions per second, and ten SDK flag
requests per second. `SIGNUP_RATE_LIMIT` changes the registration limit.

The production Compose stack and required environment variables are documented in the
self-hosting section of `README.md`. The release workflow builds the Go server, Vite assets
and data files into the runtime image for both Linux architectures.
