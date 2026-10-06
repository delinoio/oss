import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { GitHubTokenAccess, GitHubTokenKind, IntegrationQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useSettingsOpening } from "./settings-lifetime";

const accessNames = { [GitHubTokenAccess.SELECTED_REPOSITORIES]: "selected-repositories", [GitHubTokenAccess.PUBLIC_REPOSITORIES]: "public-repositories", [GitHubTokenAccess.PRIVATE_REPOSITORIES]: "private-repositories" };
export type Access = keyof typeof accessNames;
export const githubOwnerValid = (owner: string) => /^[A-Za-z0-9]([A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(owner);
export function githubForm(raw: Uint8Array, profile: Resource, access: Access): string | undefined {
  if (raw.byteLength > 4096) return;
  let value;
  try { value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return; }
  const p = document(profile);
  if (value.profile_id !== profile.id || value.profile_revision !== profile.revision.toString() || value.token_kind !== p.token_kind || text(value.resource_owner) !== text(p.resource_owner) || value.access !== accessNames[access]) return;
  const expected = githubTokenFormURL(text(p.token_kind), text(p.resource_owner), access);
  return expected && value.url === expected ? expected : undefined;
}

export function githubTokenFormURL(kind: string, owner: string, access: Access): string | undefined {
  if (owner && !githubOwnerValid(owner)) return;
  const q = new URLSearchParams();
  let path = "/settings/tokens/new";
  if (kind === "fine-grained" && access === GitHubTokenAccess.SELECTED_REPOSITORIES && githubOwnerValid(owner)) {
    path = "/settings/personal-access-tokens/new";
    q.set("name", "DeliDev read-only"); q.set("description", "Read-only repository inspection"); q.set("target_name", owner); q.set("expires_in", "30");
    for (const key of ["metadata", "contents", "pull_requests", "statuses", "issues"]) q.set(key, "read");
  } else if (kind === "classic" && access === GitHubTokenAccess.PUBLIC_REPOSITORIES) q.set("description", "DeliDev public read-only");
  else if (kind === "classic" && access === GitHubTokenAccess.PRIVATE_REPOSITORIES) { q.set("description", "DeliDev private repository lookup"); q.set("scopes", "repo"); }
  else return;
  q.sort();
  return `https://github.com${path}?${q}`;
}

export function OpenGitHub({ url, disabled = false, label = "Open on GitHub" }: { url: string; disabled?: boolean; label?: string }) {
  const [busy, setBusy] = useState(false), [status, setStatus] = useState("");
  const open = async () => {
    if (busy || disabled || !isTauri()) return;
    setBusy(true); setStatus("");
    try { await invoke("open_github", { url }); setStatus("Sent to your default browser. This does not verify page access."); }
    catch { setStatus("Browser opening was not confirmed. Inspect your browser before trying again."); }
    finally { setBusy(false); }
  };
  return <div><button type="button" disabled={disabled || busy || !isTauri()} onClick={() => void open()}>{label}</button>{!isTauri() ? <p>Browser opening is available in the desktop app.</p> : null}{status ? <p role="status">{status}</p> : null}</div>;
}

export function GitHubTokenForm({ profile, active, disabled, showHeading = true }: { profile: Resource; active: boolean; disabled: boolean; showHeading?: boolean }) {
  const p = document(profile), fine = p.token_kind === "fine-grained";
  const [access, setAccess] = useState<Access>(fine ? GitHubTokenAccess.SELECTED_REPOSITORIES : GitHubTokenAccess.PUBLIC_REPOSITORIES);
  const [busy, setBusy] = useState(false), [status, setStatus] = useState("");
  const epoch = useRef(0);
  useEffect(() => { epoch.current++; if (!active || disabled) setBusy(false); return () => { epoch.current++; }; }, [active, disabled]);
  const form = useQuery(IntegrationQuery.getGitHubTokenForm, { profileId: profile.id, expectedRevision: profile.revision, access }, { enabled: false, retry: false, gcTime: 0, staleTime: 0 });
  const open = async () => {
    if (!active || busy || disabled || !isTauri()) return;
    setBusy(true); setStatus("");
    const original = epoch.current;
    try {
      const reply = await form.refetch({ throwOnError: true });
      const url = reply.data?.schemaVersion === 1 ? githubForm(reply.data.documentJson, profile, access) : undefined;
      if (!url) throw new Error("Invalid form");
      if (epoch.current !== original) return;
      await invoke("open_github", { url });
      if (epoch.current === original) setStatus("Sent to your default browser. Review repository selection, permissions and expiry before generating a token.");
    } catch { if (epoch.current === original) setStatus("The official form could not be confirmed. Refresh the profile and inspect your browser before trying again."); }
    finally { if (epoch.current === original) setBusy(false); }
  };
  return <section aria-label="GitHub token creation">{showHeading ? <h4>Create a token on GitHub</h4> : null}{fine ? <><p>Resource owner: {text(p.resource_owner)}. The official form defaults to All repositories. Choose Only select repositories and select the repositories this profile needs.</p><p>Metadata, Contents, Pull requests, Issues and Commit statuses are preselected as read-only, with a 30-day expiry. The verified form offers no Checks permission; access must be checked separately. Organization approval may be required.</p></> : <><label>Classic token access<select value={access} disabled={busy || disabled} onChange={(event) => { setAccess(Number(event.target.value) as Access); setStatus(""); }}><option value={GitHubTokenAccess.PUBLIC_REPOSITORIES}>Public repositories only (no scopes)</option><option value={GitHubTokenAccess.PRIVATE_REPOSITORIES}>Private repositories (broad repo scope)</option></select></label>{access === GitHubTokenAccess.PRIVATE_REPOSITORIES ? <p role="note">Classic repo grants broad read/write access to private repositories even though DeliDev only reads GitHub APIs. Prefer fine-grained selected repositories when possible.</p> : <p>No classic scopes will be preselected for public repository lookup.</p>}</>}<p>Review the form and create the token yourself, then enter it in the protected token field below. Opening a form does not create or save a token.</p><button type="button" disabled={!active || disabled || busy || !isTauri()} onClick={() => void open()}>Open official GitHub token form</button>{!isTauri() ? <p>Open the official form from the desktop app or use the CLI token-form command.</p> : null}{status ? <p role="status">{status}</p> : null}</section>;
}

// Draft preparation cannot weaken the saved profile's revision-bound form read.
// It shares the canonical URL check and the same closed native opener instead.
export function GitHubDraftTokenForm({ kind, owner, changeKind, changeOwner, active, disabled }: { kind: GitHubTokenKind; owner: string; changeKind: (kind: GitHubTokenKind) => void; changeOwner: (owner: string) => void; active: boolean; disabled: boolean }) {
  const opening = useSettingsOpening();
  const [classicAccess, setClassicAccess] = useState<Access>(GitHubTokenAccess.PUBLIC_REPOSITORIES);
  const [busy, setBusy] = useState(false), [status, setStatus] = useState("");
  const [requestId] = useState(newRequestId);
  const fine = kind === GitHubTokenKind.FINE_GRAINED;
  const access = fine ? GitHubTokenAccess.SELECTED_REPOSITORIES : classicAccess;
  const epoch = useRef(0);
  useLayoutEffect(() => { epoch.current++; setStatus(""); setBusy(false); return () => { epoch.current++; }; }, [active, disabled, kind, owner, access]);
  const expected = githubTokenFormURL(fine ? "fine-grained" : "classic", owner, access);
  const form = useQuery(IntegrationQuery.prepareGitHubTokenForm, { requestId, tokenKind: kind, resourceOwner: owner, access }, { enabled: false, retry: false, gcTime: 0, staleTime: 0 });
  const open = async () => {
    if (!active || disabled || busy || !expected || !isTauri()) return;
    const original = epoch.current;
    setBusy(true); setStatus("");
    try {
      const result = (await form.refetch({ throwOnError: true })).data;
      if (!result || result.requestId !== requestId || result.tokenKind !== kind || result.resourceOwner !== owner || result.access !== access || result.url !== expected) throw new Error("Invalid draft form");
      if (epoch.current !== original || opening?.disposed) return;
      await invoke("open_github", { url: expected });
      if (epoch.current === original) setStatus("Sent to your default browser. Review repository selection, permissions and expiry before generating a token.");
    } catch { if (epoch.current === original) setStatus("The official form could not be confirmed. Inspect your browser before trying again."); }
    finally { if (epoch.current === original) setBusy(false); }
  };
  return <details className="integration-draft-guidance" open>
    <summary>Create a token on GitHub</summary>
    <form onSubmit={event => { event.preventDefault(); void open(); }}>
      <fieldset disabled={disabled || busy}>
        <label>Token type<select value={kind} onChange={event => changeKind(Number(event.target.value) as GitHubTokenKind)}><option value={GitHubTokenKind.FINE_GRAINED}>Fine-grained PAT (preferred)</option><option value={GitHubTokenKind.CLASSIC}>Classic PAT</option></select></label>
        <label>Resource owner<input value={owner} onChange={event => changeOwner(event.target.value)} maxLength={100} placeholder="GitHub user or organization" required={fine} pattern="[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?" /></label>
        {fine ? <><p>The official form defaults to All repositories. Choose Only select repositories and select the repositories this profile needs.</p><p>Metadata, Contents, Pull requests, Issues and Commit statuses are preselected as read-only, with a 30-day expiry. The verified form offers no Checks permission; access must be checked separately. Organization approval may be required.</p></> : <><label>Classic token access<select value={classicAccess} onChange={event => setClassicAccess(Number(event.target.value) as Access)}><option value={GitHubTokenAccess.PUBLIC_REPOSITORIES}>Public repositories only (no scopes)</option><option value={GitHubTokenAccess.PRIVATE_REPOSITORIES}>Private repositories (broad repo scope)</option></select></label>{classicAccess === GitHubTokenAccess.PRIVATE_REPOSITORIES ? <p role="note">Classic repo grants broad read/write access to private repositories even though DeliDev only reads GitHub APIs. Prefer fine-grained selected repositories when possible.</p> : <p>No classic scopes will be preselected for public repository lookup.</p>}</>}
        <button type="submit" disabled={!active || !expected || !isTauri() || busy}><svg className="integration-external-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M14 3h7v7m0-7L10 14M10 3H3v18h18v-7" /></svg>Open official GitHub token form</button>
        <p>Review the owner, permissions and expiry, create the token yourself, then paste it above. Opening a form does not create or save a token.</p>
      </fieldset>
    </form>
    {!isTauri() ? <p>Browser opening is available in the desktop app.</p> : null}
    {busy ? <p role="status">Preparing the official GitHub token form…</p> : null}{status ? <p role="status">{status}</p> : null}
  </details>;
}
