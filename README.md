# Autonomous Battlefield

Autonomous Battlefield is a Go backend for an auto-battler and expansion game. The long-term product will let human players and AI agents control factions that gather resources, expand territory, and fight on a continuously running battlefield.

The project is in an early stage. The current backend provides PostgreSQL-backed faction creation and lookup, an HTTP health endpoint, validated configuration, and graceful shutdown. The battlefield simulation, commands, units, combat, authentication, and AI agents described in the [revision guide](REVISION.md) are planned but not implemented yet.

## Current capabilities

- Create a faction with a caller-supplied UUID and non-empty name.
- Retrieve a faction's name by ID.
- Store factions in PostgreSQL through `pgx`.
- Configure HTTP and database connection settings from YAML and `DATABASE_URL`.
- Apply pending schema changes through a migration-only server mode.
- Shut down the HTTP server gracefully on `SIGINT` or `SIGTERM`.
- Exercise the API, service, repository, configuration, and JSON helpers with unit tests.

## Requirements

- Go 1.26.5 or a compatible newer version.
- Docker with Docker Compose for the local PostgreSQL instance.

## Run locally

Run these commands from the project root:

```bash
cd backend
docker compose up -d postgres

export DATABASE_URL='postgres://battlefield:battlefield@localhost:5432/battlefield?sslmode=disable'

go run ./cmd/server -migrate

go run ./cmd/server
```

The server listens on `http://localhost:8080` by default. Verify it in another terminal:

```bash
curl http://localhost:8080/healthz
```

The response is `ok` with HTTP status `200`.

The credentials in `compose.yaml` and `.env.example` are for local development only. Use a secret manager to provide `DATABASE_URL` in deployed environments. The application only runs schema migrations when explicitly started with `-migrate`.

### Use a different configuration file

The server loads `config.yaml` from its working directory by default. Pass another path with `-config`:

```bash
go run ./cmd/server -config /path/to/config.yaml
```

Unknown YAML fields, invalid durations, invalid connection-pool values, and a missing database URL cause startup to fail with an error.

### Apply database migrations

Apply all pending migrations and exit without starting the HTTP server:

```bash
go run ./cmd/server -migrate
```

Migration files are embedded in the executable, so a deployed binary does not need a separate `migrations` directory or Goose CLI. The command is safe to repeat; if the database is current, it reports that zero migrations were applied. Use `-config` alongside `-migrate` when needed.

## API

### Health check

```http
GET /healthz
```

Returns `200 OK` with a plain-text `ok` response. This confirms that the HTTP process is running; it does not currently check database readiness.

### Create a faction

```http
POST /v1/factions
Content-Type: application/json

{
  "id": "4db8d152-1e04-4d10-8824-859c6ed72878",
  "name": "Northern Alliance"
}
```

Example:

```bash
curl -i \
  -H 'Content-Type: application/json' \
  -d '{"id":"4db8d152-1e04-4d10-8824-859c6ed72878","name":"Northern Alliance"}' \
  http://localhost:8080/v1/factions
```

Responses:

| Status | Meaning |
| --- | --- |
| `201 Created` | The faction was created. The response has no body. |
| `400 Bad Request` | The JSON is invalid, the ID is not UUID-shaped, or the trimmed name is empty. |
| `409 Conflict` | A faction with the same ID already exists. |
| `500 Internal Server Error` | The faction could not be persisted. |

Request bodies are limited to 64 KiB and must contain exactly one JSON value. Names are trimmed before storage.

### Get a faction name

```http
GET /v1/factions/{id}/name
```

Example:

```bash
curl http://localhost:8080/v1/factions/4db8d152-1e04-4d10-8824-859c6ed72878/name
```

A successful request returns:

```json
{"name":"Northern Alliance"}
```

The endpoint returns `404 Not Found` when the faction does not exist and `500 Internal Server Error` for other storage failures.

## Configuration

Non-secret settings live in `backend/config.yaml`:

| Setting | Default | Purpose |
| --- | --- | --- |
| `http.address` | `:8080` | HTTP listen address. |
| `http.readHeaderTimeout` | `5s` | Maximum time to read request headers. |
| `http.readTimeout` | `10s` | Maximum time to read a request. |
| `http.writeTimeout` | `10s` | Maximum time to write a response. |
| `http.idleTimeout` | `60s` | Keep-alive idle timeout. |
| `http.shutdownTimeout` | `10s` | Graceful-shutdown deadline. |
| `database.urlEnvironmentVariable` | `DATABASE_URL` | Environment variable containing the PostgreSQL URL. |
| `database.connectTimeout` | `5s` | Initial database connection deadline. |
| `database.maxConnections` | `10` | Maximum pool size. |
| `database.minConnections` | `1` | Minimum pool size. |
| `database.maxConnectionLifetime` | `30m` | Maximum lifetime of a pooled connection. |

## Development

Run all tests from `backend`:

```bash
go test ./...
```

Apply migrations explicitly:

```bash
go run ./cmd/server -migrate
```

Rollback remains an intentional operator action available through the standalone [Goose CLI](https://github.com/pressly/goose).

Stop the local database without deleting its volume:

```bash
docker compose stop postgres
```

## Project structure

```text
backend/
├── cmd/server/            Application entry point and shutdown lifecycle
├── internal/api/          HTTP router, handlers, and JSON helpers
├── internal/app/          Dependency composition
├── internal/config/       YAML and environment configuration
├── internal/database/     PostgreSQL pool construction
├── internal/repository/   PostgreSQL persistence
├── internal/service/      Business validation and use cases
├── migrations/            Embedded Goose SQL migrations
├── compose.yaml           Local PostgreSQL service
└── config.yaml            Default non-secret configuration
```

The request path is currently:

```text
HTTP handler -> faction service -> faction repository -> PostgreSQL
```

## Project direction

The intended architecture uses an authoritative, deterministic engine: clients propose typed commands and the engine validates and applies them. This keeps game rules out of HTTP handlers and prevents clients or AI agents from writing arbitrary world state.

See [REVISION.md](REVISION.md) for the proposed architecture, design exercises, AI-agent boundary, infrastructure evolution, and milestone roadmap. The separate [Interview Revision Hub](../../revision/README.md) contains the broader Go, backend/cloud, system-design, AI, and behavioral study material.
