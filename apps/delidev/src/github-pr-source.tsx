import { LocalizedText, copy, useLocale } from "./localization";
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
  useLocale();
  if (value == null) return <p>{copy("github-pr-source.sourceRepositoryNotRecordedInThis_fe8030")}</p>;
  const source = object(value), repo = object(source.repository);
  if (source.state === HeadRepositoryState.Unavailable) return <p>{copy("github-pr-source.sourceRepositoryUnavailableOnGithubIts_9fb632")}</p>;
  return <div><p><LocalizedText id="github-pr-source.sourceRepository_62ff85" components={{ s0: <code>{text(repo.owner)}/{text(repo.name)}</code>, s1: <>{repo.private ? copy("github-pr-source.private_c9faf8") : copy("github-pr-source.public_3469de")}</> }} /></p><details><summary>{copy("github-pr-source.sourceRepositoryIdentity_996ce5")}</summary><p><LocalizedText id="github-pr-source.repositoryIdNodeId_31fb4b" components={{ s0: <code>{text(repo.id)}</code>, s1: <code>{text(repo.node_id)}</code> }} /></p></details></div>;
}
