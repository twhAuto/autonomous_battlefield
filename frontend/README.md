# Autonomous Battlefield frontend

React and TypeScript command interface for faction management and queued battles.

## Run locally

Follow the [project setup](../README.md#run-locally) to configure the database
password and start all processes with `./run-dev.sh` from the project root.

With the API and engine already running, start only the frontend from this directory:

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

See [frontend organization](../docs/design/codebase-guide.md#7-frontend-organization)
for feature ownership, shared transport, polling hooks, and test placement.
