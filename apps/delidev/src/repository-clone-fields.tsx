// SPDX-License-Identifier: Apache-2.0
import { useId, type ReactNode } from "react";

export interface RepositoryCloneDraft { url: string; parent: string; directory?: string }
export interface RepositoryCloneURL { directory: string; githubOwner: string; githubName: string }

// This is a presentation check only. The owning Go server and Worker validate
// the original bytes independently before Git or filesystem operations.
export function repositoryCloneURL(value: string): RepositoryCloneURL | undefined {
  if (!value || value.length > 4096 || /[\s\u0000-\u001f\u007f\\?#]/u.test(value)) return;
  let host: string, path: string;
  if (value.includes("://")) {
    if (!/^(https|ssh):\/\//.test(value)) return;
    const rawPath = value.slice(value.indexOf("://") + 3).replace(/^[^/]+/, "");
    if (value.slice(value.indexOf("://") + 3).split("/")[0].endsWith(":")) return;
    try {
      const parsed = new URL(value);
      if (parsed.port && Number(parsed.port) < 1) return;
      if (!parsed.hostname || (parsed.protocol === "https:" && (parsed.username || parsed.password)) || parsed.password || (parsed.username && !/^[A-Za-z0-9_.][A-Za-z0-9_.-]*$/.test(parsed.username))) return;
      host = parsed.hostname;
      if (parsed.port && !((parsed.protocol === "https:" && parsed.port === "443") || (parsed.protocol === "ssh:" && parsed.port === "22"))) host += `:${parsed.port}`;
      path = decodeURIComponent(rawPath);
    } catch { return; }
  } else {
    const match = /^(?:([A-Za-z0-9_.][A-Za-z0-9_.-]*)@)?([A-Za-z0-9][A-Za-z0-9.-]*):([^:]+)$/.exec(value);
    if (!match || match[2].length < 2) return;
    host = match[2]; path = match[3];
  }
  if (!path || path === "/" || /[\u0000-\u001f\u007f\\]/u.test(path) || path.split("/").some(part => part === "." || part === "..")) return;
  const parts = path.replace(/^\/+|\/+$/g, "").split("/");
  const directory = parts.at(-1)!.replace(/\.git$/, "");
  const github = host.toLowerCase() === "github.com" && parts.length === 2 && /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(parts[0]) && /^[A-Za-z0-9_.-]{1,100}$/.test(directory) && directory !== "." && directory !== "..";
  return { directory, githubOwner: github ? parts[0] : "", githubName: github ? directory : "" };
}
export function repositoryCloneDirectory(value: string): boolean {
  return Boolean(value && new TextEncoder().encode(value).length <= 255 && !/[<>:"/\\|?*\u0000-\u001f\u007f]/u.test(value) && value !== "." && value !== ".." && value.trim() === value && !value.endsWith(".") && !/^(?:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)/i.test(value));
}
export function repositoryCloneParent(value: string): boolean {
  return Boolean(value && value.length <= 4096 && !/[\u0000-\u001f\u007f]/u.test(value) && (value.startsWith("/") || /^[A-Za-z]:[\\/]/.test(value)));
}
export function repositoryClonePath(parent: string, directory: string): string {
  const separator = /^[A-Za-z]:\\/.test(parent) ? "\\" : "/";
  return `${parent.replace(/[\\/]+$/, "")}${separator}${directory}`;
}

export function RepositoryCloneFields({ draft, change, busy, browse, github, supported }: {
  draft: RepositoryCloneDraft; change: (draft: RepositoryCloneDraft) => void; busy: boolean; browse: () => void; github?: ReactNode; supported: boolean;
}) {
  const parentId = useId();
  const parsed = repositoryCloneURL(draft.url);
  const directory = draft.directory ?? parsed?.directory ?? "";
  return <section className="repository-clone-fields" aria-label="Clone repository">
    <label>Git URL<input type="text" value={draft.url} maxLength={4096} placeholder="https://github.com/owner/repository.git" disabled={busy} autoComplete="off" spellCheck={false} onChange={event => change({ ...draft, url: event.target.value })} /></label>
    {draft.url && !parsed ? <p role="alert">Enter a credential-free HTTPS or SSH Git URL.</p> : null}
    {github}
    <div><label htmlFor={parentId}>Clone to</label><div className="repository-clone-destination"><input id={parentId} type="text" value={draft.parent} maxLength={4096} placeholder="Choose a parent folder" disabled={busy} autoComplete="off" spellCheck={false} onChange={event => change({ ...draft, parent: event.target.value })} /><button type="button" disabled={busy} onClick={browse}>Browse…</button></div></div>
    <p>A new repository folder will be created here.</p>
    {parsed ? <><label>Repository folder name<input type="text" value={directory} maxLength={255} disabled={busy} onChange={event => change({ ...draft, directory: event.target.value })} /></label>{!repositoryCloneDirectory(directory) ? <p role="alert">Enter a portable folder name without separators or reserved device names.</p> : null}</> : null}
    {draft.parent && !repositoryCloneParent(draft.parent) ? <p role="alert">Choose an absolute parent folder on this computer.</p> : null}
    {parsed && repositoryCloneDirectory(directory) && repositoryCloneParent(draft.parent) ? <p className="repository-path">Final path: {repositoryClonePath(draft.parent, directory)}</p> : null}
    <p>Private repositories use this computer's Git or SSH credentials.</p>
    {!supported ? <p role="status">Update the selected server and this computer's Worker to clone repositories.</p> : null}
  </section>;
}
