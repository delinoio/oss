import { copy, useLocale } from "./localization";
import { object } from "./documents";

const errors = new Set(["authentication_failed", "oauth_org_not_allowed", "account_on_hold", "billing_error", "rate_limit", "overloaded", "invalid_request", "model_not_found", "server_error", "unknown", "max_output_tokens"]);
const fields = ["native_event_id", "attempt", "max_retries", "retry_delay_ms", "error_status", "error"];
const count = (value: unknown): value is string => typeof value === "string" && /^(0|[1-9][0-9]{0,19})$/.test(value) && BigInt(value) <= 18446744073709551615n;

export function validClaudeAPIRetry(value: unknown) {
  const r = object(value);
  return Object.keys(r).length === fields.length && fields.every((key) => Object.hasOwn(r, key)) && typeof r.native_event_id === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(r.native_event_id) && [r.attempt, r.max_retries, r.retry_delay_ms].every(count) && (r.error_status === null || Number.isInteger(r.error_status) && Number(r.error_status) >= 400 && Number(r.error_status) <= 599) && typeof r.error === "string" && errors.has(r.error);
}

export function NativeClaudeAPIRetry({ value }: { value: unknown }) {
  useLocale();
  if (!validClaudeAPIRetry(value)) return <p>{copy("native-claude-retry.theRetainedClaudeRetryIsUnavailable_6f0bde")}</p>;
  const r = object(value);
  return <><dl>
    <dt>{copy("native-claude-retry.nativeRetryAttempt_a0b7b9")}</dt><dd>{r.attempt as string}</dd>
    <dt>{copy("native-claude-retry.nativeMaximumRetries_617637")}</dt><dd>{r.max_retries as string}</dd>
    <dt>{copy("native-claude-retry.reportedRetryDelayMs_951c2b")}</dt><dd>{r.retry_delay_ms as string}</dd>
    <dt>{copy("native-claude-retry.reportedError_1f1a98")}</dt><dd>{r.error as string}</dd>
    <dt>{copy("native-claude-retry.httpStatus_0f7cf9")}</dt><dd>{r.error_status === null ? copy("native-claude-retry.notReported_adadfa") : String(r.error_status)}</dd>
  </dl><p>{copy("native-claude-retry.thisRecordsClaudeSRetryObservation_e4c795")}</p></>;
}
