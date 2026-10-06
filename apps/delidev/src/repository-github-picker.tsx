// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { Code, ConnectError } from "@connectrpc/connect";
import { EntityKind, IntegrationQuery, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { Problem } from "./ui";

export interface GitHubCloneSelection { profileId: string; expectedRevision: bigint; repositoryId: string; nodeId: string; owner: string; name: string }
export interface GitHubRepositoryChoice { repository: { id: string; node_id: string; owner: string; name: string; private: boolean }; archived: boolean; https_url: string; ssh_url: string }
interface RepositoryPage { profile_id: string; profile_revision: string; generation_id: string; page: number; page_size: number; next_page: number; repositories: GitHubRepositoryChoice[] }
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export function connectedGitHubProfile(row: Resource): boolean { const data = document(row); return row.kind === EntityKind.INTEGRATION && uuid.test(row.id) && row.revision > 0n && row.schemaVersion === 1 && data.provider === "github.com" && Boolean(data.connection) && !data.pending && uuid.test(text(object(data.connection).generation_id)); }
export function repositoryPage(reply: { schemaVersion: number; documentJson: Uint8Array } | undefined, profile: Resource | undefined, page: number): RepositoryPage | undefined {
  if (!reply || !profile) return;
  const malformed = () => { throw new ConnectError("The GitHub repository page could not be verified. Refresh this profile and page.", Code.Internal); };
  if (reply.schemaVersion !== 1 || reply.documentJson.byteLength > 1 << 20) return malformed();
  let data: Record<string, unknown>;
  try { data = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(reply.documentJson))); } catch { return malformed(); }
  if (!text(data.observed_at) || !Number.isFinite(Date.parse(text(data.observed_at))) || data.profile_id !== profile.id || data.profile_revision !== String(profile.revision) || data.generation_id !== object(document(profile).connection).generation_id || data.page !== page || data.page_size !== 50 || !Number.isInteger(data.next_page) || (data.next_page !== 0 && data.next_page !== page + 1) || !Array.isArray(data.repositories) || data.repositories.length > 50) return malformed();
  const ids = new Set<string>(), nodes = new Set<string>(), names = new Set<string>();
  for (const raw of data.repositories) {
    const entry = object(raw), repo = object(entry.repository), name = `${text(repo.owner)}/${text(repo.name)}`.toLowerCase();
    if (repo.provider !== "github.com" || !/^[1-9][0-9]*$/.test(text(repo.id)) || !text(repo.node_id) || text(repo.node_id).length > 256 || !/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(text(repo.owner)) || !/^[A-Za-z0-9_.-]{1,100}$/.test(text(repo.name)) || [".", ".."].includes(text(repo.name)) || typeof repo.private !== "boolean" || typeof entry.archived !== "boolean" || entry.https_url !== `https://github.com/${text(repo.owner)}/${text(repo.name)}.git` || entry.ssh_url !== `git@github.com:${text(repo.owner)}/${text(repo.name)}.git` || ids.has(text(repo.id)) || nodes.has(text(repo.node_id)) || names.has(name)) return malformed();
    ids.add(text(repo.id)); nodes.add(text(repo.node_id)); names.add(name);
  }
  return data as unknown as RepositoryPage;
}

// Only one profile inventory page and one repository page are retained. Reads
// use the containing dialog's disposable transport and never grant Git auth.
export function RepositoryGitHubPicker({ active, supported, disabled, choose }: { active: boolean; supported: boolean; disabled: boolean; choose: (selection: GitHubCloneSelection, url: string) => void }) {
  const [open, setOpen] = useState(false), [profilePage, setProfilePage] = useState("");
  const [profile, setProfile] = useState<Resource>(), [page, setPage] = useState(1), [filter, setFilter] = useState("");
  const profiles = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.INTEGRATION, pageSize: 50, pageToken: profilePage } }, { enabled: active, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const rows = profiles.data?.resources ?? [], available = rows.filter(connectedGitHubProfile);
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.INTEGRATION, id: profile?.id ?? "" }, { enabled: active && open && Boolean(profile), refetchInterval: active && open && profile ? 5000 : false, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const stale = Boolean(profile && current.data?.resource && (current.data.resource.revision !== profile.revision || !connectedGitHubProfile(current.data.resource)));
  const repositories = useQuery(IntegrationQuery.listGitHubRepositories, { profileId: profile?.id ?? "", expectedRevision: profile?.revision ?? 0n, page, pageSize: 50 }, { enabled: active && open && supported && Boolean(profile) && !stale && !disabled, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false });
  let result: RepositoryPage | undefined, malformed: unknown;
  try { result = repositoryPage(repositories.data, profile, page); } catch (error) { malformed = error; }
  const reading = repositories.isFetching;
  const usable = Boolean(result && !repositories.error && !malformed && !current.error && !stale && !reading);
  const changeProfile = (id: string) => { setProfile(available.find(row => row.id === id)); setPage(1); setFilter(""); };
  const changeProfilePage = (token: string) => { setProfilePage(token); setProfile(undefined); setPage(1); setFilter(""); };
  const refreshProfiles = () => { setProfile(undefined); setPage(1); setFilter(""); void profiles.refetch(); };
  return <div className="repository-github-picker">
    {available.length > 0 && !profiles.error && !disabled ? <button type="button" disabled={disabled || profiles.isFetching} onClick={() => setOpen(value => !value)}>{open ? "Back to Git URL" : "Choose from GitHub"}</button> : null}
    {profiles.isPending ? <p role="status">Checking connected GitHub profiles…</p> : null}
    <Problem error={profiles.error} />{profiles.error ? <button type="button" disabled={disabled || profiles.isFetching} onClick={refreshProfiles}>Refresh GitHub profiles</button> : null}
    {!open && (profiles.data?.nextPageToken || profilePage) ? <div className="actions"><button type="button" disabled={disabled || profiles.isFetching || !profilePage} onClick={() => changeProfilePage("")}>First profile page</button><button type="button" disabled={disabled || profiles.isFetching || !profiles.data?.nextPageToken} onClick={() => changeProfilePage(profiles.data!.nextPageToken)}>Next profile page</button></div> : null}
    {open ? <section aria-label="GitHub repositories"><h3>Choose a GitHub repository</h3>
      {!supported ? <p role="status">Update the selected server to choose repositories from GitHub. Existing folder registration remains available.</p> : null}
      <label>GitHub profile<select value={profile?.id ?? ""} disabled={disabled || profiles.isFetching || !supported} onChange={event => changeProfile(event.target.value)}><option value="">Choose a profile</option>{available.map(row => <option key={`${row.id}:${row.revision}`} value={row.id}>{resourceName(row)}</option>)}</select></label>
      <div className="actions"><button type="button" disabled={disabled || profiles.isFetching} onClick={refreshProfiles}>Refresh profiles</button><button type="button" disabled={disabled || profiles.isFetching || !profilePage} onClick={() => changeProfilePage("")}>First profile page</button><button type="button" disabled={disabled || profiles.isFetching || !profiles.data?.nextPageToken} onClick={() => changeProfilePage(profiles.data!.nextPageToken)}>Next profile page</button></div>
      {!available.length && !profiles.isPending && !profiles.error ? <p>No connected GitHub profiles on this page. Connect a PAT in Integrations or read another profile page.</p> : null}
      {profile ? <><label>Filter this page<input value={filter} maxLength={200} disabled={disabled} onChange={event => setFilter(event.target.value)} /></label>
        {reading ? <p role="status">Loading GitHub repositories…</p> : null}
        {stale ? <p role="alert">The selected profile changed. Refresh profiles and select its current version.</p> : null}
        <Problem error={current.error || repositories.error || malformed} />
        {result && !reading ? <><p>Page {page}</p>{repositories.error ? <p>Cached page; refresh before selecting a repository.</p> : null}{result.repositories.length === 0 && usable ? <p>No repositories are accessible on this page.</p> : null}
          <ul className="repository-github-list">{result.repositories.filter(entry => `${entry.repository.owner}/${entry.repository.name}`.toLowerCase().includes(filter.toLowerCase())).map(entry => <li key={entry.repository.id}><button type="button" disabled={disabled || !usable} onClick={() => { const repo = entry.repository; choose({ profileId: profile.id, expectedRevision: profile.revision, repositoryId: repo.id, nodeId: repo.node_id, owner: repo.owner, name: repo.name }, entry.https_url); setOpen(false); }}><span>{entry.repository.owner}/{entry.repository.name}</span><small>{entry.repository.private ? "Private" : "Public"}{entry.archived ? " · Archived" : ""}</small></button></li>)}</ul>
          {result.repositories.length > 0 && !result.repositories.some(entry => `${entry.repository.owner}/${entry.repository.name}`.toLowerCase().includes(filter.toLowerCase())) ? <p>No matches on this page.</p> : null}</> : null}
        <div className="actions"><button type="button" disabled={disabled || reading || stale || !supported} onClick={() => void repositories.refetch()}>Refresh repositories</button><button type="button" disabled={disabled || reading || page === 1} onClick={() => { setPage(1); setFilter(""); }}>First</button><button type="button" disabled={disabled || !usable || !result?.next_page} onClick={() => { setPage(result!.next_page); setFilter(""); }}>Next</button></div>
      </> : null}
      <p>The PAT reads repository metadata. Clone uses this computer's Git or SSH credentials.</p>
    </section> : null}
  </div>;
}
