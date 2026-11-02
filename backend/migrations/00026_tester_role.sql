-- Spells the tester role "tester" in the saved model-access policy. Earlier releases
-- stored it as "developer"; the previous release reads both spellings. Bumping the
-- version makes an administrator reload before saving over the rewritten policy.
-- +goose Up
UPDATE model_access_policy
SET policy = jsonb_set(policy, '{profiles}', (
        SELECT jsonb_object_agg(id, CASE WHEN jsonb_typeof(access->'allowedRoles') = 'array' THEN
            jsonb_set(access, '{allowedRoles}', (
                SELECT COALESCE(jsonb_agg(CASE WHEN role = '"developer"' THEN '"tester"'::jsonb ELSE role END ORDER BY position), '[]')
                FROM jsonb_array_elements(access->'allowedRoles') WITH ORDINALITY AS r(role, position)))
            ELSE access END)
        FROM jsonb_each(policy->'profiles') AS p(id, access))),
    version = version + 1
WHERE policy @? '$.profiles.*.allowedRoles[*] ? (@ == "developer")';

-- +goose Down
UPDATE model_access_policy
SET policy = jsonb_set(policy, '{profiles}', (
        SELECT jsonb_object_agg(id, CASE WHEN jsonb_typeof(access->'allowedRoles') = 'array' THEN
            jsonb_set(access, '{allowedRoles}', (
                SELECT COALESCE(jsonb_agg(CASE WHEN role = '"tester"' THEN '"developer"'::jsonb ELSE role END ORDER BY position), '[]')
                FROM jsonb_array_elements(access->'allowedRoles') WITH ORDINALITY AS r(role, position)))
            ELSE access END)
        FROM jsonb_each(policy->'profiles') AS p(id, access))),
    version = version + 1
WHERE policy @? '$.profiles.*.allowedRoles[*] ? (@ == "tester")';
