import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  subscriptionServiceFromWire, subscriptionServiceLabel, subscriptionServiceHarnesses, isEntityId, RequestDiagnosticSource as Source, RequestDiagnosticState as State,
  RequestDiagnosticOperation as Operation, SessionQuery, SystemCapability, SystemQuery,
  type ListRequestDiagnosticsResponse, type RequestDiagnostic,
} from "@delinoio/delidev-api-client";
import { Problem } from "./ui";

const efforts = new Set(["none", "minimal", "low", "medium", "high", "xhigh", "max"]);
const tiers = new Set(["auto", "default", "flex", "priority", "standard"]);
const errors = new Set(["", "invalid_argument", "unsupported", "unauthenticated", "permission_denied", "resource_exhausted", "canceled", "conflict", "not_found", "recovery_required", "unavailable", "internal"]);
const opaqueId = /^(?:[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}|(?:req[_-]|resp_|msg_|chatcmpl-)[a-zA-Z0-9_-]+)$/;
const harnesses = new Set(["codex", "claude-code", "opencode", "grok-build"]);
const utcTimestamp = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/;
function nativeIdentity(value: string, harness: string, turn: boolean): boolean {
  if (harness === "opencode") return (turn ? /^msg_[0-9a-f]{12}[a-zA-Z0-9]{14}$/ : /^ses_[0-9a-f]{12}[a-zA-Z0-9]{14}$/).test(value);
  if (!turn || harness === "codex") return isEntityId(value);
  return (harness === "grok-build" ? /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/ : /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/).test(value);
}

export function validateDiagnosticPage(response: ListRequestDiagnosticsResponse, sessionId: string, executionId: string): ListRequestDiagnosticsResponse {
  const invalid = () => { throw new Error("Request diagnostic observations are unavailable."); };
  if (response.records.length > 50 || response.nextPageToken.length > 2048) invalid();
  const ids = new Set<string>();
  for (const value of response.records) {
    if (![value.id, value.sessionId, value.executionId, value.accountId, value.connectionId, value.modelId].every(isEntityId) || ids.has(value.id) || value.sessionId !== sessionId || executionId && value.executionId !== executionId || value.revision < 1n || value.revision > 9223372036854775807n) invalid();
    const service = subscriptionServiceFromWire(value.subscriptionService);
    if (value.subscriptionService ? !service || value.providerId || value.source !== Source.NATIVE_INPUT || subscriptionServiceHarnesses[service] !== value.harness : !isEntityId(value.providerId)) invalid();
    ids.add(value.id);
    if (![Source.NATIVE_INPUT, Source.PROXY_HTTP].includes(value.source) || ![State.IN_PROGRESS, State.SUCCEEDED, State.FAILED, State.CANCELED].includes(value.state) || value.operation < Operation.INPUT || value.operation > Operation.COUNT || !errors.has(value.errorCode) || !["conversation", "session-title"].includes(value.purpose) || !harnesses.has(value.harness) || (value.state === State.IN_PROGRESS) !== (value.finishedAt === undefined) || [State.IN_PROGRESS, State.SUCCEEDED].includes(value.state) && value.errorCode) invalid();
    for (const id of [value.inputId, value.publicationRequestId, value.correlationId]) if (id && !isEntityId(id)) invalid();
    for (const id of [value.nativeRequestId, value.providerRequestId, value.nativeResponseId]) if (id && (id.length > 128 || !opaqueId.test(id))) invalid();
    if (value.nativeThreadId && !nativeIdentity(value.nativeThreadId, value.harness, false) || value.nativeTurnId && !nativeIdentity(value.nativeTurnId, value.harness, true)) invalid();
    for (const setting of [value.requestedEffort, value.effectiveEffort]) if (setting !== undefined && !efforts.has(setting)) invalid();
    for (const setting of [value.requestedServiceTier, value.effectiveServiceTier]) if (setting !== undefined && !tiers.has(setting)) invalid();
    const observed = Date.parse(value.observedAt), finished = value.finishedAt === undefined ? undefined : Date.parse(value.finishedAt);
    if (!utcTimestamp.test(value.observedAt) || !Number.isFinite(observed) || finished !== undefined && (!utcTimestamp.test(value.finishedAt!) || !Number.isFinite(finished) || finished < observed) || value.durationMs !== undefined && (value.durationMs < 0n || value.durationMs > 960000n) || value.httpStatus !== undefined && (value.httpStatus < 100 || value.httpStatus > 599)) invalid();
    if (value.source === Source.NATIVE_INPUT && (value.operation !== Operation.INPUT || !value.inputId || !value.nativeThreadId || value.nativeRequestId !== value.id || value.httpAttempted !== undefined || value.httpStatus !== undefined || value.durationMs !== undefined || value.correlationId || value.nativeResponseId || value.providerRequestId) || value.source === Source.PROXY_HTTP && (value.operation === Operation.INPUT || value.httpAttempted === undefined || value.correlationId !== value.id || value.inputId || value.nativeThreadId || value.nativeTurnId || value.httpStatus !== undefined && !value.httpAttempted)) invalid();
  }
  return response;
}

function DiagnosticRow({ value }: { value: RequestDiagnostic }) {
  const native = value.source === Source.NATIVE_INPUT;
  const state = State[value.state].toLowerCase().replaceAll("_", " ");
  return <article className="result" aria-label={`${native ? "Native input" : "HTTP request"} ${value.id}`}>
    <h3>{native ? "Native input" : Operation[value.operation].toLowerCase()} · {state}</h3>
    <dl>
      <dt>Request</dt><dd>{value.id}</dd><dt>Execution</dt><dd>{value.executionId}</dd>
      <dt>Account at request time</dt><dd>{value.accountId}</dd><dt>Connection</dt><dd>{value.connectionId}</dd>
      <dt>{value.subscriptionService ? "Subscription service" : "Provider"}</dt><dd>{value.subscriptionService ? subscriptionServiceLabel(value.subscriptionService) : value.providerId}</dd><dt>Model</dt><dd>{value.modelId}</dd><dt>Purpose</dt><dd>{value.purpose}</dd>
      <dt>Observed</dt><dd>{value.observedAt}</dd><dt>Finished</dt><dd>{value.finishedAt ?? "Unavailable"}</dd>
      <dt>HTTP latency</dt><dd>{value.durationMs === undefined ? "Unavailable" : `${value.durationMs.toString()} ms`}</dd>
      <dt>HTTP attempt</dt><dd>{value.httpAttempted === undefined ? "Unavailable" : value.httpAttempted ? "Send claimed; provider acceptance is unconfirmed" : "No HTTP attempt"}</dd>
      <dt>HTTP status</dt><dd>{value.httpStatus ?? "Unavailable"}</dd><dt>Error</dt><dd>{value.errorCode || "None observed"}</dd>
      <dt>{native ? "Selected effort" : "Requested effort"}</dt><dd>{value.requestedEffort ?? "Unavailable"}</dd>
      <dt>{native ? "Native effective effort" : "Provider observed effort"}</dt><dd>{value.effectiveEffort ?? "Unavailable"}</dd>
      <dt>{native ? "Selected service tier" : "Requested service tier"}</dt><dd>{value.requestedServiceTier ?? "Unavailable"}</dd>
      <dt>{native ? "Native effective service tier" : "Provider observed service tier"}</dt><dd>{value.effectiveServiceTier ?? "Unavailable"}</dd>
    </dl>
    <details><summary>Original identities</summary><dl>{[
      ["Publication request", value.publicationRequestId], ["Correlation", value.correlationId], ["Native request", value.nativeRequestId],
      ["Provider request", value.providerRequestId], ["Native response", value.nativeResponseId], ["Native thread", value.nativeThreadId], ["Native turn", value.nativeTurnId],
    ].map(([label, id]) => <div key={label}><dt>{label}</dt><dd>{id || "Unavailable"}</dd></div>)}</dl></details>
  </article>;
}

export function RequestDiagnostics({ sessionId, close }: { sessionId: string; close: () => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [draft, setDraft] = useState(""), [execution, setExecution] = useState(""), [page, setPage] = useState("");
  const [problem, setProblem] = useState("");
  const status = useQuery(SystemQuery.getStatus, {}, { retry: false });
  const supported = status.data?.capabilities.includes(SystemCapability.REQUEST_DIAGNOSTICS_V1) ?? false;
  const result = useQuery(SessionQuery.listRequestDiagnostics, { sessionId, executionId: execution, pageSize: 50, pageToken: page }, {
    enabled: supported, retry: false, gcTime: 0, staleTime: Infinity, refetchOnWindowFocus: false,
    select: (response) => validateDiagnosticPage(response, sessionId, execution),
  });
  useEffect(() => { input.current?.focus(); }, []);
  return <aside className="session-files" aria-label="Model request diagnostics" onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); close(); } }}>
    <header><h2>Model request diagnostics</h2><button onClick={close}>Close diagnostics</button></header>
    <p>Native inputs and individual HTTP attempts are separate observations. Requests are matched only by original identities. Missing observations remain unavailable.</p>
    <p>HTTP latency covers the observed request through its response delivery. Native input outcomes do not confirm process cleanup. A retained send claim may not have reached the provider; replaying a publication receipt creates no HTTP attempt.</p>
    <form onSubmit={(event) => { event.preventDefault(); if (draft && !isEntityId(draft)) { setProblem("Enter an exact execution UUID v7, or leave this empty for all executions."); return; } setProblem(""); setExecution(draft); setPage(""); }}>
      <label>Execution ID (optional)<input ref={input} value={draft} onChange={(event) => setDraft(event.target.value)} autoComplete="off" spellCheck={false} /></label><button disabled={!supported}>Apply execution filter</button>
    </form>
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={status.error || result.error} />
    {status.isPending ? <p role="status">Checking diagnostic support…</p> : status.data && !supported ? <p>Request diagnostics are unavailable on this server.</p> : null}
    {status.error ? <button onClick={() => void status.refetch()}>Retry server capabilities</button> : null}
    {supported ? <button disabled={result.isFetching} onClick={() => void result.refetch()}>Refresh diagnostics</button> : null}
    {supported && result.isPending ? <p role="status">Loading request observations…</p> : null}
    {result.error && result.data ? <p role="alert">Refresh failed. The displayed observations may be stale.</p> : null}
    {result.data?.records.map((value) => <DiagnosticRow key={value.id} value={value} />)}
    {result.data?.records.length === 0 ? <p>No retained request observations for this selection. Historical or unobserved native requests cannot be reconstructed.</p> : null}
    <nav aria-label="Diagnostic pages"><button disabled={!page || result.isFetching} onClick={() => setPage("")}>First page</button><button disabled={!result.data?.nextPageToken || result.isFetching} onClick={() => setPage(result.data!.nextPageToken)}>Next page</button></nav>
  </aside>;
}
