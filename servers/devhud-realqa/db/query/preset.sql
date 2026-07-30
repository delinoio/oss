-- name: CountPresetsForOwner :one
SELECT count(*)::bigint
FROM realqa_presets
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id);

-- name: LockPresetOwner :exec
SELECT pg_advisory_xact_lock(
    hashtextextended(
        sqlc.arg(owner_kind)::text || ':' || sqlc.arg(owner_id)::uuid::text,
        757
    )
);

-- name: UpsertDestination :one
INSERT INTO realqa_destinations (
    id, owner_kind, owner_id, installation_id,
    repository_id, repository_owner, repository_name
) VALUES (
    sqlc.arg(id), sqlc.arg(owner_kind), sqlc.arg(owner_id),
    sqlc.arg(installation_id), sqlc.arg(repository_id),
    sqlc.arg(repository_owner), sqlc.arg(repository_name)
)
ON CONFLICT (owner_kind, owner_id, installation_id, repository_id)
DO UPDATE SET repository_owner = EXCLUDED.repository_owner,
              repository_name = EXCLUDED.repository_name
RETURNING *;

-- name: CreatePreset :one
INSERT INTO realqa_presets (
    id, owner_kind, owner_id, created_by_account_id,
    payer_organization_id, payer_team_id, destination_id,
    name, capture_mode, include_pointer, selector_mode,
    issue_definition_kind, issue_definition_id, issue_definition_name,
    issue_definition_path, issue_definition_etag,
    default_labels, default_assignees, milestone_number, project_node_ids
) VALUES (
    sqlc.arg(id), sqlc.arg(owner_kind), sqlc.arg(owner_id),
    sqlc.arg(created_by_account_id), sqlc.arg(payer_organization_id),
    sqlc.arg(payer_team_id), sqlc.arg(destination_id), sqlc.arg(name),
    sqlc.arg(capture_mode), sqlc.arg(include_pointer), sqlc.arg(selector_mode),
    sqlc.arg(issue_definition_kind), sqlc.arg(issue_definition_id),
    sqlc.arg(issue_definition_name), sqlc.arg(issue_definition_path),
    sqlc.arg(issue_definition_etag), sqlc.arg(default_labels),
    sqlc.arg(default_assignees), sqlc.narg(milestone_number),
    sqlc.arg(project_node_ids)
)
RETURNING *;

-- name: CreateProcessURLRule :exec
INSERT INTO realqa_process_url_rules (
    id, preset_id, ordinal, exact_process_name,
    safe_window_title_pattern, url_template, enabled
) VALUES (
    sqlc.arg(id), sqlc.arg(preset_id), sqlc.arg(ordinal),
    sqlc.arg(exact_process_name), sqlc.arg(safe_window_title_pattern),
    sqlc.arg(url_template), sqlc.arg(enabled)
);

-- name: CreateShortcut :exec
INSERT INTO realqa_shortcuts (id, preset_id, accelerator, active)
VALUES (
    sqlc.arg(id), sqlc.arg(preset_id),
    sqlc.arg(accelerator), sqlc.arg(active)
);

-- name: GetPresetRecord :one
SELECT
    preset.*,
    destination.installation_id,
    destination.repository_id,
    destination.repository_owner,
    destination.repository_name,
    shortcut.id AS shortcut_id,
    shortcut.accelerator,
    shortcut.active AS shortcut_active
FROM realqa_presets AS preset
JOIN realqa_destinations AS destination ON destination.id = preset.destination_id
LEFT JOIN realqa_shortcuts AS shortcut ON shortcut.preset_id = preset.id
WHERE preset.id = sqlc.arg(id);

-- name: GetDestinationRecord :one
SELECT *
FROM realqa_destinations
WHERE id = sqlc.arg(id);

-- name: ListPresetRecords :many
SELECT
    preset.*,
    destination.installation_id,
    destination.repository_id,
    destination.repository_owner,
    destination.repository_name,
    shortcut.id AS shortcut_id,
    shortcut.accelerator,
    shortcut.active AS shortcut_active
FROM realqa_presets AS preset
JOIN realqa_destinations AS destination ON destination.id = preset.destination_id
LEFT JOIN realqa_shortcuts AS shortcut ON shortcut.preset_id = preset.id
WHERE preset.owner_kind = sqlc.arg(owner_kind)
  AND preset.owner_id = sqlc.arg(owner_id)
  AND preset.id > sqlc.arg(after_id)
ORDER BY preset.id
LIMIT sqlc.arg(page_limit);

-- name: ListProcessURLRules :many
SELECT *
FROM realqa_process_url_rules
WHERE preset_id = sqlc.arg(preset_id)
ORDER BY ordinal;

-- name: LockPreset :one
SELECT *
FROM realqa_presets
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: UpdatePreset :one
UPDATE realqa_presets
SET payer_organization_id = sqlc.arg(payer_organization_id),
    payer_team_id = sqlc.arg(payer_team_id),
    destination_id = sqlc.arg(destination_id),
    name = sqlc.arg(name),
    capture_mode = sqlc.arg(capture_mode),
    include_pointer = sqlc.arg(include_pointer),
    selector_mode = sqlc.arg(selector_mode),
    issue_definition_kind = sqlc.arg(issue_definition_kind),
    issue_definition_id = sqlc.arg(issue_definition_id),
    issue_definition_name = sqlc.arg(issue_definition_name),
    issue_definition_path = sqlc.arg(issue_definition_path),
    issue_definition_etag = sqlc.arg(issue_definition_etag),
    default_labels = sqlc.arg(default_labels),
    default_assignees = sqlc.arg(default_assignees),
    milestone_number = sqlc.narg(milestone_number),
    project_node_ids = sqlc.arg(project_node_ids),
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(id)
  AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: DeleteProcessURLRules :exec
DELETE FROM realqa_process_url_rules WHERE preset_id = sqlc.arg(preset_id);

-- name: UpsertShortcut :exec
INSERT INTO realqa_shortcuts (id, preset_id, accelerator, active)
VALUES (sqlc.arg(id), sqlc.arg(preset_id), sqlc.arg(accelerator), sqlc.arg(active))
ON CONFLICT (preset_id)
DO UPDATE SET id = EXCLUDED.id,
              accelerator = EXCLUDED.accelerator,
              active = EXCLUDED.active;

-- name: DeleteShortcut :exec
DELETE FROM realqa_shortcuts WHERE preset_id = sqlc.arg(preset_id);

-- name: DeletePresetAtRevision :one
DELETE FROM realqa_presets
WHERE id = sqlc.arg(id)
  AND revision = sqlc.arg(expected_revision)
RETURNING (revision + 1)::bigint AS deleted_revision;

-- name: GetIdempotencyRecord :one
SELECT *
FROM realqa_idempotency_records
WHERE caller_kind = sqlc.arg(caller_kind)
  AND caller_digest = sqlc.arg(caller_digest)
  AND operation = sqlc.arg(operation)
  AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CreateIdempotencyRecord :one
INSERT INTO realqa_idempotency_records (
    id, caller_kind, caller_digest, operation,
    idempotency_key, request_digest, resource_id, response_payload
) VALUES (
    sqlc.arg(id), sqlc.arg(caller_kind), sqlc.arg(caller_digest),
    sqlc.arg(operation), sqlc.arg(idempotency_key),
    sqlc.arg(request_digest), sqlc.arg(resource_id), sqlc.narg(response_payload)
)
RETURNING *;
