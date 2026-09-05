# Redis completed battle cache

From the project root, after configuring `backend/.env` as described in the README:

```bash
docker compose -f backend/compose.yaml --profile cache up -d redis
BATTLEFIELD_REDIS_URL=redis://127.0.0.1:6379/0 ./run-dev.sh
```

Compose exposes Redis only on loopback, caps its data memory at 128 MB, uses
`allkeys-lru` eviction, and disables persistence. The cache is disposable. The
launcher leaves Redis running, just as it leaves PostgreSQL running. Stop it with:

```bash
docker compose -f backend/compose.yaml stop redis
```

Unset `BATTLEFIELD_REDIS_URL` and restart the API to disable caching. The engine
and MCP service do not use this variable. The default configuration is:

```yaml
resultCache:
  urlEnvironmentVariable: BATTLEFIELD_REDIS_URL
  ttl: 1h
  timeout: 100ms
```

TTL and timeout must be positive durations. Each cache read/write gets its own
100 ms budget; an unavailable cache can therefore add up to roughly two such
operation budgets to a successful completed-battle database read. No cache error
changes the successful HTTP result. Redis is not included in API readiness checks.

## Verify locally

1. Complete a battle through the UI and open its results. The first completed
   `GET /v1/battles/{id}` stores the final snapshot; subsequent reads use Redis.
2. Find cache entries and inspect a particular entry's remaining lifetime:

   ```bash
   docker compose -f backend/compose.yaml exec redis redis-cli --scan --pattern 'battlefield:completed-battle:v1:*'
   docker compose -f backend/compose.yaml exec redis redis-cli TTL 'battlefield:completed-battle:v1:<battle-uuid>'
   ```

   Replace `<battle-uuid>` with the completed battle ID. Running, queued, failed,
   and stopped battles do not populate the cache.
3. Stop Redis with the command above and reload the results: PostgreSQL should
   still serve them. Restart Redis and reload to repopulate the cache.

The backend suite tests cache hits without database reads, expiry, corrupt entries,
Redis read/write failures, and unchanged HTTP authentication. No browser change or
new result endpoint is required. Simultaneous cold reads may query PostgreSQL more
than once; request coalescing and cache-specific metrics are not included yet.

## Kubernetes

The API Deployment optionally reads `BATTLEFIELD_REDIS_URL` from the
`battlefield-cache` Secret. Existing clusters continue without Redis when the Secret
is absent. This change does not provision a Kubernetes Redis server; supply an
instance reachable by API pods. Use a dedicated cache database per environment,
authenticated access, and `rediss://` when the connection requires TLS.

Create the Secret through your environment's secret-management process, using the
key `BATTLEFIELD_REDIS_URL`, then roll out the API so it reads the new environment.
Do not commit a credential-bearing Redis URL. To customize TTL/timeout in a cluster,
add the `resultCache` block to its `battlefield-config` YAML and roll out the API.
The same defaults apply when this block is omitted.

The snapshot includes full game state; Redis must stay private to trusted backend
infrastructure. Faction views still pass through the existing projection logic.
Clear the cache after database restoration or any manual modification/deletion of
completed battles. Application commands currently cannot modify completed results.
