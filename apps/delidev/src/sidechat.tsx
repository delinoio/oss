import { LocalizedText, copy, useLocale } from "./localization";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, SessionQuery, SystemCapability, SystemQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export function SidechatFindings({ session, messages }: { session: Resource; messages: readonly Resource[] }) {
  useLocale();
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
  return <section className="sidechat-findings" aria-label={copy("sidechat.sidechatFindings_b85000")}>
    <p><LocalizedText id="sidechat.readOnlySidechatParentSessionSelect_ceb705" components={{ s0: <>{parentId}</> }} /></p>
    {!supported ? <p role="status">{copy("sidechat.updateTheConnectedServerToSend_9dd279")}</p> : <>
      <fieldset disabled={blocked}><legend>{copy("sidechat.repliesToSend_a20827")}</legend>{eligible.length ? eligible.map((row, index) => <label key={row.id}><input type="checkbox" checked={selected.has(row.id)} onChange={(event) => setSelected((current) => { const next = new Set(current); if (event.target.checked) next.add(row.id); else next.delete(row.id); return next; })} /><LocalizedText id="sidechat.reply_b9b235" components={{ s0: <>{index + 1}</>, s1: <span>{text(document(row).text)}</span> }} /></label>) : <p>{copy("sidechat.noCompleteSidechatRepliesOnThis_b35106")}</p>}</fieldset>
      {chosen.length > 20 || bytes > 256 * 1024 ? <p role="alert">{copy("sidechat.selectAtMost20RepliesWithin_7b1bc4")}</p> : null}
      <button disabled={blocked || parent.isFetching || !parent.data?.resource || chosen.length === 0 || chosen.length > 20 || bytes > 256 * 1024} onClick={send}>{copy("sidechat.sendSelectedFindingsToParentQueue_5db660")}</button>
    </>}
    <Problem error={parent.error} /><Problem error={mutation.error} />{mutation.uncertain ? <><p role="status">{copy("sidechat.theOriginalSelectedMessagesAndRevisions_45372d")}</p><button disabled={mutation.busy} onClick={mutation.retry}>{copy("sidechat.retryTheSameFindingsRequest_23e1d3")}</button></> : null}
    {accepted ? <p role="status"><LocalizedText id="sidechat.selectedFindingsQueuedAsReviewThem_7c06ec" components={{ s0: <>{accepted}</> }} /></p> : null}
  </section>;
}
