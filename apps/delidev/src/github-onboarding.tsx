// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useRef, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { useTransport } from "@connectrpc/connect-query";
import { createClient } from "@connectrpc/connect";
import { EntityKind, FailureCode, GitHubTokenIdentityState as IdentityState, GitHubTokenKind, IntegrationService, SaveIntegrationProfileRequestSchema, clientFailure, isEntityId, newRequestId, type GitHubTokenIdentity, type Resource, type SaveIntegrationProfileRequest } from "@delinoio/delidev-api-client";
import { document, encode, object, text, type Document } from "./documents";
import { GitHubDraftTokenForm, githubOwnerValid } from "./github-opening";
import { useSettingsOpening } from "./settings-lifetime";
import { Problem } from "./ui";

export type GitHubTokenRetry = { id: string; expectedRevision: bigint; requestId: string };
enum Stage { Token, Confirm }
const tokenValid = (token: string) => /^[!-~]{1,512}$/.test(token);
const uncertain = (error: unknown) => [FailureCode.Unavailable, FailureCode.ServerUnavailable, FailureCode.Canceled, FailureCode.Internal].includes(clientFailure(error).code);
const identityFailure: Partial<Record<IdentityState, string>> = {
  [IdentityState.INVALID_TOKEN]: "GitHub rejected this token. Enter a valid token and try again.",
  [IdentityState.ACCESS_RESTRICTED]: "GitHub restricted this token. Check its permissions and organization approval.",
  [IdentityState.SSO_REQUIRED]: "Authorize this token for your organization’s SSO, then try again.",
  [IdentityState.RATE_LIMITED]: "GitHub rate-limited verification. Wait for the limit to reset and try again.",
  [IdentityState.UNAVAILABLE]: "GitHub identity could not be verified. Check the server connection and try again.",
};
function validIdentity(identity?: GitHubTokenIdentity): identity is GitHubTokenIdentity {
  return Boolean(identity && /^[1-9][0-9]{0,19}$/.test(identity.id) && BigInt(identity.id) <= 18446744073709551615n && githubOwnerValid(identity.login) && identity.nodeId.trim() && !identity.nodeId.includes("\0") && new TextEncoder().encode(identity.nodeId).length <= 256);
}
function savedProfile(profile: Resource | undefined, request: SaveIntegrationProfileRequest): profile is Resource {
  if (!profile || !isEntityId(profile.id) || profile.kind !== EntityKind.INTEGRATION || profile.schemaVersion !== 1 || profile.revision < 1n) return false;
  const expected = object(JSON.parse(new TextDecoder().decode(request.documentJson))), actual = document(profile);
  return actual.name === expected.name && actual.provider === expected.provider && actual.token_kind === expected.token_kind && text(actual.resource_owner) === text(expected.resource_owner);
}
function replyProblem(raw: Uint8Array): Document | undefined {
  if (!raw.length) return;
  if (raw.length > 4096) return { message: "Inspect the saved profile before continuing." };
  try { return object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return { message: "Inspect the saved profile before continuing." }; }
}

export function GitHubOnboarding({ active, close, connected }: { active: boolean; close: () => void; connected: (profile: Resource, retry?: GitHubTokenRetry, problem?: Document) => void }) {
  const transport = useTransport(), opening = useSettingsOpening();
  const client = useMemo(() => createClient(IntegrationService, transport), [transport]);
  const [stage, setStage] = useState(Stage.Token), [token, setToken] = useState("");
  const [kind, setKind] = useState(GitHubTokenKind.FINE_GRAINED), [owner, setOwner] = useState("");
  const [name, setName] = useState(""), [identity, setIdentity] = useState<GitHubTokenIdentity>();
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>(), [message, setMessage] = useState("");
  const [saveUncertain, setSaveUncertain] = useState(false);
  const secret = useRef<Uint8Array | undefined>(undefined), sent = useRef<Uint8Array | undefined>(undefined);
  const originalSave = useRef<SaveIntegrationProfileRequest | undefined>(undefined);
  const request = useRef<AbortController | undefined>(undefined);
  const epoch = useRef(0), working = useRef(false), mounted = useRef(false), currentActive = useRef(active), nameEdited = useRef(false);
  currentActive.current = active;
  const tokenInput = useRef<HTMLInputElement>(null), nameInput = useRef<HTMLInputElement>(null);
  const clearSecrets = () => { secret.current?.fill(0); secret.current = undefined; sent.current?.fill(0); sent.current = undefined; };
  const live = (original: number) => mounted.current && currentActive.current && !opening?.disposed && epoch.current === original;
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; epoch.current++; request.current?.abort(); clearSecrets(); };
  }, [opening]);
  useEffect(() => {
    if (!active) { epoch.current++; request.current?.abort(); clearSecrets(); setToken(""); setIdentity(undefined); setStage(Stage.Token); close(); }
  }, [active]);
  useEffect(() => { if (active) (stage === Stage.Token ? tokenInput : nameInput).current?.focus(); }, [active, stage]);

  const verify = async () => {
    if (!active || working.current || saveUncertain || !tokenValid(token)) return;
    working.current = true; setBusy(true); setError(undefined); setMessage(""); clearSecrets();
    const original = ++epoch.current, requestId = newRequestId(), controller = new AbortController();
    request.current = controller;
    const draft = new TextEncoder().encode(token), copy = draft.slice();
    secret.current = draft; sent.current = copy; setToken("");
    let verified = false;
    try {
      // The only retained PAT is this wizard's bounded draft buffer. Generated
      // direct calls keep both the token and inspection out of query caches.
      const result = await client.inspectGitHubToken({ requestId, token: copy }, { signal: controller.signal });
      if (!live(original)) return;
      if (result.requestId !== requestId) throw new Error("Invalid inspection identity");
      if (result.state !== IdentityState.VERIFIED) {
        if (result.identity || !identityFailure[result.state]) throw new Error("Invalid identity observation");
        setMessage(identityFailure[result.state]!); return;
      }
      if (!validIdentity(result.identity) || result.problemJson.length) throw new Error("Invalid verified identity");
      verified = true; setIdentity(result.identity);
      if (!nameEdited.current) setName(result.identity.login);
      setStage(Stage.Confirm);
    } catch (reason) { if (live(original)) setError(reason); }
    finally {
      copy.fill(0); if (sent.current === copy) sent.current = undefined;
      if (!verified) { draft.fill(0); if (secret.current === draft) secret.current = undefined; }
      working.current = false;
      if (live(original)) { setBusy(false); if (!verified) tokenInput.current?.focus(); }
    }
  };
  const ownerValid = kind === GitHubTokenKind.FINE_GRAINED ? githubOwnerValid(owner) : !owner || githubOwnerValid(owner);
  const nameBytes = new TextEncoder().encode(name).length;
  const nameValid = Boolean(name.trim()) && nameBytes <= 160;
  const save = async (retry = false) => {
    if (!active || working.current || (retry ? !originalSave.current : saveUncertain || !secret.current || !identity || !ownerValid || !nameValid)) return;
    working.current = true; setBusy(true); setError(undefined); setMessage("");
    const original = ++epoch.current;
    const bytes = secret.current;
    const input = originalSave.current ?? create(SaveIntegrationProfileRequestSchema, { mutation: { expectedRevision: 0n, requestId: newRequestId() }, schemaVersion: 1, documentJson: encode({ name, provider: "github.com", token_kind: kind === GitHubTokenKind.FINE_GRAINED ? "fine-grained" : "classic", ...(owner ? { resource_owner: owner } : {}) }) });
    originalSave.current = input;
    let profile: Resource | undefined, tokenRetry: GitHubTokenRetry | undefined;
    try {
      const result = await client.saveIntegrationProfile(input);
      if (!live(original)) return;
      if (result.requestId !== input.mutation?.requestId || !savedProfile(result.profile, input)) throw new Error("Invalid profile acknowledgment");
      profile = result.profile; originalSave.current = undefined; setSaveUncertain(false);
      // A metadata replay after uncertainty has no retained credential. Enter
      // Manage with the acknowledged profile instead of creating or deleting it.
      if (!bytes?.length || !bytes.some(byte => byte !== 0)) {
        connected(profile, undefined, { message: "Profile saved. Reenter your token to finish connecting it." }); return;
      }
      tokenRetry = { id: profile.id, expectedRevision: profile.revision, requestId: newRequestId() };
      const tokenCopy = bytes.slice();
      sent.current = tokenCopy;
      const replaced = await client.replaceIntegrationToken({ mutation: tokenRetry, token: tokenCopy });
      if (!live(original)) return;
      if (replaced.requestId !== tokenRetry.requestId || !savedProfile(replaced.profile, input) || replaced.profile.id !== profile.id || replaced.profile.revision < profile.revision) throw new Error("Invalid token acknowledgment");
      const problem = replyProblem(replaced.problemJson);
      if (problem || document(replaced.profile).pending) connected(replaced.profile, undefined, problem);
      else close();
    } catch (reason) {
      if (!live(original)) return;
      if (profile) connected(profile, uncertain(reason) ? tokenRetry : undefined, { message: "The profile was saved, but token connection was not confirmed. Inspect its current state and reenter the token to continue." });
      else {
        setSaveUncertain(uncertain(reason)); setError(reason);
        if (!uncertain(reason)) { originalSave.current = undefined; setIdentity(undefined); setStage(Stage.Token); }
      }
    } finally { clearSecrets(); working.current = false; if (live(original)) setBusy(false); }
  };
  const back = () => { epoch.current++; clearSecrets(); setToken(""); setIdentity(undefined); setError(undefined); setMessage(""); setStage(Stage.Token); };
  const cancel = () => { epoch.current++; request.current?.abort(); clearSecrets(); close(); };
  return <section className="integration-onboarding" aria-label="New GitHub profile">
    <h3>New GitHub profile</h3>
    <p className="integration-secondary">Step {stage === Stage.Token ? "1 of 2 · Verify token" : "2 of 2 · Confirm profile"}</p>
    {stage === Stage.Token ? <>
      <form onSubmit={event => { event.preventDefault(); void verify(); }}>
        <fieldset disabled={busy}><label>GitHub personal access token<input ref={tokenInput} type="password" autoComplete="off" spellCheck={false} maxLength={512} value={token} onChange={event => setToken(event.target.value)} placeholder="Enter a personal access token" /></label><p>We’ll use your token to find your GitHub username.</p></fieldset>
        {busy ? <p role="status">Verifying GitHub token…</p> : null}{message ? <p role="alert">{message}</p> : null}<Problem error={error} />
        <div className="actions"><button className="primary" disabled={busy || !tokenValid(token)}>Verify token</button><button type="button" onClick={cancel}>Cancel</button></div>
      </form>
      <GitHubDraftTokenForm kind={kind} owner={owner} changeKind={setKind} changeOwner={setOwner} active={active} disabled={busy} />
      <p className="integration-storage-note">Your token is saved only when you confirm the profile.</p>
    </> : <form onSubmit={event => { event.preventDefault(); void save(); }}>
      <p className="integration-verified">Authenticated as <strong>{identity?.login}</strong></p>
      <fieldset disabled={busy || saveUncertain}>
        <label>Profile name<input ref={nameInput} required maxLength={160} value={name} onChange={event => { nameEdited.current = true; setName(event.target.value); }} /></label><p>Filled from your GitHub username. You can change it.</p>
        {nameBytes > 160 ? <p role="alert">This profile name is too long. Shorten it before saving.</p> : name && !name.trim() ? <p role="alert">Enter a nonblank profile name.</p> : null}
        <label>Token type<select value={kind} onChange={event => setKind(Number(event.target.value) as GitHubTokenKind)}><option value={GitHubTokenKind.FINE_GRAINED}>Fine-grained PAT (preferred)</option><option value={GitHubTokenKind.CLASSIC}>Classic PAT</option></select></label>
        <label>Resource owner<input required={kind === GitHubTokenKind.FINE_GRAINED} maxLength={100} pattern="[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?" placeholder="GitHub user or organization" value={owner} onChange={event => setOwner(event.target.value)} /></label><p>Enter the owner selected when you created this token. Token form settings carry into this field; you can change them.</p>
        <p>Use a separate fine-grained profile for each repository owner. Repositories explicitly select their profile.</p><p>Token type and owner cannot be changed after creation.</p>
      </fieldset>
      {busy ? <p role="status">Saving GitHub profile and connecting token…</p> : null}<Problem error={error} />
      {saveUncertain ? <p role="status">The profile save is uncertain. Retry only the original save; your token was cleared and must be reentered after reconciliation.</p> : null}
      <div className="actions"><button className="primary" disabled={busy || saveUncertain || !ownerValid || !nameValid || !secret.current}>Save and connect</button>{saveUncertain ? <button type="button" disabled={busy} onClick={() => void save(true)}>Retry the same profile save</button> : null}<button type="button" disabled={busy || saveUncertain} onClick={back}>Back</button><button type="button" disabled={busy || saveUncertain} onClick={cancel}>Cancel</button></div>
      <p className="integration-storage-note">Tokens are stored in the selected server’s OS credential store. Saved tokens cannot be displayed.</p>
    </form>}
    <p className="integration-access-note">Identity verification does not confirm repository access.</p>
  </section>;
}
