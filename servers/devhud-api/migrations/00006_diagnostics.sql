CREATE TABLE devhud_crash_reports (
    crash_report_id uuid PRIMARY KEY,
    owner_user_id uuid NOT NULL REFERENCES devhud_users(user_id) ON DELETE CASCADE,
    request_correlation_id uuid NOT NULL,
    client_correlation_id uuid NOT NULL,
    payload_sha256 bytea NOT NULL CHECK (octet_length(payload_sha256) = 32),
    report_schema_version bigint NOT NULL CHECK (report_schema_version BETWEEN 1 AND 4294967295),
    app_version text NOT NULL CHECK (octet_length(app_version) BETWEEN 1 AND 256),
    build_id text NOT NULL CHECK (octet_length(build_id) BETWEEN 1 AND 256),
    platform smallint NOT NULL CHECK (platform BETWEEN 1 AND 6),
    architecture smallint NOT NULL CHECK (
        architecture BETWEEN 1 AND 3 OR (platform = 6 AND architecture = 0)
    ),
    os_version text NOT NULL CHECK (octet_length(os_version) BETWEEN 1 AND 256),
    tauri_revision text NOT NULL CHECK (
        (platform BETWEEN 1 AND 5 AND tauri_revision ~ '^[0-9a-f]{40}$')
        OR (platform = 6 AND tauri_revision = '')
    ),
    cef_revision text NOT NULL CHECK (octet_length(cef_revision) <= 256),
    occurred_at timestamptz NOT NULL,
    component smallint NOT NULL CHECK (component BETWEEN 1 AND 6),
    severity smallint NOT NULL CHECK (severity BETWEEN 1 AND 2),
    error_code text NOT NULL CHECK (error_code ~ '^[A-Z][A-Z0-9_]{0,63}$'),
    redacted_summary text NOT NULL CHECK (octet_length(redacted_summary) <= 4096),
    redacted_stack_trace text NOT NULL CHECK (octet_length(redacted_stack_trace) <= 32768),
    related_correlation_ids uuid[] NOT NULL DEFAULT '{}'
        CHECK (cardinality(related_correlation_ids) <= 32),
    duration_milliseconds bigint NOT NULL CHECK (duration_milliseconds BETWEEN 0 AND 86400000),
    accepted_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CONSTRAINT devhud_crash_reports_client_idempotency UNIQUE (owner_user_id, client_correlation_id),
    CONSTRAINT devhud_crash_reports_retention CHECK (
        expires_at = accepted_at + interval '720 hours'
    ),
    CONSTRAINT devhud_crash_reports_webview_revision CHECK (
        (platform BETWEEN 1 AND 3 AND octet_length(cef_revision) BETWEEN 1 AND 256)
        OR (platform BETWEEN 4 AND 6 AND cef_revision = '')
    )
);

CREATE INDEX devhud_crash_reports_expiry_idx ON devhud_crash_reports (expires_at, crash_report_id);
