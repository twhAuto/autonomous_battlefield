# Autonomous Battlefield frontend

React and TypeScript interface for creating factions through the Go API.

## Run locally

Start the backend on `http://localhost:8080`, then in another terminal:

```bash
npm install
npm run dev
```

Open `http://localhost:5173`. Vite proxies `/v1` requests to the backend during
development, so no local CORS configuration is required.

## Commands

```bash
npm run dev       # start the development server
npm run build     # type-check and create a production build
npm test          # run the component tests once
```

For a deployed frontend, set `VITE_API_URL` at build time to the API's public
origin, or route `/v1` to the API through the same reverse proxy as the frontend.

