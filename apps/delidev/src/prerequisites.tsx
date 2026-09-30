import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, FailureCode, ResourceQuery, SystemQuery, isEntityId, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, text, type Document } from "./documents";
import { Problem } from "./ui";

enum CheckState { NotChecked = "Not checked", Observed = "Observed", Setup = "Needs setup", Unknown = "Unknown", Failed = "Check failed" }
const harnesses = new Set(["codex", "claude-code", "opencode", "grok-build"]);
const healthStates = new Set(["disconnected", "unverified", "ready", "expired", "revoked", "failed"]);
enum InstallationState { Unchecked = "unchecked", Detected = "detected", Missing = "missing", Denied = "permission-denied", Incompatible = "incompatible", Failed = "failed" }
enum ProtocolState { Verified = "verified", Unsupported = "unsupported", Failed = "failed" }
const installationFields = new Set(["harness", "state", "version", "capabilities", "observed_at", "protocol_verified", "protocol_state", "problem_code", "guidance"]);
const capabilities = new Set(["execute", "steer", "fork", "plan", "compact", "questions", "approvals", "usage", "subagents", "read-only", "models"]);
const installationProblems: Partial<Record<InstallationState, FailureCode>> = {
  [InstallationState.Missing]: FailureCode.NotFound, [InstallationState.Denied]: FailureCode.PermissionDenied,
  [InstallationState.Incompatible]: FailureCode.Unsupported, [InstallationState.Failed]: FailureCode.Unavailable,
};
function validInstallation(value: Document): boolean {
  const state = value.state as InstallationState, protocol = value.protocol_state as ProtocolState | undefined;
  if (Object.keys(value).some(key => !installationFields.has(key)) || !harnesses.has(text(value.harness)) || !Object.values(InstallationState).includes(state) || typeof value.protocol_verified !== "boolean") return false;
  if (!Array.isArray(value.capabilities) || value.capabilities.length > capabilities.size || value.capabilities.some(item => typeof item !== "string" || !capabilities.has(item)) || new Set(value.capabilities).size !== value.capabilities.length) return false;
  if (protocol !== undefined && !Object.values(ProtocolState).includes(protocol)) return false;
  if (value.protocol_verified !== (protocol === ProtocolState.Verified) || (state !== InstallationState.Detected && protocol !== undefined) || (value.capabilities.length > 0 && !value.protocol_verified)) return false;
  if (state === InstallationState.Detected) {
    if (typeof value.version !== "string" || !/^[0-9]{1,8}\.[0-9]{1,8}\.[0-9]{1,8}([+-][a-zA-Z0-9.-]{1,64})?$/.test(value.version)) return false;
  } else if (value.version !== undefined) return false;
  if (state === InstallationState.Unchecked) {
    if (value.observed_at !== undefined) return false;
  } else if (!timestamp(value.observed_at)) return false;
  const problem = protocol === ProtocolState.Unsupported ? FailureCode.Unsupported : protocol === ProtocolState.Failed ? FailureCode.Unavailable : installationProblems[state];
  if (value.problem_code !== problem) return false;
  if (value.guidance !== undefined && (typeof value.guidance !== "string" || value.guidance.length > 4096 || value.guidance.includes("\0"))) return false;
  return true;
}

enum DiagnosticState { Observed = "observed", Unavailable = "unavailable", Unconfigured = "unconfigured", NotApplicable = "not-applicable", Failed = "failed", Superseded = "superseded" }
const reportFields = new Set(["schema_version", "observed_at", "version", "protocol_version", "database_schema_version", "os", "architecture", "server_id", "listener", "database", "credential_store", "inference_probes", "storage", "machines", "more_machines", "credentials", "more_credentials"]);
const machineFields = new Set(["machine_id", "name", "os", "architecture", "version", "last_seen", "disabled", "active_stream", "installations"]);
const storageBytes = ["database_bytes", "wal_bytes", "logical_database_bytes", "volume_capacity_bytes", "volume_available_bytes"];
const resourceKinds = new Set(["pairing", "project", "repository", "agent", "account", "provider", "model", "machine", "session", "template", "settings", "schedule", "occurrence", "message", "queue", "steer", "interaction", "review", "snapshot", "device", "integration", "pull_request", "problem", "inbox", "usage", "job", "routing"]);
const unavailableCodes = new Set([FailureCode.Unavailable, FailureCode.ServerUnavailable, FailureCode.Unsupported, FailureCode.Canceled]);
function shape(value: unknown, fields: Set<string>): value is Document {
  return value !== null && typeof value === "object" && !Array.isArray(value) && Object.keys(value).every(key => fields.has(key));
}
function boundedText(value: unknown, max: number, required = true): value is string {
  return typeof value === "string" && !value.includes("\0") && !/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(value) && new TextEncoder().encode(value).length <= max && (!required || Boolean(value.trim()));
}
function timestamp(value: unknown): boolean {
  if (typeof value !== "string") return false;
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/.exec(value);
  if (!match || !Number.isFinite(Date.parse(value))) return false;
  const year = Number(match[1]), month = Number(match[2]), day = Number(match[3]);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return month >= 1 && month <= 12 && day >= 1 && day <= days[month - 1]! && Number(match[4]) < 24 && Number(match[5]) < 60 && Number(match[6]) < 60;
}
function uint64(value: unknown): boolean {
  return typeof value === "string" && /^(0|[1-9][0-9]{0,19})$/.test(value) && BigInt(value) <= 18446744073709551615n;
}
function uint32(value: unknown): boolean { return typeof value === "number" && Number.isInteger(value) && value > 0 && value <= 4294967295; }
function platform(value: Document): boolean { return ["darwin", "linux", "windows"].includes(text(value.os)) && ["amd64", "arm64"].includes(text(value.architecture)); }
function diagnosticResult(value: unknown): value is Document {
  if (!shape(value, new Set(["state", "code", "guidance"])) || !Object.values(DiagnosticState).includes(value.state as DiagnosticState) || (value.guidance !== undefined && !boundedText(value.guidance, 4096, false))) return false;
  if ([DiagnosticState.Observed, DiagnosticState.Unconfigured, DiagnosticState.NotApplicable].includes(value.state as DiagnosticState)) return value.code === undefined;
  if (!Object.values(FailureCode).includes(value.code as FailureCode)) return false;
  if (value.state === DiagnosticState.Superseded) return value.code === FailureCode.Conflict;
  return (value.state === DiagnosticState.Unavailable) === unavailableCodes.has(value.code as FailureCode);
}
function validStorage(value: unknown, database: unknown): boolean {
  if (!shape(value, new Set(["result", "resources", ...storageBytes])) || !diagnosticResult(value.result) || ![DiagnosticState.Observed, DiagnosticState.Failed, DiagnosticState.Unavailable].includes(value.result.state as DiagnosticState)) return false;
  if (storageBytes.some(key => value[key] !== undefined && !uint64(value[key])) || (value.result.state === DiagnosticState.Observed && storageBytes.some(key => value[key] === undefined))) return false;
  if ((database === "ready") !== (value.logical_database_bytes !== undefined) || !Array.isArray(value.resources) || value.resources.length > resourceKinds.size) return false;
  const seen = new Set();
  for (const entry of value.resources) {
    if (!shape(entry, new Set(["kind", "count"])) || !resourceKinds.has(text(entry.kind)) || seen.has(entry.kind) || !uint64(entry.count)) return false;
    seen.add(entry.kind);
  }
  return true;
}
function reportFrom(bytes: Uint8Array | undefined, server: string): Document | undefined {
  if (!bytes || bytes.byteLength > 1 << 20 || !isEntityId(server)) return;
  try {
    const report: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
    if (!shape(report, reportFields) || report.schema_version !== 2 || report.server_id !== server || report.inference_probes !== false || !timestamp(report.observed_at) || !boundedText(report.version, 256) || !uint32(report.protocol_version) || !uint32(report.database_schema_version) || !platform(report) || !boundedText(report.listener, 2048) || !["ready", "failed"].includes(text(report.database)) || report.credential_store !== "owner-credential-ready" || !validStorage(report.storage, report.database) || !Array.isArray(report.machines) || report.machines.length > 50 || typeof report.more_machines !== "boolean" || !Array.isArray(report.credentials) || report.credentials.length > 50 || typeof report.more_credentials !== "boolean") return;
    const listener = new URL(report.listener);
    if (!["http:", "https:"].includes(listener.protocol) || listener.username || listener.password || listener.search || listener.hash || listener.pathname !== "/") return;
    const seen = new Set();
    for (const machine of report.machines) {
      if (!shape(machine, machineFields) || !isEntityId(text(machine.machine_id)) || seen.has(machine.machine_id) || !boundedText(machine.name, 256) || !platform(machine) || !boundedText(machine.version, 256, false) || !timestamp(machine.last_seen) || typeof machine.disabled !== "boolean" || typeof machine.active_stream !== "boolean" || !Array.isArray(machine.installations) || machine.installations.length !== harnesses.size) return;
      seen.add(machine.machine_id);
      const installed = new Set();
      for (const item of machine.installations) {
        const installation = object(item);
        if (!validInstallation(installation) || installed.has(installation.harness)) return;
        installed.add(installation.harness);
      }
    }
    const accounts = new Set();
    for (const credential of report.credentials) {
      if (!shape(credential, new Set(["account_id", "connection_id", "result"])) || !isEntityId(text(credential.account_id)) || accounts.has(credential.account_id) || (credential.connection_id !== undefined && !isEntityId(text(credential.connection_id))) || !diagnosticResult(credential.result)) return;
      if ([DiagnosticState.Observed, DiagnosticState.NotApplicable].includes(credential.result.state as DiagnosticState) && credential.connection_id === undefined) return;
      if (credential.result.state === DiagnosticState.Unconfigured && credential.connection_id !== undefined) return;
      accounts.add(credential.account_id);
    }
    return report;
  } catch { return; }
}
function configurations(rows: Resource[] | undefined, kind: EntityKind): Document[] | undefined {
  if (!rows || rows.length > 50) return;
  const seen = new Set();
  const values = [];
  for (const row of rows) {
    if (row.kind !== kind || !isEntityId(row.id) || row.revision <= 0n || row.schemaVersion !== 1 || seen.has(row.id) || row.sessionId || row.projectId) return;
    seen.add(row.id);
    const value = document(row);
    if (kind === EntityKind.ACCOUNT) {
      if (!healthStates.has(text(value.health)) || typeof value.enabled !== "boolean" || !isEntityId(text(value.provider_id)) || (value.connection !== undefined && !isEntityId(text(object(value.connection).id)))) return;
    } else if (!harnesses.has(text(value.harness)) || !isEntityId(text(value.model_id)) || !Array.isArray(value.accounts)) return;
    values.push(value);
  }
  return values;
}
function Step({ label, state, children }: { label: string; state: CheckState; children: React.ReactNode }) {
  return <li><h4>{label}: {state}</h4>{children}</li>;
}

// Read-only snapshots deliberately do not grant execution readiness. In particular,
// a retained handshake, readable credential and configured Agent are separate facts.
export function Prerequisites({ active, openSettings }: { active: boolean; openSettings: () => void }) {
  const [checked, setChecked] = useState(false);
  const options = { enabled: active && checked, retry: false, staleTime: 0, gcTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false } as const;
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active, refetchInterval: active ? 30000 : false });
  const doctor = useQuery(SystemQuery.getDoctor, {}, options);
  const accounts = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 50 } }, options);
  const agents = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.AGENT, pageSize: 50 } }, options);
  const report = checked && !doctor.error && !doctor.isFetching && !status.error ? reportFrom(doctor.data?.reportJson, status.data?.serverId ?? "") : undefined;
  const accountRows = checked && !accounts.error && !accounts.isFetching ? configurations(accounts.data?.resources, EntityKind.ACCOUNT) : undefined;
  const agentRows = checked && !agents.error && !agents.isFetching ? configurations(agents.data?.resources, EntityKind.AGENT) : undefined;
  const machines = report ? items(report.machines).map(object) : [];
  const connected = machines.filter(machine => machine.active_stream === true && machine.disabled === false);
  const verified = connected.flatMap(machine => items(machine.installations).map(object)).filter(installation => installation.state === "detected" && installation.protocol_verified === true && installation.protocol_state === "verified" && text(installation.version));
  const readyAccounts = accountRows?.filter(account => account.enabled === true && account.health === "ready" && isEntityId(text(object(account.connection).id)) && account.removal === undefined).length ?? 0;
  const loading = checked && [doctor, accounts, agents].some(query => query.isFetching);
  const observationState = (error: unknown, valid: boolean, found: number, more: unknown) => !checked ? CheckState.NotChecked : error ? CheckState.Failed : !valid ? CheckState.Unknown : found ? CheckState.Observed : more ? CheckState.Unknown : CheckState.Setup;
  const refresh = () => {
    if (!checked) setChecked(true);
    else void Promise.all([status.refetch(), doctor.refetch(), accounts.refetch(), agents.refetch()]);
  };
  return <section aria-label="First session checklist">
    <h3>Before your first session</h3>
    <p>Check the selected server's saved setup. This does not log in, install or probe a harness, refresh a provider, or run inference.</p>
    <div className="actions"><button disabled={!active || loading} onClick={refresh}>{checked ? "Refresh prerequisites" : "Check prerequisites"}</button><button onClick={(event) => { event.currentTarget.focus(); openSettings(); }}>View prerequisites in Settings</button></div>
    {loading ? <p role="status">Reading prerequisite observations…</p> : null}
    <Problem error={status.error} />{checked ? <><Problem error={doctor.error} /><Problem error={accounts.error} /><Problem error={agents.error} /></> : null}
    <ol>
      <Step label="Server connection" state={status.error ? CheckState.Failed : status.data?.stopping ? CheckState.Setup : status.data && isEntityId(status.data.serverId) ? CheckState.Observed : CheckState.Unknown}><p>{status.error ? "Reconnect to the selected server." : status.data?.stopping ? "The server is stopping." : status.data && isEntityId(status.data.serverId) ? `Connected to server ${status.data.version}.` : "Waiting for an authenticated server identity."}</p></Step>
      <Step label="Server diagnostics" state={!checked ? CheckState.NotChecked : doctor.error ? CheckState.Failed : !report ? CheckState.Unknown : report.database === "ready" && object(report.storage).result && object(object(report.storage).result).state === "observed" ? CheckState.Observed : CheckState.Setup}><p>{report ? `Database read: ${report.database === "ready" ? "succeeded" : "not established"}. Storage: ${text(object(object(report.storage).result).state) || "unknown"}. Observed at ${text(report.observed_at)}.` : checked ? "No current supported diagnostic report is available." : "Run the read-only check for database and storage observations."}</p></Step>
      <Step label="Runner Device and harness" state={observationState(doctor.error, Boolean(report), verified.length, report?.more_machines)}><p>{report ? `${connected.length} enabled Worker(s) had an active connection; ${verified.length} retained harness handshake(s) were verified on those Workers.` : "Pair a Worker and explicitly discover its installed harness in Settings."}</p>{report?.more_machines ? <p>Only the first 50 Workers were inspected; absence from this page does not establish missing setup.</p> : null}<p>A connection or past handshake does not establish current account compatibility or execution readiness.</p></Step>
      <Step label="AI account" state={observationState(accounts.error, Boolean(accountRows), readyAccounts, accounts.data?.nextPageToken)}><p>{accountRows ? `${accountRows.length} account(s) inspected; ${readyAccounts} enabled account(s) have a connection and saved ready status.` : "Connect and validate an account explicitly in Settings."}</p>{accounts.data?.nextPageToken ? <p>More accounts exist. Open AI accounts for the remaining records.</p> : null}<p>Saved status does not prove current quota or provider access.</p></Step>
      <Step label="Agent Worker configuration" state={observationState(agents.error, Boolean(agentRows), agentRows?.length ?? 0, agents.data?.nextPageToken)}><p>{agentRows ? `${agentRows.length} Agent Worker configuration(s) inspected.` : "Configure an Agent Worker with its selected model, account and harness."}</p>{agents.data?.nextPageToken ? <p>More Agent Workers exist. Open Settings for the remaining records.</p> : null}<p>Choose a project for repository work, or General Chat. Session creation validates the exact selections again.</p></Step>
    </ol>
    {checked && !loading ? <p>These are separate observations, not a successful execution test. Refresh after setup changes.</p> : null}
  </section>;
}
