-- name: EnsureOwnerLock :exec
INSERT INTO deck_owner_locks (owner_hash)
VALUES (sqlc.arg(owner_hash))
ON CONFLICT DO NOTHING;

-- name: LockOwner :one
SELECT owner_hash
FROM deck_owner_locks
WHERE owner_hash = sqlc.arg(owner_hash)
FOR UPDATE;

-- name: IsOwnerTombstoned :one
SELECT EXISTS (
    SELECT 1 FROM deck_owner_tombstones
    WHERE target_hash = sqlc.arg(target_hash)
)::boolean;

-- name: CountPersonalViews :one
SELECT count(*)::integer
FROM deck_views
WHERE owner_scope = 1 AND owner_account_id = sqlc.arg(owner_account_id);

-- name: CountOrganizationViews :one
SELECT count(*)::integer
FROM deck_views
WHERE owner_scope = 2 AND owner_organization_id = sqlc.arg(owner_organization_id);

-- name: GetCreateViewIdempotency :one
SELECT subject_hash, idempotency_key, request_digest, owner_hash, view_id,
       response_ciphertext, created_at
FROM deck_view_create_idempotency
WHERE subject_hash = sqlc.arg(subject_hash)
  AND idempotency_key = sqlc.arg(idempotency_key);

-- name: InsertView :one
INSERT INTO deck_views (
    view_id, owner_scope, owner_account_id, owner_organization_id,
    billing_organization_id, billing_team_id, name_ciphertext,
    query_ciphertext, kind, sort, grouping, notification_ciphertext,
    connection_state, repository_authorization_index, revision,
    created_at, updated_at
) VALUES (
    sqlc.arg(view_id), sqlc.arg(owner_scope), sqlc.narg(owner_account_id),
    sqlc.narg(owner_organization_id), sqlc.narg(billing_organization_id),
    sqlc.narg(billing_team_id), sqlc.arg(name_ciphertext),
    sqlc.arg(query_ciphertext), sqlc.arg(kind), sqlc.arg(sort),
    sqlc.arg(grouping), sqlc.arg(notification_ciphertext),
    sqlc.arg(connection_state), sqlc.arg(repository_authorization_index),
    1, sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: InsertCreateViewIdempotency :exec
INSERT INTO deck_view_create_idempotency (
    subject_hash, idempotency_key, request_digest, owner_hash, view_id,
    response_ciphertext
) VALUES (
    sqlc.arg(subject_hash), sqlc.arg(idempotency_key),
    sqlc.arg(request_digest), sqlc.arg(owner_hash), sqlc.arg(view_id),
    sqlc.arg(response_ciphertext)
);

-- name: GetView :one
SELECT * FROM deck_views WHERE view_id = sqlc.arg(view_id);

-- name: ListPersonalViews :many
SELECT * FROM deck_views
WHERE owner_scope = 1
  AND owner_account_id = sqlc.arg(owner_account_id)
  AND view_id > sqlc.arg(after_view_id)
ORDER BY view_id
LIMIT sqlc.arg(page_limit);

-- name: ListOrganizationViews :many
SELECT * FROM deck_views
WHERE owner_scope = 2
  AND owner_organization_id = sqlc.arg(owner_organization_id)
  AND view_id > sqlc.arg(after_view_id)
ORDER BY view_id
LIMIT sqlc.arg(page_limit);

-- name: UpdateView :one
UPDATE deck_views
SET billing_organization_id = sqlc.narg(billing_organization_id),
    billing_team_id = sqlc.narg(billing_team_id),
    name_ciphertext = sqlc.arg(name_ciphertext),
    query_ciphertext = sqlc.arg(query_ciphertext),
    sort = sqlc.arg(sort),
    grouping = sqlc.arg(grouping),
    notification_ciphertext = sqlc.arg(notification_ciphertext),
    connection_state = CASE
        WHEN repository_authorization_index IS NULL
            THEN sqlc.arg(connection_state)
        ELSE connection_state
    END,
    repository_authorization_index =
        sqlc.arg(repository_authorization_index),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE view_id = sqlc.arg(view_id)
  AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: DeleteView :one
DELETE FROM deck_views
WHERE view_id = sqlc.arg(view_id)
  AND revision = sqlc.arg(expected_revision)
RETURNING revision;

-- name: DeleteViewSnapshots :exec
DELETE FROM deck_pull_request_snapshots
WHERE view_id = sqlc.arg(view_id) AND viewer_hash = sqlc.arg(viewer_hash);

-- name: DeleteViewSnapshotState :exec
DELETE FROM deck_pull_request_snapshot_states
WHERE view_id = sqlc.arg(view_id) AND viewer_hash = sqlc.arg(viewer_hash);

-- name: DeleteViewSnapshotsByViewer :exec
DELETE FROM deck_pull_request_snapshots
WHERE viewer_hash = sqlc.arg(viewer_hash);

-- name: DeleteViewSnapshotStatesByViewer :exec
DELETE FROM deck_pull_request_snapshot_states
WHERE viewer_hash = sqlc.arg(viewer_hash);

-- name: DeleteAllViewSnapshots :exec
DELETE FROM deck_pull_request_snapshots
WHERE view_id = sqlc.arg(view_id);

-- name: DeleteAllViewSnapshotStates :exec
DELETE FROM deck_pull_request_snapshot_states
WHERE view_id = sqlc.arg(view_id);

-- name: InsertViewSnapshot :exec
INSERT INTO deck_pull_request_snapshots (
    view_id, viewer_hash, ordinal, repository_hash, pull_request_number,
    repository_ciphertext, snapshot_ciphertext
) VALUES (
    sqlc.arg(view_id), sqlc.arg(viewer_hash), sqlc.arg(ordinal),
    sqlc.arg(repository_hash), sqlc.arg(pull_request_number),
    sqlc.arg(repository_ciphertext), sqlc.arg(snapshot_ciphertext)
);

-- name: UpdateViewSnapshotState :exec
INSERT INTO deck_pull_request_snapshot_states (
    view_id, viewer_hash, truncated, refreshed_at
) VALUES (
    sqlc.arg(view_id), sqlc.arg(viewer_hash),
    sqlc.arg(snapshot_truncated), sqlc.arg(snapshot_refreshed_at)
)
ON CONFLICT (view_id, viewer_hash) DO UPDATE
SET truncated = EXCLUDED.truncated,
    refreshed_at = EXCLUDED.refreshed_at;

-- name: GetViewSnapshotState :one
SELECT view_id, viewer_hash, truncated, refreshed_at
FROM deck_pull_request_snapshot_states
WHERE view_id = sqlc.arg(view_id) AND viewer_hash = sqlc.arg(viewer_hash);

-- name: ListViewSnapshots :many
SELECT view_id, viewer_hash, ordinal, repository_hash,
       repository_ciphertext, snapshot_ciphertext
FROM deck_pull_request_snapshots
WHERE view_id = sqlc.arg(view_id)
  AND viewer_hash = sqlc.arg(viewer_hash)
  AND ordinal >= sqlc.arg(after_ordinal)
ORDER BY ordinal
LIMIT sqlc.arg(page_limit);

-- name: GetViewSnapshotByReference :one
SELECT view_id, viewer_hash, ordinal, repository_ciphertext, snapshot_ciphertext
FROM deck_pull_request_snapshots
WHERE view_id = sqlc.arg(view_id)
  AND viewer_hash = sqlc.arg(viewer_hash)
  AND repository_hash = sqlc.arg(repository_hash)
  AND pull_request_number = sqlc.arg(pull_request_number);
