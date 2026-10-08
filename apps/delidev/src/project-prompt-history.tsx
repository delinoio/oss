// SPDX-License-Identifier: Apache-2.0
import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { useInfiniteQuery, useTransport } from "@connectrpc/connect-query";
import { ConfigurationQuery, newRequestId, type ProjectPromptHistoryEntry } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { copy, useLocale } from "./localization";
import { Modal, Problem } from "./ui";

export interface PromptDraft { text: string; start: number; end: number }
export class PromptHistoryNavigation {
  private entries?: readonly string[];
  private draft?: PromptDraft;
  private index = -1;
  get active() { return this.entries !== undefined; }
  reset() { this.entries = undefined; this.draft = undefined; this.index = -1; }
  move(direction: "older" | "newer", entries: readonly string[], draft: PromptDraft): PromptDraft | undefined {
    if (!this.entries) {
      if (direction === "newer" || !entries.length) return;
      this.entries = [...entries]; this.draft = { ...draft };
    }
    const next = this.index + (direction === "older" ? 1 : -1);
    if (next >= this.entries.length) return;
    if (next < 0) { const restored = this.draft; this.reset(); return restored; }
    this.index = next;
    const text = this.entries[next]!;
    const position = direction === "older" ? 0 : text.length;
    return { text, start: position, end: position };
  }
}
function validEntries(entries: readonly ProjectPromptHistoryEntry[], projectId: string): boolean {
  const ids = new Set<string>(); let previous: bigint | undefined;
  return entries.length <= 100 && entries.every(entry => {
    const valid = entry.projectId === projectId && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(entry.id) && !ids.has(entry.id) && entry.acceptanceSequence > 0n && (previous === undefined || entry.acceptanceSequence < previous) && Number.isFinite(Date.parse(entry.acceptedAt)) && Boolean(entry.prompt.trim()) && !entry.prompt.includes("\0") && new TextEncoder().encode(entry.prompt).byteLength <= (256 << 10);
    ids.add(entry.id); previous = entry.acceptanceSequence; return valid;
  });
}
export function useProjectPromptHistory({ projectId, enabled, active, blocked, textarea, replace }: { projectId: string; enabled: boolean; active: boolean; blocked: boolean; textarea: RefObject<HTMLTextAreaElement | null>; replace: (text: string, caret: number) => void }) {
  useLocale(); const transport = useTransport();
  const navigation = useRef(new PromptHistoryNavigation()); const composing = useRef(false);
  const scope = useRef({ projectId, transport });
  const current = useRef({ projectId, transport, active, blocked, enabled }); current.current = { projectId, transport, active, blocked, enabled };
  const [confirm, setConfirm] = useState(false);
  const query = useInfiniteQuery(ConfigurationQuery.listProjectPromptHistory, { projectId, pageSize: 100, pageToken: "" }, {
    enabled: enabled && active && !!projectId, retry: false, gcTime: 0, staleTime: 0, refetchInterval: 30000,
    pageParamKey: "pageToken", getNextPageParam: page => page.nextPageToken || undefined,
  });
  const sameScope = scope.current.projectId === projectId && scope.current.transport === transport;
  useLayoutEffect(() => { scope.current = { projectId, transport }; navigation.current.reset(); composing.current = false; setConfirm(false); }, [projectId, transport]);
  useEffect(() => { if (enabled && active && query.hasNextPage && !query.isFetching && !query.error) void query.fetchNextPage(); }, [enabled, active, query.hasNextPage, query.isFetching, query.error, query.fetchNextPage]);
  const entries = query.data?.pages.flatMap(page => page.entries) ?? [];
  const valid = validEntries(entries, projectId);
  const ready = sameScope && enabled && !!query.data && !query.hasNextPage && !query.error && valid;
  const clear = useRetainedMutation(`project-prompt-history-clear:${projectId}`, ConfigurationQuery.clearProjectPromptHistory, (_result, request) => {
    if (current.current.projectId !== request.projectId || current.current.transport !== transport) return;
    navigation.current.reset(); setConfirm(false); void query.refetch();
  }, (result, request) => result.projectId === request.projectId && result.requestId === request.requestId && result.removedCount <= 100);
  const editable = () => current.current.active && current.current.enabled && !current.current.blocked && !clear.busy && !clear.uncertain && sameScope && !!textarea.current?.isConnected && !textarea.current.matches(":disabled") && !textarea.current.closest("[hidden], [inert]");
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (!editable() || !ready || composing.current || event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229 || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || event.getModifierState("AltGraph") || (event.key !== "ArrowUp" && event.key !== "ArrowDown")) return false;
    const field = event.currentTarget;
    if (field.selectionStart !== field.selectionEnd || (event.key === "ArrowUp" ? field.selectionStart !== 0 : field.selectionEnd !== field.value.length)) return false;
    const next = navigation.current.move(event.key === "ArrowUp" ? "older" : "newer", entries.map(entry => entry.prompt), { text: field.value, start: field.selectionStart, end: field.selectionEnd });
    if (!next) return false;
    event.preventDefault(); replace(next.text, next.start);
    if (!navigation.current.active) void query.refetch();
    const original = { projectId, transport };
    queueMicrotask(() => {
      if (current.current.projectId !== original.projectId || current.current.transport !== original.transport || !editable()) return;
      textarea.current?.setSelectionRange(next.start, next.end);
    });
    return true;
  };
  const feedback = enabled && projectId ? <>
    <p id="project-prompt-history-guidance">{copy("new-session.promptHistoryGuidance")}</p>
    {query.isFetching ? <p role="status">{copy("new-session.promptHistoryLoading")}</p> : query.error || !valid ? <p role="status">{copy("new-session.promptHistoryUnavailable")}</p> : null}
    <button type="button" disabled={!active || blocked || clear.busy} onClick={() => { if (editable()) setConfirm(true); }}>{copy("new-session.clearPromptHistory")}</button>
    {clear.error ? <Problem error={clear.error} /> : null}
    {clear.uncertain ? <button type="button" disabled={!active || clear.busy} onClick={() => void clear.retry()}>{copy("new-session.retryPromptHistoryClear")}</button> : null}
    {confirm && active ? <Modal title={copy("new-session.clearPromptHistory")} close={() => setConfirm(false)} focusClose>
      <p>{copy("new-session.confirmPromptHistoryClear")}</p>
      <button type="button" disabled={blocked || clear.busy || clear.uncertain} onClick={() => { if (editable()) void clear.send({ projectId, requestId: newRequestId(), confirmed: true }); }}>{copy("new-session.clearPromptHistory")}</button>
    </Modal> : null}
  </> : null;
  return { onKeyDown, onEdit: () => { const recalled = navigation.current.active; navigation.current.reset(); if (recalled && enabled && active && projectId) void query.refetch(); }, onCompositionStart: () => { composing.current = true; }, onCompositionEnd: () => { composing.current = false; }, feedback };
}
