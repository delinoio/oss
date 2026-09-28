import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { IntegrationQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { Problem } from "./ui";

const features = ["repository-metadata", "repository-contents", "pull-requests", "issues", "checks", "commit-statuses", "rulesets", "reviewer-permissions"] as const;
const names = ["Repository metadata", "Repository contents", "Pull requests", "Issues", "Checks API", "Commit statuses API", "Active rulesets API", "Current user's permission lookup"] as const;
const states = new Set(["available", "invalid-token", "restricted", "not-found-or-inaccessible", "sso-required", "rate-limited", "unavailable", "not-evaluated"]);
const uuid = (value: unknown): value is string => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
const positive = (value: unknown): value is string => typeof value === "string" && /^[1-9][0-9]{0,19}$/.test(value) && BigInt(value) <= 18446744073709551615n;
export function repositoryAccess(raw: Uint8Array, selected: Resource): Document | undefined {
  if (raw.byteLength > 1 << 20) return;
  let result: Document;
  try { result = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return; }
  const selection = document(selected);
  if (result.repository_id !== selected.id || !positive(result.repository_revision) || result.repository_revision !== selected.revision.toString() || result.profile_id !== selection.integration_id || !uuid(result.profile_id) || !uuid(result.generation_id) || !text(result.observed_at) || !Number.isFinite(Date.parse(text(result.observed_at)))) return;
  const list = result.features;
  if (!Array.isArray(list) || list.length !== features.length) return;
  const remote = object(result.repository), identity = object(result.identity);
  if (result.identity != null && (!positive(identity.id) || !text(identity.node_id) || text(identity.node_id).length > 256 || !/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?$/.test(text(identity.login)))) return;
  if (result.repository != null && (remote.provider !== "github.com" || !positive(remote.id) || !text(remote.node_id) || text(remote.node_id).length > 256 || text(remote.owner).toLowerCase() !== text(selection.github_owner).toLowerCase() || text(remote.name).toLowerCase() !== text(selection.github_name).toLowerCase() || typeof remote.private !== "boolean" || !result.identity || text(remote.default_branch).length > 1024 || (remote.head_commit != null && !/^[0-9a-f]{40}$/.test(text(remote.head_commit))))) return;
  for (const [index, value] of list.entries()) {
    const access = object(value);
    if (access.feature !== features[index] || !states.has(text(access.state))) return;
    if (access.state === "available") {
      if (access.problem != null || !result.repository || !result.identity) return;
      if ([1, 4, 5].includes(index) && !text(remote.head_commit)) return;
      if (index === 6 && !text(remote.default_branch)) return;
    } else if (!text(object(access.problem).message) || text(object(access.problem).message).length > 4096 || text(object(access.problem).guidance).length > 4096) return;
  }
  if (Boolean(result.repository) !== (object(list[0]).state === "available")) return;
  return result;
}
function AccessObservation({ selected, active }: { selected: Resource; active: boolean }) {
  const result = useQuery(IntegrationQuery.inspectRepositoryIntegration, { repositoryId: selected.id }, { enabled: active, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false, staleTime: 0, gcTime: 0 });
  const observed = result.data?.schemaVersion === 1 ? repositoryAccess(result.data.documentJson, selected) : undefined;
  const remote = object(observed?.repository), identity = object(observed?.identity);
  return <section aria-label="Repository GitHub access"><p>These are fresh reads using this repository's selected profile. API availability does not mean CI passed, that rules are satisfied, or that another reviewer has been verified.</p><button disabled={!active || result.isFetching} onClick={() => void result.refetch()}>Refresh GitHub access</button>{result.isFetching ? <p role="status">Inspecting GitHub read access…</p> : null}<Problem error={result.error} />
    {result.data && !observed ? <p role="alert">The access observation does not match this repository revision. Refresh repository settings before inspecting again.</p> : null}
    {observed ? <><p>{result.error || result.isFetching ? "Previous observation" : "Observed"}: {text(observed.observed_at)}</p>{text(identity.login) ? <p>Authenticated as {text(identity.login)}</p> : null}{text(remote.name) ? <p>{text(remote.owner)}/{text(remote.name)} · {remote.private ? "Private" : "Public"}{text(remote.default_branch) ? ` · default branch ${text(remote.default_branch)}` : ""}</p> : null}{text(remote.head_commit) ? <p>Observed default-branch commit: <code>{text(remote.head_commit)}</code></p> : null}<table><caption>Access to this repository's read endpoints</caption><thead><tr><th scope="col">Feature</th><th scope="col">Access</th><th scope="col">Details</th></tr></thead><tbody>{(observed.features as unknown[]).map((entry, index) => { const access = object(entry), problem = object(access.problem); return <tr key={features[index]}><th scope="row">{names[index]}</th><td>{text(access.state)}</td><td>{text(problem.message)} {text(problem.guidance)}</td></tr>; })}</tbody></table></> : null}
  </section>;
}
export function RepositoryGitHubAccess({ selected, active }: { selected: Resource; active: boolean }) {
  const [open, setOpen] = useState(false);
  const data = document(selected), configured = Boolean(text(data.integration_id) && text(data.github_owner) && text(data.github_name));
  return <div><button disabled={!configured || selected.schemaVersion !== 1} aria-expanded={open} onClick={() => setOpen((value) => !value)}>{open ? "Close GitHub access" : "Inspect GitHub access"}</button>{!configured ? <p>Set the GitHub owner, repository name and profile to inspect access.</p> : null}{open && active ? <AccessObservation key={`${selected.id}:${selected.revision}`} selected={selected} active={active} /> : null}</div>;
}
