// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SystemCapability, SystemQuery, type Resource } from "@delinoio/delidev-api-client";
import { items, object, text } from "./documents";
import { Problem } from "./ui";
import { validateSubagentPage, type SubagentRow } from "./subagent-record";

const unavailable = <p>The retained child page is unavailable or inconsistent.</p>;
export function SubagentRows({ rows, sessionId }: { rows: readonly Resource[]; sessionId: string }) {
  const accepted = useMemo(() => validateSubagentPage(rows, sessionId), [rows, sessionId]);
  return accepted ? <ChildRows rows={accepted} /> : unavailable;
}

function ChildRows({ rows }: { rows: readonly SubagentRow[] }) {
  return <table aria-label="Native child-agent hierarchy">
    <thead><tr><th>Child</th><th>Parent</th><th>Status</th><th>Model</th><th>Recent output</th><th>Usage observation</th></tr></thead>
    <tbody>{rows.map(({ resource: row, record, child }) => {
      const output = object(child.output), usage = object(child.usage);
      const blocks = items(output.blocks).map(object);
      return <tr key={row.id}>
        <td><code>{text(child.native_id)}</code><details><summary>Source coverage</summary><p>{text(record.harness)} {text(record.native_version)}</p><p>Execution: {text(record.execution_id)}</p><p>Parent tool: {text(child.parent_tool_id) || "Unavailable"}</p><ul>{items(record.sources).map(object).map((source, index) => <li key={index}>{text(source.source)} · {text(source.source_id)} · sequence {String(source.sequence ?? "Unavailable")}{text(object(source.usage).native_report) ? <pre>{text(object(source.usage).native_report)}</pre> : null}</li>)}</ul></details></td>
        <td><code>{text(child.parent_id)}</code>{child.parent_id === record.root_id ? <p>Root session</p> : null}</td>
        <td>{text(child.status) || "Unavailable"}</td>
        <td>Observed: {text(child.observed_model) || "Unavailable"}<p>Requested: {text(child.requested_model) || "Unavailable"}</p></td>
        <td>{child.output == null ? "Unavailable" : <><p>{output.partial === true ? "Partial native output" : "Observed native output"}</p><code>{text(output.native_message_id)}</code>{text(output.text) ? <pre>{text(output.text)}</pre> : null}{blocks.map((block, index) => <div key={index}><small>{text(block.kind)}</small>{block.text == null ? <p>Content unavailable</p> : <pre>{text(block.text)}</pre>}</div>)}</>}</td>
        <td>{child.usage == null ? "Unavailable" : <><p>{text(usage.scope)}</p><p>Total: {text(usage.total) || "Unavailable"}</p><p>Input: {text(usage.input) || "Unavailable"}</p><p>Output: {text(usage.output) || "Unavailable"}</p><p>Observation only; excluded from additive billing totals.</p></>}</td>
      </tr>;
    })}</tbody>
  </table>;
}

export function Subagents({ sessionId, revision }: { sessionId: string; revision: string }) {
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
  return <details><summary>Subagents</summary>
    <p>Read-only native hierarchy. Parent completion does not complete running children. Codex 0.151.0, Claude 2.1.236 and OpenCode 1.18.32 API observations are supported; unavailable telemetry stays explicit.</p>
    {status.error ? <Problem error={status.error} /> : null}
    {status.data && !supported ? <p>This server does not support child-agent observations.</p> : null}
    {query.error ? <Problem error={query.error} /> : null}
    {query.isPending && supported ? <p>Loading child observations…</p> : null}
    {supported && query.data && !rows ? unavailable : null}
    {supported && rows?.length === 0 ? <p>No native child observations are available.</p> : null}
    {needsOpenCodeUpdate ? <p>Update the server and Runner Device to view foreground child observations.</p> : supported && rows && rows.length > 0 ? <ChildRows rows={rows} /> : null}
    <button disabled={!supported || query.isFetching} onClick={() => void query.refetch()}>Refresh subagents</button>
    <button disabled={!page} onClick={() => setPage("")}>First child page</button>
    <button disabled={!supported || needsOpenCodeUpdate || !rows || !query.data?.nextPageToken} onClick={() => setPage(query.data?.nextPageToken ?? "")}>Next child page</button>
  </details>;
}
