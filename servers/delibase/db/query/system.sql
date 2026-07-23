-- name: Ping :one
SELECT 1::bigint;

-- name: CreateAccount :one
INSERT INTO accounts (id, logto_subject, display_name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetAccountByLogtoSubject :one
SELECT *
FROM accounts
WHERE logto_subject = $1;

-- name: LockOrganizationForMutation :one
SELECT id
FROM organizations
WHERE id = $1
  AND deleted_at IS NULL
FOR UPDATE;
