// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary, DisclosureDensity } from "./disclosure";
import { Timestamp, TimestampMode } from "./timestamp-display";
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
interface Route { evidence: Document; policy: string; selected?: string; candidates: Candidate[]; candidatesAvailable: boolean; fallback?: boolean; sources?: Source[]; source_index?: number }
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
  const { sources: _sources, source_index: _sourceIndex, ...evidence } = data;
  return { evidence, policy: data.policy, selected: text(data.selected), candidates, candidatesAvailable: Array.isArray(data.candidates), fallback: data.fallback as boolean | undefined, sources, source_index: data.source_index as number | undefined };
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
  return <Disclosure density={DisclosureDensity.Settings} className="routing-account-id"><DisclosureSummary>{copy("routing-preview.accountId")}</DisclosureSummary><code>{id}</code></Disclosure>;
}
function Candidates({ route, metadata, active }: { route: Route; metadata: Map<string, RoutingAccountMetadata>; active: boolean }) {
  return !route.candidatesAvailable ? <p className="routing-empty">{copy("configuration-actions.routingEvidenceIsUnavailable_3f3ff4")}</p> : route.candidates.length ? <ul className="routing-candidates">{route.candidates.map(candidate => <li key={candidate.id} className="routing-candidate" data-selected={route.selected === candidate.id}>
    <div className="routing-candidate-heading"><AccountIdentity metadata={metadata.get(candidate.id)} /><span className="routing-eligibility" data-warning={candidate.eligibility !== "eligible"}>{eligibilityLabel(candidate.eligibility)}</span>{route.selected === candidate.id ? <span className="routing-selected-label">{copy("routing-preview.selectedAccount")}</span> : null}</div>
    <dl className="routing-candidate-evidence"><div><dt>{copy("routing-preview.weight")}</dt><dd>{candidate.weight}</dd></div><div><dt>{copy("routing-preview.quota")}</dt><dd>{statusLabel(candidate.quota_state)}</dd></div></dl>
    <Disclosure density={DisclosureDensity.Settings} className="routing-account-id"><DisclosureSummary>{copy("routing-preview.accountId")}</DisclosureSummary><code>{candidate.id}</code><dl className="routing-detail-evidence">{typeof candidate.score === "number" ? <div><dt>{copy("routing-preview.score")}</dt><dd>{candidate.score}</dd></div> : null}{candidate.reset_at ? <div><dt>{copy("routing-preview.reset")}</dt><dd><Timestamp value={candidate.reset_at} active={active} mode={TimestampMode.QuotaCountdown} /></dd></div> : null}</dl></Disclosure>
  </li>)}</ul> : <p className="routing-empty">{copy("routing-preview.noCandidates")}</p>;
}
// Object keys are representation details; ordered arrays and every supplied
// decision field remain evidence. Structural source indexes grant no equality.
function canonicalEvidence(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalEvidence).join(",")}]`;
  if (value && typeof value === "object") return `{${Object.entries(value).sort(([a], [b]) => a.localeCompare(b)).map(([key, item]) => `${JSON.stringify(key)}:${canonicalEvidence(item)}`).join(",")}}`;
  return JSON.stringify(value) ?? "undefined";
}
function sameDecision(first: Route, second: Route) { return canonicalEvidence(first.evidence) === canonicalEvidence(second.evidence); }
function SourceLabel({ source, metadata }: { source: Source; metadata: Map<string, RoutingAccountMetadata> }) {
  const name = metadata.get(`source:${source.source}`);
  return <span>{!name || name.state === RoutingMetadataState.Loading ? copy("routing-preview.loadingSource") : name.name || copy("routing-preview.sourceUnavailable")} · {source.native_model || copy("routing-preview.modelUnavailable")}</span>;
}
function Fallback({ route }: { route: Route }) { return route.fallback ? <p className="routing-fallback">{copy("configuration-actions.insufficientComparableQuotaEvidenceTheServer_95890b")}</p> : null; }
function SourceNotices({ source, index, metadata, hideFallback }: { source: Source; index: number; metadata: Map<string, RoutingAccountMetadata>; hideFallback: boolean }) {
  if (!source.problem && source.route.candidatesAvailable && (!source.route.fallback || hideFallback)) return null;
  return <div className="routing-source-notices"><p>{index + 1} · <SourceLabel source={source} metadata={metadata} /></p>{source.problem ? <ServiceProblem code={text(source.problem.code)}>{text(source.problem.message)}</ServiceProblem> : null}{!hideFallback ? <Fallback route={source.route} /> : null}{!source.route.candidatesAvailable ? <p role="alert">{copy("configuration-actions.routingEvidenceIsUnavailable_3f3ff4")}</p> : null}</div>;
}
export function RoutingPreview({ agent, active, close }: { agent: Resource; active: boolean; close: () => void }) {
  useLocale();
  const closeTask = useCloseSettingsTask(close);
  const [project, setProject] = useState("");
  const [detailsOpen, setDetailsOpen] = useState(false);
  const result = useQuery(ConfigurationQuery.previewRouting, { agentId: agent.id, projectId: project }, { enabled: active, retry: false, placeholderData: undefined });
  const route = useMemo(() => {
    try { return result.data ? routeEvidence(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(result.data.routeJson))) : undefined; } catch { return undefined; }
  }, [result.data]);
  const ids = route ? [route.selected, ...route.candidates.map(candidate => candidate.id), ...(route.sources ?? []).flatMap(source => [source.route.selected, ...source.route.candidates.map(candidate => candidate.id)])].filter((id): id is string => Boolean(id)) : [];
  const metadata = useRoutingAccountMetadata(ids, active, `${agent.id}:${project}:${result.dataUpdatedAt}`, route?.sources?.map(source => source.source));
  const selectedSource = route?.source_index === undefined ? undefined : route.sources?.[route.source_index];
  const repeatedFinal = route && selectedSource && sameDecision(route, selectedSource.route);
  const selectedCandidate = route?.candidates.find(candidate => candidate.id === route.selected);
  return <section className="routing-preview" aria-label={copy("routing-preview.label", { v0: resourceName(agent) })}>
    <p className="routing-agent-name">{resourceName(agent)}</p>
    <div className="routing-project"><div className="routing-project-controls"><span className="routing-read-only">{copy("routing-preview.readOnly")}</span><ResourceChoice label={copy("configuration-actions.project_985959")} emptyLabel={copy("documents.generalChat_f634bc")} kind={EntityKind.PROJECT} value={project} change={setProject} active={active} disabled={!active} /><button type="button" disabled={!active || result.isFetching} aria-label={copy("configuration-actions.refreshRoutingPreview_3b8c83")} onClick={() => void result.refetch()}>{result.isFetching ? copy("routing-preview.refreshing") : copy("routing-preview.refresh")}</button></div><p>{project ? copy("configuration-actions.usingTheSelectedProjectSRestrictions_f4c29e") : copy("configuration-actions.generalChatNoProjectRestrictions_abff59")}</p></div>
    {route ? <div className="routing-result" data-warning={!route.selected} role="status"><span aria-hidden="true" className="routing-result-icon">{route.selected ? "✓" : "!"}</span><div><h3>{route.selected ? copy("routing-preview.selectedAccount") : copy("routing-preview.noEligible")}</h3>{route.selected ? <><AccountIdentity metadata={metadata.get(route.selected)} />{selectedSource ? <SourceLabel source={selectedSource} metadata={metadata} /> : null}{selectedCandidate ? <span className="routing-eligibility" data-warning={selectedCandidate.eligibility !== "eligible"}>{eligibilityLabel(selectedCandidate.eligibility)}</span> : null}</> : <p>{copy("routing-preview.reviewStatus")}</p>}<dl className="routing-summary-evidence routing-policy"><div><dt>{copy("routing-preview.policy")}</dt><dd>{policyLabel(route.policy)}</dd></div>{selectedCandidate ? <div><dt>{copy("routing-preview.quota")}</dt><dd>{statusLabel(selectedCandidate.quota_state)}</dd></div> : null}</dl>{route.selected ? <AccountId id={route.selected} /> : null}</div></div> : result.isPending && !result.error ? <p role="status">{copy("routing-preview.loading")}</p> : null}
    {result.error && route ? <p role="status">{copy("routing-preview.refreshFailed")}</p> : null}<Problem error={result.error} actions={result.error ? <button type="button" disabled={!active || result.isFetching} onClick={() => void result.refetch()}>{copy("ui.retryCurrentRead")}</button> : undefined} />
    {result.data && !route ? <p role="alert">{copy("configuration-actions.routingEvidenceIsUnavailable_3f3ff4")}</p> : null}
    {route ? <>
      <div className="routing-notices"><Fallback route={route} />{!route.candidatesAvailable ? <p role="alert">{copy("configuration-actions.routingEvidenceIsUnavailable_3f3ff4")}</p> : null}{route.sources?.map((source, index) => <SourceNotices key={`${index}:${source.source}`} source={source} index={index} metadata={metadata} hideFallback={Boolean(repeatedFinal && route.source_index === index)} />)}</div>
      <Disclosure density={DisclosureDensity.Settings} className="routing-details" open={detailsOpen} onToggle={event => setDetailsOpen(event.currentTarget.open)}><DisclosureSummary><span>{copy("routing-preview.details")}</span><span className="routing-details-count">{route.candidatesAvailable ? copy("routing-preview.candidateCount", { count: route.candidates.length }) : copy("routing-preview.countUnavailable")}</span></DisclosureSummary><div className="routing-details-body">
      {route.sources?.length ? <section className="routing-sources"><h3>{copy("configuration-actions.sourceDecisions")}</h3><ol>{route.sources.map((source, index) => <li key={`${index}:${source.source}`} className="routing-source"><h4>{index + 1} · <SourceLabel source={source} metadata={metadata} />{route.source_index === index ? copy("configuration-actions.selectedSource") : ""}</h4><Disclosure density={DisclosureDensity.Settings} className="routing-source-id"><DisclosureSummary>{copy("routing-preview.sourceDetails")}</DisclosureSummary><dl><div><dt>{copy("routing-preview.sourceKey")}</dt><dd><code>{source.source}</code></dd></div><div><dt>{copy("routing-preview.modelId")}</dt><dd><code>{source.model_id}</code></dd></div></dl></Disclosure><dl className="routing-policy"><div><dt>{copy("routing-preview.policy")}</dt><dd>{policyLabel(source.route.policy) || statusLabel("unknown")}</dd></div></dl><Candidates route={source.route} metadata={metadata} active={active} /></li>)}</ol></section> : null}
      {!repeatedFinal ? <section className="routing-account-section"><h3>{route.sources?.length ? copy("routing-preview.finalResult") : copy("routing-preview.candidates")}</h3><dl className="routing-policy"><div><dt>{copy("routing-preview.policy")}</dt><dd>{policyLabel(route.policy)}</dd></div></dl><Candidates route={route} metadata={metadata} active={active} /></section> : null}</div></Disclosure></> : null}
    <p className="routing-note">{copy("configuration-actions.thisPreviewIsReadOnlyAnd_dd9bb1")}</p>
    <SettingsTaskActions><SettingsTaskDismissButton type="button" data-settings-task-cancel onClick={closeTask}>{copy("settings-task.close")}</SettingsTaskDismissButton></SettingsTaskActions>
  </section>;
}
