-- Seed data for run-query-plans.sh: 10k battles with a realistic status mix spread
-- over ~90 days, 200 battles x 500 events, and one 20k-event battle.
SET session_replication_role = replica;  -- bypass admission triggers for bulk seeding
INSERT INTO battle_runs (id, scenario_id, scenario_digest, scenario_snapshot, requested_ticks, current_tick, status, paused, attempt, result, created_at, started_at, completed_at, initial_state, current_state, owner_id)
SELECT gen_random_uuid(), 'frontier-siege-v1', md5(g::text),
  jsonb_build_object('id','frontier-siege-v1','decisionIntervalTicks',10,'factions', jsonb_build_array(
    jsonb_build_object('id','f'||(g%40),'name','Faction '||(g%40),'controller',jsonb_build_object('type','model','profileId','p1'),'background',repeat('lore ',150),'doctrine',repeat('hold ',60)),
    jsonb_build_object('id','f'||((g+7)%40),'name','Faction '||((g+7)%40),'controller',jsonb_build_object('type','scripted'),'background',repeat('lore ',150),'doctrine',repeat('push ',60)))),
  200, 200,
  CASE WHEN g % 100 < 70 THEN 'completed' WHEN g % 100 < 80 THEN 'failed' WHEN g % 100 < 90 THEN 'stopped'
       WHEN g % 100 < 95 THEN 'running' ELSE 'queued' END,
  (g % 100 >= 90 AND g % 7 = 0), 1,
  CASE WHEN g % 100 < 70 THEN jsonb_build_object('winnerFactionId','f'||(g%40),'reason','castle_destroyed') END,
  now() - (g || ' minutes')::interval * 13, now() - (g || ' minutes')::interval * 13, now() - (g || ' minutes')::interval * 12,
  jsonb_build_object('tick',0,'units',jsonb_build_array()), jsonb_build_object('tick',200,'blob',repeat('x',2000)),
  'owner-'||(g%25)
FROM generate_series(1,10000) g;
-- 200 battles x 500 events, plus one long battle with 20,000 events
INSERT INTO battle_events (battle_id, attempt, sequence, tick, type, payload)
SELECT b.id, 1, s, s/5, 'unit_moved', jsonb_build_object('unitId','u'||s,'x',s%20,'y',s%15)
FROM (SELECT id FROM battle_runs ORDER BY created_at DESC LIMIT 200) b, generate_series(1,500) s;
INSERT INTO battle_events (battle_id, attempt, sequence, tick, type, payload)
SELECT b.id, 1, s, s/5, 'unit_moved', jsonb_build_object('unitId','u'||s)
FROM (SELECT id FROM battle_runs ORDER BY created_at ASC LIMIT 1) b, generate_series(1,20000) s;
SET session_replication_role = origin;
ANALYZE battle_runs; ANALYZE battle_events;
SELECT status, paused, count(*) FROM battle_runs GROUP BY 1,2 ORDER BY 1,2;
SELECT count(*) AS events FROM battle_events;
SELECT pg_size_pretty(pg_relation_size('battle_events_pkey')) AS events_pkey;
