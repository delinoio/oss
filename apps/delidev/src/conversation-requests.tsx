// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import { useTransport } from "@connectrpc/connect-query";
import { type Resource } from "@delinoio/delidev-api-client";
import { type useConversationPages } from "./conversation-pagination";
import { Interaction, QuestionReceiptRecovery } from "./interactions";
import { initialInteractionDraft, interactionRequestIdentity, type InboxInteractionDraft, type InteractionDraftState } from "./inbox-drafts";
import { useRetainedMutationIntents, useRetainedMutationNotifications } from "./mutation";
import { activeRequestRows, projectActiveQuestion, questionPresentation, questionReceiptMatches, type QuestionPresentation } from "./active-question";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, LocalizedText, useLocale } from "./localization";
import { Failure } from "./ui";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { ScrollContinuation } from "./scroll-continuation";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { document as readDocument } from "./documents";

type Pages = ReturnType<typeof useConversationPages>;
const answerKey = (key: string) => /^(?:answer|opencode-answer|claude-answer|grok-answer):/.test(key);
export function ConversationRequests({ sessionId, query, live, removed, arrivals, active, current, composer, drafts, saveDraft }: {
  sessionId: string; query: Pages; live: ReadonlyMap<string, Resource>; removed: ReadonlySet<string>; arrivals: readonly string[];
  active: boolean; current: boolean; composer: RefObject<HTMLTextAreaElement | null>;
  drafts: ReadonlyMap<string, InboxInteractionDraft>; saveDraft: (id: string, draft?: InboxInteractionDraft) => void;
}) {
  useLocale();
  const transport = useTransport();
  const root = useRef<HTMLDivElement>(null), focused = useRef<HTMLElement | null>(null);
  const [open, setOpen] = useState(false);
  // Keep only submission RPC overlays until list/live reads catch up. Revision
  // watermarks retain no question/answer bytes across payload-page eviction.
  const [receiptScope, setReceiptScope] = useState({ transport, sessionId, rows: new Map<string, Resource>() });
  const receipts = receiptScope.transport === transport && receiptScope.sessionId === sessionId ? receiptScope.rows : new Map<string, Resource>();
  const known = useRef(new Map<string, QuestionPresentation>());
  const scope = useRef({ transport, sessionId });
  if (scope.current.transport !== transport || scope.current.sessionId !== sessionId) {
    known.current.clear(); focused.current = null; scope.current = { transport, sessionId };
  }
  const intents = useRetainedMutationIntents("").filter(intent => {
    const id = (intent.input as { mutation?: { id?: string } }).mutation?.id;
    return answerKey(intent.key) && Boolean(id && (known.current.has(id) || query.rows.some(row => row.id === id) || live.get(id)?.sessionId === sessionId));
  });
  useRetainedMutationNotifications((key, request, result) => {
    const row = (result as { interaction?: Resource }).interaction;
    if (!answerKey(key) || !questionReceiptMatches(row, request, sessionId)) return;
    setReceiptScope(previous => {
      const rows = previous.transport === transport && previous.sessionId === sessionId ? previous.rows : new Map<string, Resource>();
      if (rows.get(row.id) && rows.get(row.id)!.revision >= row.revision) return previous;
      return { transport, sessionId, rows: new Map(rows).set(row.id, row) };
    });
  });
  const merged = new Map(live);
  for (const row of receipts.values()) if (!merged.has(row.id) || merged.get(row.id)!.revision < row.revision) merged.set(row.id, row);
  const owner = (id: string) => intents.some(intent => (intent.input as { mutation?: { id?: string } }).mutation?.id === id);
  const project = (payload: readonly Resource[], lastPage = false) => activeRequestRows(payload, merged, removed, arrivals, sessionId, lastPage).filter(row => {
    const presentation = questionPresentation(row), previous = known.current.get(row.id);
    if (presentation && (!previous || presentation.revision >= previous.revision)) {
      const contradictory = previous && ((presentation.revision === previous.revision && (presentation.submitted !== previous.submitted || presentation.retired !== previous.retired || presentation.state !== previous.state || presentation.closure !== previous.closure)) || previous.submitted && !presentation.submitted);
      known.current.set(row.id, contradictory ? { revision: presentation.revision, submitted: true, retired: false, state: "unknown", closure: "unknown" } : presentation);
    }
    return projectActiveQuestion(row, owner(row.id), known.current.get(row.id)).displayed;
  });
  const rows = project(query.data?.resources ?? [], Boolean(query.data && !query.nextPageToken));
  const displayedIds = new Set(rows.map(row => row.id));
  const recoveries = intents.filter(intent => !displayedIds.has((intent.input as { mutation?: { id?: string } }).mutation?.id ?? ""));
  const complete = current && Boolean(query.data) && !query.isPending && !query.error && !query.loading && !query.nextPageToken && query.pages.every(page => query.payloadPages.some(payload => payload.token === page.token));
  const visible = !complete || rows.length > 0 || recoveries.length > 0;
  const expanded = open || rows.some(row => (known.current.get(row.id)?.closure ?? readDocument(row).closure) === "open" && !projectActiveQuestion(row, owner(row.id), known.current.get(row.id)).compact) || recoveries.length > 0;
  useLayoutEffect(() => {
    if (!active || !focused.current || focused.current.isConnected || document.activeElement && document.activeElement !== document.body) return;
    (root.current?.querySelector<HTMLElement>("button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled)") ?? composer.current)?.focus({ preventScroll: true });
    focused.current = null;
  });
  useLayoutEffect(() => {
    setReceiptScope(previous => {
      if (previous.transport !== transport || previous.sessionId !== sessionId) return { transport, sessionId, rows: new Map<string, Resource>() };
      const next = new Map(previous.rows);
      for (const row of [...(query.data?.resources ?? []), ...live.values()]) if (next.has(row.id) && row.revision >= next.get(row.id)!.revision) next.delete(row.id);
      return next.size === previous.rows.size ? previous : { transport, sessionId, rows: next };
    });
  }, [query.data, live, transport, sessionId]);
  const renderRow = (row: Resource) => {
    const draft = drafts.get(row.id) ?? initialInteractionDraft(row);
    return <Interaction key={row.id} resource={row} questionPresentation={known.current.get(row.id)} compactQuestion={projectActiveQuestion(row, owner(row.id), known.current.get(row.id)).compact} refresh={query.refresh} draft={draft} saveDraft={(editable: InteractionDraftState) => { if (draft) saveDraft(row.id, { ...draft, editable }); }} clearDraft={() => saveDraft(row.id)} submissionAllowed={!query.error && (!draft || draft.requestIdentity === interactionRequestIdentity(row))} />;
  };
  if (!visible) return null;
  return <Disclosure className="requests" open={expanded} onToggle={event => setOpen(event.currentTarget.open)}>
    <DisclosureSummary>{query.isPending ? copy("session.loadingRequests") : <LocalizedText id="session.agentRequestsOnThisPage_5e8644" components={{ s0: <>{rows.length}</> }} />}</DisclosureSummary>
    <div ref={root} className="session-tray-content" onFocusCapture={event => { focused.current = event.target as HTMLElement; }} onBlurCapture={event => { if (event.relatedTarget instanceof HTMLElement && !event.currentTarget.contains(event.relatedTarget)) focused.current = null; }}>
      <Failure failure={query.error?.failure} />
      <ScrollPayloadWindow identity={paginationIdentity} revision={paginationRevision} query={query} root={root} active={active && expanded}>{payload => project(payload).map(renderRow)}</ScrollPayloadWindow>
      {!query.nextPageToken ? rows.filter(row => !query.rows.some(known => known.id === row.id)).map(renderRow) : null}
      {recoveries.map(intent => <QuestionReceiptRecovery key={intent.key} intentKey={intent.key} allowed={!query.error} refresh={query.refresh} />)}
      <ScrollContinuation query={query} root={root} active={active && expanded} label={copy("session.requestPages_d06a30")} />
    </div>
  </Disclosure>;
}
