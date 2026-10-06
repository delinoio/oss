import { LocalizedText, copy, displayLocale, useLocale } from "./localization";
import { SettingsHeading } from "./settings-presentation";
import "./doctor.css";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { SystemQuery } from "@delinoio/delidev-api-client";
import { items, object, text, type Document } from "./documents";
import { ServiceProblem, Problem  } from "./ui";

enum DiagnosticState { Observed = "observed", Unavailable = "unavailable", Unconfigured = "unconfigured", NotApplicable = "not-applicable", Failed = "failed", Superseded = "superseded" }
const stateNames: Record<DiagnosticState, string> = {
  get [DiagnosticState.Observed]() { return copy("doctor.observed_64fa8a"); }, get [DiagnosticState.Unavailable]() { return copy("doctor.unavailable_ca1844"); }, get [DiagnosticState.Unconfigured]() { return copy("doctor.notConfigured_dd1841"); }, get [DiagnosticState.NotApplicable]() { return copy("doctor.notApplicable_5237c9"); }, get [DiagnosticState.Failed]() { return copy("doctor.failed_031a8f"); }, get [DiagnosticState.Superseded]() { return copy("doctor.connectionChangedDuringInspection_419fb5"); },
};
const ownerCaveat = () => copy("doctor.extra.abdc75b60aba");
function observation(value: unknown, success = copy("doctor.extra.64fa8a14a90f")) {
  const result = object(value), state = text(result.state);
  const name = Object.hasOwn(stateNames, state) ? stateNames[state as DiagnosticState] : copy("doctor.extra.b764cdc0eab7");
  return <div className="diagnostics-result"><strong>{state === DiagnosticState.Observed ? success : name}</strong>{text(result.code) ? copy("doctor.message_2fa20b", { v0: text(result.code) }) : ""}{text(result.guidance) ? <ServiceProblem code={text(result.code) || text(result.problem_code)}><p>{text(result.guidance)}</p></ServiceProblem> : null}</div>;
}
function decimal(value: unknown): string | undefined {
  if (typeof value !== "string" || !/^(0|[1-9][0-9]{0,19})$/.test(value)) return;
  const number = BigInt(value);
  if (number > 18446744073709551615n) return;
  return number.toLocaleString(displayLocale());
}
function bytes(value: unknown) { const number = decimal(value); return number === undefined ? copy("doctor.extra.ca1844969742") : copy("doctor.sentence.4c59147c97a5", { v0: number }); }
function completeness(more: unknown, kind: string) {
  if (more === true) return <p className="notice"><LocalizedText id="doctor.onlyTheFirst50AreIncluded_eb0ea8" components={{ s0: <>{kind}</> }} /></p>;
  return more === false ? null : <p>{copy("doctor.inventoryCompletenessIsUnknown_44746c")}</p>;
}
function Disclosure({ title, children }: { title: string; children: ReactNode }) {
  useLocale();
  return <details className="diagnostics-disclosure"><summary>{title}</summary>{children}</details>;
}
function Storage({ value }: { value: Document }) {
  useLocale();
  const resources = Array.isArray(value.resources) ? value.resources : undefined;
  return <section className="diagnostics-panel" aria-label={copy("doctor.storageDiagnostics_fe1577")}>
    <h2>{copy("doctor.serverStorage_3e7362")}</h2>{observation(value.result)}
    <dl className="diagnostics-facts"><dt>{copy("doctor.databaseFile_812d99")}</dt><dd>{bytes(value.database_bytes)}</dd><dt>{copy("doctor.writeAheadLog_ccc16f")}</dt><dd>{bytes(value.wal_bytes)}</dd><dt>{copy("doctor.logicalDatabaseSize_e1995b")}</dt><dd>{bytes(value.logical_database_bytes)}</dd><dt>{copy("doctor.filesystemCapacity_b12161")}</dt><dd>{bytes(value.volume_capacity_bytes)}</dd><dt>{copy("doctor.availableToTheServer_c4503b")}</dt><dd>{bytes(value.volume_available_bytes)}</dd></dl>
    <p>{copy("doctor.sizesAreSampledSeparatelyTheyCannot_10fb3e")}</p>
    {resources ? resources.length ? null : <p>{copy("doctor.noRetainedResourcesWereCounted_24a23d")}</p> : <p>{copy("doctor.resourceCountsAreUnavailable_55abbd")}</p>}
    <Disclosure title={copy("doctor.retainedResources_0846dd")}>{resources?.length ? <ul>{resources.slice(0, 100).map((entry, index) => { const count = object(entry); return <li key={index}>{text(count.kind) || copy("doctor.extra.b32d920f3e25")}: {decimal(count.count) ?? copy("doctor.unknown_b764cd")}</li>; })}</ul> : null}</Disclosure>
  </section>;
}
const installationStates: Record<string, string> = { get unchecked() { return copy("doctor.extra.d16948e73a68"); }, get detected() { return copy("doctor.extra.4db4587e4b04"); }, get missing() { return copy("doctor.extra.1c2850a512c4"); }, get "permission-denied"() { return copy("doctor.extra.59b77dbd5bd2"); }, get incompatible() { return copy("doctor.extra.8777e3a9e3ec"); }, get failed() { return copy("doctor.extra.450e4a86d32c"); } };
function Installation({ value, secondary = false }: { value: Document; secondary?: boolean }) {
  useLocale();
  if (secondary) return <li><strong>{text(value.harness) || copy("doctor.extra.22b7d757d71d")}</strong><p><LocalizedText id="doctor.lastDiscovery_d132d5" components={{ s0: <>{text(value.observed_at) || copy("doctor.extra.1d3efcd60643")}</> }} /></p><p><LocalizedText id="doctor.reportedCapabilities_24b97b" components={{ s0: <>{items(value.capabilities).map(text).filter(Boolean).join(", ") || copy("doctor.extra.dc937b598926")}</> }} /></p></li>;
  const protocol = value.protocol_verified === true && value.protocol_state === "verified" ? copy("doctor.extra.496da28ce709") : value.protocol_state === "failed" ? copy("doctor.extra.9a71446fe830") : value.protocol_state === "unsupported" ? copy("doctor.extra.dfbc698f5beb") : value.protocol_verified === false && !value.protocol_state ? copy("doctor.extra.b5efd1b1759a") : copy("doctor.extra.248274344040");
  const state = text(value.state);
  return <li><strong>{text(value.harness) || copy("doctor.extra.22b7d757d71d")}</strong>: {Object.hasOwn(installationStates, state) ? installationStates[state] : copy("doctor.unknownInstallationState_648bed")}{text(value.version) ? copy("doctor.message_2fa20b", { v0: text(value.version) }) : ""}<p>{protocol}</p>{text(value.problem_code) ? <p>{text(value.problem_code)}</p> : null}{text(value.guidance) ? <ServiceProblem code={text(value.code) || text(value.problem_code)}><p>{text(value.guidance)}</p></ServiceProblem> : null}</li>;
}
type RecordKey = (record: Document, identity: string) => string;
function Workers({ report, recordKey }: { report: Document; recordKey: RecordKey }) {
  useLocale();
  return <section className="diagnostics-panel diagnostics-wide" aria-label={copy("doctor.workerDiagnostics_9e4c35")}><h2>{copy("doctor.runnerDevices_a176a8")}</h2><p>{copy("doctor.theseAreRetainedWorkerReportsAnd_c6d5b0")}</p>{completeness(report.more_machines, copy("doctor.extra.7a1ec9fe6c3b"))}{Array.isArray(report.machines) ? report.machines.length ? <div className="diagnostics-records">{report.machines.slice(0, 50).map((entry) => {
    const machine = object(entry), installations = items(machine.installations).slice(0, 4);
    return <article className="diagnostics-record" key={recordKey(machine, text(machine.machine_id))}><h3>{text(machine.name) || copy("doctor.extra.cdc2f44852c6")}</h3><p><LocalizedText id="doctor.reportedVersion_e37bb8" components={{ s0: <>{text(machine.version) || copy("doctor.extra.b764cdc0eab7")}</>, s1: <>{text(machine.os) || copy("doctor.extra.5bf49f5564d9")}</>, s2: <>{text(machine.architecture) || copy("doctor.extra.5181df98c1d2")}</> }} /></p><p>{machine.active_stream === true ? copy("doctor.connectedAtObservation_2685ce") : machine.active_stream === false ? copy("doctor.noActiveStreamObserved_57ef54") : copy("doctor.connectionUnknown_e68b86")} · {machine.disabled === true ? copy("doctor.disabled_75081b") : machine.disabled === false ? copy("doctor.enabled_92c1cd") : copy("doctor.availabilityUnknown_2e0078")}</p><p><LocalizedText id="doctor.lastContact_052200" components={{ s0: <>{text(machine.last_seen) && !text(machine.last_seen).startsWith("0001-") ? text(machine.last_seen) : copy("doctor.notObserved_1d3efc")}</> }} /></p>
      <ul className="diagnostics-installations">{installations.map((value, key) => <Installation key={key} value={object(value)} />)}</ul>
      <Disclosure title={copy("doctor.installationDetails_7ee3b5")}><p><LocalizedText id="doctor.machineIdentity_a4bfec" components={{ s0: <>{text(machine.machine_id) || copy("doctor.extra.a1dddb1d9c42")}</> }} /></p><ul>{installations.map((value, key) => <Installation key={key} value={object(value)} secondary />)}</ul></Disclosure>
    </article>;
  })}</div> : <p>{copy("doctor.noWorkersAreRegistered_9b7693")}</p> : <p>{copy("doctor.workerObservationsAreUnavailable_ef7c26")}</p>}</section>;
}
function Credentials({ report, recordKey }: { report: Document; recordKey: RecordKey }) {
  useLocale();
  return <section className="diagnostics-panel diagnostics-wide" aria-label={copy("doctor.protectedCredentialDiagnostics_8f870f")}><h2>{copy("doctor.protectedAccountStorage_fd07a5")}</h2><p>{copy("doctor.onlyEachCurrentAccountConnectionS_aa0ac9")}</p>{completeness(report.more_credentials, "accounts")}{Array.isArray(report.credentials) ? report.credentials.length ? <div className="diagnostics-records">{report.credentials.slice(0, 50).map((entry) => {
    const credential = object(entry), account = text(credential.account_id), connection = text(credential.connection_id);
    return <article className="diagnostics-record" key={recordKey(credential, account && connection ? JSON.stringify([account, connection]) : "")}><h3><LocalizedText id="doctor.account_e07497" components={{ s0: <>{account || copy("doctor.extra.b764cdc0eab7")}</> }} /></h3>{observation(credential.result, copy("doctor.extra.0abb2cc2f20e"))}{connection ? <Disclosure title={copy("doctor.connectionIdentity_527d26")}><p><LocalizedText id="doctor.connection_654eff" components={{ s0: <>{connection}</> }} /></p></Disclosure> : null}</article>;
  })}</div> : <p>{copy("doctor.noAccountsAreConfiguredCredentialStore_7a1e48")}</p> : <p>{copy("doctor.protectedStorageObservationsAreUnavailable_d6d0ba")}</p>}</section>;
}
function Report({ report }: { report: Document }) {
  useLocale();
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
    {expanded ? <p className="diagnostics-observed"><LocalizedText id="doctor.observedAt_2044a6" components={{ s0: <span>{text(report.observed_at) || copy("doctor.extra.b764cdc0eab7")}</span> }} /></p> : null}
    <div className="diagnostics-observations">
      <section className="diagnostics-panel diagnostics-observation"><h2>{copy("doctor.databaseReadCheck_465f92")}</h2><p><span className={report.database === "ready" ? "diagnostics-success" : "diagnostics-symbol"} aria-hidden="true">{report.database === "ready" ? "✓" : "?"}</span>{report.database === "ready" ? copy("doctor.readSucceeded_14f977") : report.database === "failed" ? copy("doctor.readFailedInspectServerStorageAnd_06b368") : copy("doctor.unknown_b764cd")}</p></section>
      <section className="diagnostics-panel diagnostics-observation"><h2>{copy("doctor.ownerCredential_028058")}</h2><p><span className="diagnostics-symbol" aria-hidden="true">ⓘ</span>{report.credential_store === "owner-credential-ready" ? copy("doctor.serverOwnerCredentialLoaded_ed8968") : copy("doctor.unknown_b764cd")}</p></section>
      <section className="diagnostics-panel diagnostics-observation"><h2>{copy("doctor.inferenceProbes_3cbb76")}</h2><p><span className="diagnostics-neutral" aria-hidden="true">−</span>{report.inference_probes === false ? copy("doctor.notPerformed_c48729") : copy("doctor.unknown_b764cd")}</p></section>
    </div>
    <div className="diagnostics-sections">
      <section className="diagnostics-panel" aria-label={copy("doctor.serverInformation_078792")}><h2>{copy("doctor.serverInformation_078792")}</h2><dl className="diagnostics-facts"><dt>{copy("doctor.serverVersion_3f34bb")}</dt><dd>{text(report.version) || copy("doctor.extra.b764cdc0eab7")}</dd>{expanded ? <><dt>{copy("doctor.serverPlatform_9d0c00")}</dt><dd>{text(report.os) || copy("doctor.extra.b764cdc0eab7")} / {text(report.architecture) || copy("doctor.extra.b764cdc0eab7")}</dd><dt>{copy("doctor.protocolVersion_cdd735")}</dt><dd>{Number.isSafeInteger(report.protocol_version) && Number(report.protocol_version) > 0 ? Number(report.protocol_version) : copy("doctor.unknown_b764cd")}</dd><dt>{copy("doctor.databaseSchema_bf3efc")}</dt><dd>{Number.isSafeInteger(report.database_schema_version) && Number(report.database_schema_version) > 0 ? Number(report.database_schema_version) : copy("doctor.unknown_b764cd")}</dd></> : null}<dt>{copy("doctor.boundEndpoint_5501d6")}</dt><dd>{text(report.listener) || copy("doctor.extra.b764cdc0eab7")}</dd></dl><p>{ownerCaveat()}</p><Disclosure title={copy("doctor.serverIdentity_fa4fb0")}><p>{text(report.server_id) || copy("doctor.extra.b764cdc0eab7")}</p></Disclosure></section>
      {expanded ? <><Storage value={object(report.storage)} /><Workers report={report} recordKey={recordKey} /><Credentials report={report} recordKey={recordKey} /></> : <p>{copy("doctor.thisServerReturnedALegacyReport_0a90d0")}</p>}
    </div>
  </>;
}
export enum DoctorTitle { Diagnostics = "Diagnostics", ConnectionDiagnostics = "Connection & diagnostics" }
export function Doctor({ active, visible = true, title = DoctorTitle.Diagnostics, connectionControls }: { active: boolean; visible?: boolean; title?: DoctorTitle; connectionControls?: ReactNode }) {
  useLocale();
  const result = useQuery(SystemQuery.getDoctor, {}, { enabled: active });
  const [opening, setOpening] = useState(0);
  useEffect(() => {
    // Category inactivity is not a close. Reset only this presentation subtree
    // when the actual Settings visit ends; queries and operations stay owned.
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
    <SettingsHeading title={copy(title === DoctorTitle.Diagnostics ? "doctor.title.diagnostics" : "doctor.title.connection")} description={copy("doctor.readOnlyObservationsFromTheSelected_9e6c27")} scope={copy("doctor.attribute.7ebf691bfd08")} actions={<button disabled={!active || result.isFetching} onClick={() => void result.refetch()}>{copy("doctor.refreshDiagnostics_7bce98")}</button>} />
    {connectionControls ? <section aria-label={copy("doctor.connection_639a40")}><h2>{copy("doctor.connection_639a40")}</h2>{connectionControls}</section> : null}
    <Problem error={result.error} />{result.isFetching && active ? <p role="status">{copy("doctor.readingServerDiagnostics_f724cd")}</p> : null}{result.error && report ? <p role="alert">{copy("doctor.refreshFailedTheReportBelowIs_f8d24e")}</p> : null}
    {report ? <Report key={JSON.stringify([opening, text(report.server_id) || unknownServer.current.key])} report={report} /> : result.data ? <p role="alert">{unsupported ? copy("doctor.thisDiagnosticReportVersionIsUnsupported_46df0e") : copy("doctor.theDiagnosticReportIsUnavailableOr_c9eb5b")}</p> : null}
    <p className="diagnostics-guidance"><LocalizedText id="doctor.useAiAccountsForValidationAnd_7713b8" components={{ s0: <>{ownerCaveat()}</> }} /></p>
  </section>;
}
