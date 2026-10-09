// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale } from "./localization";
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

export function RepositoryCloneFields({ draft, change, busy, browse, github, supported, showURL = true }: {
  draft: RepositoryCloneDraft; change: (draft: RepositoryCloneDraft) => void; busy: boolean; browse: () => void; github?: ReactNode; supported: boolean; showURL?: boolean;
}) {
  useLocale();
  const parentId = useId();
  const parsed = repositoryCloneURL(draft.url);
  const directory = draft.directory ?? parsed?.directory ?? "";
  return <section className="repository-clone-fields" aria-label={copy("repository-clone-fields.inline.e8fa94aba0")}>
    {showURL ? <><label>{copy("repository-clone-fields.inline.cd01c2ef6a")}<input type="text" value={draft.url} maxLength={4096} placeholder={copy("repository-clone-fields.inline.a2116e2c72")} disabled={busy} autoComplete="off" spellCheck={false} onChange={event => change({ ...draft, url: event.target.value })} /></label>
    {draft.url && !parsed ? <p role="alert">{copy("repository-clone-fields.inline.2f00f15706")}</p> : null}
    {github}</> : null}
    <div><label htmlFor={parentId}>{copy("repository-clone-fields.inline.274474037d")}</label><div className="repository-clone-destination"><input id={parentId} type="text" value={draft.parent} maxLength={4096} placeholder={copy("repository-clone-fields.inline.5dbae92725")} disabled={busy} autoComplete="off" spellCheck={false} onChange={event => change({ ...draft, parent: event.target.value })} /><button type="button" disabled={busy} onClick={browse}>{copy("repository-clone-fields.inline.93c6b664dc")}</button></div></div>
    <p>{copy("repository-clone-fields.inline.50edff7c50")}</p>
    {parsed ? <><label>{copy("repository-clone-fields.inline.734294b760")}<input type="text" value={directory} maxLength={255} disabled={busy} onChange={event => change({ ...draft, directory: event.target.value })} /></label>{!repositoryCloneDirectory(directory) ? <p role="alert">{copy("repository-clone-fields.inline.ebb54b60a0")}</p> : null}</> : null}
    {draft.parent && !repositoryCloneParent(draft.parent) ? <p role="alert">{copy("repository-clone-fields.inline.a0acf59b0f")}</p> : null}
    {parsed && repositoryCloneDirectory(directory) && repositoryCloneParent(draft.parent) ? <p className="repository-path">{copy("repository-clone-fields.inline.51d016541d")} {repositoryClonePath(draft.parent, directory)}</p> : null}
    <p>{copy("repository-clone-fields.inline.4df6bcbdda")}</p>
    {!supported ? <p role="status">{copy("repository-clone-fields.inline.11e37089ac")}</p> : null}
  </section>;
}
