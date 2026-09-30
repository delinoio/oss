import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { ConfigurationQuery, EntityKind, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, resourceName, text, type Document } from "./documents";
import { ResourceChoice } from "./configuration-fields";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export function ConfigurationDeletion({ initial, deleted, close }: { initial: Resource; deleted: () => void; close: () => void }) {
  const mutation = useRetainedMutation(`configuration-delete:${initial.kind}:${initial.id}`, ConfigurationQuery.deleteConfiguration, deleted);
  const blocked = mutation.busy || mutation.uncertain;
  const apiEntry = initial.kind === EntityKind.ACCOUNT && document(initial).type === "api";
  return <section><h3>Delete {apiEntry ? "entry " : ""}{resourceName(initial)}?</h3><p>This deletes its saved configuration. Retained sessions and history remain. The server rejects references that must be reconfigured first.</p>{initial.kind === EntityKind.PROJECT || initial.kind === EntityKind.AGENT ? <p>Schedules using this configuration will be disabled for future runs. Already accepted sessions are retained.</p> : null}{initial.kind === EntityKind.ACCOUNT ? <p>Disconnect the {apiEntry ? "entry" : "account"} and finish credential cleanup before deleting it.</p> : null}<Problem error={mutation.error} /><div className="actions"><button disabled={blocked} onClick={() => void mutation.send({ kind: initial.kind, mutation: { id: initial.id, expectedRevision: initial.revision, requestId: newRequestId() } })}>Confirm configuration deletion</button>{mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry the same deletion</button> : null}<button disabled={blocked} onClick={close}>Keep configuration</button></div></section>;
}
export function RoutingPreview({ agent, active, close }: { agent: Resource; active: boolean; close: () => void }) {
  const [project, setProject] = useState("");
  const result = useQuery(ConfigurationQuery.previewRouting, { agentId: agent.id, projectId: project }, { enabled: active });
  let route: Document | undefined;
  try { if (result.data) route = object(JSON.parse(new TextDecoder().decode(result.data.routeJson))); } catch { /* Invalid evidence stays unavailable. */ }
  return <section><header><h3>Account routing · {resourceName(agent)}</h3><button onClick={close}>Back to Agent Workers</button></header><p>This preview is read-only and does not consume routing turns or reserve an account. Execution rechecks current eligibility.</p><ResourceChoice label="Project" kind={EntityKind.PROJECT} value={project} change={setProject} active={active} /><p>{project ? "Using the selected project's restrictions." : "General Chat · no project restrictions."}</p><button disabled={result.isFetching} onClick={() => void result.refetch()}>Refresh routing preview</button><Problem error={result.error} />{route ? <><p>Policy: {text(route.policy)} · Selected account: {text(route.selected) || "None eligible"}</p>{route.fallback === true ? <p>Insufficient comparable quota evidence; the server used the configured policy's fallback.</p> : null}<ul>{items(route.candidates).map(object).map((candidate) => <li key={text(candidate.id)}><p>{text(candidate.id)} · {text(candidate.eligibility)}</p><p>Weight: {String(candidate.weight)} · Quota: {text(candidate.quota_state)}{typeof candidate.score === "number" ? ` · Score: ${candidate.score}` : ""}{text(candidate.reset_at) ? ` · Reset: ${text(candidate.reset_at)}` : ""}</p></li>)}</ul></> : result.data ? <p role="alert">Routing evidence is unavailable.</p> : null}</section>;
}
