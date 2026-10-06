// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, SessionQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export function SidechatFindings({ session, messages }: { session: Resource; messages: readonly Resource[] }) {
  const fork = object(document(session).fork);
  const sidechat = Boolean(fork.sidechat_parent_snapshot);
  const parentId = text(fork.source_session_id);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [accepted, setAccepted] = useState<string>();
  const client = useQueryClient();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: sidechat });
  const parent = useQuery(ResourceQuery.getResource, { kind: EntityKind.SESSION, id: parentId }, { enabled: sidechat });
  const mutation = useRetainedMutation(`sidechat-findings:${session.id}`, SessionQuery.sendSidechatFindings, (response) => {
    setSelected(new Set());
    setAccepted(response.change?.input?.id);
    void client.invalidateQueries({ refetchType: "active" });
  }, (response, request) => response.change?.session?.id === request.parentId && response.change.input?.kind === EntityKind.QUEUE);
  if (!sidechat) return null;
  const supported = status.data?.capabilities.includes(SystemCapability.NATIVE_SIDECHAT_V1);
  const eligible = messages.filter((row) => {
    const m = document(row);
    return row.kind === EntityKind.MESSAGE && row.sessionId === session.id && m.role === "assistant" && m.state === "complete" && !m.inherited && !m.tool && !m.artifact && Boolean(text(m.text));
  });
  const chosen = eligible.filter((row) => selected.has(row.id));
  const bytes = chosen.reduce((sum, row) => sum + new TextEncoder().encode(text(document(row).text)).length + 2, 30);
  const blocked = mutation.busy || mutation.uncertain;
  const send = () => {
    const target = parent.data?.resource;
    if (blocked || !supported || !target || target.id !== parentId || target.kind !== EntityKind.SESSION || chosen.length === 0 || chosen.length > 20 || bytes > 256 * 1024) return;
    setAccepted(undefined);
    void mutation.send({ mutation: { id: session.id, expectedRevision: session.revision, requestId: newRequestId() }, parentId, expectedParentRevision: target.revision, messages: chosen.map((row) => ({ messageId: row.id, expectedRevision: row.revision })) });
  };
  return <section className="sidechat-findings" aria-label="Sidechat findings">
    <p>Read-only Sidechat · Parent session {parentId}. Select complete replies on this page to add to the parent queue. Use the parent queue's Steer action explicitly when needed.</p>
    {!supported ? <p role="status">Update the connected server to send Sidechat findings.</p> : <>
      <fieldset disabled={blocked}><legend>Replies to send</legend>{eligible.length ? eligible.map((row, index) => <label key={row.id}><input type="checkbox" checked={selected.has(row.id)} onChange={(event) => setSelected((current) => { const next = new Set(current); if (event.target.checked) next.add(row.id); else next.delete(row.id); return next; })} />Reply {index + 1}<span>{text(document(row).text)}</span></label>) : <p>No complete Sidechat replies on this page.</p>}</fieldset>
      {chosen.length > 20 || bytes > 256 * 1024 ? <p role="alert">Select at most 20 replies within 256 KiB. Replies are sent in full.</p> : null}
      <button disabled={blocked || parent.isFetching || !parent.data?.resource || chosen.length === 0 || chosen.length > 20 || bytes > 256 * 1024} onClick={send}>Send selected findings to parent queue</button>
    </>}
    <Problem error={parent.error} /><Problem error={mutation.error} />{mutation.uncertain ? <><p role="status">The original selected messages and revisions are retained while the result is uncertain.</p><button disabled={mutation.busy} onClick={mutation.retry}>Retry the same findings request</button></> : null}
    {accepted ? <p role="status">Selected findings queued as {accepted}. Review them in the parent queue.</p> : null}
  </section>;
}
