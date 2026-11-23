## Setting up a development environment

Install Go 1.26.5 or newer, Node.js 22, and Docker with the Compose plugin. From the
repository root, run:

```sh
docker compose -f docker-compose.dev.yml up -d
npm ci --prefix src
make dev
```

Open `http://localhost:3000`. Vite proxies requests to `https://localhost:5251`, where
`go run . -dev` supplies a temporary certificate for local development. PostgreSQL uses
port `5433`, ClickHouse uses `8123`, and Mailcatcher's web interface uses `1080`.
Set `DEV_POSTGRES_PORT` in the shell before starting both Compose and `make dev` to use
another PostgreSQL port. Use `docker compose -f docker-compose.dev.yml down` to stop
the development services.

Run backend tests with `go test ./...` from `backend/`. See `backend/compat/README.md` for
HTTP checks against a disposable stack. Deployment uses the [production Compose file](./README.md#-self-hosted).

## Changing analytics queries or schema

Aptabase has two analytics backends and both must be kept in sync:

- `etc/clickhouse/` is used by self-hosted installs, local development and integration tests. Queries are
  `queries/<name>.liquid` files and schema changes are numbered `.sql` migrations that run when the
  app starts. This is what ships inside the Docker image.
- `etc/tinybird/` is a Tinybird Forward project used only by the managed cloud regions (EU and US).
  This fork does not deploy it to Tinybird Cloud or include it in the Docker image. See
  `etc/tinybird/README.md` for local validation.

When you add or change an endpoint, update both the `.pipe` and the matching `.liquid` file.
Verify locally that each Tinybird endpoint has a ClickHouse query with the same name.

## Getting some work done

We have a very light process for contributing to Aptabase, simply find an issue you want to work on, ask for it to be assigned to you, and submit a PR when you're done. We'll review it and merge it as soon as possible. If you can't find an issue you want to work on, feel free to open one and we'll discuss it with you.

We recommend you to give a brief description of how you plan to solve the issue before starting to work on it. This will help us to avoid wasting time on PRs that don't get merged.

🚀
