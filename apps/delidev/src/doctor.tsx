import { useQuery } from "@connectrpc/connect-query";
import { SystemQuery } from "@delinoio/delidev-api-client";
import { items, object, text, type Document } from "./documents";
import { Problem } from "./ui";

enum DiagnosticState { Observed = "observed", Unavailable = "unavailable", Unconfigured = "unconfigured", NotApplicable = "not-applicable", Failed = "failed", Superseded = "superseded" }
const stateNames: Record<DiagnosticState, string> = {
  [DiagnosticState.Observed]: "Observed", [DiagnosticState.Unavailable]: "Unavailable", [DiagnosticState.Unconfigured]: "Not configured", [DiagnosticState.NotApplicable]: "Not applicable", [DiagnosticState.Failed]: "Failed", [DiagnosticState.Superseded]: "Connection changed during inspection",
};
function observation(value: unknown, success = "Observed") {
  const result = object(value), state = text(result.state);
  const name = Object.hasOwn(stateNames, state) ? stateNames[state as DiagnosticState] : "Unknown";
  return <><strong>{state === DiagnosticState.Observed ? success : name}</strong>{text(result.code) ? ` · ${text(result.code)}` : ""}{text(result.guidance) ? <p>{text(result.guidance)}</p> : null}</>;
}
function decimal(value: unknown): string | undefined {
  if (typeof value !== "string" || !/^(0|[1-9][0-9]{0,19})$/.test(value)) return;
  const number = BigInt(value);
  if (number > 18446744073709551615n) return;
  return number.toLocaleString();
}
function bytes(value: unknown) { const number = decimal(value); return number === undefined ? "Unavailable" : `${number} bytes`; }
function completeness(more: unknown, kind: string) {
  if (more === true) return <p className="notice">Only the first 50 {kind} are included. Open their settings to inspect the remaining records.</p>;
  return more === false ? null : <p>Inventory completeness is unknown.</p>;
}
function Storage({ value }: { value: Document }) {
  return <section aria-label="Storage diagnostics"><h4>Server storage</h4>{observation(value.result)}<dl><dt>Database file</dt><dd>{bytes(value.database_bytes)}</dd><dt>Write-ahead log</dt><dd>{bytes(value.wal_bytes)}</dd><dt>Logical database size</dt><dd>{bytes(value.logical_database_bytes)}</dd><dt>Filesystem capacity</dt><dd>{bytes(value.volume_capacity_bytes)}</dd><dt>Available to the server</dt><dd>{bytes(value.volume_available_bytes)}</dd></dl><p>Sizes are sampled separately. They cannot be added together or treated as reclaimable space.</p><h5>Retained resources</h5>{Array.isArray(value.resources) ? value.resources.length ? <ul>{value.resources.slice(0, 100).map((entry, index) => { const count = object(entry); return <li key={index}>{text(count.kind) || "Unknown resource"}: {decimal(count.count) ?? "Unknown"}</li>; })}</ul> : <p>No retained resources were counted.</p> : <p>Resource counts are unavailable.</p>}</section>;
}
const installationStates: Record<string, string> = { unchecked: "Not checked", detected: "Version detected", missing: "Executable missing", "permission-denied": "Permission denied", incompatible: "Incompatible", failed: "Probe failed" };
function Installation({ value }: { value: Document }) {
  const protocol = value.protocol_verified === true && value.protocol_state === "verified" ? "Handshake verified" : value.protocol_state === "failed" ? "Handshake failed" : value.protocol_state === "unsupported" ? "Unsupported protocol" : value.protocol_verified === false && !value.protocol_state ? "Handshake not checked" : "Unknown protocol state";
  const state = text(value.state);
  return <li><strong>{text(value.harness) || "Unknown harness"}</strong>: {Object.hasOwn(installationStates, state) ? installationStates[state] : "Unknown installation state"}{text(value.version) ? ` · ${text(value.version)}` : ""}<p>{protocol} · Last discovery: {text(value.observed_at) || "Not observed"}</p><p>Reported capabilities: {items(value.capabilities).map(text).filter(Boolean).join(", ") || "None"}</p>{text(value.problem_code) ? <p>{text(value.problem_code)}</p> : null}{text(value.guidance) ? <p>{text(value.guidance)}</p> : null}</li>;
}
function Workers({ report }: { report: Document }) {
  return <section aria-label="Worker diagnostics"><h4>Execution Workers</h4><p>These are retained Worker reports and a connection snapshot. Discovery does not run during this check. A verified handshake does not establish account readiness or native process cleanup.</p>{completeness(report.more_machines, "Workers")}{Array.isArray(report.machines) ? report.machines.length ? report.machines.slice(0, 50).map((entry, index) => {
    const machine = object(entry);
    return <article className="card" key={index}><h5>{text(machine.name) || "Unnamed Worker"}</h5><p>{text(machine.machine_id) || "Unknown machine"}</p><p>Reported version: {text(machine.version) || "Unknown"} · {text(machine.os) || "Unknown OS"} / {text(machine.architecture) || "Unknown architecture"}</p><p>{machine.active_stream === true ? "Connected at observation" : machine.active_stream === false ? "No active stream observed" : "Connection unknown"} · {machine.disabled === true ? "Disabled" : machine.disabled === false ? "Enabled" : "Availability unknown"}</p><p>Last contact: {text(machine.last_seen) && !text(machine.last_seen).startsWith("0001-") ? text(machine.last_seen) : "Not observed"}</p><ul>{items(machine.installations).slice(0, 4).map((value, key) => <Installation key={key} value={object(value)} />)}</ul></article>;
  }) : <p>No Workers are registered.</p> : <p>Worker observations are unavailable.</p>}</section>;
}
function Credentials({ report }: { report: Document }) {
  return <section aria-label="Protected credential diagnostics"><h4>Protected account storage</h4><p>Only each current account connection's exact saved credential is checked. Store access does not verify provider authentication, execution readiness or quota.</p>{completeness(report.more_credentials, "accounts")}{Array.isArray(report.credentials) ? report.credentials.length ? <ul>{report.credentials.slice(0, 50).map((entry, index) => { const credential = object(entry); return <li key={index}><p>Account: {text(credential.account_id) || "Unknown"}</p>{text(credential.connection_id) ? <p>Connection: {text(credential.connection_id)}</p> : null}{observation(credential.result, "Saved credential readable")}</li>; })}</ul> : <p>No accounts are configured; credential-store health was not probed.</p> : <p>Protected storage observations are unavailable.</p>}</section>;
}
export function Doctor({ active }: { active: boolean }) {
  const result = useQuery(SystemQuery.getDoctor, {}, { enabled: active });
  let report: Document | undefined;
  let unsupported = false;
  if (result.data && result.data.reportJson.byteLength <= 1 << 20) {
    try {
      const candidate = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(result.data.reportJson)));
      unsupported = candidate.schema_version !== undefined && candidate.schema_version !== 2;
      if (!unsupported && text(candidate.version)) report = candidate;
    } catch { /* Malformed reports cannot establish health. */ }
  }
  return <section><h3>Diagnostics</h3><p>Read-only observations from the selected server. This check does not repair state, connect an account or run model inference.</p><button disabled={!active || result.isFetching} onClick={() => void result.refetch()}>Refresh diagnostics</button><Problem error={result.error} />{result.isPending && active ? <p role="status">Reading server diagnostics…</p> : null}{result.error && report ? <p role="alert">Refresh failed. The report below is the last returned observation.</p> : null}{report ? <><dl><dt>Server version</dt><dd>{text(report.version) || "Unknown"}</dd><dt>Server identity</dt><dd>{text(report.server_id) || "Unknown"}</dd><dt>Bound endpoint</dt><dd>{text(report.listener) || "Unknown"}</dd><dt>Database read check</dt><dd>{report.database === "ready" ? "Read succeeded" : report.database === "failed" ? "Read failed — inspect server storage and its private structured log." : "Unknown"}</dd><dt>Owner credential</dt><dd>{report.credential_store === "owner-credential-ready" ? "Server owner credential loaded" : "Unknown"}</dd><dt>Inference probes</dt><dd>{report.inference_probes === false ? "Not performed" : "Unknown"}</dd></dl>{report.schema_version === 2 ? <><dl><dt>Observed at</dt><dd>{text(report.observed_at) || "Unknown"}</dd><dt>Server platform</dt><dd>{text(report.os) || "Unknown"} / {text(report.architecture) || "Unknown"}</dd><dt>Protocol version</dt><dd>{Number.isSafeInteger(report.protocol_version) && Number(report.protocol_version) > 0 ? Number(report.protocol_version) : "Unknown"}</dd><dt>Database schema</dt><dd>{Number.isSafeInteger(report.database_schema_version) && Number(report.database_schema_version) > 0 ? Number(report.database_schema_version) : "Unknown"}</dd></dl><Storage value={object(report.storage)} /><Workers report={report} /><Credentials report={report} /></> : <p>This server returned a legacy report. Capacity and protected-storage health are not yet reported.</p>}</> : result.data ? <p role="alert">{unsupported ? "This diagnostic report version is unsupported. No health result can be inferred." : "The diagnostic report is unavailable or malformed. No health result can be inferred."}</p> : null}<p>Owner credential availability does not verify protected account storage, account readiness or quota. Use AI accounts for validation and Execution Workers for discovery and connection recovery.</p></section>;
}
