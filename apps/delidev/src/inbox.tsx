import { statusLabel } from "./product-status";
import { formatTimestamp } from "./localization";
import { LocalizedText, copy, displayLocale, useLocale } from "./localization";
import { useEffect, useRef, useState } from "react";
import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, FailureCode, InboxQuery, InboxReadState, InboxSource, clientFailure, isEntityId, newRequestId, type InboxView, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { Interaction } from "./interactions";
import { currentInboxSource, inboxResponseCurrent } from "./inbox-source";
import { draftByteLength, initialInteractionDraft, interactionRequestIdentity, isEmptyInteractionDraft, type InboxInteractionDraft, type InteractionDraftState } from "./inbox-drafts";
import { ResourceChoice } from "./configuration-fields";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";

enum DetailReadState { Loading = "loading", Ready = "ready", Stale = "stale", Unavailable = "unavailable" }
interface DetailRead { id: string; state: DetailReadState; view?: InboxView; error?: unknown }
interface DraftCollection { values: ReadonlyMap<string, InboxInteractionDraft>; errors: ReadonlyMap<string, string> }

function itemLabel(view: InboxView): string {
  const entry = view.entry;
  if (!entry) return copy("inbox.extra.3414410d9a33");
  const source = document(entry).source;
  if (source === "interaction" && view.interaction) {
    const data = document(view.interaction);
    const type = text(data.type);
    if (type === "user-question") return copy("inbox.extra.1a6b3f08b7cf");
    if (type === "native-approval") return copy("inbox.extra.c515b98726fe");
    return copy("inbox.extra.6e39d7d8300f");
  }
  if (source==="subscription-recovery") return copy("inbox.extra.41bb49877013");
 if (source === "execution-terminal") {
    const outcome = text(object(document(entry).terminal).outcome);
    if (outcome === "succeeded") return copy("inbox.extra.c05259db17d5");
    if (outcome === "failed") return copy("inbox.extra.19e5e642387b");
    if (outcome === "stopped") return copy("inbox.extra.963e6e93605c");
  }
  return copy("inbox.extra.3414410d9a33");
}

function recordedTime(resource: Resource): { label: string; machineValue?: string } {
  const raw = resource.createdAt;
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/i.test(raw) || !Number.isFinite(Date.parse(raw))) return { label: copy("inbox.extra.b0e8642e6377") };
  return { label: new Date(raw).toLocaleString(displayLocale()), machineValue: raw };
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
  if (data.source==="subscription-recovery") return {symbol:"✓",tone:"success"};
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
  if (!state) return copy("inbox.extra.d3289e625281");
  if (["queued", "claimed", "transmitted", "uncertain", "accepted", "canceled"].includes(state)) return statusLabel(state[0]!.toUpperCase() + state.slice(1));
  return copy("inbox.extra.ca1844969742");
}

function closureText(value: unknown): string {
  const state = text(value);
  if (state === "open") return copy("inbox.extra.ed077f3d8125");
  if (state === "native-closed") return copy("inbox.extra.c21ead0614e7");
  if (state === "turn-ended") return copy("inbox.extra.ce8389f3b06b");
  return copy("inbox.extra.ca1844969742");
}

export function Inbox({ active, open, notificationId = "", notificationActivation = 0 }: { active: boolean; open: (sessionId: string) => void; notificationId?: string; notificationActivation?: number }) {
  useLocale();
  const emptyFilters = { source: InboxSource.UNSPECIFIED, readState: InboxReadState.UNSPECIFIED, projectId: "", sessionId: "" };
  const [draftFilters, setDraftFilters] = useState(emptyFilters);
  const [filters, setFilters] = useState(emptyFilters);
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
  const closeDrawer = useCloseSidebarDrawer();
  const list = useQuery(InboxQuery.listInbox, { ...filters, pageSize: 20, pageToken: page }, { enabled: active, refetchInterval: active ? 5000 : false, refetchIntervalInBackground: false, retry: false });
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
        errors.set(resource.id, values.size > 1000 ? copy("inbox.extra.7b8f9efcb9a9") : copy("inbox.extra.c3dba8527625"));
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

  const applyFilters = (next = draftFilters) => { setFilters(next); setPage(""); closeDrawer(); };

  return <>
    <SidebarSurface active={active} title={copy("inbox.inbox_94835e")}>
      <div className="sidebar-filter-options" aria-label={copy("inbox.inboxReadState_b2b35f")}>
        {([[InboxReadState.UNSPECIFIED, copy("inbox.extra.51107686754a")], [InboxReadState.UNREAD, copy("inbox.extra.1b9f384c1436")], [InboxReadState.READ, copy("inbox.extra.9b9a8d05a7ec")]] as const).map(([value, label]) => <button key={value} type="button" aria-pressed={draftFilters.readState === value} onClick={() => setDraftFilters((current) => ({ ...current, readState: value }))}>{label}</button>)}
      </div>
      <label>{copy("inbox.source_0e570c")}<select value={draftFilters.source} onChange={(event) => setDraftFilters((current) => ({ ...current, source: Number(event.target.value) as InboxSource }))}><option value={InboxSource.UNSPECIFIED}>{copy("inbox.allSources_08e774")}</option><option value={InboxSource.INTERACTION}>{copy("inbox.requests_ada275")}</option><option value={InboxSource.SUBSCRIPTION_RECOVERY}>{copy("inbox.quotaRecovery_26ec8e")}</option><option value={InboxSource.EXECUTION_TERMINAL}>{copy("inbox.executionResults_d2adcc")}</option></select></label>
      <ResourceChoice label={copy("inbox.project_985959")} kind={EntityKind.PROJECT} value={draftFilters.projectId} change={(projectId) => setDraftFilters((current) => ({ ...current, projectId }))} active={active} />
      <ResourceChoice label={copy("inbox.session_6959b4")} kind={EntityKind.SESSION} value={draftFilters.sessionId} change={(sessionId) => setDraftFilters((current) => ({ ...current, sessionId }))} active={active} />
      <div className="actions"><button className="primary" onClick={() => applyFilters()}>{copy("inbox.applyFilters_d80ab1")}</button><button onClick={() => { const defaults = { ...emptyFilters }; setDraftFilters(defaults); applyFilters(defaults); }}>{copy("inbox.reset_daee76")}</button></div>
      <p className="sidebar-help">{copy("inbox.openingAnItemDoesNotMark_d46181")}</p>
    </SidebarSurface>
  <section className={`inbox ${selectedId ? "has-selection" : ""}`} aria-label={copy("inbox.inboxWorkspace_bcd93a")} hidden={!active}>
    <header className="inbox-header"><div><h2>{copy("inbox.inbox_94835e")}</h2><p>{copy("inbox.requestsAndExecutionResults_7a0305")}</p></div><button onClick={refresh} disabled={!active || list.isFetching}>{copy("inbox.refresh_0e9161")}</button></header>
    <div className="inbox-workspace">
      <section className="inbox-list-pane" aria-label={copy("inbox.inboxItems_950c1d")}>
        <h3 ref={listHeading} tabIndex={-1} className="inbox-list-heading">{copy("inbox.items_fb8e7a")}</h3>
        <div className="inbox-list-scroll" ref={listScroller} aria-busy={list.isPending || list.isFetching}>
          <Problem error={list.error} />
          {list.isPending && !list.data ? <div className="inbox-skeleton" role="status" aria-label={copy("inbox.loadingInbox_fd917e")}><span /><span /><span /></div> : null}
          {entries.length > 0 ? <ul className="inbox-items">{entries.map((view, index) => {
            const entry = view.entry;
            if (!entry) return <li className="inbox-item-unavailable" key={`unavailable-${index}`}>{copy("inbox.inboxItemUnavailable_341441")}</li>;
            const data = document(entry), state = text(data.read_state);
            const time = recordedTime(entry);
            const kind = itemLabel(view);
            const icon = itemIcon(view);
            return <li key={entry.id}><button className="inbox-row" type="button" data-inbox-id={entry.id} aria-current={selectedId === entry.id ? "true" : undefined} aria-label={copy("inbox.message_3c2edd", { v0: kind, v1: resourceName(view.account ?? view.session), v2: state === "read" ? copy("inbox.read_9b9a8d") : state === "unread" ? copy("inbox.unread_1b9f38") : copy("inbox.readStateUnavailable_c6a29d") })} onClick={(event) => selectItem(entry.id, event.detail === 0)}>
              <span className={`inbox-kind-icon is-${icon.tone}`} aria-hidden="true">{icon.symbol}</span>
              <span className="inbox-row-copy"><strong>{resourceName(view.account ?? view.session)}</strong><span>{kind}</span><small>{time.label}</small></span>
              <span className={`inbox-read-label ${state === "unread" ? "is-unread" : ""}`}>{state === "unread" ? <><LocalizedText id="inbox.unread_2cbf9b" components={{ s0: <span className="inbox-unread-dot" aria-hidden="true" /> }} /></> : state === "read" ? copy("inbox.read_9b9a8d") : copy("inbox.unavailable_ca1844")}</span>
            </button></li>;
          })}</ul> : null}
          {!list.isPending && !list.error && entries.length === 0 ? filters.source === InboxSource.UNSPECIFIED && filters.readState === InboxReadState.UNSPECIFIED && !filters.projectId && !filters.sessionId
            ? <p className="inbox-empty">{copy("inbox.noRetainedRequestsOrExecutionResults_8f8962")}</p>
            : <div className="inbox-empty"><p>{copy("inbox.noItemsMatchTheseFilters_da10bc")}</p><button onClick={() => { setDraftFilters({ ...emptyFilters }); applyFilters({ ...emptyFilters }); }}>{copy("inbox.resetFilters_10afa9")}</button></div> : null}
        </div>
        <nav className="inbox-pager" aria-label={copy("inbox.inboxPages_0921bb")}><button disabled={!page || list.isFetching} onClick={() => setPage("")}>{copy("inbox.firstPage_0bdbb7")}</button><button disabled={!list.data?.nextPageToken || list.isFetching} onClick={() => setPage(list.data!.nextPageToken)}>{copy("inbox.nextPage_c08ac7")}</button></nav>
      </section>
      <section className="inbox-detail-pane" aria-label={copy("inbox.selectedInboxItem_959221")} ref={detailPane}>
        {!selectedId ? <div className="inbox-no-selection"><h3>{copy("inbox.selectAnItemToViewIts_e2e998")}</h3></div> : <>
          <div className="inbox-detail-top"><button className="inbox-back" onClick={closeDetail}>{copy("inbox.backToInbox_97555c")}</button><h3 ref={detailHeading} tabIndex={-1}>{selectedView ? itemLabel(selectedView) : copy("inbox.selectedInboxItem_959221")}</h3></div>
          {detailRead?.id === selectedId && detailRead.state === DetailReadState.Loading ? <p role="status" className="inbox-read-progress">{copy("inbox.loadingTheCurrentItem_15d15c")}</p> : null}
          {detailRead?.id === selectedId && detailRead.state === DetailReadState.Stale ? <div className="inbox-stale-warning"><p>{copy("inbox.theLatestReadFailedThisRetained_cb4adc")}</p><Problem error={detailRead.error} /><button onClick={() => setReadTrigger((value) => value + 1)}>{copy("inbox.retryCurrentRead_79b708")}</button></div> : null}
          {detailRead?.id === selectedId && detailRead.state === DetailReadState.Unavailable ? <div className="inbox-unavailable"><p>{copy("inbox.thisItemIsUnavailableItsCurrent_5617b2")}</p><Problem error={detailRead.error} /></div> : null}
          {selectedView && detailRead?.state !== DetailReadState.Unavailable ? <InboxDetail view={selectedView} readOnly={!latestReadReady} draft={selectedView.interaction ? drafts.get(selectedView.interaction.id) ?? initialInteractionDraft(selectedView.interaction) : undefined} draftError={selectedView.interaction ? draftCollection.errors.get(selectedView.interaction.id) : undefined} saveDraft={saveDraft} clearDraft={clearDraft} open={open} refresh={refresh} pageContains={entries.some((entry) => entry.entry?.id === selectedId)} /> : null}
          {!selectedView && detailRead?.state !== DetailReadState.Unavailable && detailRead?.state !== DetailReadState.Stale ? <div className="inbox-skeleton" role="status" aria-label={copy("inbox.loadingSelectedItem_903c91")}><span /><span /></div> : null}
        </>}
      </section>
    </div>
  </section></>;
}

function InboxDetail({ view, readOnly, draft, draftError, saveDraft, clearDraft, open, refresh, pageContains }: { view: InboxView; readOnly: boolean; draft?: InboxInteractionDraft; draftError?: string; saveDraft: (resource: Resource, editable: InteractionDraftState) => void; clearDraft: (resourceId: string) => void; open: (sessionId: string) => void; refresh: () => void; pageContains: boolean }) {
  useLocale();
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
    {!pageContains ? <p className="inbox-outside-list">{copy("inbox.thisItemIsOutsideTheCurrent_99d4bb")}</p> : null}
    <h4>{resourceName(view.account ?? view.session)}</h4>
    <div className="inbox-detail-meta"><span className={`inbox-state-badge ${state === "unread" ? "is-unread" : ""}`}>{state === "read" ? copy("inbox.read_9b9a8d") : state === "unread" ? copy("inbox.unread_1b9f38") : copy("inbox.readStateUnavailable_c6a29d")}</span><span><LocalizedText id="inbox.recorded_18a5bf" components={{ s0: <time dateTime={time.machineValue}>{time.label}</time> }} /></span></div>
    <div className="actions inbox-detail-actions"><button onClick={() => open(entry.sessionId)} disabled={!entry.sessionId}>{copy("inbox.openSession_b205bb")}</button>{state === "read" || state === "unread" ? <button disabled={!canMutate || readMutation.busy || readMutation.uncertain} onClick={() => void readMutation.send({ mutation: { requestId: newRequestId(), id: entry.id, expectedRevision: entry.revision }, readState: mark })}>{state === "read" ? copy("inbox.markUnread_54b4e3") : copy("inbox.markRead_b49c9b")}</button> : null}</div>
    <Problem error={readMutation.error} />{readMutation.uncertain ? <button disabled={!canMutate || readMutation.busy} onClick={readMutation.retry}>{copy("inbox.retryTheSameReadStateChange_ddea2f")}</button> : null}
    {draftError ? <p className="inbox-draft-limit" role="alert">{draftError}</p> : null}
    {identityChanged ? <p className="inbox-draft-stale" role="status">{copy("inbox.theOriginalRequestChangedThisResponse_20278e")}</p> : null}
    {view.interaction ? <>
      <section className="inbox-original-request"><h4>{copy("inbox.originalRequest_b00e25")}</h4><p><LocalizedText id="inbox.requestStatus_a8224c" components={{ s0: <>{closureText(document(view.interaction).closure)}</> }} /></p><p><LocalizedText id="inbox.responseStatus_1ab779" components={{ s0: <>{statusText(object(document(view.interaction).response ?? document(view.interaction).approval_response).state)}</> }} /></p>
        {responseCurrent ? null : <p>{copy("inbox.thisRequestIsRetainedForInspection_208dff")}</p>}
        <Interaction resource={view.interaction} refresh={refresh} draft={draft} saveDraft={(editable) => saveDraft(view.interaction!, editable)} clearDraft={() => clearDraft(view.interaction!.id)} submissionAllowed={canMutate && responseCurrent && !identityChanged} receiptRetryAllowed={canMutate} />
      </section>
    </> : data.source === "subscription-recovery" ? <section className="inbox-recovery" aria-label={copy("inbox.subscriptionQuotaRecovery_44e5d3")}><h4>{copy("inbox.subscriptionQuotaRecovery_44e5d3")}</h4><p><LocalizedText id="inbox.account_e07497" components={{ s0: <>{resourceName(view.account)}</> }} /></p><p><LocalizedText id="inbox.observed_e8e2c1" components={{ s0: <time dateTime={text(object(data.recovery).observed_at)}>{formatTimestamp(text(object(data.recovery).observed_at))}</time> }} /></p><p>{copy("inbox.thisRecordsTheAccountSObserved_bd3f28")}</p></section> : <section className="inbox-terminal"><h4>{copy("inbox.originalTerminalObservation_b3bd33")}</h4><p>{itemLabel(view)}</p><pre>{JSON.stringify(terminal, null, 2)}</pre><p>{copy("inbox.recordedTimeIsTheInboxRecord_4acabe")}</p></section>}
  </article>;
}

function documentVisibilityVisible(): boolean { return typeof globalThis.document !== "undefined" && globalThis.document.visibilityState === "visible"; }
