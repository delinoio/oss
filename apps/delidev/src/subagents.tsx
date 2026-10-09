import { createPortal } from "react-dom";
import { useSessionActive, useSessionQuery as useQuery } from "./session-activity";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { useConversationPages } from "./conversation-pagination";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { LocalizedText, copy, useLocale } from "./localization";
import { useCallback, useEffect, useMemo, useRef } from "react";

import { EntityKind, ResourceQuery, SystemCapability, SystemQuery, type Resource } from "@delinoio/delidev-api-client";
import { items, object, text } from "./documents";
import { Failure, Problem } from "./ui";
import { validateSubagentPage, type SubagentRow } from "./subagent-record";

const unavailable = () => <p>{copy("subagents.theRetainedChildPageIsUnavailable_e2dd4a")}</p>;
export function SubagentRows({ rows, sessionId, technical = true, labels }: { rows: readonly Resource[]; sessionId: string; technical?: boolean; labels?: Map<string, number> }) {
  useLocale();
  const accepted = useMemo(() => validateSubagentPage(rows, sessionId), [rows, sessionId]);
  return accepted ? <ChildRows rows={accepted} technical={technical} labels={labels} /> : unavailable();
}

function ChildRows({ rows, technical = true, labels }: { rows: readonly SubagentRow[]; technical?: boolean; labels?: Map<string, number> }) {
  useLocale();
  return <table aria-label={copy("subagents.nativeChildAgentHierarchy_212425")}>
    <thead><tr><th>{copy("subagents.child_805332")}</th><th>{copy("subagents.parent_5f7953")}</th><th>{copy("subagents.status_920e41")}</th><th>{copy("subagents.model_5e2c61")}</th><th>{copy("subagents.recentOutput_4bafa7")}</th><th>{copy("subagents.usageObservation_9c75b9")}</th></tr></thead>
    <tbody>{rows.map(({ resource: row, record, child }) => {
      const output = object(child.output), usage = object(child.usage);
      const blocks = items(output.blocks).map(object);
      return <tr key={row.id}>
        {technical ? <td><code>{text(child.native_id)}</code><section><h4>{copy("subagents.sourceCoverage_0b8649")}</h4><p>{text(record.harness)} {text(record.native_version)}</p><p><LocalizedText id="subagents.execution_2ce365" components={{ s0: <>{text(record.execution_id)}</> }} /></p><p><LocalizedText id="subagents.parentTool_5586e7" components={{ s0: <>{text(child.parent_tool_id) || copy("subagents.extra.ca1844969742")}</> }} /></p><ul>{items(record.sources).map(object).map((source, index) => <li key={index}><LocalizedText id="subagents.sequence_440e77" components={{ s0: <>{text(source.source)}</>, s1: <>{text(source.source_id)}</>, s2: <>{String(source.sequence ?? copy("subagents.extra.ca1844969742"))}</>, s3: <>{text(object(source.usage).native_report) ? <pre>{text(object(source.usage).native_report)}</pre> : null}</> }} /></li>)}</ul></section></td> : <td>{copy("session-name.subagent", { number: labels?.get(`${record.execution_id}:${child.native_id}`) ?? "—" })}</td>}
        <td>{technical ? <code>{text(child.parent_id)}</code> : child.parent_id === record.root_id ? copy("subagents.rootSession_6cdfaa") : labels?.has(`${record.execution_id}:${child.parent_id}`) ? copy("session-name.subagent", { number: labels.get(`${record.execution_id}:${child.parent_id}`)! }) : copy("subagents.unavailable_ca1844")}</td>
        <td>{text(child.status) || copy("subagents.extra.ca1844969742")}</td>
        <td><LocalizedText id="subagents.observed_ae32e4" components={{ s0: <>{text(child.observed_model) || copy("subagents.extra.ca1844969742")}</> }} /><p><LocalizedText id="subagents.requested_a7d830" components={{ s0: <>{text(child.requested_model) || copy("subagents.extra.ca1844969742")}</> }} /></p></td>
        <td>{child.output == null ? copy("subagents.unavailable_ca1844") : <><p>{output.partial === true ? copy("subagents.partialNativeOutput_731a58") : copy("subagents.observedNativeOutput_37d11c")}</p>{technical ? <code>{text(output.native_message_id)}</code> : null}{text(output.text) ? <pre>{text(output.text)}</pre> : null}{blocks.map((block, index) => <div key={index}><small>{text(block.kind)}</small>{block.text == null ? <p>{copy("subagents.contentUnavailable_e9f250")}</p> : <pre>{text(block.text)}</pre>}</div>)}</>}</td>
        <td>{child.usage == null ? copy("subagents.unavailable_ca1844") : <><p>{text(usage.scope)}</p><p><LocalizedText id="subagents.total_dc9841" components={{ s0: <>{text(usage.total) || copy("subagents.extra.ca1844969742")}</> }} /></p><p><LocalizedText id="subagents.input_f51d4a" components={{ s0: <>{text(usage.input) || copy("subagents.extra.ca1844969742")}</> }} /></p><p><LocalizedText id="subagents.output_296ead" components={{ s0: <>{text(usage.output) || copy("subagents.extra.ca1844969742")}</> }} /></p><p>{copy("subagents.observationOnlyExcludedFromAdditiveBilling_23cc58")}</p></>}</td>
      </tr>;
    })}</tbody>
  </table>;
}

export function Subagents({ sessionId, revision, visible = true, emptyChanged, diagnosticsTarget }: { sessionId: string; revision: string; visible?: boolean; emptyChanged?: (value: boolean) => void; diagnosticsTarget?: HTMLElement | null }) {
  useLocale();
  const active=useSessionActive();
  const status = useQuery(SystemQuery.getStatus, {});
  const supported = status.data?.capabilities.includes(SystemCapability.SUBAGENT_OBSERVATION_V1) === true;
  const openCodeSupported = status.data?.capabilities.includes(SystemCapability.OPENCODE_FOREGROUND_SUBAGENTS_V1) === true;
  const root = useRef<HTMLDivElement>(null);
  const validate = useCallback((resources: Resource[]) => { if (!validateSubagentPage(resources, sessionId)) throw new Error("The retained child page is unavailable."); }, [sessionId]);
  const query = useConversationPages(EntityKind.SUBAGENT, sessionId, supported, 50, validate);
  // Native events invalidate only this read. No observation can issue a child
  // input, resume, interruption, retry, or mutation.
  useEffect(() => { if (active && supported) void query.refresh(); }, [revision, supported, query.refresh, active]);
  const rows = useMemo(() => {
    if (!query.loaded) return undefined;
    // The accepted payload window can contain several independently bounded
    // pages. Keep each page's validation bound before composing its rows.
    const pages = query.payloadPages.map(page => validateSubagentPage(page.payload, sessionId));
    return pages.every((page): page is SubagentRow[] => page !== undefined) ? pages.flat() : undefined;
  }, [query.loaded, query.payloadPages, sessionId]);
  const labels = useRef(new Map<string, number>());
  for (const row of rows ?? []) { const key = `${row.record.execution_id}:${row.child.native_id}`; if (!labels.current.has(key) && labels.current.size < 1000) labels.current.set(key, labels.current.size + 1); }
  useEffect(() => { emptyChanged?.(supported && query.loaded && !query.error && !query.loading && !query.nextPageToken && query.rows.length === 0); }, [supported, query.loaded, query.error, query.loading, query.nextPageToken, query.rows.length, emptyChanged]);
  const needsOpenCodeUpdate = rows?.some(row => row.record.harness === "opencode") === true && !openCodeSupported;
  return <div ref={root} className="conversation-page-scroll">
    <p>{copy("subagents.readOnlyNativeHierarchyParentCompletion_036e57")}</p>
    {status.isPending ? <p role="status">{copy("subagents.checkingCapability")}</p> : null}
    {status.error ? <Problem error={status.error} /> : null}
    {status.data && !supported ? <p>{copy("subagents.thisServerDoesNotSupportChild_0baf88")}</p> : null}
    {query.error ? <Failure failure={query.error?.failure} /> : null}
    {query.isPending && supported ? <p>{copy("subagents.loadingChildObservations_0a8d53")}</p> : null}
    {supported && (query.error && !query.loaded || query.data && !rows) ? unavailable() : null}
    {supported && rows?.length === 0 ? <p>{copy("subagents.noNativeChildObservationsAreAvailable_8c2be5")}</p> : null}
    {needsOpenCodeUpdate ? <p>{copy("subagents.updateTheServerAndRunnerDevice_ac66cd")}</p> : supported && rows && rows.length > 0 ? <div><ScrollPayloadWindow identity={paginationIdentity} revision={paginationRevision} query={query} root={root} active={active && visible}>{payload => <><SubagentRows rows={payload} sessionId={sessionId} technical={false} labels={labels.current} />{diagnosticsTarget ? createPortal(<section><h3>{copy("session-name.nativeEvidence")}</h3><SubagentRows rows={payload} sessionId={sessionId} /></section>, diagnosticsTarget) : null}</>}</ScrollPayloadWindow></div> : null}
    {query.error ? <button disabled={!supported || query.isFetching} onClick={() => void query.refetch()}>{copy("session-name.retryRead")}</button> : null}
    <ScrollContinuation query={query} root={root} active={active && visible && supported && !needsOpenCodeUpdate} label={copy("subagents.subagents_88296a")} />
  </div>;
}
