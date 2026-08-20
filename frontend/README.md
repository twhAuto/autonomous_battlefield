# Autonomous Battlefield frontend

React and TypeScript command interface for faction management and queued battles.

## Run locally

Start PostgreSQL, apply migrations, and run both backend processes:

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
npm install
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
```

For a deployed frontend, set `VITE_API_URL` at build time to the API's public
origin, or route `/v1` to the API through the same reverse proxy as the frontend.
