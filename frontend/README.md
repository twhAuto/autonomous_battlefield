# Autonomous Battlefield frontend

React and TypeScript command interface for faction management and queued battles.

## Run locally

Follow the [project setup](../README.md#run-locally). Run `./deploy-local.sh`
from the project root and open `http://battlefield.localhost:8888`. The project-root
`.env` supplies development backend settings; frontend assets contain no secrets.
Vite remains available for focused frontend work and tests, but is not a separate
application deployment workflow.

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
npx playwright install chromium  # install the matching browser after npm ci
npm run test:e2e  # desktop and mobile Chromium with deterministic API fixtures
```

`npm run build` also rejects unused local declarations and parameters through
the TypeScript configuration.

The application browser suite in `e2e/` starts its own local Vite server on
`127.0.0.1:4173` and intercepts every `/v1` request. It exercises navigation,
keyboard access, responsive forms, both board sizes, map expansion/selection,
early results, event presets, clipboard failure, AI availability/quota/cooldown,
and deletion cancellation/failure/success. It needs no credentials, live AI,
backend, or shared database. Unexpected API requests fail the fixture assertion.
On a fresh Linux machine, provision browser libraries once with
`npx playwright install-deps chromium` (requires system package permissions).
`npx playwright show-report` opens `playwright-report/`; failed tests retain
screenshots and traces in `test-results/`. Both folders are ignored by Git.

Mobile navigation uses a Menu disclosure; creation forms precede collapsed
previews. The inline map permits page scrolling on mobile; Expand map opens its
interactive modal. Key events filters loaded history by default. Full identifiers
and copy fallbacks live in Details disclosures. Faction deletion is in the edit
page's Danger zone, subject to existing ownership permissions.

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
