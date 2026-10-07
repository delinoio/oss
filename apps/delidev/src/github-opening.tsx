import { ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
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

export function OpenGitHub({ url, disabled = false, label = copy("github-opening.extra.03f69885814b") }: { url: string; disabled?: boolean; label?: string }) {
  useLocale();
  const [busy, setBusy] = useState(false), [status, setStatus] = useProductMessage("");
  const open = async () => {
    if (busy || disabled || !isTauri()) return;
    setBusy(true); setStatus("");
    try { await invoke("open_github", { url }); setStatus(ownedMessage("github-opening.extra.96dce755f727")); }
    catch { setStatus(ownedMessage("github-opening.extra.45a2c8995dcd")); }
    finally { setBusy(false); }
  };
  return <div><button type="button" disabled={disabled || busy || !isTauri()} onClick={() => void open()}>{label}</button>{!isTauri() ? <p>{copy("github-opening.browserOpeningIsAvailableInThe_33be1f")}</p> : null}{status ? <p role="status">{status}</p> : null}</div>;
}

export function GitHubTokenForm({ profile, active, disabled, showHeading = true }: { profile: Resource; active: boolean; disabled: boolean; showHeading?: boolean }) {
  useLocale();
  const p = document(profile), fine = p.token_kind === "fine-grained";
  const [access, setAccess] = useState<Access>(fine ? GitHubTokenAccess.SELECTED_REPOSITORIES : GitHubTokenAccess.PUBLIC_REPOSITORIES);
  const [busy, setBusy] = useState(false), [status, setStatus] = useProductMessage("");
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
      if (epoch.current === original) setStatus(ownedMessage("github-opening.extra.783701cedcbe"));
    } catch { if (epoch.current === original) setStatus(ownedMessage("github-opening.extra.f2427e02d7bb")); }
    finally { if (epoch.current === original) setBusy(false); }
  };
  return <section aria-label={copy("github-opening.githubTokenCreation_acffe8")}>{showHeading ? <h4>{copy("github-opening.createATokenOnGithub_519994")}</h4> : null}{fine ? <><p><LocalizedText id="github-opening.resourceOwnerTheOfficialFormDefaults_f8b002" components={{ s0: <>{text(p.resource_owner)}</> }} /></p><p>{copy("github-opening.metadataContentsPullRequestsIssuesAnd_a0bd31")}</p></> : <><label>{copy("github-opening.classicTokenAccess_d1110d")}<select value={access} disabled={busy || disabled} onChange={(event) => { setAccess(Number(event.target.value) as Access); setStatus(""); }}><option value={GitHubTokenAccess.PUBLIC_REPOSITORIES}>{copy("github-opening.publicRepositoriesOnlyNoScopes_d7d490")}</option><option value={GitHubTokenAccess.PRIVATE_REPOSITORIES}>{copy("github-opening.privateRepositoriesBroadRepoScope_5791af")}</option></select></label>{access === GitHubTokenAccess.PRIVATE_REPOSITORIES ? <p role="note">{copy("github-opening.classicRepoGrantsBroadReadWrite_1e0aa7")}</p> : <p>{copy("github-opening.noClassicScopesWillBePreselected_667cb5")}</p>}</>}<p>{copy("github-opening.reviewTheFormAndCreateThe_d41506")}</p><button type="button" disabled={!active || disabled || busy || !isTauri()} onClick={() => void open()}>{copy("github-opening.openOfficialGithubTokenForm_d8a937")}</button>{!isTauri() ? <p>{copy("github-opening.openTheOfficialFormFromThe_dc9177")}</p> : null}{status ? <p role="status">{status}</p> : null}</section>;
}

// Draft preparation cannot weaken the saved profile's revision-bound form read.
// It shares the canonical URL check and the same closed native opener instead.
export function GitHubDraftTokenForm({ kind, owner, changeKind, changeOwner, active, disabled }: { kind: GitHubTokenKind; owner: string; changeKind: (kind: GitHubTokenKind) => void; changeOwner: (owner: string) => void; active: boolean; disabled: boolean }) {
  const opening = useSettingsOpening();
  const [classicAccess, setClassicAccess] = useState<Access>(GitHubTokenAccess.PUBLIC_REPOSITORIES);
  const [busy, setBusy] = useState(false), [status, setStatus] = useProductMessage("");
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
      if (epoch.current === original) setStatus(ownedMessage("github-opening.extra.783701cedcbe"));
    } catch { if (epoch.current === original) setStatus(ownedMessage("github-opening.extra.f2427e02d7bb")); }
    finally { if (epoch.current === original) setBusy(false); }
  };
  return <details className="integration-draft-guidance" open>
    <summary>{copy("github-opening.createATokenOnGithub_519994")}</summary>
    <form onSubmit={event => { event.preventDefault(); void open(); }}>
      <fieldset disabled={disabled || busy}>
        <label>Token type<select value={kind} onChange={event => changeKind(Number(event.target.value) as GitHubTokenKind)}><option value={GitHubTokenKind.FINE_GRAINED}>Fine-grained PAT (preferred)</option><option value={GitHubTokenKind.CLASSIC}>Classic PAT</option></select></label>
        <label>Resource owner<input value={owner} onChange={event => changeOwner(event.target.value)} maxLength={100} placeholder="GitHub user or organization" required={fine} pattern="[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?" /></label>
        {fine ? <><p>{copy("github-opening.resourceOwnerTheOfficialFormDefaults_f8b002", { v0: owner })}</p><p>{copy("github-opening.metadataContentsPullRequestsIssuesAnd_a0bd31")}</p></> : <><label>{copy("github-opening.classicTokenAccess_d1110d")}<select value={classicAccess} onChange={event => setClassicAccess(Number(event.target.value) as Access)}><option value={GitHubTokenAccess.PUBLIC_REPOSITORIES}>{copy("github-opening.publicRepositoriesOnlyNoScopes_d7d490")}</option><option value={GitHubTokenAccess.PRIVATE_REPOSITORIES}>{copy("github-opening.privateRepositoriesBroadRepoScope_5791af")}</option></select></label>{classicAccess === GitHubTokenAccess.PRIVATE_REPOSITORIES ? <p role="note">{copy("github-opening.classicRepoGrantsBroadReadWrite_1e0aa7")}</p> : <p>{copy("github-opening.noClassicScopesWillBePreselected_667cb5")}</p>}</>}
        <button type="submit" disabled={!active || !expected || !isTauri() || busy}>{copy("github-opening.openOfficialGithubTokenForm_d8a937")}</button>
        <p>{copy("github-opening.reviewTheFormAndCreateThe_d41506")}</p>
      </fieldset>
    </form>
    {!isTauri() ? <p>{copy("github-opening.browserOpeningIsAvailableInThe_33be1f")}</p> : null}
    {busy ? <p role="status">Preparing the official GitHub token form…</p> : null}{status ? <p role="status">{status}</p> : null}
  </details>;
}
