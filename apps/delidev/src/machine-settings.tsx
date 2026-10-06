import { SettingsTaskActions } from "./settings-task";
import { useRetainSettingsTask, useCloseSettingsTask } from "./settings-task-context";
import { useState, useId } from "react";
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
  const formId = useId(), closeTask = useCloseSettingsTask(close);
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: initial.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const current = [initial, result.data?.resource, acknowledged].filter((row): row is Resource => Boolean(row)).reduce((a, b) => a.revision >= b.revision ? a : b);
  const data = document(current);
  const [edit, setEdit] = useState<{ revision: bigint; paths: Record<string, string> }>();
  const [verify, setVerify] = useState(false);
  const [job, setJob] = useState<Resource | "unknown">();
  const discovery = useRetainedMutation(`machine-discovery:${initial.id}`, WorkerQuery.discoverHarnesses, (response) => { if (response.machine) setAcknowledged(response.machine); setJob(response.job ?? "unknown"); });
  useRetainSettingsTask(Boolean(job));
  const pending = discovery.busy || discovery.uncertain || Boolean(job);
  const stale = Boolean(edit && edit.revision !== current.revision);
  return <section><header><h3>{resourceName(current)}</h3></header><p>{text(data.os)} · {text(data.architecture)} · Worker {text(data.version)}</p><p>Last observed: {text(data.last_seen) || "Unknown"}{data.disabled === true ? " · Disabled" : ""}</p><p>Checks run on this Worker. DeliDev does not install harnesses. Version detection, native protocol verification and account execution readiness are separate.</p>
    {items(data.installations).map(object).map((installation) => <article className="result" key={text(installation.harness)}><h4>{text(installation.harness)}</h4><p>{text(installation.state)} · Version: {text(installation.version) || "Unknown"}</p><p>Selected path: {text(installation.explicit_path) || "Worker PATH"}</p><p>Resolved path: {text(installation.resolved_path) || "Unknown"}</p><p>Native protocol: {text(object(installation.protocol).state) || "Not checked"}</p>{text(object(installation.problem).message) ? <p role="alert">{text(object(installation.problem).message)} {text(object(installation.problem).guidance)}</p> : null}{text(object(object(installation.protocol).problem).message) ? <p role="alert">{text(object(object(installation.protocol).problem).message)}</p> : null}</article>)}
    {job ? job === "unknown" ? <p role="alert">Discovery was acknowledged without a readable job. Inspect the original request before starting another check.</p> : <TrackedJob initial={job} active={active}>{(state) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState) ? <SettingsTaskActions><button onClick={() => { setJob(undefined); setEdit(undefined); void result.refetch(); }}>Finish inspection</button></SettingsTaskActions> : null}</TrackedJob> : <form id={formId} onSubmit={(event) => { event.preventDefault(); if (pending || stale) return; void discovery.send({ mutation: { id: initial.id, expectedRevision: edit?.revision ?? current.revision, requestId: newRequestId() }, verifyProtocol: verify, ...(edit ? { selectionsJson: encode({ executables: Object.values(Harness).map((harness) => ({ harness, path: edit.paths[harness] ?? "" })) }) } : {}) }); }}>
      <fieldset disabled={pending}>{edit ? <><p>These selections replace all four harness paths. Empty paths explicitly use this Worker's PATH.</p>{Object.values(Harness).map((harness) => <TextField key={harness} label={`${harness} executable path`} value={edit.paths[harness]} max={4096} change={(path) => setEdit({ ...edit, paths: { ...edit.paths, [harness]: path } })} />)}<button type="button" onClick={() => setEdit(undefined)}>Discard path edits</button></> : <button type="button" onClick={() => setEdit({ revision: current.revision, paths: Object.fromEntries(items(data.installations).map(object).map((row) => [text(row.harness), text(row.explicit_path)])) })}>Edit executable paths</button>}
      <label className="checkbox"><input type="checkbox" checked={verify} onChange={(event) => setVerify(event.target.checked)} />Verify the installed native protocol without login or inference</label></fieldset><SettingsTaskActions form={formId}><button type="button" data-settings-task-cancel onClick={closeTask}>Back to Runner Devices</button><button className="primary" disabled={pending || stale || Boolean(result.error)}>Check installed harnesses</button></SettingsTaskActions>{stale ? <p role="alert">Worker configuration changed elsewhere. Your path draft is retained; discard it and reopen the latest paths before saving.</p> : null}</form>}
    <Problem error={result.error || discovery.error} />{discovery.uncertain ? <button disabled={discovery.busy} onClick={discovery.retry}>Retry the same harness check</button> : null}
    <Updates active={active} machine={current} />
    <NetworkSettings active={active} machine={initial.id} authority={authority} />
  </section>;
}
