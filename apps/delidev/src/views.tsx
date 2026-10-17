// SPDX-License-Identifier: Apache-2.0
import { consumeFormSubmit, guardEnterSubmission } from "./form-enter";
import { useProductLabels, PresentationKind } from "./product-identity-labels";
import { useSidebarPaneVisible } from "./sidebar-context";
import { copy, useLocale } from "./localization";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { ScrollContinuation, useScrollRoot } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import {
  useConnectPaginationReader,
  usePaginationChain,
  usePaginationRefresh,
} from "./scroll-pagination-query";
import {
  EntityKind,
  SearchArchiveState,
  SearchExecutionOutcome,
  SearchQuery,
  isEntityId,
  type ConversationSearchHit,
  type SearchConversationsResponse,
} from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import { SearchOpenStatus } from "./search-session-routing";
import { Failure } from "./ui";
import { ResourceChoice } from "./configuration-fields";
import {
  SidebarSurface,
  useCloseSidebarDrawer,
  useSidebarDrawerOpen,
  useOpenSidebarDrawer,
  useSidebarPresentation,
} from "./sidebar-context";
import { Surface } from "./surface";
import type { DestinationClick } from "./destination-window";
import {
  EntranceMotion,
  useActivationCount,
  useEntranceActivation,
} from "./entrance-motion";
export { Surface } from "./surface";
import { useShortcuts } from "./shortcut-provider";
import { ShortcutExecution, ShortcutId, ShortcutInput } from "./shortcuts";

const searchIdentity = (hit: ConversationSearchHit) => hit.message!.id;
const searchRevision = (hit: ConversationSearchHit) => hit.message!.revision;
function searchPage(response: SearchConversationsResponse) {
  if (
    response.hits.length > 30 ||
    response.hits.some(
      (hit) =>
        !hit.message ||
        !isEntityId(hit.message.id) ||
        !isEntityId(hit.message.sessionId),
    )
  )
    throw new ConnectError("Invalid conversation search page", Code.DataLoss);
  return {
    rows: response.hits.map((hit) => ({
      id: hit.message!.id,
      revision: hit.message!.revision,
    })),
    nextPageToken: response.nextPageToken,
    payload: response.hits,
  };
}
interface SearchFilters {
  query: string;
  archive: SearchArchiveState;
  projectId: string;
  sessionId: string;
  agentId: string;
  accountId: string;
  outcome: SearchExecutionOutcome;
}
const emptySearch: SearchFilters = {
  query: "",
  archive: SearchArchiveState.UNSPECIFIED,
  projectId: "",
  sessionId: "",
  agentId: "",
  accountId: "",
  outcome: SearchExecutionOutcome.UNSPECIFIED,
};

export function Search({
  active,
  open,
  opening,
  submitted,
}: {
  active: boolean;
  open: (id: string, event?: DestinationClick) => void;
  opening?: { id: string; status: SearchOpenStatus };
  // Called when a submission replaces the result set, so an opening started
  // from the previous results cannot navigate or report on unrelated rows.
  submitted?: () => void;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const [draft, setDraft] = useState<SearchFilters>(emptySearch);
  const [query, setQuery] = useState<SearchFilters>();
  const content = useRef<HTMLElement>(null);
  const root = useScrollRoot(content);
  // Destination activation (a rising edge of `active`) owns one 120ms Y4
  // entrance on the results section. Typing, IME composition, debounce, result
  // replacement, pagination and refresh never change the key, and the focus
  // effects below never wait for the motion.
  const destinationEntrance = useEntranceActivation(
    `search:${useActivationCount(active)}`,
    active,
    EntranceMotion.Y4Short,
  );
  const attachEntrance = destinationEntrance.ref;
  // One stable callback keeps the scroll-root ref and the entrance listeners on
  // the same section without re-attaching on every render.
  const pageRef = useCallback(
    (node: HTMLElement | null) => {
      content.current = node;
      const detach = attachEntrance?.(node);
      return () => {
        if (typeof detach === "function") detach();
        content.current = null;
      };
    },
    [attachEntrance],
  );
  const searchInput = useRef<HTMLInputElement>(null);
  const focusedOnce = useRef(false);
  const activatedWhileWidePaneHidden = useRef(false);
  const requestedFocus = useRef<
    { generation: number; opening: boolean } | undefined
  >(undefined);
  const closeDrawer = useCloseSidebarDrawer();
  const drawerOpen = useSidebarDrawerOpen();
  const paneVisible = useSidebarPaneVisible();
  const presentation = useSidebarPresentation();
  const compact =
    typeof window.matchMedia === "function" &&
    window.matchMedia("(max-width: 759px)").matches;
  const presentationBlocked = presentation.problem && !compact;
  const openDrawer = useOpenSidebarDrawer();
  const request = useCallback(
    (token: string) => ({
      ...(query ?? emptySearch),
      pageSize: 30,
      pageToken: token,
    }),
    [query],
  );
  const reader = useConnectPaginationReader(
    SearchQuery.searchConversations,
    request,
    searchPage,
  );
  const result = usePaginationChain(
    JSON.stringify(query),
    active && Boolean(query?.query.trim()),
    reader,
  );
  usePaginationRefresh(
    SearchQuery.searchConversations,
    request(""),
    active && Boolean(query?.query.trim()),
    result.refresh,
  );
  useEffect(() => {
    const request = requestedFocus.current;
    if (!active || presentationBlocked) {
      requestedFocus.current = undefined;
      return;
    }
    if (!request) return;
    if (request.generation !== presentation.generation) {
      if (
        !request.opening &&
        presentation.focusAllowed &&
        paneVisible &&
        presentation.generation === request.generation + 1
      ) {
        request.generation = presentation.generation;
        request.opening = true;
      } else {
        requestedFocus.current = undefined;
        return;
      }
    }
    if (paneVisible && presentation.settled) {
      requestedFocus.current = undefined;
      searchInput.current?.focus();
      focusedOnce.current = true;
    }
  }, [
    active,
    drawerOpen,
    paneVisible,
    presentation.settled,
    presentation.generation,
    presentationBlocked,
    presentation.focusAllowed,
  ]);
  useEffect(() => {
    if (!active) {
      activatedWhileWidePaneHidden.current = false;
      return;
    }
    // Activating Search behind a collapsed pane must not turn a later ordinary
    // wide expansion into a focus request. An explicit compact drawer opening
    // remains a focusable presentation of the retained Search surface.
    if (!paneVisible) {
      if (
        typeof window.matchMedia !== "function" ||
        !window.matchMedia("(max-width: 759px)").matches
      )
        activatedWhileWidePaneHidden.current = true;
      return;
    }
    // The wide-only latch must not outlive the presentation it guards. Once the
    // retained Search is explicitly shown in the compact drawer, that opening is
    // a focusable first presentation. Ordinary wide expansion keeps the latch.
    if (compact && drawerOpen) activatedWhileWidePaneHidden.current = false;
    if (
      !presentation.settled ||
      activatedWhileWidePaneHidden.current ||
      focusedOnce.current
    )
      return;
    if (compact && !drawerOpen) return;
    const frame = window.requestAnimationFrame(() => {
      searchInput.current?.focus();
      focusedOnce.current = true;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [
    active,
    drawerOpen,
    paneVisible,
    presentation.settled,
    presentation.generation,
    presentation.focusAllowed,
  ]);
  const shortcuts = useShortcuts([
    {
      id: ShortcutId.SearchFocus,
      scope: Surface.Search,
      active,
      label: "shortcuts.focusSearch",
      bindings: [{ key: "i", primary: true }],
      input: ShortcutInput.Allow,
      run: () => {
        if (!paneVisible || !presentation.settled) {
          if (openDrawer())
            requestedFocus.current = {
              generation: presentation.generation,
              opening: paneVisible,
            };
        } else searchInput.current?.focus();
      },
    },
    {
      id: ShortcutId.SearchSubmit,
      scope: Surface.Search,
      active,
      label: "shortcuts.searchSubmit",
      bindings: [{ key: "Enter" }],
      target: searchInput,
      input: ShortcutInput.Target,
      execution: ShortcutExecution.Native,
      enabled: Boolean(draft.query.trim()),
      unavailableReason: "shortcuts.searchRequired",
    },
  ]);
  const change = <K extends keyof SearchFilters>(
    key: K,
    value: SearchFilters[K],
  ) => setDraft((current) => ({ ...current, [key]: value }));
  const submit = (event: FormEvent<HTMLFormElement>) => {
    if (!consumeFormSubmit(event)) return;
    if (!draft.query.trim()) return;
    submitted?.();
    const next = { ...draft, query: draft.query.trim() };
    if (JSON.stringify(query) === JSON.stringify(next)) result.reload();
    else setQuery(next);
    closeDrawer();
  };
  return (
    <>
      <SidebarSurface active={active} title={copy("views.search_49c266")}>
        <form
          onKeyDown={guardEnterSubmission}
          className="sidebar-form"
          onSubmit={submit}
        >
          <label>
            {copy("views.searchConversations_8abdf3")}
            <input
              ref={searchInput}
              aria-keyshortcuts={shortcuts.aria(
                ShortcutId.SearchFocus,
                ShortcutId.SearchSubmit,
              )}
              value={draft.query}
              onChange={(event) => change("query", event.target.value)}
            />
          </label>
          <label>
            {copy("views.archive_66f480")}
            <select
              value={draft.archive}
              onChange={(event) =>
                change(
                  "archive",
                  Number(event.target.value) as SearchArchiveState,
                )
              }
            >
              <option value={SearchArchiveState.UNSPECIFIED}>
                {copy("views.includeArchived_b6c334")}
              </option>
              <option value={SearchArchiveState.ACTIVE}>
                {copy("views.activeOnly_a9b5ed")}
              </option>
              <option value={SearchArchiveState.ARCHIVED}>
                {copy("views.archivedOnly_da7ebd")}
              </option>
            </select>
          </label>
          <ResourceChoice
            label={copy("views.project_985959")}
            kind={EntityKind.PROJECT}
            value={draft.projectId}
            change={(id) => change("projectId", id)}
            active={active}
          />
          <ResourceChoice
            label={copy("views.session_6959b4")}
            kind={EntityKind.SESSION}
            value={draft.sessionId}
            change={(id) => change("sessionId", id)}
            active={active}
          />
          <ResourceChoice
            label={copy("views.agentWorker_a4caa7")}
            kind={EntityKind.AGENT}
            value={draft.agentId}
            change={(id) => change("agentId", id)}
            active={active}
          />
          <ResourceChoice
            label={copy("views.account_7e1b0d")}
            kind={EntityKind.ACCOUNT}
            value={draft.accountId}
            change={(id) => change("accountId", id)}
            active={active}
          />
          <label>
            {copy("views.outcome_4e80ab")}
            <select
              value={draft.outcome}
              onChange={(event) =>
                change(
                  "outcome",
                  Number(event.target.value) as SearchExecutionOutcome,
                )
              }
            >
              {[
                [
                  SearchExecutionOutcome.UNSPECIFIED,
                  copy("views.extra.a52ace420f21"),
                ],
                [
                  SearchExecutionOutcome.NOT_STARTED,
                  copy("views.extra.ba35f0c47d86"),
                ],
                [
                  SearchExecutionOutcome.RUNNING,
                  copy("views.extra.f4ccae29e1bb"),
                ],
                [
                  SearchExecutionOutcome.SUCCEEDED,
                  copy("views.extra.6d9a6f97a5fd"),
                ],
                [
                  SearchExecutionOutcome.FAILED,
                  copy("views.extra.031a8f0f659d"),
                ],
                [
                  SearchExecutionOutcome.STOPPED,
                  copy("views.extra.1a4f630ac1b6"),
                ],
              ].map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <button
            className="primary"
            aria-keyshortcuts={shortcuts.aria(ShortcutId.SearchSubmit)}
            disabled={!draft.query.trim()}
          >
            {copy("views.search_49c266")}
          </button>
        </form>
      </SidebarSurface>
      <section
        ref={pageRef}
        hidden={!active}
        className="page"
        data-entrance={destinationEntrance["data-entrance"]}
      >
        <h2>{copy("views.searchConversations_8abdf3")}</h2>
        {!query ? (
          <p>{copy("views.chooseASearchTermAndFilters_99f204")}</p>
        ) : null}
        <Failure failure={result.error?.failure} />
        {opening?.status === SearchOpenStatus.Pending ? (
          <p role="status">{copy("views.openingTheConversation_4c1a7e")}</p>
        ) : null}
        {opening?.status === SearchOpenStatus.Unavailable ? (
          <p className="notice" role="alert">
            {copy("views.theConversationParentCouldNotBe_9d2b53")}
          </p>
        ) : null}
        {query && !result.loaded && result.loading ? (
          <p role="status">{copy("views.searching_c31723")}</p>
        ) : null}
        {query && result.error && result.loaded ? (
          <p className="notice">
            {copy("views.theRefreshFailedTheseAreThe_22c320")}
          </p>
        ) : null}
        <ScrollPayloadWindow
          identity={searchIdentity}
          revision={searchRevision}
          query={result}
          root={root}
          active={active}
        >
          {(payload) =>
            payload.map((hit) => (
              <article key={hit.message?.id} className="result">
                <button
                  disabled={!hit.message?.sessionId}
                  onClick={(event) => open(hit.message!.sessionId, event)}
                >
                  {hit.sessionName}
                </button>
                <p>{text(document(hit.message).text)}</p>
                <small>
                  {productLabel(
                    PresentationKind.Session,
                    hit.message?.sessionId,
                  )}
                </small>
              </article>
            ))
          }
        </ScrollPayloadWindow>
        {result.loaded && result.rows.length === 0 ? (
          <p>{copy("views.noRetainedConversationMatches_b59f78")}</p>
        ) : null}
        <ScrollContinuation
          query={result}
          root={root}
          active={active}
          label={copy("views.searchConversations_8abdf3")}
        />
      </section>
    </>
  );
}

export { Settings } from "./settings";
