// SPDX-License-Identifier: Apache-2.0
import "./doctor.css";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { SystemQuery } from "@delinoio/delidev-api-client";
import { items, object, text, type Document } from "./documents";
import { Problem } from "./ui";

enum DiagnosticState { Observed = "observed", Unavailable = "unavailable", Unconfigured = "unconfigured", NotApplicable = "not-applicable", Failed = "failed", Superseded = "superseded" }
const stateNames: Record<DiagnosticState, string> = {
  [DiagnosticState.Observed]: "Observed", [DiagnosticState.Unavailable]: "Unavailable", [DiagnosticState.Unconfigured]: "Not configured", [DiagnosticState.NotApplicable]: "Not applicable", [DiagnosticState.Failed]: "Failed", [DiagnosticState.Superseded]: "Connection changed during inspection",
};
const scope = "Read-only observations from the selected server. This check does not repair state, connect an account or run model inference.";
const ownerCaveat = "Owner credential availability does not verify protected account storage, account readiness or quota.";
function observation(value: unknown, success = "Observed") {
  const result = object(value), state = text(result.state);
  const name = Object.hasOwn(stateNames, state) ? stateNames[state as DiagnosticState] : "Unknown";
  return <div className="diagnostics-result"><strong>{state === DiagnosticState.Observed ? success : name}</strong>{text(result.code) ? ` · ${text(result.code)}` : ""}{text(result.guidance) ? <p>{text(result.guidance)}</p> : null}</div>;
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
function Disclosure({ title, children }: { title: string; children: ReactNode }) {
  return <details className="diagnostics-disclosure"><summary>{title}</summary>{children}</details>;
}
function Storage({ value }: { value: Document }) {
  const resources = Array.isArray(value.resources) ? value.resources : undefined;
  return <section className="diagnostics-panel" aria-label="Storage diagnostics">
    <h2>Server storage</h2>{observation(value.result)}
    <dl className="diagnostics-facts"><dt>Database file</dt><dd>{bytes(value.database_bytes)}</dd><dt>Write-ahead log</dt><dd>{bytes(value.wal_bytes)}</dd><dt>Logical database size</dt><dd>{bytes(value.logical_database_bytes)}</dd><dt>Filesystem capacity</dt><dd>{bytes(value.volume_capacity_bytes)}</dd><dt>Available to the server</dt><dd>{bytes(value.volume_available_bytes)}</dd></dl>
    <p>Sizes are sampled separately. They cannot be added together or treated as reclaimable space.</p>
    {resources ? resources.length ? null : <p>No retained resources were counted.</p> : <p>Resource counts are unavailable.</p>}
    <Disclosure title="Retained resources">{resources?.length ? <ul>{resources.slice(0, 100).map((entry, index) => { const count = object(entry); return <li key={index}>{text(count.kind) || "Unknown resource"}: {decimal(count.count) ?? "Unknown"}</li>; })}</ul> : null}</Disclosure>
  </section>;
}
const installationStates: Record<string, string> = { unchecked: "Not checked", detected: "Version detected", missing: "Executable missing", "permission-denied": "Permission denied", incompatible: "Incompatible", failed: "Probe failed" };
function Installation({ value, secondary = false }: { value: Document; secondary?: boolean }) {
  if (secondary) return <li><strong>{text(value.harness) || "Unknown harness"}</strong><p>Last discovery: {text(value.observed_at) || "Not observed"}</p><p>Reported capabilities: {items(value.capabilities).map(text).filter(Boolean).join(", ") || "None"}</p></li>;
  const protocol = value.protocol_verified === true && value.protocol_state === "verified" ? "Handshake verified" : value.protocol_state === "failed" ? "Handshake failed" : value.protocol_state === "unsupported" ? "Unsupported protocol" : value.protocol_verified === false && !value.protocol_state ? "Handshake not checked" : "Unknown protocol state";
  const state = text(value.state);
  return <li><strong>{text(value.harness) || "Unknown harness"}</strong>: {Object.hasOwn(installationStates, state) ? installationStates[state] : "Unknown installation state"}{text(value.version) ? ` · ${text(value.version)}` : ""}<p>{protocol}</p>{text(value.problem_code) ? <p>{text(value.problem_code)}</p> : null}{text(value.guidance) ? <p>{text(value.guidance)}</p> : null}</li>;
}
type RecordKey = (record: Document, identity: string) => string;
function Workers({ report, recordKey }: { report: Document; recordKey: RecordKey }) {
  return <section className="diagnostics-panel diagnostics-wide" aria-label="Worker diagnostics"><h2>Runner Devices</h2><p>These are retained Worker reports and a connection snapshot. Discovery does not run during this check. A verified handshake does not establish account readiness or native process cleanup.</p>{completeness(report.more_machines, "Workers")}{Array.isArray(report.machines) ? report.machines.length ? <div className="diagnostics-records">{report.machines.slice(0, 50).map((entry) => {
    const machine = object(entry), installations = items(machine.installations).slice(0, 4);
    return <article className="diagnostics-record" key={recordKey(machine, text(machine.machine_id))}><h3>{text(machine.name) || "Unnamed Worker"}</h3><p>Reported version: {text(machine.version) || "Unknown"} · {text(machine.os) || "Unknown OS"} / {text(machine.architecture) || "Unknown architecture"}</p><p>{machine.active_stream === true ? "Connected at observation" : machine.active_stream === false ? "No active stream observed" : "Connection unknown"} · {machine.disabled === true ? "Disabled" : machine.disabled === false ? "Enabled" : "Availability unknown"}</p><p>Last contact: {text(machine.last_seen) && !text(machine.last_seen).startsWith("0001-") ? text(machine.last_seen) : "Not observed"}</p>
      <ul className="diagnostics-installations">{installations.map((value, key) => <Installation key={key} value={object(value)} />)}</ul>
      <Disclosure title="Installation details"><p>Machine identity: {text(machine.machine_id) || "Unknown machine"}</p><ul>{installations.map((value, key) => <Installation key={key} value={object(value)} secondary />)}</ul></Disclosure>
    </article>;
  })}</div> : <p>No Workers are registered.</p> : <p>Worker observations are unavailable.</p>}</section>;
}
function Credentials({ report, recordKey }: { report: Document; recordKey: RecordKey }) {
  return <section className="diagnostics-panel diagnostics-wide" aria-label="Protected credential diagnostics"><h2>Protected account storage</h2><p>Only each current account connection's exact saved credential is checked. Store access does not verify provider authentication, execution readiness or quota.</p>{completeness(report.more_credentials, "accounts")}{Array.isArray(report.credentials) ? report.credentials.length ? <div className="diagnostics-records">{report.credentials.slice(0, 50).map((entry) => {
    const credential = object(entry), account = text(credential.account_id), connection = text(credential.connection_id);
    return <article className="diagnostics-record" key={recordKey(credential, account && connection ? JSON.stringify([account, connection]) : "")}><h3>Account: {account || "Unknown"}</h3>{observation(credential.result, "Saved credential readable")}{connection ? <Disclosure title="Connection identity"><p>Connection: {connection}</p></Disclosure> : null}</article>;
  })}</div> : <p>No accounts are configured; credential-store health was not probed.</p> : <p>Protected storage observations are unavailable.</p>}</section>;
}
function Report({ report }: { report: Document }) {
  // Missing identities get response-object keys, so a replacement cannot inherit
  // another record's native disclosure state. Known identities survive reorder.
  const missingKeys = useRef(new WeakMap<Document, string>()), nextKey = useRef(0);
  const recordKey: RecordKey = (record, identity) => {
    if (identity) return identity;
    let key = missingKeys.current.get(record);
    if (!key) { key = `missing-${++nextKey.current}`; missingKeys.current.set(record, key); }
    return key;
  };
  const expanded = report.schema_version === 2;
  return <>
    {expanded ? <p className="diagnostics-observed">Observed at <span>{text(report.observed_at) || "Unknown"}</span></p> : null}
    <div className="diagnostics-observations">
      <section className="diagnostics-panel diagnostics-observation"><h2>Database read check</h2><p><span className={report.database === "ready" ? "diagnostics-success" : "diagnostics-symbol"} aria-hidden="true">{report.database === "ready" ? "✓" : "?"}</span>{report.database === "ready" ? "Read succeeded" : report.database === "failed" ? "Read failed — inspect server storage and its private structured log." : "Unknown"}</p></section>
      <section className="diagnostics-panel diagnostics-observation"><h2>Owner credential</h2><p><span className="diagnostics-symbol" aria-hidden="true">ⓘ</span>{report.credential_store === "owner-credential-ready" ? "Server owner credential loaded" : "Unknown"}</p></section>
      <section className="diagnostics-panel diagnostics-observation"><h2>Inference probes</h2><p><span className="diagnostics-neutral" aria-hidden="true">−</span>{report.inference_probes === false ? "Not performed" : "Unknown"}</p></section>
    </div>
    <div className="diagnostics-sections">
      <section className="diagnostics-panel" aria-label="Server information"><h2>Server information</h2><dl className="diagnostics-facts"><dt>Server version</dt><dd>{text(report.version) || "Unknown"}</dd>{expanded ? <><dt>Server platform</dt><dd>{text(report.os) || "Unknown"} / {text(report.architecture) || "Unknown"}</dd><dt>Protocol version</dt><dd>{Number.isSafeInteger(report.protocol_version) && Number(report.protocol_version) > 0 ? Number(report.protocol_version) : "Unknown"}</dd><dt>Database schema</dt><dd>{Number.isSafeInteger(report.database_schema_version) && Number(report.database_schema_version) > 0 ? Number(report.database_schema_version) : "Unknown"}</dd></> : null}<dt>Bound endpoint</dt><dd>{text(report.listener) || "Unknown"}</dd></dl><p>{ownerCaveat}</p><Disclosure title="Server identity"><p>{text(report.server_id) || "Unknown"}</p></Disclosure></section>
      {expanded ? <><Storage value={object(report.storage)} /><Workers report={report} recordKey={recordKey} /><Credentials report={report} recordKey={recordKey} /></> : <p>This server returned a legacy report. Capacity and protected-storage health are not yet reported.</p>}
    </div>
  </>;
}
export function Doctor({ active, visible = true }: { active: boolean; visible?: boolean }) {
  const result = useQuery(SystemQuery.getDoctor, {}, { enabled: active });
  const [opening, setOpening] = useState(0);
  useEffect(() => {
    // Category inactivity is not a close. Reset only this presentation subtree
    // when the actual Settings modal hides; queries and operations stay owned.
    if (!visible) setOpening((value) => value + 1);
  }, [visible]);
  const { report, unsupported } = useMemo(() => {
    let report: Document | undefined, unsupported = false;
    if (result.data && result.data.reportJson.byteLength <= 1 << 20) {
      try {
        const candidate = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(result.data.reportJson)));
        unsupported = candidate.schema_version !== undefined && candidate.schema_version !== 2;
        if (!unsupported && text(candidate.version)) report = candidate;
      } catch { /* Malformed reports cannot establish health. */ }
    }
    return { report, unsupported };
  }, [result.data]);
  // Unknown server identity is response-scoped, never a shared fallback key.
  const unknownServer = useRef({ report, key: 0 });
  if (unknownServer.current.report !== report) unknownServer.current = { report, key: unknownServer.current.key + 1 };
  return <section className="diagnostics">
    <header className="diagnostics-header"><div><h1 aria-live="polite" aria-atomic="true">Diagnostics</h1><p>{scope}</p></div><button disabled={!active || result.isFetching} onClick={() => void result.refetch()}>Refresh diagnostics</button></header>
    <Problem error={result.error} />{result.isFetching && active ? <p role="status">Reading server diagnostics…</p> : null}{result.error && report ? <p role="alert">Refresh failed. The report below is the last returned observation.</p> : null}
    {report ? <Report key={JSON.stringify([opening, text(report.server_id) || unknownServer.current.key])} report={report} /> : result.data ? <p role="alert">{unsupported ? "This diagnostic report version is unsupported. No health result can be inferred." : "The diagnostic report is unavailable or malformed. No health result can be inferred."}</p> : null}
    <p className="diagnostics-guidance">{ownerCaveat} Use AI accounts for validation and Runner Devices for discovery and connection recovery.</p>
  </section>;
}
