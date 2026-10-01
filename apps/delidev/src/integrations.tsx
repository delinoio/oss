import { SettingsHeading, SettingsEmpty, SettingsLoading } from "./settings-presentation";
import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, FailureCode, IntegrationQuery, ResourceQuery, clientFailure, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, object, resourceName, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { GitHubTokenForm } from "./github-opening";
import { useSettingsOpening } from "./settings-lifetime";

enum TokenKind { FineGrained = "fine-grained", Classic = "classic" }
enum IdentityState {
  Verified = "identity-verified", Invalid = "invalid-token", Restricted = "access-restricted",
  SsoRequired = "sso-required", RateLimited = "rate-limited", Unavailable = "unavailable",
}
const identityLabels: Record<IdentityState, string> = {
  [IdentityState.Verified]: "Identity verified", [IdentityState.Invalid]: "Invalid token",
  [IdentityState.Restricted]: "Access restricted", [IdentityState.SsoRequired]: "SSO required",
  [IdentityState.RateLimited]: "Rate limited", [IdentityState.Unavailable]: "Unavailable",
};
function profileDescription(data: Document): string {
  const kind = data.token_kind === TokenKind.FineGrained ? "Fine-grained PAT" : data.token_kind === TokenKind.Classic ? "Classic PAT" : "Unsupported token type";
  return `${kind} · ${text(data.resource_owner) || "No owner restriction declared"}`;
}
function ProfileFacts({ profile }: { profile: Resource }) {
  const data = document(profile), validation = object(object(data.connection).validation);
  const state = text(validation.state);
  const supported = profile.schemaVersion === 1;
  return <dl className="integration-facts">
    <div><dt>Token storage</dt><dd>{!supported ? "Unsupported profile version" : data.pending ? "Change pending; token use disabled" : data.connection ? "Token stored" : "No token connected"}</dd></div>
    <div><dt>Identity validation</dt><dd>{!supported ? "Unsupported profile version" : !state ? "Not verified" : Object.hasOwn(identityLabels, state) ? identityLabels[state as IdentityState] : "Unknown identity observation"}</dd>
      {supported && text(validation.checked_at) ? <dd className="integration-secondary">Checked at {text(validation.checked_at)}</dd> : null}
    </div>
  </dl>;
}
enum IntegrationIconKind { GitHub, Link, Shield }
function IntegrationIcon({ kind }: { kind: IntegrationIconKind }) {
  return <svg className="integration-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
    {kind === IntegrationIconKind.GitHub ? <path d="M7 19c-4 1-4-2-6-2m7 5v-4c0-1 .5-2 1-2-4 0-6-2-6-5 0-2 1-3 2-4 0-1 0-3 1-4l4 2h4l4-2c1 1 1 3 1 4 1 1 2 2 2 4 0 3-2 5-6 5 1 0 1 1 1 2v4" />
      : kind === IntegrationIconKind.Link ? <path d="m9 15 6-6m-6 8-2 2a4 4 0 0 1-6-6l4-4a4 4 0 0 1 6 0m2-2 2-2a4 4 0 0 1 6 6l-4 4a4 4 0 0 1-6 0" />
      : <path d="m12 2 8 3v6c0 5-4 8-8 11-4-3-8-6-8-11V5Zm-4 9 3 3 5-6" />}
  </svg>;
}
const tokenStorageNote = "Tokens are stored in the selected server's OS credential store. Saved tokens cannot be displayed.";
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
  const nameInput = useRef<HTMLInputElement>(null);
  // Focus only when this editor first becomes visible. Category changes and
  // background reads retain its draft and must not steal focus on return.
  const entered = useRef(false);
  useEffect(() => { if (active && !entered.current) { entered.current = true; nameInput.current?.focus(); } }, [active]);
  return <form className="integration-editor" onSubmit={(event) => { event.preventDefault(); if (blocked || stale || current.error) return; void save.send({ mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, schemaVersion: 1, documentJson: encode({ name, provider: "github.com", token_kind: kind, ...(owner ? { resource_owner: owner } : {}) }) }); }}>
    <h3>{initial ? "Rename GitHub profile" : "New GitHub profile"}</h3>
    <fieldset disabled={blocked}><label>Profile name<input ref={nameInput} required maxLength={160} value={name} onChange={(event) => setName(event.target.value)} /></label><label>Token type<select disabled={Boolean(initial)} value={kind} onChange={(event) => setKind(event.target.value)}><option value={TokenKind.FineGrained}>Fine-grained PAT (preferred)</option><option value={TokenKind.Classic}>Classic PAT</option></select></label><label>Resource owner<input disabled={Boolean(initial)} required={kind === TokenKind.FineGrained} maxLength={100} pattern="[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?" value={owner} onChange={(event) => setOwner(event.target.value)} /></label><p>Use a separate fine-grained profile for each repository owner. Repositories explicitly select their profile.</p><p>Token type and owner cannot be changed after creation.</p></fieldset>
    {stale ? <p role="alert">This profile changed. Reopen its current version before saving.</p> : null}<Problem error={current.error || save.error} />
    <div className="actions"><button className="primary" disabled={blocked || stale || Boolean(current.error)}>Save profile</button>{save.uncertain ? <button type="button" disabled={save.busy} onClick={save.retry}>Retry the same profile save</button> : null}<button type="button" disabled={blocked} onClick={close}>Cancel edit</button></div>
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
  const opening = useSettingsOpening();
  const initiallyExplainToken = useRef(!document(initial).connection);

  const replace = useMutation(IntegrationQuery.replaceIntegrationToken, { retry: false, gcTime: 0, meta: opening?.mutationMeta });
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
  return <section className="integration-manage">
    <header><div><h3>{resourceName(current)}</h3><p>{profileDescription(data)}</p></div><button disabled={replace.isPending} onClick={close}>Back to GitHub profiles</button></header>
    <ProfileFacts profile={current} />
    {text(identity.login) ? <p>Authenticated as {text(identity.login)} · GitHub ID {text(identity.id)}</p> : null}
    <section className="integration-section" aria-label="Connect a token"><h4>Connect a token</h4>
      <details className="integration-token-guidance" open={initiallyExplainToken.current}>
        <summary>Create a token on GitHub</summary>
        {active ? <GitHubTokenForm key={`${current.id}:${current.revision}`} profile={current} active={active} disabled={blocked || Boolean(retryIdentity || data.pending || result.error)} showHeading={false} /> : null}
      </details>
      <form onSubmit={(event) => { event.preventDefault(); void sendToken(); }}><fieldset disabled={blocked || deleting || Boolean(result.error)}>
        <label>GitHub personal access token<input type="password" autoComplete="off" spellCheck={false} maxLength={512} placeholder="Enter a personal access token" value={token} onChange={(event) => setToken(event.target.value)} /></label>
        <p className="integration-secondary">{tokenStorageNote}</p>
        {retryIdentity || pending.operation === "replace-token" ? <p>Reenter the same token to retry the original replacement. Delete this profile if that token is no longer available.</p> : null}
        <button className="primary" disabled={!/^[!-~]{1,512}$/.test(token) || (pending.operation === "replace-token" && !original)}>{retryIdentity || pending.operation === "replace-token" ? "Retry original token replacement" : "Save and validate token"}</button>
      </fieldset></form>
      {retryIdentity ? <p>Pending request: {retryIdentity.requestId}</p> : null}
    </section>
    <section className="integration-section" aria-label="Validate identity"><h4>Identity validation</h4>
      <p>Identity validation does not verify access to repositories, pull requests, issues, checks or rulesets.</p>
      <button disabled={blocked || Boolean(retryIdentity || data.pending || !data.connection || result.error)} onClick={() => void validate.send({ mutation: mutation() })}>Validate profile</button>
    </section>
    <section className="integration-section integration-delete" aria-label="Delete profile"><h4>Delete profile</h4>
      <p>Deletion requires confirmation. Repository associations will need reconfiguration.</p>
      <button className="integration-danger" disabled={blocked || Boolean(result.error) || (deleting && !original)} onClick={() => deleting && original ? void remove.send({ mutation: original }) : setConfirm(true)}>{deleting ? "Retry original profile deletion" : "Delete profile"}</button>
      {confirm ? <div className="notice"><p>Delete this profile and its server-stored token? Repository associations will require reconfiguration.</p><div className="actions"><button className="integration-danger" disabled={blocked} onClick={() => void remove.send({ mutation: mutation() })}>Confirm profile deletion</button><button disabled={blocked} onClick={() => setConfirm(false)}>Keep profile</button></div></div> : null}
    </section>
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
  const successfulEmpty = Boolean(result.data && !result.error && !page && result.data.resources.length === 0 && !result.data.nextPageToken);
  const createProfile = <button className="primary" onClick={() => setEditing({ key: newRequestId() })}><span aria-hidden="true">+ </span>New GitHub profile</button>;
  return <section className="github-integrations" aria-label="GitHub integrations">
    {showCategoryIntro ? <SettingsHeading title="Integrations" description="Manage GitHub profiles for repository access. AI accounts are configured separately." actions={!editing && !selected ? <><button aria-label="Refresh GitHub profiles" onClick={() => void result.refetch()}>Refresh</button>{createProfile}</> : undefined} /> : null}
    {editing ? <IntegrationEditor key={editing.key} initial={editing.initial} active={active} close={done} /> : selected ? <IntegrationConnection key={selected.id} initial={selected} active={active} close={done} /> : <>
      <section className="integration-panel" aria-label="GitHub profiles" aria-busy={result.isFetching}>
        <header className="integration-panel-header"><div className="integration-provider"><span className="integration-provider-mark"><IntegrationIcon kind={IntegrationIconKind.GitHub} /></span><div><h3>GitHub</h3><p>GitHub.com · Personal access tokens</p></div></div>
          {!showCategoryIntro ? <div className="actions"><button aria-label="Refresh GitHub profiles" onClick={() => void result.refetch()}>Refresh</button>{createProfile}</div> : null}
        </header>
        <div className="integration-panel-body">
          {!result.data && result.isPending ? <SettingsLoading label="Loading GitHub profiles…" /> : null}
          {result.data && result.isFetching ? <p role="status">Refreshing GitHub profiles… Previous results are shown.</p> : null}
          <Problem error={result.error} />
          {result.data && result.error ? <p role="status">Previous GitHub profile results are stale because the refresh failed.</p> : null}
          {successfulEmpty ? <SettingsEmpty title="Add your first GitHub profile" icon={<IntegrationIcon kind={IntegrationIconKind.Link} />}><p>Create a named profile, then connect a personal access token.</p><p>Each repository selects its profile explicitly.</p></SettingsEmpty> : <>
            {result.data?.resources.map((row) => <article className="integration-row" key={row.id}><div><h3>{resourceName(row)}</h3><p className="integration-secondary">{profileDescription(document(row))}</p><ProfileFacts profile={row} /></div>
              <div className="actions"><button aria-label={`Manage ${resourceName(row)}`} disabled={row.schemaVersion !== 1} onClick={() => setSelected(row)}>Manage</button><button aria-label={`Rename ${resourceName(row)}`} disabled={row.schemaVersion !== 1} onClick={() => setEditing({ initial: row, key: newRequestId() })}>Rename</button></div>
            </article>)}
            {result.data?.resources.length === 0 ? <p>No GitHub profiles on this page.</p> : null}
          </>}
        </div>
        {successfulEmpty ? <ol className="integration-steps" aria-label="GitHub profile setup explanation"><li>Create a profile</li><li>Connect a token</li><li>Select it in Repositories</li></ol> : null}
        <p className="integration-access-note">Identity verification does not confirm repository access.</p>
      </section>
      <p className="integration-storage-note"><IntegrationIcon kind={IntegrationIconKind.Shield} /><span>{tokenStorageNote}</span></p>
      {!successfulEmpty ? <nav className="settings-pages" aria-label="GitHub profile pages"><button disabled={!page || result.isFetching} onClick={() => setPage("")}>First page</button><button disabled={!result.data?.nextPageToken || result.isFetching} onClick={() => setPage(result.data!.nextPageToken)}>Next page</button></nav> : null}
    </>}
  </section>;
}
