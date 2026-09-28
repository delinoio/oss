import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { GitHubTokenAccess, IntegrationQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";

const accessNames = { [GitHubTokenAccess.SELECTED_REPOSITORIES]: "selected-repositories", [GitHubTokenAccess.PUBLIC_REPOSITORIES]: "public-repositories", [GitHubTokenAccess.PRIVATE_REPOSITORIES]: "private-repositories" };
type Access = keyof typeof accessNames;
export function githubForm(raw: Uint8Array, profile: Resource, access: Access): string | undefined {
  if (raw.byteLength > 4096) return;
  let value;
  try { value = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return; }
  const p = document(profile);
  if (value.profile_id !== profile.id || value.profile_revision !== profile.revision.toString() || value.token_kind !== p.token_kind || text(value.resource_owner) !== text(p.resource_owner) || value.access !== accessNames[access]) return;
  const q = new URLSearchParams();
  let path = "/settings/tokens/new";
  if (p.token_kind === "fine-grained" && access === GitHubTokenAccess.SELECTED_REPOSITORIES && /^[A-Za-z0-9]([A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(text(p.resource_owner))) {
    path = "/settings/personal-access-tokens/new";
    q.set("name", "DeliDev read-only"); q.set("description", "Read-only repository inspection"); q.set("target_name", text(p.resource_owner)); q.set("expires_in", "30");
    for (const key of ["metadata", "contents", "pull_requests", "statuses", "issues"]) q.set(key, "read");
  } else if (p.token_kind === "classic" && access === GitHubTokenAccess.PUBLIC_REPOSITORIES) q.set("description", "DeliDev public read-only");
  else if (p.token_kind === "classic" && access === GitHubTokenAccess.PRIVATE_REPOSITORIES) { q.set("description", "DeliDev private repository lookup"); q.set("scopes", "repo"); }
  else return;
  q.sort();
  const expected = `https://github.com${path}?${q}`;
  return value.url === expected ? expected : undefined;
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

export function GitHubTokenForm({ profile, active, disabled }: { profile: Resource; active: boolean; disabled: boolean }) {
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
  return <section aria-label="GitHub token creation"><h4>Create a token on GitHub</h4>{fine ? <><p>Resource owner: {text(p.resource_owner)}. The official form defaults to All repositories. Choose Only select repositories and select the repositories this profile needs.</p><p>Metadata, Contents, Pull requests, Issues and Commit statuses are preselected as read-only, with a 30-day expiry. The verified form offers no Checks permission; access must be checked separately. Organization approval may be required.</p></> : <><label>Classic token access<select value={access} disabled={busy || disabled} onChange={(event) => { setAccess(Number(event.target.value) as Access); setStatus(""); }}><option value={GitHubTokenAccess.PUBLIC_REPOSITORIES}>Public repositories only (no scopes)</option><option value={GitHubTokenAccess.PRIVATE_REPOSITORIES}>Private repositories (broad repo scope)</option></select></label>{access === GitHubTokenAccess.PRIVATE_REPOSITORIES ? <p role="note">Classic repo grants broad read/write access to private repositories even though DeliDev only reads GitHub APIs. Prefer fine-grained selected repositories when possible.</p> : <p>No classic scopes will be preselected for public repository lookup.</p>}</>}<p>Review the form and create the token yourself, then enter it in the protected token field below. Opening a form does not create or save a token.</p><button type="button" disabled={!active || disabled || busy || !isTauri()} onClick={() => void open()}>Open official GitHub token form</button>{!isTauri() ? <p>Open the official form from the desktop app or use the CLI token-form command.</p> : null}{status ? <p role="status">{status}</p> : null}</section>;
}
