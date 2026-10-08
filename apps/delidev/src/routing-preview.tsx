// SPDX-License-Identifier: Apache-2.0
import { Timestamp } from "./timestamp-display";
import { SettingsTaskDismissButton } from "./settings-task";
import { useMemo, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { ConfigurationQuery, isEntityId, type Resource, EntityKind } from "@delinoio/delidev-api-client";
import { copy, useLocale, type MessageKey } from "./localization";
import { items, object, resourceName, text, type Document } from "./documents";
import { ResourceChoice } from "./configuration-fields";
import { Routing } from "./configuration-fields";
import { statusLabel } from "./product-status";
import { Problem, ServiceProblem } from "./ui";
import { SettingsTaskActions } from "./settings-task";
import { useCloseSettingsTask } from "./settings-task-context";
import { RoutingMetadataState, useRoutingAccountMetadata, type RoutingAccountMetadata } from "./routing-account-metadata";
import "./routing-preview.css";

interface Candidate { id: string; eligibility: string; quota_state: string; weight: number; score?: number | null; reset_at?: string | null }
interface Route { policy: string; selected?: string; candidates: Candidate[]; fallback?: boolean; sources?: Source[]; source_index?: number }
interface Source { source: string; model_id: string; native_model: string; route: Route; problem?: Document }
function routeEvidence(value: unknown, nested = false): Route | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return;
  const data = object(value);
  if (typeof data.policy !== "string" || data.candidates !== null && !Array.isArray(data.candidates)) return;
  if (data.selected !== undefined && data.selected !== "" && !isEntityId(text(data.selected)) || data.fallback !== undefined && typeof data.fallback !== "boolean") return;
  const candidates: Candidate[] = [];
  for (const item of items(data.candidates)) {
    const candidate = object(item);
    if (!isEntityId(text(candidate.id)) || typeof candidate.eligibility !== "string" || typeof candidate.quota_state !== "string" || !Number.isInteger(candidate.weight) || Number(candidate.weight) < 0 || candidate.score != null && (typeof candidate.score !== "number" || !Number.isFinite(candidate.score)) || candidate.reset_at != null && typeof candidate.reset_at !== "string") return;
    candidates.push(candidate as unknown as Candidate);
  }
  if (new Set(candidates.map(candidate => candidate.id)).size !== candidates.length) return;
  const sources: Source[] = [];
  if (data.sources !== undefined && (!Array.isArray(data.sources) || nested)) return;
  for (const item of items(data.sources)) {
    const source = object(item), route = routeEvidence(source.route, true);
    if (!route || typeof source.source !== "string" || typeof source.model_id !== "string" || typeof source.native_model !== "string" || source.problem !== undefined && (!source.problem || typeof source.problem !== "object" || Array.isArray(source.problem))) return;
    sources.push({ source: source.source, model_id: source.model_id, native_model: source.native_model, route, problem: source.problem as Document | undefined });
  }
  if (data.source_index !== undefined && (!Number.isInteger(data.source_index) || Number(data.source_index) < 0 || Number(data.source_index) >= sources.length)) return;
  return { policy: data.policy, selected: text(data.selected), candidates, fallback: data.fallback as boolean | undefined, sources, source_index: data.source_index as number | undefined };
}
const policyLabels: Record<Routing, MessageKey> = {
  [Routing.Fixed]: "routing-preview.policyFixed", [Routing.Priority]: "routing-preview.policyPriority",
  [Routing.RoundRobin]: "routing-preview.policyRoundRobin", [Routing.Quota]: "routing-preview.policyQuota",
  [Routing.Reset]: "routing-preview.policyReset", [Routing.Sequential]: "routing-preview.policySequential",
};
const eligibilityLabels: Record<string, MessageKey> = {
  eligible: "routing-preview.eligible", unauthenticated: "routing-preview.unauthenticated",
  "missing-account": "routing-preview.missingAccount", "project-restricted": "status.project-restricted",
  "agent-restricted": "routing-preview.agentRestricted", "incompatible-account": "routing-preview.incompatibleAccount",
  disabled: "routing-preview.disabled", exhausted: "routing-preview.exhausted", "automatic-excluded": "routing-preview.automaticExcluded",
};
function policyLabel(policy: string) { return Object.hasOwn(policyLabels, policy) ? copy(policyLabels[policy as Routing]) : policy || statusLabel("unknown"); }
function eligibilityLabel(value: string) { return Object.hasOwn(eligibilityLabels, value) ? copy(eligibilityLabels[value]) : statusLabel(value); }
function AccountIdentity({ metadata }: { metadata?: RoutingAccountMetadata }) {
  const loading = !metadata || metadata.state === RoutingMetadataState.Loading;
  return <div className="routing-account-identity"><strong>{loading ? copy("routing-preview.loadingAccount") : metadata.name || copy("routing-preview.accountUnavailable")}</strong><p>{loading || metadata?.sourceState === RoutingMetadataState.Loading ? copy("routing-preview.loadingSource") : metadata.source || copy("routing-preview.sourceUnavailable")}</p></div>;
}
function AccountId({ id }: { id: string }) {
  return <details className="routing-account-id"><summary>{copy("routing-preview.accountId")}</summary><code>{id}</code></details>;
}
function Candidates({ route, metadata }: { route: Route; metadata: Map<string, RoutingAccountMetadata> }) {
  return route.candidates.length ? <ul className="routing-candidates">{route.candidates.map(candidate => <li key={candidate.id} className="routing-candidate" data-selected={route.selected === candidate.id}>
    <div className="routing-candidate-heading"><AccountIdentity metadata={metadata.get(candidate.id)} /><span className="routing-eligibility" data-warning={candidate.eligibility !== "eligible"}>{eligibilityLabel(candidate.eligibility)}</span></div>
    {route.selected === candidate.id ? <p className="routing-selected-label">{copy("routing-preview.selectedAccount")}</p> : null}
    <dl className="routing-candidate-evidence"><div><dt>{copy("routing-preview.weight")}</dt><dd>{candidate.weight}</dd></div><div><dt>{copy("routing-preview.quota")}</dt><dd>{statusLabel(candidate.quota_state)}</dd></div>{typeof candidate.score === "number" ? <div><dt>{copy("routing-preview.score")}</dt><dd>{candidate.score}</dd></div> : null}{candidate.reset_at ? <div><dt>{copy("routing-preview.reset")}</dt><dd>{<Timestamp value={candidate.reset_at} />}</dd></div> : null}</dl>
    <AccountId id={candidate.id} />
  </li>)}</ul> : <p className="routing-empty">{copy("routing-preview.noCandidates")}</p>;
}
function Fallback({ route }: { route: Route }) { return route.fallback ? <p className="routing-fallback">{copy("configuration-actions.insufficientComparableQuotaEvidenceTheServer_95890b")}</p> : null; }
export function RoutingPreview({ agent, active, close }: { agent: Resource; active: boolean; close: () => void }) {
  useLocale();
  const closeTask = useCloseSettingsTask(close);
  const [project, setProject] = useState("");
  const result = useQuery(ConfigurationQuery.previewRouting, { agentId: agent.id, projectId: project }, { enabled: active, retry: false, placeholderData: undefined });
  const route = useMemo(() => {
    try { return result.data ? routeEvidence(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(result.data.routeJson))) : undefined; } catch { return undefined; }
  }, [result.data]);
  const ids = route ? [route.selected, ...route.candidates.map(candidate => candidate.id), ...(route.sources ?? []).flatMap(source => [source.route.selected, ...source.route.candidates.map(candidate => candidate.id)])].filter((id): id is string => Boolean(id)) : [];
  const metadata = useRoutingAccountMetadata(ids, active, `${agent.id}:${project}:${result.dataUpdatedAt}`);
  return <section className="routing-preview" aria-label={copy("routing-preview.label", { v0: resourceName(agent) })}>
    <p className="routing-agent-name">{resourceName(agent)}</p>
    <span className="routing-read-only">{copy("routing-preview.readOnly")}</span>
    {route ? <div className="routing-result" data-warning={!route.selected} role="status"><span aria-hidden="true" className="routing-result-icon">{route.selected ? "✓" : "!"}</span><div><h3>{route.selected ? copy("routing-preview.selectedAccount") : copy("routing-preview.noEligible")}</h3>{route.selected ? <><AccountIdentity metadata={metadata.get(route.selected)} /><AccountId id={route.selected} /></> : <p>{copy("routing-preview.reviewStatus")}</p>}</div></div> : result.isPending && !result.error ? <p role="status">{copy("routing-preview.loading")}</p> : null}
    <div className="routing-project"><div className="routing-project-controls"><ResourceChoice label={copy("configuration-actions.project_985959")} emptyLabel={copy("documents.generalChat_f634bc")} kind={EntityKind.PROJECT} value={project} change={setProject} active={active} disabled={!active} /><button type="button" disabled={!active || result.isFetching} aria-label={copy("configuration-actions.refreshRoutingPreview_3b8c83")} onClick={() => void result.refetch()}>{result.isFetching ? copy("routing-preview.refreshing") : copy("routing-preview.refresh")}</button></div><p>{project ? copy("configuration-actions.usingTheSelectedProjectSRestrictions_f4c29e") : copy("configuration-actions.generalChatNoProjectRestrictions_abff59")}</p></div>
    {result.error && route ? <p role="status">{copy("routing-preview.refreshFailed")}</p> : null}<Problem error={result.error} actions={result.error ? <button type="button" disabled={!active || result.isFetching} onClick={() => void result.refetch()}>{copy("ui.retryCurrentRead")}</button> : undefined} />
    {result.data && !route ? <p role="alert">{copy("configuration-actions.routingEvidenceIsUnavailable_3f3ff4")}</p> : null}
    {route ? <><dl className="routing-policy"><div><dt>{copy("routing-preview.policy")}</dt><dd>{policyLabel(route.policy)}</dd></div></dl><Fallback route={route} />
      {route.sources?.length ? <section className="routing-sources"><h3>{copy("configuration-actions.sourceDecisions")}</h3><ol>{route.sources.map((source, index) => <li key={`${index}:${source.source}`} className="routing-source"><h4>{index + 1} · {source.source}{route.source_index === index ? copy("configuration-actions.selectedSource") : ""}</h4><p>{copy("configuration-actions.sourceModel", { v0: source.native_model, v1: source.model_id })}</p>{source.problem ? <ServiceProblem code={text(source.problem.code)}>{text(source.problem.message)}</ServiceProblem> : null}<dl className="routing-policy"><div><dt>{copy("routing-preview.policy")}</dt><dd>{policyLabel(source.route.policy) || statusLabel("unknown")}</dd></div></dl><Fallback route={source.route} /><Candidates route={source.route} metadata={metadata} /></li>)}</ol></section> : null}
      <section className="routing-account-section"><h3>{copy("routing-preview.candidates")}</h3><Candidates route={route} metadata={metadata} /></section></> : null}
    <p className="routing-note">{copy("configuration-actions.thisPreviewIsReadOnlyAnd_dd9bb1")}</p>
    <SettingsTaskActions><SettingsTaskDismissButton type="button" data-settings-task-cancel onClick={closeTask}>{copy("settings-task.close")}</SettingsTaskDismissButton></SettingsTaskActions>
  </section>;
}
