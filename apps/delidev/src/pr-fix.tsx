import { ownedMessage, useProductMessage, LocalizedText, copy, useLocale } from "./localization";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { type MessageShape } from "@bufbuild/protobuf";
import { EntityKind, PullRequestFixProfile, PullRequestFixQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, object, text, type Document } from "./documents";
import { uuid } from "./github-query-model";
import { useRetainedMutation } from "./mutation";
import { ResourceChoice } from "./configuration-fields";
import { readRemediationAttempt, readRemediationChain } from "./pr-remediation-model";
import { readPRProblemSet, type PRProblemSelection } from "./pr-problems";
import { Problem } from "./ui";

// Retain only the original identity, never the row, set or GitHub observation.
function fixAcknowledgement({ repositoryId, remoteRepositoryId, pullRequestId, number }: PRProblemSelection) {
 const selection = { repositoryId, remoteRepositoryId, pullRequestId, number };
 return (response: MessageShape<typeof PullRequestFixQuery.requestPullRequestFix.output>, request: MessageShape<typeof PullRequestFixQuery.requestPullRequestFix.input>) => {
  const original = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(request.documentJson)) as Document;
  const attempt = response.attempt, inventory = response.problemSet, session = response.session;
  const v = attempt && inventory && readPRProblemSet(inventory, selection) && readRemediationAttempt(attempt, inventory);
  const refs = v && Array.isArray(v.problems) ? v.problems as Document[] : [];
  const expected = Array.isArray(original.problems) ? original.problems as Document[] : [];
  return Boolean(v && session && response.requestId === request.requestId && object(v.reserved).request_id === request.requestId && v.set_id === original.set_id && v.project_id === original.project_id && session.kind === EntityKind.SESSION && session.id === v.session_id && session.sessionId === session.id && session.projectId === original.project_id && session.schemaVersion === 1 && session.revision > 0n && uuid(session.id) && document(session).project_id === original.project_id && refs.length === expected.length && refs.every((ref, i) => ref.id === expected[i].id && ref.content_version === expected[i].content_version) && object(object(v.git_target).target).repository_id === original.repository_id);
 };
}

export function PRFixAction({ row, set, value, selection, disabled, refreshed }: { row: Resource; set: Resource; value: Document; selection: PRProblemSelection; disabled: boolean; refreshed: () => void }) {
 useLocale();
 const [open, setOpen] = useState(false), [project, setProject] = useState(""), [notice, setNotice] = useProductMessage("");
 const fix = useRetainedMutation(`pr-fix:${selection.remoteRepositoryId}:${selection.pullRequestId}`, PullRequestFixQuery.requestPullRequestFix, (response) => {
  setNotice(ownedMessage("pr-fix.sentence.613a13e6da0e", { v0: response.session!.id })); setOpen(false); refreshed();
 });
 const chain = readRemediationChain(document(set).remediation);
 const owner = text(chain?.active_attempt_id);
 const blocked = disabled || fix.busy || fix.uncertain || Boolean(owner);
 return <div>{owner ? <p><LocalizedText id="pr-fix.fixAttemptOwnsThisPrInspect_85a121" components={{ s0: <code>{owner}</code> }} /></p> : null}<button disabled={blocked} aria-expanded={open} onClick={() => setOpen(!open)}>{copy("pr-fix.fixNow_879349")}</button>
  {open ? <PRFixForm blocked={blocked} project={project} setProject={setProject} cancel={() => setOpen(false)} send={() => { setNotice(""); void fix.send({ requestId: newRequestId(), schemaVersion: 1, documentJson: encode({ set_id: set.id, set_revision: set.revision.toString(), project_id: project, repository_id: selection.repositoryId, problems: [{ id: row.id, revision: row.revision.toString(), content_version: text(value.content_version) }] }) }, fixAcknowledgement(selection)); }} /> : null}
  <Problem error={fix.error} />{fix.uncertain ? <button disabled={fix.busy} onClick={fix.retry}>{copy("pr-fix.retryOriginalFixRequest_3ecf60")}</button> : null}{notice ? <p role="status">{notice}</p> : null}
 </div>;
}

function PRFixForm({ blocked, project, setProject, cancel, send }: { blocked: boolean; project: string; setProject: (value: string) => void; cancel: () => void; send: () => void }) {
  useLocale();
 const capability = useQuery(PullRequestFixQuery.getPullRequestFixCapabilities, {}, { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false });
 const supported = capability.data?.profiles.includes(PullRequestFixProfile.CODEX_GIT_V1) && !capability.error;
 return <form aria-label={copy("pr-fix.manualPrFix_5b64d4")} onSubmit={event => { event.preventDefault(); if (!blocked && uuid(project) && supported) send(); }}>
  <ResourceChoice label={copy("pr-fix.fixProject_e1ce32")} kind={EntityKind.PROJECT} value={project} change={setProject} active disabled={blocked} required autoFocus />
  <p>{copy("pr-fix.theServerReusesTheMostRecent_add8ed")}</p>
  <Problem error={capability.error} actions={capability.error ? <button type="button" disabled={capability.isFetching} onClick={() => void capability.refetch()}>{copy("ui.retryCurrentRead")}</button> : undefined} />{capability.data && !supported ? <p>{copy("pr-fix.thisServerHasNoSupportedManual_5d3d3f")}</p> : null}
  <button disabled={blocked || !uuid(project) || !supported}>{copy("pr-fix.startFix_039be8")}</button><button type="button" disabled={blocked} onClick={cancel}>{copy("pr-fix.cancel_19766e")}</button>
 </form>;
}
