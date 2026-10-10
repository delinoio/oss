// SPDX-License-Identifier: Apache-2.0
import { productDiagnosticText } from "./product-reference";
import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { ConfigurationQuery, EntityKind, ResourceQuery, SystemCapability, SystemQuery, newRequestId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, object, resourceName, text } from "./documents";
import { ResourceChoice, ResourceSelectionPending } from "./configuration-fields";
import { confirmedRepository } from "./repository-registration";
import { JobState, OperationStatus } from "./jobs";
import { copy, useLocale } from "./localization";
import { Problem, ServiceProblem } from "./ui";
import { repositoryConfigurationPrefix, useRetainedMutation, type RetainedMutationIntent } from "./mutation";

/** The receipt, job and fresh metadata all belong to the original repository. */
export function PendingRepositoryConfiguration({ intent }: { intent: RetainedMutationIntent }) {
  useLocale();
  const mutation = useRetainedMutation(intent.key, ConfigurationQuery.saveConfiguration);
  const id = intent.key.slice(repositoryConfigurationPrefix.length), job = intent.job;
  const status = useQuery(ResourceQuery.getResource, { kind: EntityKind.JOB, id: job?.id ?? "" }, { enabled: Boolean(job), refetchInterval: query => {
    const row = query.state.data?.resource;
    return row?.id === job?.id && [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(document(row).state as JobState) ? false : 2000;
  } });
  const row = status.data?.resource;
  const verified = Boolean(job && row && row.id === job.id && row.kind === EntityKind.JOB && row.revision >= job.revision && supportsResourceSchema(row) && !status.error && !status.isFetching);
  const unreadable = Boolean(status.data && !verified && !status.isFetching && !status.error);
  const state = verified ? text(document(row).state) : "";
  const output = confirmedRepository(object(document(row).output));
  const request = intent.input as { mutation?: { expectedRevision?: bigint }; documentJson?: Uint8Array };
  const expectedRevision = request.mutation?.expectedRevision ?? 0n;
  const validOutput = Boolean(output?.id === id && output.revision > expectedRevision);
  const terminal = [JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState);
  const repository = useQuery(ResourceQuery.getResource, { kind: EntityKind.REPOSITORY, id }, { enabled: false, retry: false });
  const [fresh, setFresh] = useState<Resource>();
  useEffect(() => {
    if (!job || !terminal || state === JobState.Succeeded && !validOutput) return;
    let current = true;
    setFresh(undefined);
    void repository.refetch({ cancelRefetch: true }).then(result => {
      const value = result.data?.resource;
      if (current && !result.error && value?.id === id && value.kind === EntityKind.REPOSITORY && value.revision > 0n && supportsResourceSchema(value)) setFresh(value);
    });
    return () => { current = false; };
  }, [job?.id, terminal, state, validOutput, output?.id, output?.revision, id, repository.refetch]);
  useEffect(() => {
    if (job && state === JobState.Succeeded && validOutput && output && fresh && fresh.revision >= output.revision) mutation.resolveJob(job.id);
  }, [job?.id, state, validOutput, output?.id, output?.revision, id, fresh, mutation.resolveJob]);
  return <article className="pending-pr-action"><strong>{copy("pull-requests.profileSavePending")}</strong>
    {job ? <><OperationStatus state={state} />{unreadable ? <p role="alert">{copy("jobs.unreadableStatus")}</p> : null}{text(object(document(row).problem).message) ? <ServiceProblem code={text(object(document(row).problem).code)}><p>{productDiagnosticText(text(object(document(row).problem).message))} {productDiagnosticText(text(object(document(row).problem).guidance))}</p></ServiceProblem> : null}<Problem error={status.error} />
      {state === JobState.Succeeded && !validOutput ? <p role="alert">{copy("pull-requests.profileSaveUnverified")}</p> : null}
      <Problem error={repository.error} />
      {status.error || unreadable ? <button type="button" onClick={() => void status.refetch()}>{copy("jobs.retryStatusRead")}</button> : null}
      {terminal && !fresh ? <button type="button" disabled={repository.isFetching} onClick={() => void repository.refetch().then(result => { const value = result.data?.resource; if (!result.error && value?.id === id && value.kind === EntityKind.REPOSITORY && supportsResourceSchema(value)) setFresh(value); })}>{copy("ui.retryCurrentRead")}</button> : null}
      {fresh && (state === JobState.Failed || state === JobState.Canceled) ? <button type="button" onClick={() => mutation.resolveJob(job.id)}>{copy("pull-requests.reviewRepositorySave")}</button> : null}
    </> : <><p role="status">{copy(intent.busy ? "pull-requests.submitting_cba659" : "pull-requests.acknowledgmentUncertain_62e6b9")}</p><Problem error={mutation.error} />{intent.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("pull-requests.retryOriginalProfileSave")}</button> : null}</>}
  </article>;
}

export function RepositoryProfileAssociation({ repository, active, profiles, review }: { repository: Resource; active: boolean; profiles: () => void; review: () => Promise<void> }) {
  useLocale();
  const [profile, setProfile] = useState("");
  const [pending, setPending] = useState(false), pendingOwners = useRef(new Set<string>());
  const reportPending = useCallback((identity: string, busy: boolean) => { if (busy) pendingOwners.current.add(identity); else pendingOwners.current.delete(identity); setPending(pendingOwners.current.size > 0); }, []);
  const mutation = useRetainedMutation(`${repositoryConfigurationPrefix}${repository.id}`, ConfigurationQuery.saveConfiguration);
  const capability = useQuery(SystemQuery.getStatus, {}, { enabled: active, retry: false });
  const data = document(repository);
  const remote = Boolean(text(data.remote_url));
  const capabilityBlocked = remote && (!capability.data?.capabilities.includes(SystemCapability.REMOTE_REPOSITORIES_V1) || Boolean(capability.error));
  const blocked = !active || pending || mutation.busy || mutation.uncertain || Boolean(mutation.error) || capabilityBlocked;
  return <ResourceSelectionPending.Provider value={reportPending}><form className="repository-profile-association" onSubmit={event => {
    event.preventDefault();
    if (blocked || !profile || pendingOwners.current.size) return;
    void mutation.send({ mutation: { id: repository.id, expectedRevision: repository.revision, requestId: newRequestId() }, kind: EntityKind.REPOSITORY, schemaVersion: repository.schemaVersion, documentJson: encode({ ...data, integration_id: profile }) });
  }}><h3>{copy("pull-requests.connectProfileHeading")}</h3><p>{copy("pull-requests.connectProfileHelp")}</p><p>{resourceName(repository)}</p>
    <ResourceChoice kind={EntityKind.INTEGRATION} label={copy("pull-requests.profileLabel")} value={profile} change={setProfile} active={active} disabled={mutation.busy || mutation.uncertain} autoFocus showStatus required />
    <Problem error={capability.error} /><Problem error={mutation.error} />
    {capabilityBlocked ? <p role="status">{copy("pull-requests.profileCapabilityRequired")}</p> : null}
    <div className="actions"><button type="submit" disabled={blocked || !profile}>{copy("pull-requests.connectProfile")}</button><button type="button" onClick={profiles}>{copy("pull-requests.githubProfiles")}</button>
    {mutation.error && !mutation.uncertain ? <button type="button" onClick={() => void review().then(() => { setProfile(""); mutation.clearRejected(); }, () => { console.warn("delidev.pull_requests.repository_review_failed", { phase: "read" }); })}>{copy("pull-requests.reviewRepositorySave")}</button> : null}</div>
    <p>{copy("pull-requests.needProfile")}</p>
  </form></ResourceSelectionPending.Provider>;
}
