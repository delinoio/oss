import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SystemQuery, isEntityId, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, text, type Document } from "./documents";
import { Problem } from "./ui";

enum CheckState { NotChecked = "Not checked", Observed = "Observed", Setup = "Needs setup", Unknown = "Unknown", Failed = "Check failed" }
const harnesses = new Set(["codex", "claude-code", "opencode", "grok-build"]);
const healthStates = new Set(["disconnected", "unverified", "ready", "expired", "revoked", "failed"]);
function reportFrom(bytes: Uint8Array | undefined, server: string): Document | undefined {
  if (!bytes || bytes.byteLength > 1 << 20 || !isEntityId(server)) return;
  try {
    const report = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)));
    if (report.schema_version !== 2 || report.server_id !== server || report.inference_probes !== false || !Number.isFinite(Date.parse(text(report.observed_at))) || !Array.isArray(report.machines) || report.machines.length > 50 || typeof report.more_machines !== "boolean") return;
    const seen = new Set();
    for (const entry of report.machines) {
      const machine = object(entry);
      if (!isEntityId(text(machine.machine_id)) || seen.has(machine.machine_id) || typeof machine.disabled !== "boolean" || typeof machine.active_stream !== "boolean" || !Array.isArray(machine.installations) || machine.installations.length > 4) return;
      seen.add(machine.machine_id);
      const installed = new Set();
      for (const item of machine.installations) {
        const installation = object(item);
        if (!harnesses.has(text(installation.harness)) || installed.has(installation.harness) || typeof installation.protocol_verified !== "boolean") return;
        installed.add(installation.harness);
      }
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
      <Step label="Execution Worker and harness" state={observationState(doctor.error, Boolean(report), verified.length, report?.more_machines)}><p>{report ? `${connected.length} enabled Worker(s) had an active connection; ${verified.length} retained harness handshake(s) were verified on those Workers.` : "Pair a Worker and explicitly discover its installed harness in Settings."}</p>{report?.more_machines ? <p>Only the first 50 Workers were inspected; absence from this page does not establish missing setup.</p> : null}<p>A connection or past handshake does not establish current account compatibility or execution readiness.</p></Step>
      <Step label="AI account" state={observationState(accounts.error, Boolean(accountRows), readyAccounts, accounts.data?.nextPageToken)}><p>{accountRows ? `${accountRows.length} account(s) inspected; ${readyAccounts} enabled account(s) have a connection and saved ready status.` : "Connect and validate an account explicitly in Settings."}</p>{accounts.data?.nextPageToken ? <p>More accounts exist. Open AI accounts for the remaining records.</p> : null}<p>Saved status does not prove current quota or provider access.</p></Step>
      <Step label="Agent Worker configuration" state={observationState(agents.error, Boolean(agentRows), agentRows?.length ?? 0, agents.data?.nextPageToken)}><p>{agentRows ? `${agentRows.length} Agent Worker configuration(s) inspected.` : "Configure an Agent Worker with its selected model, account and harness."}</p>{agents.data?.nextPageToken ? <p>More Agent Workers exist. Open Settings for the remaining records.</p> : null}<p>Choose a project for repository work, or General Chat. Session creation validates the exact selections again.</p></Step>
    </ol>
    {checked && !loading ? <p>These are separate observations, not a successful execution test. Refresh after setup changes.</p> : null}
  </section>;
}
