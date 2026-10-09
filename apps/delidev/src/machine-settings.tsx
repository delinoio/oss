import { SettingsActionButton, SettingsActionIcon, SettingsActionPresentation } from "./settings-action";
import { SettingsTaskDismissButton } from "./settings-task";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { SettingsTaskActions } from "./settings-task";
import { useCloseSettingsTask } from "./settings-task-context";
import { useState, useId, useMemo, useRef } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { installationObservation, validRunnerObservation } from "./runner-observation";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, WorkerQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, items, object, resourceName, text } from "./documents";
import { Harness, TextField } from "./configuration-fields";
import { JobState, TrackedJob } from "./jobs";
import { useRetainedMutation } from "./mutation";
import { InlineRemediation, Problem } from "./ui";
import { Updates } from "./updates";
import { NetworkSettings } from "./network-settings";
import type { PairingAuthority } from "./pairing-grant";

export function useMachineSettingsController(initial: Resource, active: boolean) {
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: initial.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const exact = (row?: Resource): row is Resource => validRunnerObservation(row) && row.id === initial.id;
  const current = [initial, result.data?.resource, acknowledged].filter(exact).reduce<Resource | undefined>((a, b) => !a || b.revision > a.revision ? b : a, undefined);
  const data = useMemo(() => document(current), [current]);
  const [edit, setEdit] = useState<{ revision: bigint; paths: Record<string, string> }>();
  const [verify, setVerifyValue] = useState(false);
  const [verifyEdited, setVerifyEdited] = useState(false);
  const setVerify = useMemo(() => (next: boolean) => { setVerifyValue(next); setVerifyEdited(next); }, []);
  const [job, setJob] = useState<Resource | "unknown">();
  const discovery = useRetainedMutation(`machine-discovery:${initial.id}`, WorkerQuery.discoverHarnesses, (response) => { if (exact(response.machine)) setAcknowledged(response.machine); setJob(response.job ?? "unknown"); setVerifyEdited(false); });
  const pending = discovery.busy || discovery.uncertain || Boolean(job);
  const stale = Boolean(edit && edit.revision !== current?.revision);
  const readError = useMemo(() => result.error || (result.data && !exact(result.data.resource) ? new ConnectError("Runner observation is unavailable.", Code.DataLoss) : undefined), [result.error, result.data, initial.id]);
  const mutation = useRef(discovery); mutation.current = discovery;
  const actions = useMemo(() => ({ send: (...args: Parameters<typeof discovery.send>) => mutation.current.send(...args), retry: () => mutation.current.retry() }), []);
  const retainedDiscovery = useMemo(() => ({ ...actions, busy: discovery.busy, uncertain: discovery.uncertain, error: discovery.error }), [actions, discovery.busy, discovery.uncertain, discovery.error]);
  return useMemo(() => ({ initial, current, data, edit, setEdit, verify, setVerify, job, setJob, discovery: retainedDiscovery, pending, stale, readError, refetch: result.refetch, loading: result.isFetching, locked: Boolean(edit || pending || verifyEdited) }), [initial, current, data, edit, verify, verifyEdited, setVerify, job, retainedDiscovery, pending, stale, readError, result.refetch, result.isFetching]);
}
export type MachineSettingsController = ReturnType<typeof useMachineSettingsController>;
export function MachineSettings({ initial, active, close, authority }: { initial: Resource; active: boolean; close: () => void; authority?: PairingAuthority }) {
  const controller = useMachineSettingsController(initial, active);
  return <MachineSettingsView controller={controller} active={active} close={close} authority={authority} />;
}
/** All actions belong to the original retained controller; this view owns no reads or mutations. */
export function MachineSettingsView({ controller, active, close, authority, compact = false }: { controller: MachineSettingsController; active: boolean; close: () => void; authority?: PairingAuthority; compact?: boolean }) {
  useLocale();
  const formId = useId(), closeTask = useCloseSettingsTask(close);
  const { initial, current, data, edit, setEdit, verify, setVerify, job, setJob, discovery, pending, stale, readError, refetch, loading } = controller;
  if (!current) return <InlineRemediation summary={copy("machine-settings.observationUnavailable")} actions={<SettingsActionButton icon={SettingsActionIcon.Refresh} presentation={SettingsActionPresentation.Icon} disabled={!active || loading} onClick={() => void refetch()}>{copy("claude-subscription.refreshRunners")}</SettingsActionButton>} />;
  return <section><header><h3>{resourceName(current)}</h3></header><p><LocalizedText id="machine-settings.worker_5b4b08" components={{ s0: <>{["darwin", "linux", "windows"].includes(text(data.os)) ? text(data.os) : copy("machine-settings.extra.b764cdc0eab7")}</>, s1: <>{["amd64", "arm64", "386", "arm"].includes(text(data.architecture)) ? text(data.architecture) : copy("machine-settings.extra.b764cdc0eab7")}</>, s2: <>{/^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]{1,64})?$/.test(text(data.version)) ? text(data.version) : copy("machine-settings.extra.b764cdc0eab7")}</> }} /></p><p><LocalizedText id="machine-settings.lastObserved_f1bd8c" components={{ s0: <>{/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(text(data.last_seen)) ? text(data.last_seen) : copy("machine-settings.extra.b764cdc0eab7")}</>, s1: <>{data.disabled === true ? copy("machine-settings.disabled_3c6ef5") : ""}</> }} /></p><p>{copy("machine-settings.checksRunOnThisWorkerDelidev_36310f")}</p>
    {!Array.isArray(data.installations) || items(data.installations).length > 4 ? <InlineRemediation summary={copy("machine-settings.observationUnavailable")} /> : items(data.installations).map(object).map((installation, index) => {
      const harness = text(installation.harness), known = Object.values(Harness).includes(harness as Harness), duplicate = items(data.installations).map(object).filter(row => row.harness === harness).length !== 1;
      const observation = !known || duplicate ? { cause: "unavailable" as const } : installationObservation(installation, harness === Harness.Claude ? "2.1.236" : undefined);
      return <article className="result" key={index}><h4>{known ? harness : copy("machine-settings.observationUnavailable")}</h4><p><LocalizedText id="machine-settings.nativeProtocol_4de6c7" components={{ s0: <>{statusLabel(["verified", "unsupported", "failed"].includes(text(object(installation.protocol).state)) ? text(object(installation.protocol).state) : "") || copy("machine-settings.extra.d16948e73a68")}</> }} /></p>{observation.detectedVersion ? <p>{copy("machine-settings.detectedVersion", { version: observation.detectedVersion })}</p> : null}{observation.cause ? <InlineRemediation summary={copy(harness === Harness.Claude ? `claude-subscription.excluded.${observation.cause}` : `machine-settings.excluded.${observation.cause}`)} /> : <p>{copy("machine-settings.protocolVerified")}</p>}</article>;
    })}
    {job ? job === "unknown" ? <p role="alert">{copy("machine-settings.discoveryWasAcknowledgedWithoutAReadable_b1200f")}</p> : <TrackedJob initial={job} active={active}>{(state) => [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState) ? <SettingsTaskActions><SettingsActionButton icon={SettingsActionIcon.Cancel} onClick={() => { setJob(undefined); setEdit(undefined); void refetch(); }}>{copy("machine-settings.finishInspection_c3611c")}</SettingsActionButton></SettingsTaskActions> : null}</TrackedJob> : <form id={formId} onSubmit={(event) => { event.preventDefault(); if (!active || pending || stale || readError) return; void discovery.send({ mutation: { id: initial.id, expectedRevision: edit?.revision ?? current.revision, requestId: newRequestId() }, verifyProtocol: verify, ...(edit ? { selectionsJson: encode({ executables: Object.values(Harness).map((harness) => ({ harness, path: edit.paths[harness] ?? "" })) }) } : {}) }); }}>
      <fieldset disabled={!active || pending || Boolean(readError)}>{edit ? <><p>{copy("machine-settings.theseSelectionsReplaceAllFourHarness_a99040")}</p>{Object.values(Harness).map((harness) => <TextField key={harness} label={copy("machine-settings.executablePath_327ca6", { v0: harness })} value={edit.paths[harness]} max={4096} change={(path) => setEdit({ ...edit, paths: { ...edit.paths, [harness]: path } })} />)}<SettingsActionButton icon={SettingsActionIcon.Cancel} type="button" onClick={() => setEdit(undefined)}>{copy("machine-settings.discardPathEdits_906d0c")}</SettingsActionButton></> : <SettingsActionButton icon={SettingsActionIcon.Edit} presentation={SettingsActionPresentation.Icon} type="button" onClick={() => setEdit({ revision: current.revision, paths: Object.fromEntries(items(data.installations).map(object).map((row) => [text(row.harness), text(row.explicit_path)])) })}>{copy("machine-settings.editExecutablePaths_5868fb")}</SettingsActionButton>}
      <label className="checkbox"><input type="checkbox" checked={verify} onChange={(event) => setVerify(event.target.checked)} />{copy("machine-settings.verifyTheInstalledNativeProtocolWithout_0ea550")}</label></fieldset><SettingsTaskActions form={formId}><SettingsTaskDismissButton type="button" data-settings-task-cancel onClick={closeTask}>{copy("machine-settings.backToRunnerDevices_3af74a")}</SettingsTaskDismissButton><SettingsActionButton icon={SettingsActionIcon.Inspect} className="primary" disabled={pending || stale || Boolean(readError) || !active}>{copy("machine-settings.checkInstalledHarnesses_c38636")}</SettingsActionButton></SettingsTaskActions>{stale ? <p role="alert">{copy("machine-settings.workerConfigurationChangedElsewhereYourPath_34c382")}</p> : null}</form>}
    <Problem error={readError} summary={copy("machine-settings.observationReadFailed")} actions={<SettingsActionButton icon={SettingsActionIcon.Refresh} presentation={SettingsActionPresentation.Icon} disabled={!active || loading} onClick={() => void refetch()}>{copy("claude-subscription.refreshRunners")}</SettingsActionButton>} /><Problem error={discovery.error} />{discovery.uncertain ? <SettingsActionButton icon={SettingsActionIcon.Retry} disabled={discovery.busy} onClick={discovery.retry}>{copy("machine-settings.retryTheSameHarnessCheck_7aa9ae")}</SettingsActionButton> : null}
    {!compact ? <><Updates active={active} machine={current} /><NetworkSettings active={active} machine={initial.id} authority={authority} /></> : null}
  </section>;
}
