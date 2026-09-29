import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SystemCapability, SystemQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, text } from "./documents";
import { Problem } from "./ui";

export function SubagentRows({ rows }: { rows: readonly Resource[] }) {
  return <table aria-label="Native child-agent hierarchy">
    <thead><tr><th>Child</th><th>Parent</th><th>Status</th><th>Model</th><th>Recent output</th><th>Usage observation</th></tr></thead>
    <tbody>{rows.map((row) => {
      const record = document(row), child = object(record.observation), output = object(child.output), usage = object(child.usage);
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
  const [page, setPage] = useState("");
  const query = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.SUBAGENT, sessionId, pageSize: 50, pageToken: page } }, { enabled: supported });
  // Native events invalidate only this read. No observation can issue a child
  // input, resume, interruption, retry, or mutation.
  useEffect(() => { if (supported) void query.refetch(); }, [revision, supported, query.refetch]);
  const rows = query.data?.resources ?? [];
  return <details><summary>Subagents</summary>
    <p>Read-only native hierarchy. Parent completion does not complete running children. Codex 0.151.0 and Claude 2.1.236 API observations are supported; unavailable telemetry stays explicit.</p>
    {status.error ? <Problem error={status.error} /> : null}
    {status.data && !supported ? <p>This server does not support child-agent observations.</p> : null}
    {query.error ? <Problem error={query.error} /> : null}
    {query.isPending && supported ? <p>Loading child observations…</p> : null}
    {query.data && rows.length === 0 ? <p>No native child observations are available.</p> : null}
    {rows.length ? <SubagentRows rows={rows} /> : null}
    <button disabled={!supported || query.isFetching} onClick={() => void query.refetch()}>Refresh subagents</button>
    <button disabled={!page} onClick={() => setPage("")}>First child page</button>
    <button disabled={!query.data?.nextPageToken} onClick={() => setPage(query.data?.nextPageToken ?? "")}>Next child page</button>
  </details>;
}
