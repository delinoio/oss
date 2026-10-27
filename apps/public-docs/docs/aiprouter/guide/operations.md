# Observability and troubleshooting

## Available interface and limits

The server integration interface defines `/healthz`, `/readyz`, and `/metrics`. Keep metrics private; do not publish them through the public proxy. Confirm each endpoint's exact checks in the matching release. A health response alone does not verify OIDC, SMTP, provider access, or successful disaster recovery.

Required observability includes structured logs with request IDs, Prometheus metrics, OpenTelemetry traces, Grafana dashboard examples, and role-scoped audits. The Compose assets provide Prometheus and Grafana examples. After setting a strong `GRAFANA_ADMIN_PASSWORD` in the deployment configuration, enable them from the directory containing the matching Compose file:

```sh
docker compose --profile observability up -d prometheus grafana
```

The example binds Prometheus to `127.0.0.1:9090` and Grafana to `127.0.0.1:3000`. Keep both private. Supplied dashboards cover aggregate health/outcomes. Configure alert delivery separately; evaluation of an alert does not deliver a notification by itself. Complete exporter configuration and live operational verification are not established by this page. Do not invent scrape labels or alert expressions.

Never log provider secrets, API keys, device secrets, raw prompts, or raw responses. Use request IDs and redacted error metadata to correlate problems. Keep unknown usage, usage-write failure, cancellation, limits, and upstream versus gateway errors distinct.

## Inspect usage and instance settings

Open **Usage** in the selected organization for your personal records, summary, and daily totals. Personal Usage remains scoped to you even if you own the organization. Use date, model, provider, and outcome filters; a failed filter request must remain an error rather than showing unfiltered data as a match. Per-request details retain the applied price and provenance. Daily results also have a table for inspection.

Owners and administrators use the organization usage and audit views for permitted member metadata. Instance operators use the separate instance area for health, request limits, retention, approved destinations, common price overrides, and instance audits. Changing a limit or price does not grant access to another user's credentials or content.

The console sends these filters to the service. Some server aggregation/filter behavior is still being completed, so end-to-end filtered totals are not verified here. Missing usage-recording failures can make totals incomplete; the available protocol does not provide a per-request recording-failure count. Do not infer that absent records represent zero usage. Audit filters labeled as current-page filters apply only to loaded records.

## Traces

The server enables OTLP HTTP trace export when `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is configured. Use your operator-managed collector and its supported endpoint. Keep telemetry private and omit content and secret attributes. Trace export configuration is separate from Prometheus scrape configuration; no live collector acceptance is claimed here.

## Alerts and retention

The default alert requirements are:

- Service health-check failure for one minute.
- Database connectivity-check failure for one minute.
- Three consecutive cleanup failures, or no successful cleanup for 15 minutes.

Cleanup runs every minute. Operators choose capacity and error-rate alert thresholds for their deployment; no fixed default percentage or SLO is promised.

| Record                                               | Default retention | Operator range |
| ---------------------------------------------------- | ----------------- | -------------- |
| Request usage, estimated cost, model, error metadata | 30 days           | 1–90 days      |
| Daily usage/cost aggregates                          | 365 days          | 30–730 days    |
| Organization and instance-setting audits             | 90 days           | 30–365 days    |

Expired records must be inaccessible even before physical cleanup. Owners/administrators can inspect organization audits; operators can inspect instance-setting audits and whole-instance health/aggregates. Observability must not grant access to another user's secrets or request content.

Costs are estimates, not invoices. Missing usage or price is unknown, not zero; aggregates must expose missing counts. Only operators change common model price overrides. Later price changes affect new requests, not historical estimates.

## Diagnose common failures

| Symptom                                            | What to check and do                                                                                                                                 |
| -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Login fails                                        | Check issuer, registered public callback, verified email, and identity-provider availability. Do not merge accounts by email.                        |
| Invitation fails or expires                        | Check delivery status and matching verified email. Use the newest link; request a manual resend after final failure.                                 |
| Permission denied after role change                | Refresh the view and confirm current role. An old page cannot retain revoked authority.                                                              |
| New inference requests return 429                  | Respect `Retry-After`; reduce frequency/concurrency. Distinguish admission limits from upstream provider limits.                                     |
| New inference requests return 503 during DB outage | Restore database connectivity. Cached authorization must not admit traffic. Only dispatched responses may finish; additional sends stop.             |
| Unknown usage or cost                              | Check missing usage, unavailable price, cancellation, or write failure. Do not replace unknown with zero or infer successful recording.              |
| OAuth/provider connection fails                    | Confirm the specific integration, account authorization, callback, model, and destination policy. Do not assume generic OAuth settings work.         |
| Stream stops                                       | Record the request ID and redacted error. Check cancellation and path-specific timeout/proxy behavior before replaying a possibly completed request. |
| Continuation is missing                            | State may be expired, evicted, removed, or lost on recovery. Start a new conversation explicitly.                                                    |
| Image or hosted video download fails               | Check URL expiry and object expiry/deletion. Request a new authorized URL only if the artifact still exists; do not make the bucket public.          |
| CLI cannot store a key                             | Repair OS credential-store access; keep client settings unchanged. Revoke any outstanding issued key in the web console.                             |
| Relay port is occupied                             | Identify the listener and choose a distinct supported profile port, then review affected settings. Do not silently change ports or bind publicly.    |
| Disconnect cannot reach server                     | Finish local cleanup, preserve conflict backups, then revoke the dedicated key through the web console when reachable.                               |
| Restore invalidation fails                         | Keep all servers stopped and traffic blocked until invalidation is verified.                                                                         |

When requesting help, include the application version, platform, time, request ID, and sanitized symptoms. Exclude credentials, settings backups, signed URLs, device secrets, and raw inference content.

## Destination-policy changes and conflicts

Operators can add, edit, and remove exact-origin approvals and optional CIDR restrictions in Approved destinations. Follow [the destination policy procedure](configuration.md#provider-destination-policy) and confirm the complete draft before saving. Clearing CIDRs broadens that origin's access; removing approval can block future requests that depend on it. On a concurrent-update error, your draft is preserved only while you stay on the page; reloading discards it. Note your intended edits, reload the page to load the current policy, re-enter your changes, and review the complete policy again before saving. A rejected save does not change the active policy.
