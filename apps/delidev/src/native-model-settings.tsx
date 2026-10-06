// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, NativeModelQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, text, type Document } from "./documents";
import { ResourceChoice } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { More, Problem } from "./ui";

export function NativeModelSettings({ active, createModel, selectedAccounts, pendingOperation }: { active: boolean; createModel: (data: Document) => void; selectedAccounts?: Resource[]; pendingOperation?: (pending: boolean) => void }) {
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
  return <details onToggle={(event) => setOpened(event.currentTarget.open)}><summary>Native Codex model observations</summary>{opened ? <section aria-label="Native Codex model observations">
    <h2>Observe native Codex models</h2>
    <p>Choose a Runner Device with Codex 0.151.0 and a connected account. Advisory observations do not establish account support, entitlement, readiness or fresh provider data. Choosing a model does not save configuration.</p>
    <Problem error={status.error} />
    {status.data && !supported ? <p>This server does not support native model observation.</p> : null}
    <fieldset disabled={!supported || blocked}>
      <legend>Observation scope</legend>
      <ResourceChoice label="Runner Device" kind={EntityKind.MACHINE} value={machine?.id ?? ""} active={active && supported} change={(_id, _data, row) => { setMachine(row); reset(); }} />
      {selectedAccounts ? <label>Connected selected account<select value={account?.id ?? ""} onChange={event => { setAccount(selectedAccounts.find(row => row.id === event.target.value)); reset(); }}><option value="">Select account</option>{selectedAccounts.map(row => <option key={row.id} value={row.id}>{text(document(row).alias) || row.id}</option>)}</select></label> : <ResourceChoice label="Connected account" kind={EntityKind.ACCOUNT} value={account?.id ?? ""} active={active && supported} change={(_id, _data, row) => { setAccount(row); reset(); }} />}
      <label><input type="checkbox" checked={hidden} onChange={(event) => { setHidden(event.target.checked); reset(); }} /> Include hidden models</label>
      <button type="button" disabled={!machine || !account || !accountSelected || !document(account).connection} onClick={() => void discovery.send({ mutation: { requestId: newRequestId(), id: machine!.id, expectedRevision: machine!.revision }, accountId: account!.id, accountRevision: account!.revision, includeHidden: hidden })}>Observe models</button>
    </fieldset>
    <Problem error={discovery.error} />
    {discovery.uncertain ? <button type="button" disabled={discovery.busy} onClick={discovery.retry}>Retry the same observation request</button> : null}
    <label>Original observation ID<input value={lookup} maxLength={36} disabled={blocked} onChange={(event) => setLookup(event.target.value)} /></label>
    <button type="button" disabled={!supported || blocked || !lookup} onClick={() => { setJobID(lookup); setObservationID(""); setPage(""); }}>Inspect observation</button>
    {job ? <div><p role="status">Observation {job.id}: {state}. {text(object(document(job).output).observed_at)}</p>
      <button type="button" disabled={!active || operation.isFetching} onClick={() => void operation.refetch()}>Refresh observation status</button>
      {["queued", "claimed"].includes(state) ? <button type="button" disabled={blocked} onClick={() => void cancellation.send({ mutation: { requestId: newRequestId(), id: job.id, expectedRevision: job.revision } })}>Cancel observation</button> : null}
      {document(job).problem ? <p role="alert">{text(object(document(job).problem).message)}</p> : null}
      {state !== "succeeded" && operation.data?.lastSuccess ? <button type="button" onClick={() => { setObservationID(operation.data!.lastSuccess!.id); setPage(""); }}>Show last successful observation</button> : null}
    </div> : null}
    <Problem error={operation.error} /><Problem error={cancellation.error} />
    {cancellation.uncertain ? <button type="button" disabled={cancellation.busy} onClick={cancellation.retry}>Retry the same cancellation</button> : null}
    <Problem error={models.error} />
    {malformed ? <p role="alert">The observation page is malformed. No entries can be registered from it.</p> : null}
    {models.data && !malformed ? <><p>Source observation: {models.data.job?.id}. Account: {text(observedScope.account_id)}. Installation generation: {String(observedScope.installation_generation ?? "Unavailable")}.</p>
      {entries.length === 0 ? <p>No native models in this observation page.</p> : entries.map((entry) => <article key={text(entry.id)}>
        <h3>{text(entry.display_name)}</h3><p>Picker ID: {text(entry.id)} · Executable model: {text(entry.model)}</p>
        <p>{text(entry.description)}</p><p>Reasoning: {items(entry.reasoning).map(text).join(", ")}. Input: {items(entry.modalities).map(text).join(", ")}. Service tiers: {items(entry.service_tiers).map(text).join(", ")}. {entry.hidden === true ? "Hidden" : "Visible"}.</p>
        <button type="button" disabled={!canRegister || blocked} onClick={() => createModel({ name: text(entry.display_name), provider_id: text(observedScope.provider_id), native_id: text(entry.model), alias: "", harnesses: ["codex"], hidden: false, order: 0, manual: true, new: false, metadata_source: "unknown" })}>{selectedAccounts ? "Use model" : "Register"} {text(entry.display_name)}…</button>
      </article>)}
      <button type="button" disabled={!page || models.isFetching} onClick={() => setPage("")}>First observation page</button><More available={Boolean(models.data.nextPageToken)} busy={models.isFetching} load={() => setPage(models.data!.nextPageToken)} />
    </> : null}
  </section> : null}</details>;
}
