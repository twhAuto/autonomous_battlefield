# Redis completed battle cache

Redis is optional and is not provisioned by the default Kind deployment. To use an
existing Redis service reachable from the API pods, set `BATTLEFIELD_REDIS_URL`
in the project-root `.env` and run `./scripts/cd/deploy-local.sh`. A host loopback URL does
not refer to the workstation from inside a pod. Remove the setting and redeploy
to disable caching. The cache is disposable.

The engine
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

For processes running outside Kind, first start the cache profile described in
[standalone development services](../developer/local-setup.md#standalone-development-services).
The commands below inspect that standalone stack. For a cluster-backed API, use
your configured in-cluster Redis client instead.

1. Complete a battle through the UI and open its results. The first completed
   `GET /v1/battles/{id}` stores the final snapshot; subsequent reads use Redis.
2. Find cache entries and inspect a particular entry's remaining lifetime:

   ```bash
   docker compose --env-file infra/local/.env -f infra/local/compose.yaml exec redis redis-cli --scan --pattern 'battlefield:completed-battle:v1:*'
   docker compose --env-file infra/local/.env -f infra/local/compose.yaml exec redis redis-cli TTL 'battlefield:completed-battle:v1:<battle-uuid>'
   ```

   Replace `<battle-uuid>` with the completed battle ID. Running, queued, failed,
   and stopped battles do not populate the cache.
3. Stop the standalone cache with `docker compose --env-file infra/local/.env -f infra/local/compose.yaml stop redis` and reload the results: PostgreSQL should
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

Set `BATTLEFIELD_REDIS_URL` in the environment's application `.env`, then deploy.
The deployer creates the API-only cache Secret from that field.
Do not commit a credential-bearing Redis URL. To customize TTL/timeout in a cluster,
add the `resultCache` block to its `battlefield-config` YAML and roll out the API.
The same defaults apply when this block is omitted.

The snapshot includes full game state; Redis must stay private to trusted backend
infrastructure. Faction views still pass through the existing projection logic.
Clear the cache after database restoration or any manual modification/deletion of
completed battles. Application commands currently cannot modify completed results.
