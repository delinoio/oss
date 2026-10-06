import { LocalizedText, copy, useLocale } from "./localization";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, WorkerQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, resourceName, text } from "./documents";
import { Harness, TextField } from "./configuration-fields";
import { JobState, TrackedJob } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { Updates } from "./updates";
import { NetworkSettings } from "./network-settings";
import type { PairingAuthority } from "./pairing-grant";

export function MachineSettings({ initial, active, close, authority }: { initial: Resource; active: boolean; close: () => void; authority?: PairingAuthority }) {
  useLocale();
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: initial.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const current = [initial, result.data?.resource, acknowledged].filter((row): row is Resource => Boolean(row)).reduce((a, b) => a.revision >= b.revision ? a : b);
  const data = document(current);
  const [edit, setEdit] = useState<{ revision: bigint; paths: Record<string, string> }>();
  const [verify, setVerify] = useState(false);
  const [job, setJob] = useState<Resource | "unknown">();
  const discovery = useRetainedMutation(`machine-discovery:${initial.id}`, WorkerQuery.discoverHarnesses, (response) => { if (response.machine) setAcknowledged(response.machine); setJob(response.job ?? "unknown"); });
  const pending = discovery.busy || discovery.uncertain || Boolean(job);
  const stale = Boolean(edit && edit.revision !== current.revision);
  return <section><header><h3>{resourceName(current)}</h3><button disabled={pending} onClick={close}>{copy("machine-settings.backToRunnerDevices_3af74a")}</button></header><p><LocalizedText id="machine-settings.worker_5b4b08" components={{ s0: <>{text(data.os)}</>, s1: <>{text(data.architecture)}</>, s2: <>{text(data.version)}</> }} /></p><p><LocalizedText id="machine-settings.lastObserved_f1bd8c" components={{ s0: <>{text(data.last_seen) || "Unknown"}</>, s1: <>{data.disabled === true ? copy("machine-settings.disabled_3c6ef5") : ""}</> }} /></p><p>{copy("machine-settings.checksRunOnThisWorkerDelidev_36310f")}</p>
    {items(data.installations).map(object).map((installation) => <article className="result" key={text(installation.harness)}><h4>{text(installation.harness)}</h4><p><LocalizedText id="machine-settings.version_2da6f2" components={{ s0: <>{text(installation.state)}</>, s1: <>{text(installation.version) || "Unknown"}</> }} /></p><p><LocalizedText id="machine-settings.selectedPath_bb3534" components={{ s0: <>{text(installation.explicit_path) || "Worker PATH"}</> }} /></p><p><LocalizedText id="machine-settings.resolvedPath_5d2ab1" components={{ s0: <>{text(installation.resolved_path) || "Unknown"}</> }} /></p><p><LocalizedText id="machine-settings.nativeProtocol_4de6c7" components={{ s0: <>{text(object(installation.protocol).state) || "Not checked"}</> }} /></p>{text(object(installation.problem).message) ? <p role="alert">{text(object(installation.problem).message)} {text(object(installation.problem).guidance)}</p> : null}{text(object(object(installation.protocol).problem).message) ? <p role="alert">{text(object(object(installation.protocol).problem).message)}</p> : null}</article>)}
    {job ? job === "unknown" ? <p role="alert">{copy("machine-settings.discoveryWasAcknowledgedWithoutAReadable_b1200f")}</p> : <TrackedJob initial={job} active={active}>{(state) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState) ? <button onClick={() => { setJob(undefined); setEdit(undefined); void result.refetch(); }}>{copy("machine-settings.finishInspection_c3611c")}</button> : null}</TrackedJob> : <form onSubmit={(event) => { event.preventDefault(); if (pending || stale) return; void discovery.send({ mutation: { id: initial.id, expectedRevision: edit?.revision ?? current.revision, requestId: newRequestId() }, verifyProtocol: verify, ...(edit ? { selectionsJson: encode({ executables: Object.values(Harness).map((harness) => ({ harness, path: edit.paths[harness] ?? "" })) }) } : {}) }); }}>
      <fieldset disabled={pending}>{edit ? <><p>{copy("machine-settings.theseSelectionsReplaceAllFourHarness_a99040")}</p>{Object.values(Harness).map((harness) => <TextField key={harness} label={copy("machine-settings.executablePath_327ca6", { v0: harness })} value={edit.paths[harness]} max={4096} change={(path) => setEdit({ ...edit, paths: { ...edit.paths, [harness]: path } })} />)}<button type="button" onClick={() => setEdit(undefined)}>{copy("machine-settings.discardPathEdits_906d0c")}</button></> : <button type="button" onClick={() => setEdit({ revision: current.revision, paths: Object.fromEntries(items(data.installations).map(object).map((row) => [text(row.harness), text(row.explicit_path)])) })}>{copy("machine-settings.editExecutablePaths_5868fb")}</button>}
      <label className="checkbox"><input type="checkbox" checked={verify} onChange={(event) => setVerify(event.target.checked)} />{copy("machine-settings.verifyTheInstalledNativeProtocolWithout_0ea550")}</label><button className="primary" disabled={stale || Boolean(result.error)}>{copy("machine-settings.checkInstalledHarnesses_c38636")}</button></fieldset>{stale ? <p role="alert">{copy("machine-settings.workerConfigurationChangedElsewhereYourPath_34c382")}</p> : null}</form>}
    <Problem error={result.error || discovery.error} />{discovery.uncertain ? <button disabled={discovery.busy} onClick={discovery.retry}>{copy("machine-settings.retryTheSameHarnessCheck_7aa9ae")}</button> : null}
    <Updates active={active} machine={current} />
    <NetworkSettings active={active} machine={initial.id} authority={authority} />
  </section>;
}
