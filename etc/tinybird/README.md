# Tinybird project

This folder is a [Tinybird Forward](https://www.tinybird.co/docs/forward) project and the
single source of truth for the analytics schema used by the **managed cloud** regions
(EU and US). Self-hosted installs and local development use ClickHouse instead, whose
schema lives in `etc/clickhouse/` and ships inside the Docker image.

Every Tinybird endpoint in `endpoints/` must have a ClickHouse twin with the same name in
`etc/clickhouse/queries/*.liquid`. Keep both files in sync when changing a query.

## Layout

| Folder              | Contents                                                        |
| ------------------- | --------------------------------------------------------------- |
| `datasources/`      | Landing tables (`events`, `error_events`) and MV target tables  |
| `endpoints/`        | API endpoints called by the backend, named `<name>__v<N>.pipe`  |
| `materializations/` | Materialized pipes that populate the aggregation datasources    |

Rules that Forward enforces:

- A node name must differ from every pipe and datasource name. Convention: `NODE endpoint`
  for endpoints, `NODE materialize` for materialized pipes.
- Token grants are declared in the datafiles (`TOKEN "aptabase-api" APPEND` / `READ`) and are
  reconciled on deploy. Tokens that are not declared get removed.
- Deploys are full-state: a resource missing from this folder is removed from the workspace.

## Local validation

With Tinybird Local running, validate schema changes from this directory:

```sh
tb --local build
```

## Manual operations

If you manage your own Tinybird workspace, removing a datasource, materialized view or
connection requires an explicit destructive deployment:

```sh
cd etc/tinybird
tb --cloud --host <host> --token <workspace admin token> deploy --check --allow-destructive-operations
tb --cloud --host <host> --token <workspace admin token> deploy --allow-destructive-operations
```

Schema changes that Forward considers incompatible (for example adding a column to `events`
in a workspace that lacks it) require a temporary `FORWARD_QUERY` in the datasource file.
Deploy with it, then remove it in a follow-up before deploying anywhere else.

## Local usage

```sh
cd etc/tinybird
tb login --host <host> --workspace <your-workspace>   # writes .tinyb (gitignored)
tb --cloud deploy --check
```

Always pass `--host` and `--token` explicitly when targeting production, so the target is
visible in the command. Be aware that the CLI can persist the last token it authenticated
with into the folder's `.tinyb`, replacing the stored login. Run `--token` commands from a
scratch folder if you want to keep a folder's login intact.
