import { ScrollPicker } from "./scroll-picker";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { useConnectPaginationReader, usePaginationChain } from "./scroll-pagination-query";
import { useStablePageRevisions, paginationError, invalidGitHubPage, useGitHubCatalog, useGitHubScrollRoot, visiblePageIds } from "./github-scroll";
// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon, SettingsActionPresentation } from "./settings-action";
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useQuery } from "@connectrpc/connect-query";
import { Code, ConnectError } from "@connectrpc/connect";
import { EntityKind, IntegrationQuery, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { document as resourceDocument, object, resourceName, text } from "./documents";
import { DialogSurface, Problem } from "./ui";
import { copy, useLocale } from "./localization";
import { useSettingsTaskDismiss, useSettingsTaskVisible } from "./settings-task-context";
import "./repository-github-picker.css";

export interface GitHubCloneSelection { profileId: string; expectedRevision: bigint; repositoryId: string; nodeId: string; owner: string; name: string }
export interface GitHubRepositoryChoice { repository: { id: string; node_id: string; owner: string; name: string; private: boolean }; archived: boolean; https_url: string; ssh_url: string }
interface RepositoryPage { profile_id: string; profile_revision: string; generation_id: string; page: number; page_size: number; next_page: number; repositories: GitHubRepositoryChoice[] }
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export function connectedGitHubProfile(row: Resource): boolean { const data = resourceDocument(row); return row.kind === EntityKind.INTEGRATION && uuid.test(row.id) && row.revision > 0n && row.schemaVersion === 1 && data.provider === "github.com" && Boolean(data.connection) && !data.pending && uuid.test(text(object(data.connection).generation_id)); }
export function repositoryPage(reply: { schemaVersion: number; documentJson: Uint8Array } | undefined, profile: Resource | undefined, page: number): RepositoryPage | undefined {
  if (!reply || !profile) return;
  const malformed = () => { throw new ConnectError("The GitHub repository page could not be verified. Refresh this profile and page.", Code.Internal); };
  if (reply.schemaVersion !== 1 || reply.documentJson.byteLength > 1 << 20) return malformed();
  let data: Record<string, unknown>;
  try { data = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(reply.documentJson))); } catch { return malformed(); }
  if (!text(data.observed_at) || !Number.isFinite(Date.parse(text(data.observed_at))) || data.profile_id !== profile.id || data.profile_revision !== String(profile.revision) || data.generation_id !== object(resourceDocument(profile).connection).generation_id || data.page !== page || data.page_size !== 50 || !Number.isInteger(data.next_page) || (data.next_page !== 0 && data.next_page !== page + 1) || !Array.isArray(data.repositories) || data.repositories.length > 50) return malformed();
  const ids = new Set<string>(), nodes = new Set<string>(), names = new Set<string>();
  for (const raw of data.repositories) {
    const entry = object(raw), repo = object(entry.repository), name = `${text(repo.owner)}/${text(repo.name)}`.toLowerCase();
    if (repo.provider !== "github.com" || !/^[1-9][0-9]*$/.test(text(repo.id)) || !text(repo.node_id) || text(repo.node_id).length > 256 || !/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(text(repo.owner)) || !/^[A-Za-z0-9_.-]{1,100}$/.test(text(repo.name)) || [".", ".."].includes(text(repo.name)) || typeof repo.private !== "boolean" || typeof entry.archived !== "boolean" || entry.https_url !== `https://github.com/${text(repo.owner)}/${text(repo.name)}.git` || entry.ssh_url !== `git@github.com:${text(repo.owner)}/${text(repo.name)}.git` || ids.has(text(repo.id)) || nodes.has(text(repo.node_id)) || names.has(name)) return malformed();
    ids.add(text(repo.id)); nodes.add(text(repo.node_id)); names.add(name);
  }
  return data as unknown as RepositoryPage;
}

// This read-only chooser is a separate modal, not a Settings workflow step.
// Portal it outside the parent so its controls cannot join the parent's form,
// scrolling body or focus cycle. The category still owns every query and draft.
function GitHubRepositoryDialog({ opener, close, children }: { opener: RefObject<HTMLButtonElement | null>; close: () => void; children: ReactNode }) {
  useLocale();
  const id = useId(), dialog = useRef<HTMLDialogElement>(null), heading = useRef<HTMLHeadingElement>(null);
  useLayoutEffect(() => {
    const node = dialog.current!, trigger = opener.current, parent = trigger?.closest("dialog");
    let live = true, committed = false;
    // Strict Mode's first cleanup must not move focus back into the parent.
    queueMicrotask(() => { if (live) committed = true; });
    node.showModal();
    const profile = node.querySelector<HTMLElement>('[role="combobox"]:not(:disabled)');
    (profile ?? heading.current)?.focus({ preventScroll: true });
    return () => {
      live = false;
      const ownedFocus = node.contains(document.activeElement);
      node.close();
      const replacement = [...document.querySelectorAll("dialog[open]:not([role=region])")].some(other => other !== parent && other !== node);
      if (!committed || !ownedFocus || replacement || (parent && !parent.open) || !trigger?.isConnected || trigger.matches(":disabled") || trigger.closest("[hidden],[inert],[aria-hidden=true]")) return;
      const style = getComputedStyle(trigger);
      if (style.display !== "none" && style.visibility !== "hidden") trigger.focus({ preventScroll: true });
    };
  }, [opener]);
  return createPortal(<DialogSurface ref={dialog} className="repository-github-dialog" aria-modal="true" aria-labelledby={id} onCancel={event => {
    event.preventDefault(); event.stopPropagation(); close();
  }} onKeyDown={event => {
    // React portal events still bubble through the parent task. Contain them
    // here so Escape and Enter cannot dismiss or submit the parent workflow.
    event.stopPropagation();
    if (event.key !== "Tab" || event.defaultPrevented) return;
    const controls = [...event.currentTarget.querySelectorAll<HTMLElement>("button,input,select,textarea,summary,a[href],[tabindex]")].filter(node => node.tabIndex >= 0 && !node.matches(":disabled") && !node.closest("[hidden],[inert]") && node.getClientRects().length > 0);
    const first = controls[0], last = controls.at(-1), focused = document.activeElement;
    if (!first) { event.preventDefault(); heading.current?.focus(); }
    else if (event.shiftKey && (focused === first || !controls.includes(focused as HTMLElement))) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && (focused === last || !controls.includes(focused as HTMLElement))) { event.preventDefault(); first.focus(); }
  }}>
    <header className="repository-github-header"><h2 ref={heading} tabIndex={-1} id={id}>{copy("repository-github.title")}</h2><button type="button" aria-label={copy("repository-github.close")} onClick={close}>×</button></header>
    <div className="repository-github-body">{children}</div>
  </DialogSurface>, document.body);
}

// Profile metadata and accepted boundaries accumulate; repository payloads use
// the shared three-page window. Reads
// use the containing dialog's disposable transport and never grant Git auth.
export function RepositoryGitHubPicker({ active, supported, disabled, choose }: { active: boolean; supported: boolean; disabled: boolean; choose: (selection: GitHubCloneSelection, url: string) => void }) {
  useLocale();
  const opener = useRef<HTMLButtonElement>(null), taskVisible = useSettingsTaskVisible();
  const [open, setOpen] = useState(false);
  const { root, bindRoot } = useGitHubScrollRoot();
  const { root: repositoryRoot, bindRoot: bindRepositoryRoot } = useGitHubScrollRoot();
  const [profile, setProfile] = useState<Resource>(), [filter, setFilter] = useState("");
  const visible = active && taskVisible && open;
  useSettingsTaskDismiss(() => setOpen(false));
  useEffect(() => { if (!active || !taskVisible) setOpen(false); }, [active, taskVisible]);
  const profiles = useGitHubCatalog(EntityKind.INTEGRATION, active);
  const rows = profiles.data?.resources ?? [], available = rows.filter(connectedGitHubProfile);
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.INTEGRATION, id: profile?.id ?? "" }, { enabled: visible && Boolean(profile), refetchInterval: visible && profile ? 5000 : false, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const stale = Boolean(profile && current.data?.resource && (current.data.resource.revision !== profile.revision || !connectedGitHubProfile(current.data.resource)));
  const validateBoundary = useStablePageRevisions(JSON.stringify([profile?.id, String(profile?.revision)]));
  const request = useCallback((token: string) => ({ profileId: profile?.id ?? "", expectedRevision: profile?.revision ?? 0n, page: token ? Number(token) : 1, pageSize: 50 }), [profile?.id, profile?.revision]);
  const project = useCallback((reply: { schemaVersion: number; documentJson: Uint8Array }, token: string) => {
    const data = repositoryPage(reply, profile, token ? Number(token) : 1);
    if (!data) invalidGitHubPage();
    const rows = data.repositories.map(entry => ({ id: entry.repository.id, revision: profile!.revision, label: entry.repository.owner + '/' + entry.repository.name }));
    validateBoundary(token, rows);
    return { rows, nextPageToken: data.next_page ? String(data.next_page) : "", payload: [data] };
  }, [profile, validateBoundary]);
  const reader = useConnectPaginationReader(IntegrationQuery.listGitHubRepositories, request, project);
  const traversal = usePaginationChain(JSON.stringify([profile?.id, String(profile?.revision)]), visible && supported && Boolean(profile) && !stale && !disabled, reader);
  const reading = Boolean(traversal.loading), malformed = paginationError(traversal.error?.failure);
  const usable = Boolean(traversal.loaded && !traversal.error && !current.error && !stale && !reading);
  const changeProfile = (id: string) => { setProfile(available.find(row => row.id === id)); setFilter(""); };
  const refreshProfiles = () => { setProfile(undefined); setFilter(""); profiles.refetch(); };
  return <div ref={bindRoot} className="repository-github-picker">
    {available.length > 0 && !profiles.error && !disabled ? <SettingsActionButton icon={SettingsActionIcon.Inspect} ref={opener} type="button" disabled={disabled || profiles.isFetching} aria-haspopup="dialog" onClick={event => { event.currentTarget.focus({ preventScroll: true }); setOpen(true); }}>{copy("repository-github.choose")}</SettingsActionButton> : null}
    {!visible && profiles.isPending ? <p role="status">{copy("repository-github.checkingProfiles")}</p> : null}
    {!visible ? <><Problem error={paginationError(profiles.error?.failure)} />{profiles.error ? <SettingsActionButton icon={SettingsActionIcon.Refresh} presentation={SettingsActionPresentation.Icon} type="button" disabled={disabled || profiles.isFetching} onClick={refreshProfiles}>{copy("repository-github.refreshGitHubProfiles")}</SettingsActionButton> : null}</> : null}
    {!visible ? <ScrollContinuation query={profiles} root={root} active={active && !disabled} label={copy("repository-github.profile")} /> : null}
    {visible ? <GitHubRepositoryDialog opener={opener} close={() => setOpen(false)}>
      {profiles.isPending ? <p role="status">{copy("repository-github.checkingProfiles")}</p> : null}<Problem error={paginationError(profiles.error?.failure)} />
      {!supported ? <p role="status">{copy("repository-github.updateServer")}</p> : null}
      <ScrollPicker label={copy("repository-github.profile")} value={profile?.id ?? ""} change={changeProfile} query={profiles} active={visible} disabled={disabled || !supported} placeholder={copy("repository-github.chooseProfile")} options={available.map(row => ({ id: row.id, label: resourceName(row) }))} selectedLabel={profile ? resourceName(profile) : undefined} />
      <div className="actions"><SettingsActionButton icon={SettingsActionIcon.Refresh} presentation={SettingsActionPresentation.Icon} type="button" disabled={disabled || profiles.isFetching} onClick={refreshProfiles}>{copy("repository-github.refreshProfiles")}</SettingsActionButton></div>
      {!available.length && !profiles.isPending && !profiles.error ? <p>{copy("repository-github.noProfiles")}</p> : null}
      {profile ? <><label>{copy("repository-github.filter")}<input value={filter} maxLength={200} disabled={disabled} onChange={event => setFilter(event.target.value)} /></label>
        {reading ? <p role="status">{copy("repository-github.loading")}</p> : null}
        {stale ? <p role="alert">{copy("repository-github.staleProfile")}</p> : null}
        <Problem error={current.error || malformed} />
        <div ref={bindRepositoryRoot}>
          <ScrollPayloadWindow query={traversal} root={repositoryRoot} active={visible && !disabled && !stale}>{(payload, projections) => payload.map(result => {
            const ids = visiblePageIds(traversal.pages, projections);
            return <ul key={result.page} className="repository-github-list">{result.repositories.filter(entry => ids.has(entry.repository.id) && `${entry.repository.owner}/${entry.repository.name}`.toLowerCase().includes(filter.toLowerCase())).map(entry => <li key={entry.repository.id}><button type="button" disabled={disabled || !usable} onClick={() => { const repo = entry.repository; choose({ profileId: profile.id, expectedRevision: profile.revision, repositoryId: repo.id, nodeId: repo.node_id, owner: repo.owner, name: repo.name }, entry.https_url); setOpen(false); }}><span>{entry.repository.owner}/{entry.repository.name}</span><small>{copy(entry.repository.private ? "repository-github.private" : "repository-github.public")}{entry.archived ? copy("repository-github.archived") : ""}</small></button></li>)}</ul>;
          })}</ScrollPayloadWindow>
          {traversal.loaded && !traversal.rows.length ? <p>{copy("repository-github.empty")}</p> : null}
          {traversal.rows.length > 0 && !traversal.rows.some(row => row.label.toLowerCase().includes(filter.toLowerCase())) ? <p>{copy("repository-github.noMatches")}</p> : null}
          <ScrollContinuation query={traversal} root={repositoryRoot} active={visible && !disabled && !stale} label={copy("repository-github.title")} />
        </div>
        <div className="actions"><SettingsActionButton icon={SettingsActionIcon.Refresh} presentation={SettingsActionPresentation.Icon} type="button" disabled={disabled || reading || stale || !supported} onClick={traversal.refresh}>{copy("repository-github.refreshRepositories")}</SettingsActionButton></div>
      </> : null}
      <p className="repository-github-hint">{copy("repository-github.credentials")}</p>
    </GitHubRepositoryDialog> : null}
  </div>;
}
