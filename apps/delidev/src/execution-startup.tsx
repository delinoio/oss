// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary } from "./disclosure";
import { useEffect, useRef, useState } from "react";
import { copy, useLocale } from "./localization";
import { object, text, type Document } from "./documents";

enum StartupState { Failed = 2, Uncertain = 3 }
enum InputDelivery { NotSent = 1, Claimed = 2, Acknowledged = 3, Uncertain = 4 }
enum Cleanup { Confirmed = 1, Uncertain = 2 }
const protocols = { codex: "codex-app-server-v2", "claude-code": "claude-stream-json", opencode: "opencode-http", "grok-build": "grok-acp" };
const codes = new Set(["not_found", "permission_denied", "unsupported", "invalid_argument", "unavailable", "canceled", "conflict", "recovery_required", "resource_exhausted", "provider_disabled"]);
const fields = new Set(["state", "phase", "harness", "native_version", "executable_sha256", "protocol", "problem_code", "correlation_id", "input_delivery", "cleanup"]);
const uuid = (value: unknown) => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);

export function executionStartupFailure(session: Document): Document | undefined {
  const record = object(session.startup), value = object(record.failure);
  if (!uuid(record.job_id) || !uuid(record.execution_id) || Object.keys(record).some(key => !["job_id", "execution_id", "ready", "failure"].includes(key))) return;
  const selected = object(session.current_execution ?? session.initial_execution);
  if (selected.id !== record.execution_id || !Object.keys(value).length || Object.keys(value).some(key => !fields.has(key))) return;
  if (![StartupState.Failed, StartupState.Uncertain].includes(value.state as StartupState) || !Number.isInteger(value.phase) || Number(value.phase) < 1 || Number(value.phase) > 7 || !Object.hasOwn(protocols, text(value.harness)) || !codes.has(text(value.problem_code)) || value.correlation_id !== record.job_id || typeof value.input_delivery !== "number" || ![1, 2, 3, 4].includes(value.input_delivery) || typeof value.cleanup !== "number" || ![1, 2].includes(value.cleanup)) return;
  if (value.native_version !== undefined && (typeof value.native_version !== "string" || !/^[A-Za-z0-9.+_-]{1,64}$/.test(value.native_version))) return;
  if (value.executable_sha256 !== undefined && (typeof value.executable_sha256 !== "string" || !/^[0-9a-f]{64}$/.test(value.executable_sha256))) return;
  if (value.protocol !== undefined && value.protocol !== protocols[value.harness as keyof typeof protocols]) return;
  if (value.state === StartupState.Failed && (value.input_delivery !== InputDelivery.NotSent || value.cleanup !== Cleanup.Confirmed)) return;
  return value;
}

export function canRetryExecutionStartup(session: Document): boolean {
  const failure = executionStartupFailure(session);
  return failure?.state === StartupState.Failed && session.dispatch === "paused" && session.archive === "active" && session.recovery === "none" && !session.active_execution_id && !session.compaction_job_id && Number(session.pending_inputs ?? 0) === 0 && Number(session.pending_input_bytes ?? 0) === 0;
}

export function startupCorrection(failure: Document): string {
  switch (failure.problem_code) {
    case "not_found": return copy("session.startupInstall");
    case "permission_denied": return copy("session.startupPermissions");
    case "unsupported": case "invalid_argument": return copy("session.startupSettings");
    default: return copy("session.startupCheckDevice");
  }
}

export function ExecutionStartupDetails({ failure }: { failure: Document }) {
  useLocale();
  const alive = useRef(false);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const phases = ["", copy("session.startupResolve"), copy("session.startupLaunch"), copy("session.startupInitialize"), copy("session.startupAppliedSettings"), copy("session.startupInput"), copy("session.startupExecution"), copy("session.startupCleanup")];
  const delivery = ["", copy("session.startupNotSent"), copy("session.startupClaimed"), copy("session.startupAcknowledged"), copy("session.startupUnknown")];
  // Project only validated metadata. Never copy the resource or native output.
  const metadata = { phase: failure.phase, harness: failure.harness, native_version: failure.native_version ?? "", problem_code: failure.problem_code, correlation_id: failure.correlation_id, input_delivery: failure.input_delivery, cleanup: failure.cleanup };
  return <Disclosure className="execution-startup-details"><DisclosureSummary>{copy("session.startupDetails")}</DisclosureSummary>
    <dl><dt>{copy("session.startupPhase")}</dt><dd>{phases[Number(failure.phase)]}</dd>
      <dt>{copy("session.startupVersion")}</dt><dd>{text(failure.native_version) || copy("session.startupUnavailable")}</dd>
      <dt>{copy("session.startupCode")}</dt><dd>{text(failure.problem_code)}</dd>
      <dt>{copy("session.startupReference")}</dt><dd>{text(failure.correlation_id)}</dd>
      <dt>{copy("session.startupInputDelivery")}</dt><dd>{delivery[Number(failure.input_delivery)]}</dd>
      <dt>{copy("session.startupCleanup")}</dt><dd>{failure.cleanup === Cleanup.Confirmed ? copy("session.startupConfirmed") : copy("session.startupUnknown")}</dd></dl>
    <p>{failure.state === StartupState.Failed ? copy("session.startupManualSteps") : copy("session.startupRecover")}</p>
    <button type="button" onClick={() => { setCopied(false); setCopyFailed(false); if (!navigator.clipboard) { setCopyFailed(true); return; } void navigator.clipboard.writeText(JSON.stringify(metadata, null, 2)).then(() => { if (alive.current) setCopied(true); }, () => { if (alive.current) setCopyFailed(true); }); }}>{copy("session.startupCopy")}</button>
    {copied ? <p role="status">{copy("session.startupCopied")}</p> : null}{copyFailed ? <p role="status">{copy("session.startupCopyFailed")}</p> : null}
  </Disclosure>;
}
