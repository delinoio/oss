-- name: GetDeviceByAccountAndID :one
SELECT * FROM deck_device_registrations
WHERE account_id = sqlc.arg(account_id) AND device_id = sqlc.arg(device_id);

-- name: GetDeviceByID :one
SELECT * FROM deck_device_registrations
WHERE device_id = sqlc.arg(device_id);

-- name: GetDeviceByRegistration :one
SELECT * FROM deck_device_registrations
WHERE registration_id = sqlc.arg(registration_id);

-- name: GetDeviceByGrantVerifier :one
SELECT * FROM deck_device_registrations
WHERE grant_verifier = sqlc.arg(grant_verifier);

-- name: GetRegisterDeviceIdempotency :one
SELECT account_id, idempotency_key, request_digest, registration_id,
       grant_replay_ciphertext, grant_verifier, response_ciphertext,
       lease_expires_at, created_at
FROM deck_device_registration_idempotency
WHERE account_id = sqlc.arg(account_id)
  AND idempotency_key = sqlc.arg(idempotency_key);

-- name: InsertDevice :one
INSERT INTO deck_device_registrations (
    registration_id, device_id, account_id, platform,
    display_name_ciphertext, push_ciphertext,
    detailed_notification_text_enabled, shortcuts_ciphertext,
    widgets_ciphertext, grant_verifier, revision, lease_expires_at,
    created_at, updated_at
) VALUES (
    sqlc.arg(registration_id), sqlc.arg(device_id), sqlc.arg(account_id),
    sqlc.arg(platform), sqlc.arg(display_name_ciphertext),
    sqlc.arg(push_ciphertext), sqlc.arg(detailed_notification_text_enabled),
    sqlc.arg(shortcuts_ciphertext), sqlc.arg(widgets_ciphertext),
    sqlc.arg(grant_verifier), 1, sqlc.arg(lease_expires_at),
    sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: RenewDevice :one
UPDATE deck_device_registrations
SET platform = sqlc.arg(platform),
    display_name_ciphertext = sqlc.arg(display_name_ciphertext),
    push_ciphertext = sqlc.arg(push_ciphertext),
    detailed_notification_text_enabled = sqlc.arg(detailed_notification_text_enabled),
    shortcuts_ciphertext = sqlc.arg(shortcuts_ciphertext),
    widgets_ciphertext = sqlc.arg(widgets_ciphertext),
    grant_verifier = sqlc.arg(grant_verifier),
    lease_expires_at = sqlc.arg(lease_expires_at),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE registration_id = sqlc.arg(registration_id)
  AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: UpdateDevice :one
UPDATE deck_device_registrations
SET display_name_ciphertext = sqlc.arg(display_name_ciphertext),
    push_ciphertext = sqlc.arg(push_ciphertext),
    detailed_notification_text_enabled = sqlc.arg(detailed_notification_text_enabled),
    shortcuts_ciphertext = sqlc.arg(shortcuts_ciphertext),
    widgets_ciphertext = sqlc.arg(widgets_ciphertext),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE registration_id = sqlc.arg(registration_id)
  AND account_id = sqlc.arg(account_id)
  AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: InsertRegisterDeviceIdempotency :exec
INSERT INTO deck_device_registration_idempotency (
    account_id, idempotency_key, request_digest, registration_id,
    grant_replay_ciphertext, grant_verifier, response_ciphertext,
    lease_expires_at
) VALUES (
    sqlc.arg(account_id), sqlc.arg(idempotency_key),
    sqlc.arg(request_digest), sqlc.arg(registration_id),
    sqlc.arg(grant_replay_ciphertext), sqlc.arg(grant_verifier),
    sqlc.arg(response_ciphertext), sqlc.arg(lease_expires_at)
);

-- name: DeleteExpiredDeviceIdempotency :exec
DELETE FROM deck_device_registration_idempotency
WHERE lease_expires_at <= sqlc.arg(now);

-- name: DeleteExpiredDeviceByID :exec
DELETE FROM deck_device_registrations
WHERE device_id = sqlc.arg(device_id)
  AND lease_expires_at <= sqlc.arg(now);

-- name: DeleteDeviceByRegistrationAndAccount :execrows
DELETE FROM deck_device_registrations
WHERE registration_id = sqlc.arg(registration_id)
  AND account_id = sqlc.arg(account_id);

-- name: DeleteDeviceByRegistrationAndGrant :execrows
DELETE FROM deck_device_registrations AS registration
WHERE registration.registration_id = sqlc.arg(registration_id)
  AND registration.lease_expires_at > sqlc.arg(now)
  AND (
      registration.grant_verifier = sqlc.arg(grant_verifier)
      OR EXISTS (
          SELECT 1
          FROM deck_device_registration_idempotency AS replay
          WHERE replay.registration_id = registration.registration_id
            AND replay.grant_verifier = sqlc.arg(grant_verifier)
            AND replay.lease_expires_at > sqlc.arg(now)
      )
  );

-- name: GetViewNotificationPreference :one
SELECT * FROM deck_view_notification_preferences
WHERE registration_id = sqlc.arg(registration_id)
  AND view_id = sqlc.arg(view_id);

-- name: UpsertViewNotificationPreference :one
INSERT INTO deck_view_notification_preferences (
    registration_id, view_id, preference_ciphertext, revision, updated_at
) VALUES (
    sqlc.arg(registration_id), sqlc.arg(view_id),
    sqlc.arg(preference_ciphertext), 1, sqlc.arg(updated_at)
)
ON CONFLICT (registration_id, view_id) DO UPDATE
SET preference_ciphertext = EXCLUDED.preference_ciphertext,
    revision = deck_view_notification_preferences.revision + 1,
    updated_at = EXCLUDED.updated_at
WHERE deck_view_notification_preferences.revision = sqlc.arg(expected_revision)
RETURNING *;
