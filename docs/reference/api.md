# HTTP API reference

Run the application using the [local setup](../../README.md#run-locally).
Paths below are relative to the API origin (`http://localhost:8080` locally).
Examples assume authentication is disabled; when enabled, log in and send the
returned session cookie with protected requests.


When authentication is enabled, all `/v1` routes except login and session
discovery require the signed session cookie. Lifecycle and metrics endpoints
remain public for infrastructure probes.

## Administrator session

```http
POST /v1/auth/login
Content-Type: application/json

{"username":"admin","password":"..."}
```

Successful login returns `204 No Content` and an HttpOnly, SameSite=Strict
session cookie. `GET /v1/auth/session` reports the current actor and
`POST /v1/auth/logout` expires the cookie. Invalid or expired sessions receive
`401 Unauthorized`.

## Startup check

```http
GET /startupz
```

Returns `503 Service Unavailable` while dependencies and application state are
initializing, then `200 OK`. Kubernetes uses this for its startup probe.

## Liveness check

```http
GET /livez
```

Returns `200 OK` with a plain-text `ok` response. This confirms that the HTTP
process is running and is suitable for a liveness probe. `/healthz` remains as
a compatibility alias.

## Readiness check

```http
GET /readyz
```

Returns `200 OK` with a plain-text `ok` response when PostgreSQL responds within
one second. It returns `503 Service Unavailable` when the database is not
reachable and is suitable for a readiness probe.

## Create a faction

```http
POST /v1/factions
Content-Type: application/json

{
  "id": "4db8d152-1e04-4d10-8824-859c6ed72878",
  "name": "Northern Alliance",
  "doctrine": "Protect income, scout before committing, and adapt purchases to visible threats.",
  "background": "Descendants of the northern watch, sworn to shelter displaced families."
}
```

Responses:

| Status | Meaning |
| --- | --- |
| `201 Created` | The faction was created. The response has no body. |
| `400 Bad Request` | The JSON is invalid, the ID is not UUID-shaped, or the trimmed name/doctrine is empty, or doctrine exceeds 4,000 Unicode characters, or background exceeds 1,000. |
| `409 Conflict` | A faction with the same ID already exists. |
| `500 Internal Server Error` | The faction could not be persisted. |

Request bodies are limited to 64 KiB and must contain exactly one JSON value. Names, doctrines, and backgrounds are trimmed before storage. Background is optional and limited to 1,000 Unicode characters. Obsolete blueprint/ruleset request fields are rejected.

`GET /v1/faction-rulesets` returns catalog starting gold, unit statistics, upgrades, and costs; faction creation does not require it.

## Update a faction

`PUT /v1/factions/{id}` accepts `{ "name": "Northern Alliance", "doctrine": "Protect income." }`.
Returns `204` on success, `400` for invalid fields or ID, `404` for missing profiles,
and `500` for persistence failures. Changes apply only to future battles.

## List factions

`GET /v1/factions` returns `{ "factions": [{ "id": "…", "name": "…", "doctrine": "…" }] }` in stable creation order.

## Delete a faction

```http
DELETE /v1/factions/{id}
```

A successful deletion returns `204 No Content`; malformed IDs return `400` and
missing factions return `404`. Deletion removes the faction from the active
registry. Existing battle history remains readable because each battle stores
an immutable scenario and participant snapshot.

## Get a faction name

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

## Queue a battle

Both factions must already exist. The scenario ID refers to a strict YAML template in `backend/scenarios`; the API snapshots faction names/backgrounds/doctrines, the map, ruleset, and controller plans. New snapshots use `snapshotVersion: 2` and start with castles and gold. Older queued snapshots retain legacy initialization.

The frontend discovers available templates through `GET /v1/scenarios`, which returns scenario IDs, rules versions, scenario-specific maximum ticks, tick pacing, and decision timing. `castle-siege-v1` allows at most 500 ticks.

```http
POST /v1/battles
Content-Type: application/json

{
  "id": "7f22e693-bacd-46bb-b942-64fa613cd388",
  "scenarioId": "castle-siege-v1",
  "participants": [
    {
      "factionId": "4db8d152-1e04-4d10-8824-859c6ed72878",
      "controller": {"type": "model", "profileId": "gemini-api-balanced"}
    },
    {
      "factionId": "41ac3a2f-3371-4de1-b0f6-70600fcd9052",
      "controller": {"type": "scripted"}
    }
  ],
  "tickCount": 100
}
```

A valid request returns `202 Accepted`, a queued status, and `Location: /v1/battles/{id}`. Invalid input returns `400`, a missing scenario or faction returns `404`, and a duplicate battle ID returns `409`.

`GET /v1/model-profiles` returns the operator allowlist with safe availability,
provider, runtime, scope, model-label, and tool-capability fields. It never
returns credential values or credential environment-variable names.

At ticks 10, 20, 30, and so on, the engine freezes the prior state, creates one
fog-of-war-bounded decision per faction, waits up to two minutes for model plans,
then advances the deterministic simulation with the resolved plans. See
[strategy windows and production](../design/system-architecture.md#explicit-recruitment-orders).
Late plans cannot overwrite the fallback selected by the engine. Completed
decision metadata is available from
`GET /v1/battles/{id}/decisions?attempt={attempt}`.

For persisted battle comparisons, query `battle_controller_evaluation`. It groups
scripted and model profiles by controller/provider/runtime and reports win,
invalid-plan, fallback, latency, normalized usage, and plan-diversity metrics.

## Browse battle history

```http
GET /v1/battles?status=completed&limit=25
```

This service-wide list uses the same administrator session as the other battle
routes. Omit `status` for all battles, or choose `queued`, `running`, `paused`,
`completed`, `failed`, or `stopped`. Paused means an active queued/running
battle with its pause flag set; queued and running filters exclude paused runs.

`limit` defaults to 25 and accepts integers from 1 through 100. The response is
`{ "battles": [...], "nextCursor": null }`; when more rows exist, `nextCursor`
is an opaque string to pass as `cursor` with the same filter. Rows are ordered
by creation time descending, then ID descending. Newer insertions do not shift
subsequent pages; restart without a cursor to see them. Status changes can alter
membership in a filtered list between requests.

Each summary contains `id`, `scenarioId`, `participants` (snapshot IDs, names,
and controllers), `status`, `paused`, `requestedTicks`, `currentTick`,
`createdAt`, nullable `startedAt` and `completedAt`, and nullable `outcome`.
Timestamps are RFC 3339 strings. An outcome contains `winnerFactionId`
(nullable for a draw) and `reason`. Only a completed battle with an outcome
and no winner represents a draw. Summaries omit maps, simulation states, events,
and detailed result statistics; obtain these through the battle detail routes.
Faction edits or deletion do not change saved participant identities.

Invalid limits, statuses, or cursors return `400`; persistence failures return
`500`. An empty list returns `200` with `battles: []` and `nextCursor: null`.

## Get battle status and results

```http
GET /v1/battles/{id}
```

The response reports `queued`, `running`, `completed`, `failed`, or `stopped`, along with the authored map and current units, buildings, treasuries, tick, and attempt. Completed battles include castle health plus unit, economy, loot, damage, and posture statistics.

Pause or resume an active battle with `POST /v1/battles/{id}/pause` or
`POST /v1/battles/{id}/resume`. Pausing preserves the current attempt; status
responses expose the pause flag.

Stop a queued or running battle with `POST /v1/battles/{id}/stop`. The operation
releases the engine lease and marks unresolved model decisions as failed so a
host subscription agent cannot continue the stopped run.

`GET /v1/battles/{battleId}/factions/{factionId}/view` returns the same terrain with faction fog-of-war applied. Owned entities and both castles are always present; other hostile entities appear only inside current friendly vision.

## Page through battle events

```http
GET /v1/battles/{id}/events?attempt=1&after=0&limit=100
```

`attempt` defaults to the battle's current attempt, `after` is the last event sequence already observed, and `limit` defaults to 100 with a maximum of 500. Events are ordered by sequence and the response includes `nextCursor`.


## Faction background generation

Faction profiles accept an optional `background` on POST/PUT and return it on
listing when nonempty. Background is trimmed and limited to 1,000 Unicode
characters (not UTF-8 bytes); missing background means empty. New battle snapshots
freeze it alongside name and doctrine. Historical snapshots are not rewritten.

These endpoints require the normal authenticated session. With authentication
disabled, all clients share the `local-development` actor and its allowance.

| Route | Contract |
| --- | --- |
| `GET /v1/faction-background-generations/options` | `available`, `modelLabel`, optional `reason`, `remaining`, `resetAt`, `retryAt`, optional `activeJobId` |
| `POST /v1/faction-background-generations` | Accepts `{ "name": "Northwind", "doctrine": "Protect income.", "background": "Optional inspiration", "idempotencyKey": "client-generated-unique-key" }`; returns `202` and a job |
| `GET /v1/faction-background-generations/{id}` | Returns only the requesting actor's job; another actor's ID returns `404` |

Jobs contain `id`, `input`, `profileId`, `modelLabel`, `promptVersion`, `status`,
`background`, timestamps, execution timeout, and an optional `errorCategory`.
States are `queued`, `running`, `succeeded`, `failed`, and `expired`. A succeeded
job is an editable draft, not a saved faction. The client must explicitly use the
draft and save the faction through the normal profile endpoint.

Admission allows 10 requests per actor per UTC day, at least 30 seconds apart,
and one queued/running job. A repeated key with the same normalized input returns
the original job without another charge; different input returns `409`.
Validation failures return `400`, unavailable/disabled profiles return `503`,
and neither reserves quota. Limits return `429`, `Retry-After`, and the options
object containing retry/reset information. Options and job reads use `no-store`.

Queued jobs expire after five minutes and refund their reservation on the
original admission day. Once execution starts, failures and two-minute execution
timeouts count. Expired running jobs are never automatically rerun. Repeated
identical successful submissions are idempotent. Three invalid background
submissions close the job with `invalid_output`.

The internal claim endpoint accepts `jobTypes: ["battle_decision",
"faction_background"]` and executable `profileIds`. Omitted job types mean
battle decisions only. Battle decisions receive priority. Generation capabilities
are separate from battle capabilities; `/generation-mcp` exposes only
`get_background_context` and `submit_faction_background`. Neither tool receives
battle data. Workers report actual configured model, prompt version, usage, and
failure category to `/internal/agent/generation-finish`.
