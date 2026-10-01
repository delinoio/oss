import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, PullRequestFixProfile, PullRequestFixQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, object, text, type Document } from "./documents";
import { uuid } from "./github-query-model";
import { useRetainedMutation } from "./mutation";
import { ResourceChoice } from "./configuration-fields";
import { readRemediationAttempt, readRemediationChain } from "./pr-remediation-model";
import { readPRProblemSet, type PRProblemSelection } from "./pr-problems";
import { Problem } from "./ui";

export function PRFixAction({ row, set, value, selection, disabled, refreshed }: { row: Resource; set: Resource; value: Document; selection: PRProblemSelection; disabled: boolean; refreshed: () => void }) {
 const [open, setOpen] = useState(false), [project, setProject] = useState(""), [notice, setNotice] = useState("");
 const fix = useRetainedMutation(`pr-fix:${selection.remoteRepositoryId}:${selection.pullRequestId}`, PullRequestFixQuery.requestPullRequestFix, (response) => {
  setNotice(`Fix accepted in session ${response.session!.id}. Evidence changes to handled only after a verified push.`); setOpen(false); refreshed();
 }, (response, request) => {
  const original = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(request.documentJson)) as Document;
  const attempt = response.attempt, inventory = response.problemSet, session = response.session;
  const v = attempt && inventory && readPRProblemSet(inventory, selection) && readRemediationAttempt(attempt, inventory);
  const refs = v && Array.isArray(v.problems) ? v.problems as Document[] : [];
  const expected = Array.isArray(original.problems) ? original.problems as Document[] : [];
  return Boolean(v && session && response.requestId === request.requestId && object(v.reserved).request_id === request.requestId && v.set_id === original.set_id && v.project_id === original.project_id && session.kind === EntityKind.SESSION && session.id === v.session_id && session.sessionId === session.id && session.projectId === original.project_id && session.schemaVersion === 1 && session.revision > 0n && uuid(session.id) && document(session).project_id === original.project_id && refs.length === expected.length && refs.every((ref, i) => ref.id === expected[i].id && ref.content_version === expected[i].content_version) && object(object(v.git_target).target).repository_id === original.repository_id);
 });
 const chain = readRemediationChain(document(set).remediation);
 const owner = text(chain?.active_attempt_id);
 const blocked = disabled || fix.busy || fix.uncertain || Boolean(owner);
 return <div>{owner ? <p>Fix attempt <code>{owner}</code> owns this PR. Inspect its retained history before another fix.</p> : null}<button disabled={blocked} aria-expanded={open} onClick={() => setOpen(!open)}>Fix now</button>
  {open ? <PRFixForm blocked={blocked} project={project} setProject={setProject} cancel={() => setOpen(false)} send={() => { setNotice(""); void fix.send({ requestId: newRequestId(), schemaVersion: 1, documentJson: encode({ set_id: set.id, set_revision: set.revision.toString(), project_id: project, repository_id: selection.repositoryId, problems: [{ id: row.id, revision: row.revision.toString(), content_version: text(value.content_version) }] }) }); }} /> : null}
  <Problem error={fix.error} />{fix.uncertain ? <button disabled={fix.busy} onClick={fix.retry}>Retry original fix request</button> : null}{notice ? <p role="status">{notice}</p> : null}
 </div>;
}

function PRFixForm({ blocked, project, setProject, cancel, send }: { blocked: boolean; project: string; setProject: (value: string) => void; cancel: () => void; send: () => void }) {
 const capability = useQuery(PullRequestFixQuery.getPullRequestFixCapabilities, {}, { retry: false, gcTime: 0, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false });
 const supported = capability.data?.profiles.includes(PullRequestFixProfile.CODEX_GIT_V1) && !capability.error;
 return <form aria-label="Manual PR fix" onSubmit={event => { event.preventDefault(); if (!blocked && uuid(project) && supported) send(); }}>
  <ResourceChoice label="Fix project" kind={EntityKind.PROJECT} value={project} change={setProject} active disabled={blocked} required autoFocus />
  <p>The server reuses the most recent eligible linked session or prepares a new PR-head workspace with its configured Codex Agent and Worker. Paused and archived sessions stay paused. The execution Worker uses its prepared Git identity.</p>
  <Problem error={capability.error} />{capability.data && !supported ? <p>This server has no supported manual PR fix profile.</p> : null}
  <button disabled={blocked || !uuid(project) || !supported}>Start fix</button><button type="button" disabled={blocked} onClick={cancel}>Cancel</button>
 </form>;
}
