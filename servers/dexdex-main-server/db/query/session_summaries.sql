-- name: GetSessionSummary :one
SELECT * FROM session_summaries WHERE workspace_id = $1 AND session_id = $2;

-- name: ListForkedSessions :many
SELECT * FROM session_summaries WHERE workspace_id = $1 AND parent_session_id = $2 ORDER BY created_at;

-- name: CreateSessionSummary :one
INSERT INTO session_summaries (session_id, workspace_id, parent_session_id, root_session_id, fork_status, forked_from_sequence, agent_session_status, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ArchiveSession :exec
UPDATE session_summaries SET fork_status = $3
WHERE workspace_id = $1 AND session_id = $2;

-- name: GetLatestWaitingSession :one
SELECT * FROM session_summaries
WHERE workspace_id = $1 AND agent_session_status = $2
ORDER BY created_at DESC
LIMIT 1;
