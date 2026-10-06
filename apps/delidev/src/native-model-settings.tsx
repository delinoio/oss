import { formatTimestamp } from "./localization";
import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, NativeModelQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, text, type Document } from "./documents";
import { ResourceChoice } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { ServiceProblem, More, Problem  } from "./ui";

export function NativeModelSettings({ active, createModel }: { active: boolean; createModel: (data: Document) => void }) {
export function NativeModelSettings({ active, createModel, selectedAccounts, pendingOperation }: { active: boolean; createModel: (data: Document) => void; selectedAccounts?: Resource[]; pendingOperation?: (pending: boolean) => void }) {
  useLocale();
  const [opened, setOpened] = useState(false);
  const [machine, setMachine] = useState<Resource>();
  const [account, setAccount] = useState<Resource>();
  const [hidden, setHidden] = useState(false);
  const [jobID, setJobID] = useState("");
  const [lookup, setLookup] = useState("");
  const [observationID, setObservationID] = useState("");
  const [page, setPage] = useState("");
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active && opened });
  const supported = status.data?.capabilities.includes(SystemCapability.NATIVE_CODEX_MODEL_DISCOVERY_V1) === true;
  const discovery = useRetainedMutation("native-models:discover", NativeModelQuery.discoverNativeModels, (result) => {
    if (result.job) { setJobID(result.job.id); setObservationID(""); setPage(""); }
  }, (result, request) => {
    const scope = object(document(result.job).input);
    return document(result.job).type === "native-codex-models" && scope.machine_id === request.mutation?.id && scope.account_id === request.accountId;
  });
  const operation = useQuery(NativeModelQuery.getNativeModelObservation, { jobId: jobID }, { enabled: active && opened && supported && Boolean(jobID), refetchInterval: (query) => ["queued", "claimed"].includes(text(document(query.state.data?.job).state)) ? 1000 : false });
  const cancellation = useRetainedMutation(`native-models:cancel:${jobID}`, NativeModelQuery.cancelNativeModelDiscovery, () => void operation.refetch(), (result, request) => result.job?.id === request.mutation?.id);
  const job = operation.data?.job;
  const state = text(document(job).state);
  const source = observationID || (state === "succeeded" ? jobID : "");
  const models = useQuery(NativeModelQuery.listNativeModels, { jobId: source, pageSize: 50, pageToken: page }, { enabled: active && opened && supported && Boolean(source) });
  let entries: Document[] = [];
  let malformed = false;
  if (models.data) {
    try {
      if (models.data.modelsJson.byteLength > 768 * 1024 || models.data.job?.id !== source) throw new Error();
      const data: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(models.data.modelsJson));
      if (!Array.isArray(data) || data.length > 200 || data.some((entry) => !entry || typeof entry !== "object" || typeof entry.id !== "string" || typeof entry.model !== "string" || typeof entry.display_name !== "string" || entry.id.length > 256 || entry.model.length > 256 || entry.display_name.length > 256)) throw new Error();
      entries = data;
    } catch { malformed = true; }
  }
  const observedScope = object(document(models.data?.job).input);
  const accountSelected = !selectedAccounts || selectedAccounts.some(row => row.id === account?.id);
  const canRegister = accountSelected && observedScope.account_id === account?.id && observedScope.machine_id === machine?.id && observedScope.provider_id === document(account).provider_id;
  const blocked = discovery.busy || discovery.uncertain || cancellation.busy || cancellation.uncertain;
  useEffect(() => { pendingOperation?.(blocked); return () => pendingOperation?.(false); }, [blocked, pendingOperation]);
  const reset = () => { setJobID(""); setObservationID(""); setPage(""); };
  return <details onToggle={(event) => setOpened(event.currentTarget.open)}><summary>{copy("native-model-settings.nativeCodexModelObservations_e3a909")}</summary>{opened ? <section aria-label={copy("native-model-settings.nativeCodexModelObservations_e3a909")}>
    <h2>{copy("native-model-settings.observeNativeCodexModels_7d4b36")}</h2>
    <p>{copy("native-model-settings.chooseARunnerDeviceWithCodex_8f2ab4")}</p>
    <Problem error={status.error} />
    {status.data && !supported ? <p>{copy("native-model-settings.thisServerDoesNotSupportNative_b59968")}</p> : null}
    <fieldset disabled={!supported || blocked}>
      <legend>{copy("native-model-settings.observationScope_329506")}</legend>
      <ResourceChoice label={copy("native-model-settings.runnerDevice_37efe3")} kind={EntityKind.MACHINE} value={machine?.id ?? ""} active={active && supported} change={(_id, _data, row) => { setMachine(row); reset(); }} />
      {selectedAccounts ? <label>{copy("native-model-settings.connectedSelectedAccount")}<select value={account?.id ?? ""} onChange={event => { setAccount(selectedAccounts.find(row => row.id === event.target.value)); reset(); }}><option value="">{copy("native-model-settings.selectAccount")}</option>{selectedAccounts.map(row => <option key={row.id} value={row.id}>{text(document(row).alias) || row.id}</option>)}</select></label> : <ResourceChoice label={copy("native-model-settings.connectedAccount_3903f0")} kind={EntityKind.ACCOUNT} value={account?.id ?? ""} active={active && supported} change={(_id, _data, row) => { setAccount(row); reset(); }} />}
      <label><input type="checkbox" checked={hidden} onChange={(event) => { setHidden(event.target.checked); reset(); }} />{copy("native-model-settings.includeHiddenModels_64799e")}</label>
      <button type="button" disabled={!machine || !account || !accountSelected || !document(account).connection} onClick={() => void discovery.send({ mutation: { requestId: newRequestId(), id: machine!.id, expectedRevision: machine!.revision }, accountId: account!.id, accountRevision: account!.revision, includeHidden: hidden })}>{copy("native-model-settings.observeModels_cf8865")}</button>
    </fieldset>
    <Problem error={discovery.error} />
    {discovery.uncertain ? <button type="button" disabled={discovery.busy} onClick={discovery.retry}>{copy("native-model-settings.retryTheSameObservationRequest_0b6c58")}</button> : null}
    <label>{copy("native-model-settings.originalObservationId_949ead")}<input value={lookup} maxLength={36} disabled={blocked} onChange={(event) => setLookup(event.target.value)} /></label>
    <button type="button" disabled={!supported || blocked || !lookup} onClick={() => { setJobID(lookup); setObservationID(""); setPage(""); }}>{copy("native-model-settings.inspectObservation_ded69a")}</button>
    {job ? <div><p role="status"><LocalizedText id="native-model-settings.observation_48f31e" components={{ s0: <>{job.id}</>, s1: <>{state}</>, s2: <>{formatTimestamp(text(object(document(job).output).observed_at))}</> }} /></p>
      <button type="button" disabled={!active || operation.isFetching} onClick={() => void operation.refetch()}>{copy("native-model-settings.refreshObservationStatus_2714ea")}</button>
      {["queued", "claimed"].includes(state) ? <button type="button" disabled={blocked} onClick={() => void cancellation.send({ mutation: { requestId: newRequestId(), id: job.id, expectedRevision: job.revision } })}>{copy("native-model-settings.cancelObservation_0f4be7")}</button> : null}
      {document(job).problem ? <ServiceProblem code={text(object(document(job).problem).code) || text(object(document(job).problem).problem_code)}><p role="alert">{text(object(document(job).problem).message)}</p></ServiceProblem> : null}
      {state !== "succeeded" && operation.data?.lastSuccess ? <button type="button" onClick={() => { setObservationID(operation.data!.lastSuccess!.id); setPage(""); }}>{copy("native-model-settings.showLastSuccessfulObservation_e2c0d6")}</button> : null}
    </div> : null}
    <Problem error={operation.error} /><Problem error={cancellation.error} />
    {cancellation.uncertain ? <button type="button" disabled={cancellation.busy} onClick={cancellation.retry}>{copy("native-model-settings.retryTheSameCancellation_0bc7e1")}</button> : null}
    <Problem error={models.error} />
    {malformed ? <p role="alert">{copy("native-model-settings.theObservationPageIsMalformedNo_ac73dd")}</p> : null}
    {models.data && !malformed ? <><p><LocalizedText id="native-model-settings.sourceObservationAccountInstallationGeneration_bf070d" components={{ s0: <>{models.data.job?.id}</>, s1: <>{text(observedScope.account_id)}</>, s2: <>{String(observedScope.installation_generation ?? copy("native-model-settings.extra.ca1844969742"))}</> }} /></p>
      {entries.length === 0 ? <p>{copy("native-model-settings.noNativeModelsInThisObservation_a24b4f")}</p> : entries.map((entry) => <article key={text(entry.id)}>
        <h3>{text(entry.display_name)}</h3><p><LocalizedText id="native-model-settings.pickerIdExecutableModel_ea304e" components={{ s0: <>{text(entry.id)}</>, s1: <>{text(entry.model)}</> }} /></p>
        <p>{text(entry.description)}</p><p><LocalizedText id="native-model-settings.reasoningInputServiceTiers_aa5c58" components={{ s0: <>{items(entry.reasoning).map(text).join(", ")}</>, s1: <>{items(entry.modalities).map(text).join(", ")}</>, s2: <>{items(entry.service_tiers).map(text).join(", ")}</>, s3: <>{entry.hidden === true ? copy("native-model-settings.hidden_7e6fef") : copy("native-model-settings.visible_8411f5")}</> }} /></p>
        <button type="button" disabled={!canRegister || blocked} onClick={() => createModel({ name: text(entry.display_name), provider_id: text(observedScope.provider_id), native_id: text(entry.model), alias: "", harnesses: ["codex"], hidden: false, order: 0, manual: true, new: false, metadata_source: "unknown" })}>{selectedAccounts ? copy("native-model-settings.useModel") : <LocalizedText id="native-model-settings.register_55d230" components={{ s0: <>{text(entry.display_name)}</> }} />}</button>
      </article>)}
      <button type="button" disabled={!page || models.isFetching} onClick={() => setPage("")}>{copy("native-model-settings.firstObservationPage_e0db18")}</button><More available={Boolean(models.data.nextPageToken)} busy={models.isFetching} load={() => setPage(models.data!.nextPageToken)} />
    </> : null}
  </section> : null}</details>;
}
