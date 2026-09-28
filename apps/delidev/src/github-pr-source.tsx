import { object, text, type Document } from "./documents";
import { bounded, positive } from "./github-query-model";

enum HeadRepositoryState { Available = "available", Unavailable = "unavailable" }

export function validPRSource(raw: unknown, base: Document): boolean {
  // Historical observations omitted this field; absence never selects the base
  // repository as the source of a fork pull request.
  if (raw == null) return true;
  const source = object(raw), repo = object(source.repository);
  if (source.state === HeadRepositoryState.Unavailable) return source.repository == null;
  if (source.state !== HeadRepositoryState.Available || repo.provider !== "github.com" || !positive(repo.id) || !bounded(repo.node_id, 256) || typeof repo.private !== "boolean") return false;
  if (!/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(text(repo.owner)) || !/^[A-Za-z0-9_.-]{1,100}$/.test(text(repo.name)) || [".", ".."].includes(text(repo.name))) return false;
  if (repo.default_branch != null && (!bounded(repo.default_branch, 1024, false) || /[\r\n]/.test(repo.default_branch))) return false;
  const sameName = text(repo.owner).toLowerCase() === text(base.owner).toLowerCase() && text(repo.name).toLowerCase() === text(base.name).toLowerCase();
  return repo.id === base.id ? repo.node_id === base.node_id && sameName : repo.node_id !== base.node_id && !sameName;
}

export function PRSource({ value }: { value: unknown }) {
  if (value == null) return <p>Source repository: not recorded in this observation. Refresh to check.</p>;
  const source = object(value), repo = object(source.repository);
  if (source.state === HeadRepositoryState.Unavailable) return <p>Source repository: unavailable on GitHub. Its branch cannot currently be prepared for a fix.</p>;
  return <div><p>Source repository: <code>{text(repo.owner)}/{text(repo.name)}</code>{repo.private ? " · Private" : " · Public"}</p><details><summary>Source repository identity</summary><p>Repository ID: <code>{text(repo.id)}</code> · Node ID: <code>{text(repo.node_id)}</code></p></details></div>;
}
