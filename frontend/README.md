# Autonomous Battlefield frontend

React and TypeScript command interface for faction management and queued battles.

## Run locally

From the project root, the development launcher starts PostgreSQL, applies
migrations, and runs the API, battle engine, and frontend together:

```bash
./run-dev.sh
```

Open `http://localhost:5173` and press Ctrl+C to stop the application processes.

To run the components separately, start PostgreSQL, apply migrations, and run
both backend processes:

```bash
cd ../backend
docker compose up -d postgres
export DATABASE_URL='postgres://battlefield:battlefield@localhost:5432/battlefield?sslmode=disable'
go run ./cmd/server -migrate
go run ./cmd/server
```

Run `go run ./cmd/engine` in another backend terminal with the same
`DATABASE_URL`. Then start the frontend:

```bash
npm ci
npm run dev
```

Open `http://localhost:5173`. Vite proxies `/v1` requests to the backend during
development, so no local CORS configuration is required.

The **Stage battle** flow loads registered factions and server-defined
scenarios, queues a battle, and navigates to a live status view. The view polls
status with bounded reconnects, follows events by cursor, exposes historical
attempts, and renders the final winner/draw statistics. At least two factions
must exist before a battle can be staged.

## Commands

```bash
npm run dev       # start the development server
npm run build     # type-check and create a production build
npm test          # run the component tests once
npm test -- --coverage  # generate text, JSON-summary, and LCOV coverage
```

## Container and Kubernetes behavior

The production image serves the compiled frontend through unprivileged Nginx
on port 8080. `/startupz`, `/livez`, and `/readyz` report the Nginx process
health; the deployment smoke test separately calls `/v1/scenarios` to prove the
reverse proxy can reach the API.

For a deployed frontend, either set `VITE_API_URL` at build time to the API's
public origin or route `/v1` through the same reverse proxy. The Kubernetes
manifest uses the latter and sets:

```text
API_UPSTREAM=battlefield-api.battlefield.svc.cluster.local:8080
```

The full Service DNS name is required because Nginx's runtime resolver does not
apply the pod's DNS search suffixes in the same way as normal application name
resolution. If an overlay changes the namespace from `battlefield`, it must
also update `API_UPSTREAM`.

## Source layout

`src/app` owns the application shell, routes, and application integration tests.
`src/features/battles`, `src/features/factions`, and `src/features/auth` each own
their pages, typed API clients, and feature tests. The battle map belongs to the
battle feature. `src/shared/api` provides HTTP transport, and
`src/shared/components` contains visuals used across features. `src/pages`
contains general pages; `src/test` contains shared test setup.

Keep unit tests beside the implementation. See the
[codebase guide](../docs/design/codebase-guide.md) for backend and tooling ownership.
