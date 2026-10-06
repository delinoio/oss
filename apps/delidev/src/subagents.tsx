import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SystemCapability, SystemQuery, type Resource } from "@delinoio/delidev-api-client";
import { items, object, text } from "./documents";
import { Problem } from "./ui";
import { validateSubagentPage, type SubagentRow } from "./subagent-record";

const unavailable = <p>{copy("subagents.theRetainedChildPageIsUnavailable_e2dd4a")}</p>;
export function SubagentRows({ rows, sessionId }: { rows: readonly Resource[]; sessionId: string }) {
  useLocale();
  const accepted = useMemo(() => validateSubagentPage(rows, sessionId), [rows, sessionId]);
  return accepted ? <ChildRows rows={accepted} /> : unavailable;
}

function ChildRows({ rows }: { rows: readonly SubagentRow[] }) {
  useLocale();
  return <table aria-label={copy("subagents.nativeChildAgentHierarchy_212425")}>
    <thead><tr><th>{copy("subagents.child_805332")}</th><th>{copy("subagents.parent_5f7953")}</th><th>{copy("subagents.status_920e41")}</th><th>{copy("subagents.model_5e2c61")}</th><th>{copy("subagents.recentOutput_4bafa7")}</th><th>{copy("subagents.usageObservation_9c75b9")}</th></tr></thead>
    <tbody>{rows.map(({ resource: row, record, child }) => {
      const output = object(child.output), usage = object(child.usage);
      const blocks = items(output.blocks).map(object);
      return <tr key={row.id}>
        <td><code>{text(child.native_id)}</code><details><summary>{copy("subagents.sourceCoverage_0b8649")}</summary><p>{text(record.harness)} {text(record.native_version)}</p><p><LocalizedText id="subagents.execution_2ce365" components={{ s0: <>{text(record.execution_id)}</> }} /></p><p><LocalizedText id="subagents.parentTool_5586e7" components={{ s0: <>{text(child.parent_tool_id) || "Unavailable"}</> }} /></p><ul>{items(record.sources).map(object).map((source, index) => <li key={index}><LocalizedText id="subagents.sequence_440e77" components={{ s0: <>{text(source.source)}</>, s1: <>{text(source.source_id)}</>, s2: <>{String(source.sequence ?? "Unavailable")}</>, s3: <>{text(object(source.usage).native_report) ? <pre>{text(object(source.usage).native_report)}</pre> : null}</> }} /></li>)}</ul></details></td>
        <td><code>{text(child.parent_id)}</code>{child.parent_id === record.root_id ? <p>{copy("subagents.rootSession_6cdfaa")}</p> : null}</td>
        <td>{text(child.status) || "Unavailable"}</td>
        <td><LocalizedText id="subagents.observed_ae32e4" components={{ s0: <>{text(child.observed_model) || "Unavailable"}</> }} /><p><LocalizedText id="subagents.requested_a7d830" components={{ s0: <>{text(child.requested_model) || "Unavailable"}</> }} /></p></td>
        <td>{child.output == null ? copy("subagents.unavailable_ca1844") : <><p>{output.partial === true ? copy("subagents.partialNativeOutput_731a58") : copy("subagents.observedNativeOutput_37d11c")}</p><code>{text(output.native_message_id)}</code>{text(output.text) ? <pre>{text(output.text)}</pre> : null}{blocks.map((block, index) => <div key={index}><small>{text(block.kind)}</small>{block.text == null ? <p>{copy("subagents.contentUnavailable_e9f250")}</p> : <pre>{text(block.text)}</pre>}</div>)}</>}</td>
        <td>{child.usage == null ? copy("subagents.unavailable_ca1844") : <><p>{text(usage.scope)}</p><p><LocalizedText id="subagents.total_dc9841" components={{ s0: <>{text(usage.total) || "Unavailable"}</> }} /></p><p><LocalizedText id="subagents.input_f51d4a" components={{ s0: <>{text(usage.input) || "Unavailable"}</> }} /></p><p><LocalizedText id="subagents.output_296ead" components={{ s0: <>{text(usage.output) || "Unavailable"}</> }} /></p><p>{copy("subagents.observationOnlyExcludedFromAdditiveBilling_23cc58")}</p></>}</td>
      </tr>;
    })}</tbody>
  </table>;
}

export function Subagents({ sessionId, revision }: { sessionId: string; revision: string }) {
  useLocale();
  const status = useQuery(SystemQuery.getStatus, {});
  const supported = status.data?.capabilities.includes(SystemCapability.SUBAGENT_OBSERVATION_V1) === true;
  const openCodeSupported = status.data?.capabilities.includes(SystemCapability.OPENCODE_FOREGROUND_SUBAGENTS_V1) === true;
  const [page, setPage] = useState("");
  const query = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.SUBAGENT, sessionId, pageSize: 50, pageToken: page } }, { enabled: supported });
  // Native events invalidate only this read. No observation can issue a child
  // input, resume, interruption, retry, or mutation.
  useEffect(() => { if (supported) void query.refetch(); }, [revision, supported, query.refetch]);
  const rows = useMemo(() => query.data ? validateSubagentPage(query.data.resources, sessionId) : undefined, [query.data, sessionId]);
  const needsOpenCodeUpdate = rows?.some(row => row.record.harness === "opencode") === true && !openCodeSupported;
  return <details><summary>{copy("subagents.subagents_88296a")}</summary>
    <p>{copy("subagents.readOnlyNativeHierarchyParentCompletion_036e57")}</p>
    {status.error ? <Problem error={status.error} /> : null}
    {status.data && !supported ? <p>{copy("subagents.thisServerDoesNotSupportChild_0baf88")}</p> : null}
    {query.error ? <Problem error={query.error} /> : null}
    {query.isPending && supported ? <p>{copy("subagents.loadingChildObservations_0a8d53")}</p> : null}
    {supported && query.data && !rows ? unavailable : null}
    {supported && rows?.length === 0 ? <p>{copy("subagents.noNativeChildObservationsAreAvailable_8c2be5")}</p> : null}
    {needsOpenCodeUpdate ? <p>{copy("subagents.updateTheServerAndRunnerDevice_ac66cd")}</p> : supported && rows && rows.length > 0 ? <ChildRows rows={rows} /> : null}
    <button disabled={!supported || query.isFetching} onClick={() => void query.refetch()}>{copy("subagents.refreshSubagents_1ffca1")}</button>
    <button disabled={!page} onClick={() => setPage("")}>{copy("subagents.firstChildPage_6cf629")}</button>
    <button disabled={!supported || needsOpenCodeUpdate || !rows || !query.data?.nextPageToken} onClick={() => setPage(query.data?.nextPageToken ?? "")}>{copy("subagents.nextChildPage_a95c32")}</button>
  </details>;
}
