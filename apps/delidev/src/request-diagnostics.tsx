import { Disclosure, DisclosureSummary } from "./disclosure";
import { Timestamp } from "./timestamp-display";
import { useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutInput } from "./shortcuts";
import { Surface } from "./surface";
import { ownedMessage, useProductMessage, copy, useLocale  } from "./localization";
import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  subscriptionServiceFromWire, subscriptionServiceLabel, subscriptionServiceHarnesses, isEntityId, RequestDiagnosticSource as Source, RequestDiagnosticState as State,
  RequestDiagnosticOperation as Operation, SessionQuery, SystemCapability, SystemQuery,
  type ListRequestDiagnosticsResponse, type RequestDiagnostic,
} from "@delinoio/delidev-api-client";
import { Failure, Problem } from "./ui";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";
import { ScrollContinuation, useScrollRoot } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";

const efforts = new Set(["none", "minimal", "low", "medium", "high", "xhigh", "max"]);
const effectiveTiers = new Set(["auto", "default", "flex", "priority", "standard"]);
const requestedTiers = new Set([...effectiveTiers, "standard_only"]);
const errors = new Set(["", "invalid_argument", "unsupported", "unauthenticated", "permission_denied", "resource_exhausted", "canceled", "conflict", "not_found", "recovery_required", "unavailable", "internal"]);
const opaqueId = /^(?:[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}|(?:req[_-]|resp_|msg_|chatcmpl-)[a-zA-Z0-9_-]+)$/;
const harnesses = new Set(["codex", "claude-code", "opencode", "grok-build"]);
const utcTimestamp = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/;
function preciseTimestamp(value: string): string | undefined {
  if (!utcTimestamp.test(value)) return undefined;
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds) || new Date(milliseconds).toISOString().slice(0, 19) !== value.slice(0, 19)) return undefined;
  // Canonical UTC components sort exactly; Date alone discards sub-millisecond precision.
  const fraction = value.slice(19, -1).replace(/^\./, "").padEnd(9, "0");
  return `${value.slice(0, 19)}.${fraction}Z`;
}
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
    if (![value.id, value.sessionId, value.executionId, value.accountId, value.connectionId, value.publicationRequestId].every(isEntityId) || ids.has(value.id) || value.sessionId !== sessionId || executionId && value.executionId !== executionId || value.revision < 1n || value.revision > 9223372036854775807n) invalid();
    if (!value.model || !value.model.nativeId || new TextEncoder().encode(value.model.nativeId).length > 256 || value.model.providerId !== value.providerId || value.model.subscriptionService !== value.subscriptionService || value.modelId) invalid();
    const service = subscriptionServiceFromWire(value.subscriptionService);
    if (value.subscriptionService ? !service || value.providerId || value.source !== Source.NATIVE_INPUT || subscriptionServiceHarnesses[service] !== value.harness : !isEntityId(value.providerId)) invalid();
    ids.add(value.id);
    if (![Source.NATIVE_INPUT, Source.PROXY_HTTP].includes(value.source) || ![State.IN_PROGRESS, State.SUCCEEDED, State.FAILED, State.CANCELED].includes(value.state) || value.operation < Operation.INPUT || value.operation > Operation.COUNT || !errors.has(value.errorCode) || !["conversation", "session-title"].includes(value.purpose) || !harnesses.has(value.harness) || (value.state === State.IN_PROGRESS) !== (value.finishedAt === undefined) || [State.IN_PROGRESS, State.SUCCEEDED].includes(value.state) && value.errorCode) invalid();
    for (const id of [value.inputId, value.correlationId]) if (id && !isEntityId(id)) invalid();
    for (const id of [value.nativeRequestId, value.providerRequestId, value.nativeResponseId]) if (id && (id.length > 128 || !opaqueId.test(id))) invalid();
    if (value.nativeThreadId && !nativeIdentity(value.nativeThreadId, value.harness, false) || value.nativeTurnId && !nativeIdentity(value.nativeTurnId, value.harness, true)) invalid();
    for (const setting of [value.requestedEffort, value.effectiveEffort]) if (setting !== undefined && !efforts.has(setting)) invalid();
    const nativeFast = value.source === Source.NATIVE_INPUT && value.harness === "codex";
    if (value.requestedServiceTier !== undefined && !requestedTiers.has(value.requestedServiceTier) && !(nativeFast && value.requestedServiceTier === "fast") || value.effectiveServiceTier !== undefined && !effectiveTiers.has(value.effectiveServiceTier) && !(nativeFast && value.effectiveServiceTier === "fast")) invalid();
    const observed = preciseTimestamp(value.observedAt), finished = value.finishedAt === undefined ? undefined : preciseTimestamp(value.finishedAt);
    if (observed === undefined || value.finishedAt !== undefined && (finished === undefined || finished < observed) || value.durationMs !== undefined && (value.durationMs < 0n || value.durationMs > 960000n) || value.httpStatus !== undefined && (value.httpStatus < 100 || value.httpStatus > 599)) invalid();
    if (value.source === Source.NATIVE_INPUT && (value.operation !== Operation.INPUT || !value.inputId || !value.nativeThreadId || value.nativeRequestId !== value.id || value.httpAttempted !== undefined || value.httpStatus !== undefined || value.durationMs !== undefined || value.correlationId || value.nativeResponseId || value.providerRequestId) || value.source === Source.PROXY_HTTP && (value.operation === Operation.INPUT || value.httpAttempted === undefined || value.correlationId !== value.id || value.inputId || value.nativeThreadId || value.nativeTurnId || value.httpStatus !== undefined && !value.httpAttempted || value.state === State.SUCCEEDED && (!value.httpAttempted || value.httpStatus !== 200))) invalid();
  }
  return response;
}

function DiagnosticRow({ value }: { value: RequestDiagnostic }) {
  useLocale();
  const native = value.source === Source.NATIVE_INPUT;
  const state = State[value.state].toLowerCase().replaceAll("_", " ");
  return <article className="result" aria-label={copy("request-diagnostics.message_42d375", { v0: native ? copy("request-diagnostics.nativeInput_f0007b") : copy("request-diagnostics.httpRequest_f1ee11"), v1: value.id })}>
    <h3>{native ? copy("request-diagnostics.nativeInput_f0007b") : Operation[value.operation].toLowerCase()} · {state}</h3>
    <dl>
      <dt>{copy("request-diagnostics.request_59f03d")}</dt><dd>{value.id}</dd><dt>{copy("request-diagnostics.revision_2e516d")}</dt><dd>{value.revision.toString()}</dd><dt>{copy("request-diagnostics.execution_a45cd4")}</dt><dd>{value.executionId}</dd>
      <dt>{copy("request-diagnostics.accountAtRequestTime_3521b7")}</dt><dd>{value.accountId}</dd><dt>{copy("request-diagnostics.connection_639a40")}</dt><dd>{value.connectionId}</dd>
      <dt>{value.subscriptionService ? copy("request-diagnostics.subscriptionService_0e16df") : copy("request-diagnostics.provider_472590")}</dt><dd>{value.subscriptionService ? subscriptionServiceLabel(value.subscriptionService) : value.providerId}</dd><dt>{copy("request-diagnostics.model_5e2c61")}</dt><dd>{value.model?.nativeId}</dd><dt>{copy("request-diagnostics.purpose_d4e883")}</dt><dd>{value.purpose}</dd>
      <dt>{copy("request-diagnostics.observed_64fa8a")}</dt><dd><Timestamp value={value.observedAt} /></dd><dt>{copy("request-diagnostics.finished_7804f7")}</dt><dd><Timestamp value={value.finishedAt} fallback={copy("request-diagnostics.unavailable_ca1844")} /></dd>
      <dt>{copy("request-diagnostics.httpLatency_4cebf1")}</dt><dd>{value.durationMs === undefined ? copy("request-diagnostics.unavailable_ca1844") : copy("request-diagnostics.ms_d659ce", { v0: value.durationMs.toString() })}</dd>
      <dt>{copy("request-diagnostics.httpAttempt_4a264f")}</dt><dd>{value.httpAttempted === undefined ? copy("request-diagnostics.unavailable_ca1844") : value.httpAttempted ? copy("request-diagnostics.sendClaimedProviderAcceptanceIsUnconfirmed_0370a9") : copy("request-diagnostics.noHttpAttempt_56754e")}</dd>
      <dt>{copy("request-diagnostics.httpStatus_0f7cf9")}</dt><dd>{value.httpStatus ?? copy("request-diagnostics.unavailable_ca1844")}</dd><dt>{copy("request-diagnostics.error_54a0e8")}</dt><dd>{value.errorCode || copy("request-diagnostics.extra.7b563836dc50")}</dd>
      <dt>{native ? copy("request-diagnostics.selectedEffort_2a276e") : copy("request-diagnostics.requestedEffort_59fdfe")}</dt><dd>{value.requestedEffort ?? copy("request-diagnostics.unavailable_ca1844")}</dd>
      <dt>{native ? copy("request-diagnostics.nativeEffectiveEffort_1e90c0") : copy("request-diagnostics.providerObservedEffort_b13db8")}</dt><dd>{value.effectiveEffort ?? copy("request-diagnostics.unavailable_ca1844")}</dd>
      <dt>{native ? copy("request-diagnostics.selectedServiceTier_2c8ca9") : copy("request-diagnostics.requestedServiceTier_14f88a")}</dt><dd>{value.requestedServiceTier ?? copy("request-diagnostics.unavailable_ca1844")}</dd>
      <dt>{native ? copy("request-diagnostics.nativeEffectiveServiceTier_d412ae") : copy("request-diagnostics.providerObservedServiceTier_01a40b")}</dt><dd>{value.effectiveServiceTier ?? copy("request-diagnostics.unavailable_ca1844")}</dd>
    </dl>
    <Disclosure><DisclosureSummary>{copy("request-diagnostics.originalIdentities_580b0d")}</DisclosureSummary><dl>{[
      [copy("request-diagnostics.extra.406c0cd17230"), value.publicationRequestId], [copy("request-diagnostics.extra.5f1a25573a30"), value.correlationId], [copy("request-diagnostics.extra.ba2dea0965ff"), value.nativeRequestId],
      [copy("request-diagnostics.extra.45d8583658f2"), value.providerRequestId], [copy("request-diagnostics.extra.6e5cd62e3845"), value.nativeResponseId], [copy("request-diagnostics.extra.230d0da59fbf"), value.nativeThreadId], [copy("request-diagnostics.extra.a12de5a8959e"), value.nativeTurnId],
    ].map(([label, id], index) => <div key={index}><dt>{label}</dt><dd>{id || copy("request-diagnostics.extra.ca1844969742")}</dd></div>)}</dl></Disclosure>
  </article>;
}

export function RequestDiagnostics({ sessionId, close }: { sessionId: string; close: () => void }) {
  useLocale();
  const panelRoot = useRef<HTMLElement>(null);
  const shortcuts = useShortcuts([{ id: ShortcutId.DiagnosticsClose, scope: Surface.Sessions, label: "shortcuts.closeDiagnostics", bindings: [{ key: "Escape" }], target: panelRoot, input: ShortcutInput.Target, run: close }]);
  const input = useRef<HTMLInputElement>(null);
  const [draft, setDraft] = useState(""), [execution, setExecution] = useState("");
  const [problem, setProblem] = useProductMessage("");
  const status = useQuery(SystemQuery.getStatus, {}, { retry: false });
  const supported = status.data?.capabilities.includes(SystemCapability.REQUEST_DIAGNOSTICS_V1) ?? false;
  const root = useScrollRoot(panelRoot);
  const request = useCallback((token: string) => ({ sessionId, executionId: execution, pageSize: 50, pageToken: token }), [sessionId, execution]);
  const project = useCallback((response: ListRequestDiagnosticsResponse) => {
    const page = validateDiagnosticPage(response, sessionId, execution);
    return { rows: page.records.map(row => ({ id: row.id, revision: row.revision })), payload: page.records, nextPageToken: page.nextPageToken };
  }, [sessionId, execution]);
  const reader = useConnectPaginationReader(SessionQuery.listRequestDiagnostics, request, project);
  const result = usePaginationChain(JSON.stringify([sessionId, execution]), supported, reader);
  usePaginationRefresh(SessionQuery.listRequestDiagnostics, request(""), supported, result.refresh);
  useEffect(() => { input.current?.focus(); }, []);
  return <aside ref={panelRoot} aria-keyshortcuts={shortcuts.aria(ShortcutId.DiagnosticsClose)} className="session-files" aria-label={copy("request-diagnostics.modelRequestDiagnostics_0c266b")} onKeyDown={shortcuts.onKeyDown}>
    <header><h2>{copy("request-diagnostics.modelRequestDiagnostics_0c266b")}</h2><button onClick={close} aria-keyshortcuts={shortcuts.aria(ShortcutId.DiagnosticsClose)}>{copy("request-diagnostics.closeDiagnostics_143427")}</button></header>
    <p>{copy("request-diagnostics.nativeInputsAndIndividualHttpAttempts_6d3cf6")}</p>
    <p>{copy("request-diagnostics.httpLatencyCoversTheObservedRequest_bc4448")}</p>
    <form onSubmit={(event) => { event.preventDefault(); if (draft && !isEntityId(draft)) { setProblem(ownedMessage("request-diagnostics.extra.4fa709f4f55a")); return; } setProblem(""); if (draft === execution) result.reload(); else setExecution(draft); }}>
      <label>{copy("request-diagnostics.executionIdOptional_43c4ca")}<input ref={input} value={draft} onChange={(event) => setDraft(event.target.value)} autoComplete="off" spellCheck={false} /></label><button disabled={!supported}>{copy("request-diagnostics.applyExecutionFilter_591b7d")}</button>
    </form>
    {problem ? <p role="alert">{problem}</p> : null}<Problem error={status.error} /><Failure failure={result.error?.failure} />
    {status.isPending ? <p role="status">{copy("request-diagnostics.checkingDiagnosticSupport_65aa48")}</p> : status.data && !supported ? <p>{copy("request-diagnostics.requestDiagnosticsAreUnavailableOnThis_cb5bc2")}</p> : null}
    {status.error ? <button onClick={() => void status.refetch()}>{copy("request-diagnostics.retryServerCapabilities_18a515")}</button> : null}
    {supported ? <button disabled={Boolean(result.loading)} onClick={result.refreshExplicit}>{copy("request-diagnostics.refreshDiagnostics_7bce98")}</button> : null}
    {supported && !result.loaded && result.loading ? <p role="status">{copy("request-diagnostics.loadingRequestObservations_ac40d6")}</p> : null}
    {result.error && result.loaded ? <p role="alert">{copy("request-diagnostics.refreshFailedTheDisplayedObservationsMay_c02e74")}</p> : null}
    <ScrollPayloadWindow query={result} root={root} active={supported} identity={value => value.id} revision={value => value.revision}>{payload => payload.map(value => <DiagnosticRow key={value.id} value={value} />)}</ScrollPayloadWindow>
    {result.loaded && result.rows.length === 0 ? <p>{copy("request-diagnostics.noRetainedRequestObservationsForThis_ddf6c6")}</p> : null}
    <ScrollContinuation query={result} root={root} active={supported} label={copy("request-diagnostics.modelRequestDiagnostics_0c266b")} />
  </aside>;
}
