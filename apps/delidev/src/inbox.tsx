import { useEffect, useRef, useState } from "react";
import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { FailureCode, InboxQuery, InboxReadState, InboxSource, clientFailure, isEntityId, newRequestId, type InboxView, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { Interaction } from "./interactions";
import { currentInboxSource, inboxResponseCurrent } from "./inbox-source";
import { draftByteLength, initialInteractionDraft, interactionRequestIdentity, isEmptyInteractionDraft, type InboxInteractionDraft, type InteractionDraftState } from "./inbox-drafts";

enum DetailReadState { Loading = "loading", Ready = "ready", Stale = "stale", Unavailable = "unavailable" }
interface DetailRead { id: string; state: DetailReadState; view?: InboxView; error?: unknown }
interface DraftCollection { values: ReadonlyMap<string, InboxInteractionDraft>; errors: ReadonlyMap<string, string> }

function itemLabel(view: InboxView): string {
  const entry = view.entry;
  if (!entry) return "Inbox item unavailable";
  const source = document(entry).source;
  if (source === "interaction" && view.interaction) {
    const data = document(view.interaction);
    const type = text(data.type);
    if (type === "user-question") return "Agent question";
    if (type === "native-approval") return "Native approval";
    return "Request unavailable";
  }
  if (source === "execution-terminal") {
    const outcome = text(object(document(entry).terminal).outcome);
    if (outcome === "succeeded") return "Execution succeeded";
    if (outcome === "failed") return "Execution failed";
    if (outcome === "stopped") return "Execution stopped";
  }
  return "Inbox item unavailable";
}

function recordedTime(resource: Resource): { label: string; machineValue?: string } {
  const raw = resource.createdAt;
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/i.test(raw) || !Number.isFinite(Date.parse(raw))) return { label: "Recorded time unavailable" };
  return { label: new Date(raw).toLocaleString(), machineValue: raw };
}

function itemIcon(view: InboxView): { symbol: string; tone: string } {
  const entry = view.entry;
  if (!entry) return { symbol: "?", tone: "unknown" };
  const data = document(entry);
  if (data.source === "interaction") {
    const type = text(document(view.interaction).type);
    if (type === "user-question") return { symbol: "?", tone: "question" };
    if (type === "native-approval") return { symbol: "◇", tone: "approval" };
    return { symbol: "?", tone: "unknown" };
  }
  if (data.source === "execution-terminal") {
    const outcome = text(object(data.terminal).outcome);
    if (outcome === "succeeded") return { symbol: "✓", tone: "success" };
    if (outcome === "failed") return { symbol: "×", tone: "failure" };
    if (outcome === "stopped") return { symbol: "Ⅱ", tone: "stopped" };
  }
  return { symbol: "?", tone: "unknown" };
}

function statusText(value: unknown): string {
  const state = text(value);
  if (!state) return "Not submitted";
  if (["queued", "claimed", "transmitted", "uncertain", "accepted", "canceled"].includes(state)) return state[0]!.toUpperCase() + state.slice(1);
  return "Unavailable";
}

function closureText(value: unknown): string {
  const state = text(value);
  if (state === "open") return "Open";
  if (state === "native-closed") return "Closed";
  if (state === "turn-ended") return "Turn ended";
  return "Unavailable";
}

export function Inbox({ active, open, notificationId = "", notificationActivation = 0 }: { active: boolean; open: (sessionId: string) => void; notificationId?: string; notificationActivation?: number }) {
  const [source, setSource] = useState(InboxSource.UNSPECIFIED);
  const [readState, setReadState] = useState(InboxReadState.UNSPECIFIED);
  const [page, setPage] = useState("");
  const listHeading = useRef<HTMLHeadingElement>(null);
  const listScroller = useRef<HTMLDivElement>(null);
  const lastRowFocus = useRef("");
  const restoreRowFocus = useRef(false);
  const detailHeading = useRef<HTMLHeadingElement>(null);
  const detailPane = useRef<HTMLElement>(null);
  const keyboardSelection = useRef(false);
  const readGeneration = useRef(0);
  const lastNotificationActivation = useRef(0);
  const [selectedId, setSelectedId] = useState("");
  const [readTrigger, setReadTrigger] = useState(0);
  const [detailRead, setDetailRead] = useState<DetailRead>();
  const [draftCollection, setDraftCollection] = useState<DraftCollection>({ values: new Map(), errors: new Map() });
  const transport = useTransport();
  const queryClient = useQueryClient();
  const list = useQuery(InboxQuery.listInbox, { source, readState, pageSize: 20, pageToken: page }, { enabled: active, refetchInterval: active ? 5000 : false, refetchIntervalInBackground: false, retry: false });
  const selected = useQuery(InboxQuery.getInboxEntry, { id: selectedId }, { enabled: false, retry: false, gcTime: 5 * 60 * 1000 });

  const reloadList = () => {
    if (page) setPage("");
    else void list.refetch();
  };
  const refresh = () => {
    reloadList();
    if (selectedId) setReadTrigger((value) => value + 1);
  };
  const selectItem = (id: string, keyboard = false) => {
    if (!id) return;
    lastRowFocus.current = id;
    restoreRowFocus.current = false;
    keyboardSelection.current = keyboard || (typeof window.matchMedia === "function" && window.matchMedia("(max-width: 1119px)").matches);
    setSelectedId(id);
    setReadTrigger((value) => value + 1);
  };

  useEffect(() => {
    if (!notificationId || notificationActivation === lastNotificationActivation.current) return;
    lastNotificationActivation.current = notificationActivation;
    lastRowFocus.current = notificationId;
    restoreRowFocus.current = false;
    keyboardSelection.current = typeof window.matchMedia === "function" && window.matchMedia("(max-width: 1119px)").matches;
    setSelectedId(notificationId);
    setReadTrigger((value) => value + 1);
  }, [notificationId, notificationActivation]);

  useEffect(() => {
    if (page && list.error && clientFailure(list.error).code === FailureCode.CursorExpired) setPage("");
  }, [list.error, page]);

  useEffect(() => {
    if (!active || !selectedId) return;
    const timer = window.setInterval(() => {
      if (active && selectedId && documentVisibilityVisible()) setReadTrigger((value) => value + 1);
    }, 5000);
    return () => window.clearInterval(timer);
  }, [active, selectedId]);

  useEffect(() => {
    if (!active || !selectedId) return;
    const refreshOnReturn = () => { if (documentVisibilityVisible()) setReadTrigger((value) => value + 1); };
    window.addEventListener("focus", refreshOnReturn);
    window.document.addEventListener("visibilitychange", refreshOnReturn);
    return () => { window.removeEventListener("focus", refreshOnReturn); window.document.removeEventListener("visibilitychange", refreshOnReturn); };
  }, [active, selectedId]);

  useEffect(() => {
    if (!selectedId) {
      setDetailRead(undefined);
      if (restoreRowFocus.current) {
        restoreRowFocus.current = false;
        const row = [...(listScroller.current?.querySelectorAll<HTMLButtonElement>("[data-inbox-id]") ?? [])].find((button) => button.dataset.inboxId === lastRowFocus.current);
        (row ?? listHeading.current)?.focus();
      }
      return;
    }
    if (!active) return;
    if (!isEntityId(selectedId)) {
      setDetailRead({ id: selectedId, state: DetailReadState.Unavailable });
      return;
    }
    const generation = ++readGeneration.current;
    let canceled = false;
    const priorView = detailRead?.id === selectedId ? detailRead.view : undefined;
    setDetailRead({ id: selectedId, state: DetailReadState.Loading, view: priorView });
    const queryKey = createQueryOptions(InboxQuery.getInboxEntry, { id: selectedId }, { transport }).queryKey;
    void queryClient.cancelQueries({ queryKey, exact: true }).then(() => {
      if (canceled || generation !== readGeneration.current) return undefined;
      return selected.refetch({ cancelRefetch: true });
    }).then((result) => {
      if (!result) return;
      if (canceled || generation !== readGeneration.current) return;
      const error = result.error;
      if (error) {
        const code = clientFailure(error).code;
        const inaccessible = [FailureCode.NotFound, FailureCode.PermissionDenied, FailureCode.Unauthenticated].includes(code);
        setDetailRead({ id: selectedId, state: inaccessible ? DetailReadState.Unavailable : DetailReadState.Stale, view: inaccessible ? undefined : priorView, error });
        return;
      }
      const view = result.data?.view;
      if (!view || !currentInboxSource(view, selectedId)) {
        setDetailRead({ id: selectedId, state: DetailReadState.Unavailable });
        return;
      }
      setDetailRead({ id: selectedId, state: DetailReadState.Ready, view });
    }).catch((error: unknown) => {
      if (!canceled && generation === readGeneration.current) setDetailRead({ id: selectedId, state: DetailReadState.Stale, view: priorView, error });
    });
    return () => { canceled = true; };
  }, [active, selectedId, readTrigger, queryClient, selected.refetch, transport]);

  useEffect(() => {
    if (!selectedId || !keyboardSelection.current || !detailRead?.view || detailRead.state !== DetailReadState.Ready) return;
    keyboardSelection.current = false;
    const activeElement = window.document.activeElement;
    if (activeElement && detailPane.current?.contains(activeElement) && activeElement !== detailHeading.current) return;
    detailHeading.current?.focus();
  }, [detailRead, selectedId]);

  const entries = list.data?.entries ?? [];
  const selectedView = detailRead?.id === selectedId ? detailRead.view : undefined;
  const latestReadReady = detailRead?.id === selectedId && detailRead.state === DetailReadState.Ready && Boolean(selectedView && currentInboxSource(selectedView, selectedId));
  const drafts = draftCollection.values;
  const saveDraft = (resource: Resource, editable: InteractionDraftState) => {
    setDraftCollection((current) => {
      const values = new Map(current.values), errors = new Map(current.errors);
      const base = values.get(resource.id) ?? initialInteractionDraft(resource);
      if (!base) return current;
      const next = { ...base, editable };
      if (isEmptyInteractionDraft(editable)) values.delete(resource.id); else values.set(resource.id, next);
      const total = [...values.values()].reduce((bytes, item) => bytes + draftByteLength(item), 0);
      if (values.size > 1000 || total > 4 * 1024 * 1024) {
        errors.set(resource.id, values.size > 1000 ? "The Inbox draft limit is 1,000 nonempty requests. Clear or submit a draft before changing this one." : "The Inbox draft limit is 4 MiB per connection. Shorten or clear a draft before changing this one.");
        return { values: current.values, errors };
      }
      errors.delete(resource.id);
      return { values, errors };
    });
  };
  const clearDraft = (resourceId: string) => setDraftCollection((current) => {
    const values = new Map(current.values), errors = new Map(current.errors);
    values.delete(resourceId); errors.delete(resourceId);
    return { values, errors };
  });

  const closeDetail = () => {
    restoreRowFocus.current = true;
    setSelectedId("");
    setDetailRead(undefined);
  };

  const filterChanged = (nextSource: InboxSource, nextReadState: InboxReadState) => {
    setSource(nextSource); setReadState(nextReadState); setPage("");
  };

  const sourceLabel = (value: InboxSource) => value === InboxSource.INTERACTION ? "Requests" : value === InboxSource.EXECUTION_TERMINAL ? "Results" : "All";
  const readLabel = (value: InboxReadState) => value === InboxReadState.UNREAD ? "Unread" : value === InboxReadState.READ ? "Read" : "All";

  return <section className={`inbox ${selectedId ? "has-selection" : ""}`} aria-label="Inbox workspace">
    <header className="inbox-header"><div><h2>Inbox</h2><p>Requests and execution results</p></div><button onClick={refresh} disabled={!active || list.isFetching}>Refresh</button></header>
    <div className="inbox-filters" aria-label="Inbox filters">
      <label>Source<select value={source} onChange={(event) => filterChanged(Number(event.target.value) as InboxSource, readState)}><option value={InboxSource.UNSPECIFIED}>All</option><option value={InboxSource.INTERACTION}>Requests</option><option value={InboxSource.EXECUTION_TERMINAL}>Results</option></select></label>
      <label>Read state<select value={readState} onChange={(event) => filterChanged(source, Number(event.target.value) as InboxReadState)}><option value={InboxReadState.UNSPECIFIED}>All</option><option value={InboxReadState.UNREAD}>Unread</option><option value={InboxReadState.READ}>Read</option></select></label>
    </div>
    <div className="inbox-workspace">
      <section className="inbox-list-pane" aria-label="Inbox items">
        <h3 ref={listHeading} tabIndex={-1} className="inbox-list-heading">Items</h3>
        <div className="inbox-list-scroll" ref={listScroller} aria-busy={list.isPending || list.isFetching}>
          <Problem error={list.error} />
          {list.isPending && !list.data ? <div className="inbox-skeleton" role="status" aria-label="Loading inbox"><span /><span /><span /></div> : null}
          {entries.length > 0 ? <ul className="inbox-items">{entries.map((view, index) => {
            const entry = view.entry;
            if (!entry) return <li className="inbox-item-unavailable" key={`unavailable-${index}`}>Inbox item unavailable</li>;
            const data = document(entry), state = text(data.read_state);
            const time = recordedTime(entry);
            const kind = itemLabel(view);
            const icon = itemIcon(view);
            return <li key={entry.id}><button className="inbox-row" type="button" data-inbox-id={entry.id} aria-current={selectedId === entry.id ? "true" : undefined} aria-label={`${kind}, ${resourceName(view.session)}, ${state === "read" ? "Read" : state === "unread" ? "Unread" : "Read state unavailable"}`} onClick={(event) => selectItem(entry.id, event.detail === 0)}>
              <span className={`inbox-kind-icon is-${icon.tone}`} aria-hidden="true">{icon.symbol}</span>
              <span className="inbox-row-copy"><strong>{resourceName(view.session)}</strong><span>{kind}</span><small>{time.label}</small></span>
              <span className={`inbox-read-label ${state === "unread" ? "is-unread" : ""}`}>{state === "unread" ? <><span className="inbox-unread-dot" aria-hidden="true" />Unread</> : state === "read" ? "Read" : "Unavailable"}</span>
            </button></li>;
          })}</ul> : null}
          {!list.isPending && !list.error && entries.length === 0 ? source === InboxSource.UNSPECIFIED && readState === InboxReadState.UNSPECIFIED
            ? <p className="inbox-empty">No retained requests or execution results.</p>
            : <div className="inbox-empty"><p>No items match these filters.</p><button onClick={() => filterChanged(InboxSource.UNSPECIFIED, InboxReadState.UNSPECIFIED)}>Reset filters</button></div> : null}
        </div>
        <nav className="inbox-pager" aria-label="Inbox pages"><button disabled={!page || list.isFetching} onClick={() => setPage("")}>First page</button><button disabled={!list.data?.nextPageToken || list.isFetching} onClick={() => setPage(list.data!.nextPageToken)}>Next page</button></nav>
      </section>
      <section className="inbox-detail-pane" aria-label="Selected inbox item" ref={detailPane}>
        {!selectedId ? <div className="inbox-no-selection"><h3>Select an item to view its request or result.</h3></div> : <>
          <div className="inbox-detail-top"><button className="inbox-back" onClick={closeDetail}>Back to inbox</button><h3 ref={detailHeading} tabIndex={-1}>{selectedView ? itemLabel(selectedView) : "Selected inbox item"}</h3></div>
          {detailRead?.id === selectedId && detailRead.state === DetailReadState.Loading ? <p role="status" className="inbox-read-progress">Loading the current item…</p> : null}
          {detailRead?.id === selectedId && detailRead.state === DetailReadState.Stale ? <div className="inbox-stale-warning"><p>The latest read failed. This retained view is read-only until the current source is verified again.</p><Problem error={detailRead.error} /><button onClick={() => setReadTrigger((value) => value + 1)}>Retry current read</button></div> : null}
          {detailRead?.id === selectedId && detailRead.state === DetailReadState.Unavailable ? <div className="inbox-unavailable"><p>This item is unavailable. Its current source could not be verified for this connection.</p><Problem error={detailRead.error} /></div> : null}
          {selectedView && detailRead?.state !== DetailReadState.Unavailable ? <InboxDetail view={selectedView} readOnly={!latestReadReady} draft={selectedView.interaction ? drafts.get(selectedView.interaction.id) ?? initialInteractionDraft(selectedView.interaction) : undefined} draftError={selectedView.interaction ? draftCollection.errors.get(selectedView.interaction.id) : undefined} saveDraft={saveDraft} clearDraft={clearDraft} open={open} refresh={refresh} pageContains={entries.some((entry) => entry.entry?.id === selectedId)} /> : null}
          {!selectedView && detailRead?.state !== DetailReadState.Unavailable && detailRead?.state !== DetailReadState.Stale ? <div className="inbox-skeleton" role="status" aria-label="Loading selected item"><span /><span /></div> : null}
        </>}
      </section>
    </div>
  </section>;
}

function InboxDetail({ view, readOnly, draft, draftError, saveDraft, clearDraft, open, refresh, pageContains }: { view: InboxView; readOnly: boolean; draft?: InboxInteractionDraft; draftError?: string; saveDraft: (resource: Resource, editable: InteractionDraftState) => void; clearDraft: (resourceId: string) => void; open: (sessionId: string) => void; refresh: () => void; pageContains: boolean }) {
  const entry = view.entry!;
  const data = document(entry);
  const state = text(data.read_state);
  const time = recordedTime(entry);
  const currentSource = currentInboxSource(view, entry.id);
  const responseCurrent = view.interaction ? inboxResponseCurrent(view) : false;
  const identityChanged = Boolean(view.interaction && draft && (draft.interactionId !== view.interaction.id || draft.interactionRevision !== view.interaction.revision.toString() || draft.requestIdentity !== interactionRequestIdentity(view.interaction)));
  const canMutate = !readOnly && currentSource;
  const readMutation = useRetainedMutation(`inbox-read:${entry.id}`, InboxQuery.setInboxReadState, refresh);
  const mark = state === "read" ? InboxReadState.UNREAD : InboxReadState.READ;
  const terminal = object(data.terminal);
  return <article className="inbox-detail-content">
    {!pageContains ? <p className="inbox-outside-list">This item is outside the current list.</p> : null}
    <h4>{resourceName(view.session)}</h4>
    <div className="inbox-detail-meta"><span className={`inbox-state-badge ${state === "unread" ? "is-unread" : ""}`}>{state === "read" ? "Read" : state === "unread" ? "Unread" : "Read state unavailable"}</span><span>Recorded <time dateTime={time.machineValue}>{time.label}</time></span></div>
    <div className="actions inbox-detail-actions"><button onClick={() => open(entry.sessionId)} disabled={!entry.sessionId}>Open session</button>{state === "read" || state === "unread" ? <button disabled={!canMutate || readMutation.busy || readMutation.uncertain} onClick={() => void readMutation.send({ mutation: { requestId: newRequestId(), id: entry.id, expectedRevision: entry.revision }, readState: mark })}>{state === "read" ? "Mark unread" : "Mark read"}</button> : null}</div>
    <Problem error={readMutation.error} />{readMutation.uncertain ? <button disabled={!canMutate || readMutation.busy} onClick={readMutation.retry}>Retry the same read-state change</button> : null}
    {draftError ? <p className="inbox-draft-limit" role="alert">{draftError}</p> : null}
    {identityChanged ? <p className="inbox-draft-stale" role="status">The original request changed. This response draft is preserved for inspection and cannot be submitted against the new request.</p> : null}
    {view.interaction ? <>
      <section className="inbox-original-request"><h4>Original request</h4><p>Request status: {closureText(document(view.interaction).closure)}</p><p>Response status: {statusText(object(document(view.interaction).response ?? document(view.interaction).approval_response).state)}</p>
        {responseCurrent ? null : <p>This request is retained for inspection. Its session is paused, archived, recovering or no longer owns this execution.</p>}
        <Interaction resource={view.interaction} refresh={refresh} draft={draft} saveDraft={(editable) => saveDraft(view.interaction!, editable)} clearDraft={() => clearDraft(view.interaction!.id)} submissionAllowed={canMutate && responseCurrent && !identityChanged} receiptRetryAllowed={canMutate} />
      </section>
    </> : <section className="inbox-terminal"><h4>Original terminal observation</h4><p>{itemLabel(view)}</p><pre>{JSON.stringify(terminal, null, 2)}</pre><p>Recorded time is the Inbox record time. It does not establish process cleanup or the current session outcome.</p></section>}
  </article>;
}

function documentVisibilityVisible(): boolean { return typeof globalThis.document !== "undefined" && globalThis.document.visibilityState === "visible"; }
