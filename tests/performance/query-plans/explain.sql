-- EXPLAIN script for run-query-plans.sh. The PREPARE bodies mirror ListBattles and
-- ListBattleEvents in backend/internal/battle/repo; keep them in sync.
\pset pager off
SELECT created_at AS mid_at, id AS mid_id FROM battle_runs ORDER BY created_at DESC, id DESC OFFSET 5000 LIMIT 1 \gset
SELECT id AS busy_id FROM battle_runs ORDER BY created_at ASC LIMIT 1 \gset
PREPARE history(text, timestamptz, uuid, int) AS
  SELECT id, scenario_id,
   COALESCE((SELECT jsonb_agg(jsonb_build_object('id', f->'id', 'name', f->'name', 'controller', f->'controller') ORDER BY ordinal)
     FROM jsonb_array_elements(scenario_snapshot->'factions') WITH ORDINALITY AS factions(f, ordinal)), '[]'::jsonb),
   status, paused, requested_ticks, current_tick, created_at, started_at, completed_at,
   CASE WHEN result IS NOT NULL AND result <> 'null'::jsonb THEN jsonb_build_object('winnerFactionId', result->'winnerFactionId', 'reason', result->'reason') END
  FROM battle_runs
  WHERE ($1 = '' OR
    ($1 = 'paused' AND paused AND status IN ('queued', 'running')) OR
    ($1 IN ('queued', 'running') AND status = $1 AND NOT paused) OR
    ($1 IN ('completed', 'failed', 'stopped') AND status = $1))
   AND (created_at, id) < (COALESCE($2::timestamptz, 'infinity'), COALESCE($3::uuid, 'ffffffff-ffff-ffff-ffff-ffffffffffff'))
  ORDER BY created_at DESC, id DESC
  LIMIT $4;
PREPARE events_after(uuid, int, bigint, int) AS
  SELECT attempt, sequence, tick, type, payload, created_at FROM battle_events
  WHERE battle_id = $1 AND attempt = $2 AND sequence > $3 ORDER BY sequence ASC LIMIT $4;
PREPARE events_latest(uuid, int, int) AS
  SELECT attempt, sequence, tick, type, payload, created_at FROM (
    SELECT attempt, sequence, tick, type, payload, created_at FROM battle_events
    WHERE battle_id = $1 AND attempt = $2 ORDER BY sequence DESC LIMIT $3) AS latest_events ORDER BY sequence ASC;
\echo '=== H1 history, all statuses, first page'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE history('', NULL, NULL, 26);
\echo '=== H2 history, all statuses, page at offset 5000 via cursor'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE history('', :'mid_at', :'mid_id', 26);
\echo '=== H3 history, completed (70%), first page'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE history('completed', NULL, NULL, 26);
\echo '=== H4 history, queued (5%), first page'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE history('queued', NULL, NULL, 26);
\echo '=== H5 history, paused (~1.4%), first page'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE history('paused', NULL, NULL, 26);
\echo '=== H6 history, paused, cursor at offset 5000'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE history('paused', :'mid_at', :'mid_id', 26);
\echo '=== E1 events after sequence 10000 on the 20k-event battle'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE events_after(:'busy_id', 1, 10000, 200);
\echo '=== E2 latest 200 events on the 20k-event battle'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE events_latest(:'busy_id', 1, 200);
\echo '=== G1 generic plan, all statuses, first page'
SET plan_cache_mode = force_generic_plan;
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE history('', NULL, NULL, 26);
\echo '=== G2 generic plan, all statuses, cursor at offset 5000'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE history('', :'mid_at', :'mid_id', 26);
\echo '=== G3 generic plan, queued, first page'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE history('queued', NULL, NULL, 26);
\echo '=== G4 generic plan, paused, cursor at offset 5000'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF, SUMMARY ON) EXECUTE history('paused', :'mid_at', :'mid_id', 26);
RESET plan_cache_mode;
\echo '=== Index usage'
SELECT indexrelname, idx_scan FROM pg_stat_user_indexes WHERE relname IN ('battle_runs','battle_events') ORDER BY 1;
