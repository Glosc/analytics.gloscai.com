## Setting up a development environment

1. Install the [.NET 10 SDK](https://dotnet.microsoft.com/en-us/download/dotnet/10.0)
2. Install [Node.js 22](https://nodejs.org/en/download/)
3. Install [Docker Desktop](https://www.docker.com/products/docker-desktop/) or Docker Engine with the Compose plugin
4. Clone the repo
5. From the repository root, start the development dependencies with `docker compose -f docker-compose.dev.yml up -d`
6. Copy `src/Properties/launchSettings.example.json` to `src/Properties/launchSettings.json`
7. Navigate to `src/` and run `npm install` and `dotnet restore` to install dependencies
8. While on `src/`, run `dotnet watch` to start the server in watch mode
9. In another terminal, also on `src/`, run `npm run dev` to start the client in watch mode
10. Navigate to `https://localhost:3000` to view the app

That's it! You're ready to start writing some code 🚀

**Note:** Emails are sent via Mailcatcher, the UI is available at `http://localhost:1080`

Development PostgreSQL uses host port `5433` by default. To use `5432` instead, run
`DEV_POSTGRES_PORT=5432 docker compose -f docker-compose.dev.yml up -d` and change
`DATABASE_URL` in `launchSettings.json` to port `5432`. Stop development services with
`docker compose -f docker-compose.dev.yml down`. The default `docker-compose.yml` is for
[production deployment](./README.md#-self-hosted), not local development.

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
