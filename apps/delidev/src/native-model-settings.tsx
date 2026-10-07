import { useRunnerRemediation } from "./runner-remediation";
import { RunnerWorkflow, useRunnerPreference } from "./runner-device-preferences";
import { OperationStatus } from "./jobs";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { useNativeModelPages } from "./model-pagination";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { formatTimestamp } from "./localization";
import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, FailureCode, NativeModelQuery, SystemCapability, SystemQuery, newRequestId, isEntityId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, text, type Document } from "./documents";
import { ResourceChoice } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { ServiceProblem, Failure, Problem, failureSummary  } from "./ui";

export function NativeModelSettings({ active, createModel, selectedAccounts, pendingOperation }: { active: boolean; createModel: (data: Document) => void; selectedAccounts?: Resource[]; pendingOperation?: (pending: boolean) => void }) {
  useLocale();
  const inspection = useRunnerRemediation();
  const [opened, setOpened] = useState(false);
  const [machine, setMachine] = useState<Resource>();
  const runner = useRunnerPreference(RunnerWorkflow.NativeObservation, active && opened, row => { const data = document(row); return items(data.worker_capabilities).includes("native-codex-model-discovery-v1") && items(data.installations).map(object).some(installation => installation.harness === "codex" && installation.state === "detected"); });
  const runnerTouched = useRef(false);
  const [account, setAccount] = useState<Resource>();
  const [hidden, setHidden] = useState(false);
  const [jobID, setJobID] = useState("");
  const [retainedJobID, setRetainedJobID] = useState("");
  const [lookup, setLookup] = useState("");
  const [observationID, setObservationID] = useState("");
  const listRoot = useRef<HTMLDivElement>(null);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active && opened });
  const supported = status.data?.capabilities.includes(SystemCapability.NATIVE_CODEX_MODEL_DISCOVERY_V1) === true;
  const discovery = useRetainedMutation("native-models:discover", NativeModelQuery.discoverNativeModels, (result, request) => {
    if (result.job && isEntityId(result.job.id) && result.job.revision > 0n && supportsResourceSchema(result.job)) { runner.remember(request.mutation?.id ?? ""); }
    if (result.job) { setRetainedJobID(result.job.id); setJobID(result.job.id); setObservationID(""); }
  }, (result, request) => {
    const scope = object(document(result.job).input);
    return document(result.job).type === "native-codex-models" && scope.machine_id === request.mutation?.id && scope.account_id === request.accountId;
  });
  const operation = useQuery(NativeModelQuery.getNativeModelObservation, { jobId: jobID }, { enabled: active && opened && supported && Boolean(jobID), refetchInterval: (query) => ["queued", "claimed"].includes(text(document(query.state.data?.job).state)) ? 1000 : false });
  const cancellation = useRetainedMutation(`native-models:cancel:${jobID}`, NativeModelQuery.cancelNativeModelDiscovery, () => void operation.refetch(), (result, request) => result.job?.id === request.mutation?.id);
  const candidate = operation.data?.job;
  const job = candidate?.id === jobID && candidate.kind === EntityKind.JOB && document(candidate).type === "native-codex-models" ? candidate : undefined;
  const state = text(document(job).state);
  const source = observationID || (state === "succeeded" ? jobID : "");
  // A failed manual lookup has not acquired an accepted operation. Once a
  // discovery acknowledgment or valid lookup retains it, read errors cannot
  // release that original unsettled operation for replacement.
  useEffect(() => { if (job) setRetainedJobID(job.id); }, [job]);
  const unverifiedLookupFailed = !operation.isFetching && Boolean(operation.error || operation.data && !job);
  const observationPending = Boolean(jobID && (retainedJobID === jobID || !unverifiedLookupFailed) && !["succeeded", "failed", "canceled"].includes(state));
  const blocked = discovery.busy || discovery.uncertain || cancellation.busy || cancellation.uncertain || inspection?.pendingFor(machine?.id ?? "") === true;
  useEffect(() => { if (active && !runnerTouched.current && !blocked && !observationPending && !machine && runner.suggestion) setMachine(runner.suggestion); }, [active, runner.suggestion, blocked, observationPending, machine]);
  const models = useNativeModelPages(source, active && opened && supported && Boolean(source) && !blocked);
  const selectedObservation = models.payloadPages.flatMap(page => page.payload)[0]?.job ?? (job?.id === source && state === "succeeded" ? job : operation.data?.lastSuccess?.id === source ? operation.data.lastSuccess : undefined);
  const observedScope = object(document(selectedObservation).input);
  const accountSelected = !selectedAccounts || selectedAccounts.some(row => row.id === account?.id);

  useEffect(() => { pendingOperation?.(blocked || observationPending); return () => pendingOperation?.(false); }, [blocked, observationPending, pendingOperation]);
  const reset = () => { setRetainedJobID(""); setJobID(""); setObservationID(""); };
  return <details onToggle={(event) => setOpened(event.currentTarget.open)}><summary>{copy("native-model-settings.nativeCodexModelObservations_e3a909")}</summary>{opened ? <section aria-label={copy("native-model-settings.nativeCodexModelObservations_e3a909")}>
    <h2>{copy("native-model-settings.observeNativeCodexModels_7d4b36")}</h2>
    <p>{copy("native-model-settings.chooseARunnerDeviceWithCodex_8f2ab4")}</p>
    <Problem error={status.error} actions={<button type="button" disabled={!active || blocked || observationPending || status.isFetching} onClick={() => void status.refetch()}>{copy("ui.retryCurrentRead")}</button>} />
    {status.data && !supported ? <p>{copy("native-model-settings.thisServerDoesNotSupportNative_b59968")}</p> : null}
    <fieldset disabled={!supported || blocked || observationPending}>
      <legend>{copy("native-model-settings.observationScope_329506")}</legend>
      <ResourceChoice label={copy("native-model-settings.runnerDevice_37efe3")} kind={EntityKind.MACHINE} value={machine?.id ?? ""} active={active && supported} change={(_id, _data, row) => { runnerTouched.current = true; runner.touch(); setMachine(row); reset(); }} />
      {selectedAccounts ? <label>{copy("native-model-settings.connectedSelectedAccount")}<select value={account?.id ?? ""} onChange={event => { setAccount(selectedAccounts.find(row => row.id === event.target.value)); reset(); }}><option value="">{copy("native-model-settings.selectAccount")}</option>{selectedAccounts.map(row => <option key={row.id} value={row.id}>{text(document(row).alias) || row.id}</option>)}</select></label> : <ResourceChoice label={copy("native-model-settings.connectedAccount_3903f0")} kind={EntityKind.ACCOUNT} value={account?.id ?? ""} active={active && supported} change={(_id, _data, row) => { setAccount(row); reset(); }} />}
      <label><input type="checkbox" checked={hidden} onChange={(event) => { setHidden(event.target.checked); reset(); }} />{copy("native-model-settings.includeHiddenModels_64799e")}</label>
      <button type="button" disabled={!machine || !account || !accountSelected || !document(account).connection} onClick={() => { runner.touch(); void discovery.send({ mutation: { requestId: newRequestId(), id: machine!.id, expectedRevision: machine!.revision }, accountId: account!.id, accountRevision: account!.revision, includeHidden: hidden }); }}>{copy("native-model-settings.observeModels_cf8865")}</button>
    </fieldset>
    {runner.guidance}<Problem error={discovery.error} />
    {discovery.uncertain ? <button type="button" disabled={discovery.busy} onClick={discovery.retry}>{copy("native-model-settings.retryTheSameObservationRequest_0b6c58")}</button> : null}
    <label>{copy("native-model-settings.originalObservationId_949ead")}<input value={lookup} maxLength={36} disabled={blocked || observationPending} onChange={(event) => setLookup(event.target.value)} /></label>
    <button type="button" disabled={!supported || blocked || observationPending || !lookup} onClick={() => { setJobID(lookup); setObservationID(""); }}>{copy("native-model-settings.inspectObservation_ded69a")}</button>
    {job && (state !== "succeeded" || text(object(document(job).problem).message)) ? <div><OperationStatus state={state} />
      {["queued", "claimed"].includes(state) ? <button type="button" disabled={blocked} onClick={() => void cancellation.send({ mutation: { requestId: newRequestId(), id: job.id, expectedRevision: job.revision } })}>{copy("native-model-settings.cancelObservation_0f4be7")}</button> : null}
      {text(object(document(job).problem).message) ? <ServiceProblem code={text(object(document(job).problem).code) || text(object(document(job).problem).problem_code)}><p>{failureSummary(text(object(document(job).problem).code) || text(object(document(job).problem).problem_code))}</p></ServiceProblem> : null}
      {state !== "succeeded" && operation.data?.lastSuccess ? <button type="button" onClick={() => { setObservationID(operation.data!.lastSuccess!.id); }}>{copy("native-model-settings.showLastSuccessfulObservation_e2c0d6")}</button> : null}
    </div> : null}
    <Problem error={operation.error} />{operation.error || job && !["queued", "claimed", "succeeded", "failed", "canceled"].includes(state) || operation.data && !job ? <button type="button" disabled={!active || operation.isFetching} onClick={() => void operation.refetch()}>{copy("jobs.retryStatusRead")}</button> : null}<Problem error={cancellation.error} />
    {cancellation.uncertain ? <button type="button" disabled={cancellation.busy} onClick={cancellation.retry}>{copy("native-model-settings.retryTheSameCancellation_0bc7e1")}</button> : null}
    <Failure failure={models.error?.failure} />
    {models.error?.failure.code === FailureCode.Internal ? <p role="alert">{copy("native-model-settings.theObservationPageIsMalformedNo_ac73dd")}</p> : null}
    {models.loaded && !models.rows.length && !models.error ? <p>{copy("native-model-settings.noNativeModelsInThisObservation_a24b4f")}</p> : null}
    {models.loaded && selectedObservation ? <><p>{copy("native-model-settings.observedAt", { v0: formatTimestamp(text(object(document(selectedObservation).output).observed_at)) || copy("native-model-settings.extra.ca1844969742") })}</p><p><LocalizedText id="native-model-settings.sourceObservationAccountInstallationGeneration_bf070d" components={{ s0: <>{selectedObservation.id}</>, s1: <>{text(observedScope.account_id)}</>, s2: <>{String(observedScope.installation_generation ?? copy("native-model-settings.extra.ca1844969742"))}</> }} /></p></> : null}
    <div ref={listRoot} className="conversation-page-scroll"><ScrollPayloadWindow identity={paginationIdentity} revision={paginationRevision} query={models} root={listRoot} active={active && opened && !blocked}>{payload => {
      if (!payload.length) return null;
      const page = payload[0], entries = payload.map(row => row.entry), observedScope = object(document(page.job).input);
      const canRegister = accountSelected && !models.error && observedScope.account_id === account?.id && observedScope.machine_id === machine?.id && observedScope.provider_id === document(account).provider_id;
      return <>
      {entries.length === 0 ? <p>{copy("native-model-settings.noNativeModelsInThisObservation_a24b4f")}</p> : entries.map((entry) => <article key={text(entry.id)}>
        <h3>{text(entry.display_name)}</h3><p><LocalizedText id="native-model-settings.pickerIdExecutableModel_ea304e" components={{ s0: <>{text(entry.id)}</>, s1: <>{text(entry.model)}</> }} /></p>
        <p>{text(entry.description)}</p><p><LocalizedText id="native-model-settings.reasoningInputServiceTiers_aa5c58" components={{ s0: <>{items(entry.reasoning).map(text).join(", ")}</>, s1: <>{items(entry.modalities).map(text).join(", ")}</>, s2: <>{items(entry.service_tiers).map(text).join(", ")}</>, s3: <>{entry.hidden === true ? copy("native-model-settings.hidden_7e6fef") : copy("native-model-settings.visible_8411f5")}</> }} /></p>
        <button type="button" disabled={!canRegister || blocked} onClick={() => createModel({ name: text(entry.display_name), provider_id: text(observedScope.provider_id), native_id: text(entry.model), alias: "", harnesses: ["codex"], hidden: false, order: 0, manual: true, new: false, metadata_source: "unknown" })}>{selectedAccounts ? <>{copy("native-model-settings.useModel")} {text(entry.display_name)}…</> : <LocalizedText id="native-model-settings.register_55d230" components={{ s0: <>{text(entry.display_name)}</> }} />}</button>
      </article>)}
    </>;
    }}</ScrollPayloadWindow><ScrollContinuation query={models} root={listRoot} active={active && opened && supported && Boolean(source) && !blocked} label={copy("native-model-settings.nativeCodexModelObservations_e3a909")} /></div>
  </section> : null}</details>;
}
