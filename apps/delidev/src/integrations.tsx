import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, FailureCode, IntegrationQuery, ResourceQuery, clientFailure, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, object, resourceName, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { GitHubTokenForm } from "./github-opening";

enum TokenKind { FineGrained = "fine-grained", Classic = "classic" }
type MutationIdentity = { id: string; expectedRevision: bigint; requestId: string };
function pendingIdentity(id: string, pending: Document): MutationIdentity | undefined {
  const revision = text(pending.expected_revision), requestId = text(pending.request_id);
  if (!/^[1-9][0-9]{0,18}$/.test(revision) || !/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(requestId)) return undefined;
  return { id, expectedRevision: BigInt(revision), requestId };
}
function problemDocument(raw: Uint8Array): Document | undefined {
  if (!raw.byteLength) return undefined;
  try { return object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return { message: "The operation needs inspection. Refresh the profile before continuing." }; }
}
function IntegrationEditor({ initial, active, close }: { initial?: Resource; active: boolean; close: () => void }) {
  const [name, setName] = useState(() => text(document(initial).name));
  const [kind, setKind] = useState(() => text(document(initial).token_kind) || TokenKind.FineGrained);
  const [owner, setOwner] = useState(() => text(document(initial).resource_owner));
  const save = useRetainedMutation(`integration-save:${initial?.id ?? "new"}`, IntegrationQuery.saveIntegrationProfile, close);
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.INTEGRATION, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: active && initial ? 5000 : false });
  const stale = Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  const blocked = save.busy || save.uncertain;
  return <form onSubmit={(event) => { event.preventDefault(); if (blocked || stale || current.error) return; void save.send({ mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, schemaVersion: 1, documentJson: encode({ name, provider: "github.com", token_kind: kind, ...(owner ? { resource_owner: owner } : {}) }) }); }}>
    <h3>{initial ? "Rename GitHub profile" : "New GitHub profile"}</h3>
    <fieldset disabled={blocked}><label>Profile name<input required maxLength={160} value={name} onChange={(event) => setName(event.target.value)} /></label><label>Token type<select disabled={Boolean(initial)} value={kind} onChange={(event) => setKind(event.target.value)}><option value={TokenKind.FineGrained}>Fine-grained PAT (preferred)</option><option value={TokenKind.Classic}>Classic PAT</option></select></label><label>Resource owner<input disabled={Boolean(initial)} required={kind === TokenKind.FineGrained} maxLength={100} pattern="[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?" value={owner} onChange={(event) => setOwner(event.target.value)} /></label><p>Use a separate fine-grained profile for each repository owner. Repositories explicitly select their profile.</p></fieldset>
    {stale ? <p role="alert">This profile changed. Reopen its current version before saving.</p> : null}<Problem error={current.error || save.error} />
    <div className="actions"><button disabled={blocked || stale || Boolean(current.error)}>Save profile</button>{save.uncertain ? <button type="button" disabled={save.busy} onClick={save.retry}>Retry the same profile save</button> : null}<button type="button" disabled={blocked} onClick={close}>Cancel edit</button></div>
  </form>;
}
function IntegrationConnection({ initial, active, close }: { initial: Resource; active: boolean; close: () => void }) {
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.INTEGRATION, id: initial.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const current = [initial, result.data?.resource, acknowledged].filter((row): row is Resource => Boolean(row)).reduce((a, b) => a.revision >= b.revision ? a : b);
  const data = document(current), pending = object(data.pending), connection = object(data.connection), validation = object(connection.validation), identity = object(validation.identity);
  const [token, setToken] = useState("");
  const [retryIdentity, setRetryIdentity] = useState<MutationIdentity>();
  const [tokenError, setTokenError] = useState<unknown>();
  const [problem, setProblem] = useState<Document>();
  const [confirm, setConfirm] = useState(false);
  const replace = useMutation(IntegrationQuery.replaceIntegrationToken, { retry: false, gcTime: 0 });
  const changed = (row?: Resource) => { if (row) setAcknowledged(row); void result.refetch(); };
  const validate = useRetainedMutation(`integration-validate:${initial.id}`, IntegrationQuery.validateIntegrationProfile, (reply) => { changed(reply.profile); setProblem(problemDocument(reply.problemJson)); });
  const remove = useRetainedMutation(`integration-delete:${initial.id}`, IntegrationQuery.deleteIntegrationProfile, (reply) => { setConfirm(false); setProblem(problemDocument(reply.problemJson)); if (reply.deleted) close(); else changed(reply.profile); });
  const blocked = replace.isPending || validate.busy || validate.uncertain || remove.busy || remove.uncertain;
  const original = pendingIdentity(initial.id, pending);
  const deleting = pending.operation === "delete-profile";
  const mutation = (): MutationIdentity => ({ id: initial.id, expectedRevision: current.revision, requestId: newRequestId() });
  useEffect(() => { if (!active) setToken(""); }, [active]);
  const sendToken = async () => {
    if (blocked || deleting || !/^[!-~]{1,512}$/.test(token) || (pending.operation === "replace-token" && !original)) return;
    const selected = retryIdentity ?? (pending.operation === "replace-token" ? original : undefined) ?? mutation();
    const bytes = new TextEncoder().encode(token);
    setToken(""); setTokenError(undefined); setProblem(undefined); setRetryIdentity(selected);
    try {
      const reply = await replace.mutateAsync({ mutation: selected, token: bytes });
      changed(reply.profile); setProblem(problemDocument(reply.problemJson));
      setRetryIdentity(undefined);
    } catch (error) {
      setTokenError(error);
      if (![FailureCode.Unavailable, FailureCode.ServerUnavailable, FailureCode.Canceled, FailureCode.Internal].includes(clientFailure(error).code)) setRetryIdentity(undefined);
    } finally {
      // PAT bytes are transient write-only input. Neither successful nor failed
      // mutations retain a token for replay; retries require explicit reentry.
      bytes.fill(0); replace.reset();
    }
  };
  return <section><header><h3>{resourceName(current)}</h3><button disabled={replace.isPending} onClick={close}>Back to GitHub profiles</button></header><p>{text(data.token_kind)} · {text(data.resource_owner) || "No owner restriction declared"}</p>
    <p>Connection: {data.pending ? "Change pending; token use disabled" : data.connection ? "Stored on the server" : "No token connected"}</p><p>Identity validation: {text(validation.state) || "Not verified"}{text(validation.checked_at) ? ` · ${text(validation.checked_at)}` : ""}</p>
    {text(identity.login) ? <p>Authenticated as {text(identity.login)} · GitHub ID {text(identity.id)}</p> : null}<p>Identity validation does not verify access to repositories, pull requests, issues, checks or rulesets.</p>
    {active ? <GitHubTokenForm key={`${current.id}:${current.revision}`} profile={current} active={active} disabled={blocked || Boolean(retryIdentity || data.pending || result.error)} /> : null}
    <form onSubmit={(event) => { event.preventDefault(); void sendToken(); }}><fieldset disabled={blocked || deleting || Boolean(result.error)}><label>GitHub personal access token<input type="password" autoComplete="off" spellCheck={false} maxLength={512} value={token} onChange={(event) => setToken(event.target.value)} /></label><p>The selected server stores this token in its OS credential store. Saved tokens cannot be displayed.</p>{retryIdentity || pending.operation === "replace-token" ? <p>Reenter the same token to retry the original replacement. Delete this profile if that token is no longer available.</p> : null}<button disabled={!/^[!-~]{1,512}$/.test(token) || (pending.operation === "replace-token" && !original)}>{retryIdentity || pending.operation === "replace-token" ? "Retry original token replacement" : "Save and validate token"}</button></fieldset></form>
    {retryIdentity ? <p>Pending request: {retryIdentity.requestId}</p> : null}
    <div className="actions"><button disabled={blocked || Boolean(retryIdentity || data.pending || !data.connection || result.error)} onClick={() => void validate.send({ mutation: mutation() })}>Validate profile</button><button disabled={blocked || Boolean(result.error) || (deleting && !original)} onClick={() => deleting && original ? void remove.send({ mutation: original }) : setConfirm(true)}>{deleting ? "Retry original profile deletion" : "Delete profile"}</button></div>
    {confirm ? <div className="notice"><p>Delete this profile and its server-stored token? Repository associations will require reconfiguration.</p><button disabled={blocked} onClick={() => void remove.send({ mutation: mutation() })}>Confirm profile deletion</button><button disabled={blocked} onClick={() => setConfirm(false)}>Keep profile</button></div> : null}
    {text(object(validation.problem).message) ? <p role="alert">{text(object(validation.problem).message)} {text(object(validation.problem).guidance)}</p> : null}{problem ? <p role="alert">{text(problem.message)} {text(problem.guidance)}</p> : null}
    <Problem error={result.error || tokenError} />{[validate, remove].map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}>Retry the same {index === 0 ? "validation" : "deletion"}</button> : null}</div>)}
  </section>;
}
export function Integrations({ active, showCategoryIntro = true, onWorkflowReadyChange }: { active: boolean; showCategoryIntro?: boolean; onWorkflowReadyChange?: (active: boolean) => void }) {
  const [page, setPage] = useState("");
  const [editing, setEditing] = useState<{ initial?: Resource; key: string }>();
  const [selected, setSelected] = useState<Resource>();
  const client = useQueryClient();
  const result = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.INTEGRATION, pageSize: 50, pageToken: page } }, { enabled: active });
  const done = () => { setEditing(undefined); setSelected(undefined); void client.invalidateQueries({ refetchType: "active" }); };
  useEffect(() => {
    onWorkflowReadyChange?.(Boolean(editing || selected));
    return () => onWorkflowReadyChange?.(false);
  }, [editing, onWorkflowReadyChange, selected]);
  return <section aria-label="GitHub integrations">{showCategoryIntro ? <><h2>Integrations · GitHub</h2><p>Named GitHub.com PAT profiles are separate from AI accounts. Each repository selects its profile explicitly.</p></> : null}{editing ? <IntegrationEditor key={editing.key} initial={editing.initial} active={active} close={done} /> : selected ? <IntegrationConnection key={selected.id} initial={selected} active={active} close={done} /> : <><button onClick={() => setEditing({ key: newRequestId() })}>New GitHub profile</button><button onClick={() => void result.refetch()}>Refresh GitHub profiles</button><Problem error={result.error} />{result.data?.resources.map((row) => <article className="result" key={row.id}><h3>{resourceName(row)}</h3><p>{text(document(row).token_kind)} · {text(document(row).resource_owner)}</p><button disabled={row.schemaVersion !== 1} onClick={() => setSelected(row)}>Manage {resourceName(row)}</button><button disabled={row.schemaVersion !== 1} onClick={() => setEditing({ initial: row, key: newRequestId() })}>Rename {resourceName(row)}</button></article>)}{result.data?.resources.length === 0 ? <p>No GitHub profiles.</p> : null}<nav aria-label="GitHub profile pages"><button disabled={!page || result.isFetching} onClick={() => setPage("")}>First page</button><button disabled={!result.data?.nextPageToken || result.isFetching} onClick={() => setPage(result.data!.nextPageToken)}>Next page</button></nav></>}</section>;
}
