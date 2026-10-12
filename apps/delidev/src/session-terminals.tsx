// SPDX-License-Identifier: Apache-2.0
import { createPortal } from "react-dom";
import { useShortcuts, useTerminalTabShortcuts } from "./shortcut-provider";
import {
  ShortcutId,
  ShortcutInput,
  terminalShortcutBindings,
} from "./shortcuts";
import { Surface } from "./surface";
import { useProductLabels, PresentationKind } from "./product-identity-labels";
import "./terminal-dock.css";
import { useConversationPages } from "./conversation-pagination";
import { ScrollContinuation } from "./scroll-continuation";
import { ReadStage } from "./scroll-pagination";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  Code,
  ConnectError,
  createClient,
  type Transport,
} from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import {
  EntityKind,
  ResourceService,
  ResourceQuery,
  TerminalAction,
  TerminalCreationMode,
  TerminalQuery,
  TerminalService,
  SystemQuery,
  SystemCapability,
  isEntityId,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import {
  useRetainedMutationIntents,
  useRetainedMutationNotifications,
  useRetainedMutation,
} from "./mutation";
import { useSessionTabsStore } from "./session-tabs";
import { terminalFallback } from "./terminal-presentation";
import type { TerminalScreen } from "./terminal-emulator";
import { TerminalInputQueue } from "./terminal-input-queue";
import { terminalDockHeight } from "./terminal-dock-size";
import { ServiceProblem, Failure, Problem } from "./ui";

export enum TerminalDockPresentation {
  Docked = "docked",
  CompactRestored = "compact-restored",
  Maximized = "maximized",
}

enum OutputState {
  Connecting = "connecting",
  Attached = "attached",
  Detached = "detached",
  Exited = "exited",
}

export function SessionTerminals({
  session,
  close,
  active = true,
  presentationChanged,
  selectedId,
  openTerminal,
  dismissTerminal,
  hideEmpty,
  tabbed = false,
  openIntent,
  finishOpenIntent,
  resolveActive = active,
  recoveryTarget,
  resumeOpenIntent,
}: {
  session: Resource;
  close: () => void;
  active?: boolean;
  resolveActive?: boolean;
  recoveryTarget?: HTMLElement | null;
  resumeOpenIntent?: (value: { requestId: string; revision: bigint }) => void;
  presentationChanged?: (value: TerminalDockPresentation) => void;
  selectedId?: string;
  openTerminal?: (id: string) => void;
  dismissTerminal?: (id: string, fallback: string) => void;
  hideEmpty?: () => void;
  tabbed?: boolean;
  openIntent?: { requestId: string; revision: bigint };
  finishOpenIntent?: () => void;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const tabsStore = useSessionTabsStore();
  const inventoryTransport = useTransport();
  const latest = useRef({
    active,
    resolveActive,
    session,
    openIntent,
    finishOpenIntent,
    openTerminal,
  });
  latest.current = {
    active,
    resolveActive,
    session,
    openIntent,
    finishOpenIntent,
    openTerminal,
  };
  const [openError, setOpenError] = useState<unknown>();
  const [openUnavailable, setOpenUnavailable] = useState(false);
  const [readBusy, setReadBusy] = useState(false);
  const readRetry = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => readRetry.current?.abort(), [resolveActive]);
  const presentationRecords = tabsStore.terminalPresentation(session.id);
  const intents = useRetainedMutationIntents("terminal-control:");
  const closing = new Set(
    intents
      .filter(
        (value) =>
          (value.busy || value.uncertain) &&
          object(value.input).action === TerminalAction.CLOSE,
      )
      .map((value) => value.key.slice("terminal-control:".length)),
  );
  const unsettled = new Set(
    intents
      .filter((value) => value.busy || value.uncertain)
      .map((value) => value.key.slice("terminal-control:".length)),
  );
  const [, redraw] = useState(0);
  const visibleBefore = useRef<string[]>([]);
  const observeRef = useRef<(value: Resource, settled?: boolean) => void>(
    () => {},
  );
  useRetainedMutationNotifications((key, _request, result) => {
    const value = object(result).terminal as Resource | undefined;
    if (
      key.startsWith("terminal-control:") &&
      value &&
      presentationRecords.known(value.id) &&
      value.sessionId === session.id
    )
      observeRef.current(value, true);
  });
  const dock = useRef<HTMLElement>(null);
  const [height, setHeight] = useState<number>();
  const [geometry, setGeometry] = useState({ width: 0, height: 0 }),
    [discarded, setDiscarded] = useState(false);
  useLayoutEffect(() => {
    const content = dock.current?.closest<HTMLElement>(".session-content");
    if (!content) return;
    const measure = () =>
      setGeometry({ width: content.clientWidth, height: content.clientHeight });
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(content);
    return () => observer.disconnect();
  }, []);
  const actualHeight = terminalDockHeight(
    geometry.width,
    geometry.height,
    false,
    height,
  );
  const presentation =
    !active || geometry.height <= 0
      ? TerminalDockPresentation.Docked
      : actualHeight >= geometry.height
        ? TerminalDockPresentation.Maximized
        : geometry.height - actualHeight < 200
          ? TerminalDockPresentation.CompactRestored
          : TerminalDockPresentation.Docked;
  useLayoutEffect(() => {
    presentationChanged?.(presentation);
  }, [presentation, presentationChanged]);
  useLayoutEffect(
    () => () => {
      presentationChanged?.(TerminalDockPresentation.Docked);
    },
    [presentationChanged],
  );
  useLayoutEffect(() => {
    dock.current
      ?.closest<HTMLElement>(".session-content")
      ?.style.setProperty("--terminal-dock-height", `${actualHeight}px`);
  }, [actualHeight]);
  const [internalSelected, setSelected] = useState("");
  const selected = selectedId ?? internalSelected;
  // Retain the explicit original ID through departure and inventory eviction.
  useLayoutEffect(() => {
    if (selectedId) setSelected(selectedId);
  }, [selectedId]);
  const [createdTerminal, setCreatedTerminal] = useState<Resource>();
  const [selectedTerminal, setSelectedTerminal] = useState<Resource>();
  const listRoot = useRef<HTMLDivElement>(null);
  const status = useQuery(
    SystemQuery.getStatus,
    {},
    {
      enabled: active || (resolveActive && Boolean(openIntent)),
    },
  );
  const supported =
    status.data?.capabilities.includes(SystemCapability.SESSION_TERMINALS_V1) ??
    false;
  const list = useConversationPages(
    EntityKind.TERMINAL,
    session.id,
    active && supported,
    50,
    undefined,
    1000,
  );
  const create = useRetainedMutation(
    `terminal-create:${session.id}`,
    TerminalQuery.createTerminal,
    (value, request) => {
      if (value.terminal) {
        presentationRecords.observe(
          session.id,
          value.terminal,
          unsettled.has(value.terminal.id),
        );
        if (!presentationRecords.hidden(value.terminal.id)) {
          setCreatedTerminal(value.terminal);
          setSelectedTerminal(undefined);
          setSelected(value.terminal.id);
          const owner = latest.current;
          // A verified receipt retains its original resource after departure,
          // but cannot select over a later explicit pane or a different session.
          if (
            owner.resolveActive &&
            owner.session.id === session.id &&
            ((request.creationMode !== TerminalCreationMode.REUSE_OR_CREATE &&
              owner.active) ||
              owner.openIntent?.requestId === request.mutation?.requestId)
          )
            owner.openTerminal?.(value.terminal.id);
        }
      }
      latest.current.finishOpenIntent?.();
      if (latest.current.active && supported) void list.refresh();
    },
    (value, request) =>
      Boolean(
        value.terminal &&
        value.terminal.kind === EntityKind.TERMINAL &&
        value.terminal.sessionId === request.mutation?.id &&
        isEntityId(value.terminal.id) &&
        value.terminal.schemaVersion === 1 &&
        value.terminal.revision > 0n,
      ),
    false,
    () => latest.current.finishOpenIntent?.(),
  );
  const blocked =
    !active ||
    Boolean(openIntent) ||
    !supported ||
    create.busy ||
    create.uncertain ||
    text(document(session).archive) !== "active";
  const resolution = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (!openIntent) return;
    if (!resolveActive) {
      finishOpenIntent?.();
      return;
    }
    if (status.error || (status.data && !supported)) {
      setOpenUnavailable(!status.error);
      finishOpenIntent?.();
      return;
    }
    if (!supported || status.isFetching) return;
    if (create.busy || create.uncertain) return;
    if (resolution.current === openIntent.requestId) return;
    resolution.current = openIntent.requestId;
    setOpenError(undefined);
    setOpenUnavailable(false);
    const controller = new AbortController();
    let sent = false;
    const current = () =>
      !controller.signal.aborted &&
      latest.current.resolveActive &&
      latest.current.openIntent?.requestId === openIntent.requestId &&
      latest.current.session.id === session.id;
    const run = async () => {
      try {
        if (create.busy || create.uncertain) return;
        const records = await readOriginalTerminalInventory(
          inventoryTransport,
          session.id,
          controller.signal,
        );
        const seen = new Set(records.map((row) => row.id));
        for (const row of records) presentationRecords.number(row.id);
        if (!current()) return;
        const retained = [createdTerminal, selectedTerminal].filter(
          (value): value is Resource => Boolean(value),
        );
        for (const row of retained) if (!seen.has(row.id)) records.push(row);
        const eligible = (row: Resource) => {
          const data = document(row);
          return (
            !presentationRecords.hidden(row.id) &&
            ["starting", "running"].includes(text(data.state)) &&
            !data.close_request_id &&
            object(data.pending).action !== "close" &&
            !closing.has(row.id)
          );
        };
        const reused =
          records.find((row) => row.id === internalSelected && eligible(row)) ??
          records.find(eligible);
        // Inventory chooses only a preference. The receipt transaction must
        // revalidate current Worker ownership and reuse before selecting a tab.
        if (
          !reused &&
          records.some((row) => {
            if (presentationRecords.hidden(row.id))
              return unsettled.has(row.id);
            const data = document(row);
            return (
              !["exited", "closed"].includes(text(data.state)) ||
              data.cleanup_verified !== true ||
              Object.keys(object(data.pending)).length > 0 ||
              unsettled.has(row.id)
            );
          })
        )
          throw new ConnectError(
            "Original terminal cleanup is unconfirmed.",
            Code.FailedPrecondition,
          );
        const original = latest.current.session;
        if (
          original.revision !== openIntent.revision ||
          text(document(original).archive) !== "active"
        )
          throw new ConnectError(
            "The session changed before opening a terminal.",
            Code.Aborted,
          );
        sent = true;
        await create.send({
          mutation: {
            requestId: openIntent.requestId,
            id: session.id,
            expectedRevision: openIntent.revision,
          },
          creationMode: TerminalCreationMode.REUSE_OR_CREATE,
          preferredTerminalId: reused?.id ?? "",
          shellOverride: "",
          rows: 24,
          columns: 80,
        });
      } catch (error) {
        if (current()) setOpenError(error);
      } finally {
        // Sent/uncertain requests remain owned by the retained mutation.
        if (!sent && current()) latest.current.finishOpenIntent?.();
      }
    };
    void run();
    return () => {
      controller.abort();
      if (!sent && resolution.current === openIntent.requestId)
        resolution.current = undefined;
    };
  }, [
    openIntent,
    resolveActive,
    supported,
    status.error,
    status.isFetching,
    create.busy,
    create.uncertain,
  ]);

  // The accepted resource can be beyond the first history page. Retain just
  // that one explicit selection so history eviction never detaches its shell.
  const reachedSelection = list.data?.resources.find(
    (value) => value.id === selected,
  );
  useEffect(() => {
    // Keep the newest observation of the explicit selection when its history
    // page leaves the payload window; eviction must not roll its revision back.
    if (
      reachedSelection &&
      (!selectedTerminal ||
        reachedSelection.revision > selectedTerminal.revision)
    ) {
      setSelectedTerminal(reachedSelection);
      setCreatedTerminal(undefined);
    }
  }, [reachedSelection, selectedTerminal]);
  const tabs = list.rows.filter((row) => !presentationRecords.hidden(row.id));
  if (
    createdTerminal &&
    !presentationRecords.hidden(createdTerminal.id) &&
    !tabs.some((row) => row.id === createdTerminal.id)
  )
    tabs.push({ id: createdTerminal.id, revision: createdTerminal.revision });
  const retainedResource = supported
    ? (reachedSelection ??
      (createdTerminal?.id === selected
        ? createdTerminal
        : selectedTerminal?.id === selected
          ? selectedTerminal
          : undefined))
    : undefined;
  const selectedRead = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.TERMINAL, id: selected },
    {
      enabled:
        active &&
        supported &&
        Boolean(selected) &&
        !presentationRecords.hidden(selected) &&
        !retainedResource,
      retry: false,
    },
  );
  const readResource = selectedRead.data?.resource;
  const resource = presentationRecords.hidden(selected)
    ? undefined
    : (retainedResource ??
      (readResource?.kind === EntityKind.TERMINAL &&
      readResource.id === selected &&
      readResource.sessionId === session.id &&
      readResource.schemaVersion === 1
        ? readResource
        : undefined));
  if (resource && !tabs.some((row) => row.id === resource.id))
    tabs.push({ id: resource.id, revision: resource.revision });
  // A disappeared payload is not exit evidence. Only original authenticated
  // resources advance this connection's monotonic presentation record.
  observeRef.current = (value, settled = false) => {
    if (
      !presentationRecords.observe(
        session.id,
        value,
        !settled && unsettled.has(value.id),
      )
    )
      return;
    const previous = visibleBefore.current;
    const remaining = previous.filter((id) => !presentationRecords.hidden(id));
    visibleBefore.current = remaining;
    const next =
      selected === value.id
        ? terminalFallback(
            previous.filter(
              (id) => id === value.id || !presentationRecords.hidden(id),
            ),
            value.id,
          )
        : "";
    dismissTerminal?.(value.id, next);
    if (selected === value.id) {
      setSelected(next);
      // Session owns original terminal content-tab order and focus.
      if (next && !tabbed) openTerminal?.(next);
      if (!tabbed && next)
        requestAnimationFrame(() =>
          dock.current
            ?.querySelector<HTMLButtonElement>(`[id="terminal-tab-${next}"]`)
            ?.focus({ preventScroll: true }),
        );
    }
    if (active && previous.includes(value.id) && !remaining.length)
      (hideEmpty ?? close)();
    redraw((value) => value + 1);
  };
  useEffect(() => {
    // Keep known rows through payload eviction; an initial exited-only read
    // does not discard retained mutation ownership.
    for (const value of [
      ...(list.data?.resources ?? []),
      ...(resource ? [resource] : []),
    ])
      observeRef.current(value);
    visibleBefore.current = tabs
      .filter((row) => !presentationRecords.hidden(row.id))
      .map((row) => row.id);
  }, [list.data, resource, intents, active]);
  const createAdditional = () => {
    if (blocked) return;
    void create.send({
      mutation: {
        requestId: newRequestId(),
        id: session.id,
        expectedRevision: session.revision,
      },
      shellOverride: "",
      rows: 24,
      columns: 80,
    });
  };
  const retryInventory = async () => {
    if (!resolveActive || !supported || readBusy) return;
    const controller = new AbortController();
    readRetry.current = controller;
    setReadBusy(true);
    try {
      const records = await readOriginalTerminalInventory(
        inventoryTransport,
        session.id,
        controller.signal,
      );
      if (controller.signal.aborted || !latest.current.resolveActive) return;
      for (const row of records) observeRef.current(row);
      setOpenError(undefined);
    } catch (error) {
      if (!controller.signal.aborted && latest.current.resolveActive)
        setOpenError(error);
    } finally {
      if (readRetry.current === controller) {
        readRetry.current = undefined;
        setReadBusy(false);
      }
    }
  };
  const recovery = (
    <>
      {openIntent || create.busy ? (
        <p role="status">{copy("session-terminals.openingTerminal")}</p>
      ) : null}
      <Problem
        error={openError}
        actions={
          <button
            type="button"
            disabled={
              !resolveActive ||
              !supported ||
              readBusy ||
              Boolean(openIntent) ||
              create.busy
            }
            onClick={() => void retryInventory()}
          >
            {copy("session-terminals.retryInventory")}
          </button>
        }
      />
      <Problem
        error={status.error}
        actions={
          <button
            type="button"
            disabled={!resolveActive || status.isFetching}
            onClick={() => void status.refetch()}
          >
            {copy("session-terminals.retryCapability")}
          </button>
        }
      />
      {((active && Boolean(status.data)) ||
        openUnavailable ||
        openIntent ||
        openError ||
        create.error) &&
      !supported &&
      !status.error ? (
        <>
          <p role="status">
            {copy("session-terminals.waitingForAServerThatSupports_e0becc")}
          </p>
          <button
            type="button"
            disabled={!resolveActive || status.isFetching}
            onClick={() => void status.refetch()}
          >
            {copy("session-terminals.retryCapability")}
          </button>
        </>
      ) : null}
      {!active ? (
        <Failure failure={supported ? list.error?.failure : undefined} />
      ) : null}
      <Problem error={create.error} />
      {create.uncertain ? (
        <button
          type="button"
          disabled={!resolveActive || create.busy}
          onClick={() => {
            const original = object(create.input),
              mutation = object(original.mutation);
            if (
              typeof mutation.requestId === "string" &&
              typeof mutation.expectedRevision === "bigint"
            )
              resumeOpenIntent?.({
                requestId: mutation.requestId,
                revision: mutation.expectedRevision,
              });
            void create.retry();
          }}
        >
          {copy("session-terminals.retryTheSameTerminalCreation_bc3946")}
        </button>
      ) : null}
      {intents
        .filter(
          (intent) =>
            intent.uncertain &&
            (!active || intent.key !== `terminal-control:${selected}`) &&
            object(object(intent.input).mutation).id &&
            presentationRecords.known(
              String(object(object(intent.input).mutation).id),
            ),
        )
        .map((intent) => (
          <TerminalControlRecovery
            key={intent.key}
            sessionId={session.id}
            terminalId={intent.key.slice("terminal-control:".length)}
            active={resolveActive}
            observe={(value) => observeRef.current(value)}
          />
        ))}
    </>
  );
  return (
    <>
      {recoveryTarget ? createPortal(recovery, recoveryTarget) : null}
      <aside
        ref={dock}
        hidden={!active}
        className="terminal-dock"
        aria-label={copy("session-terminals.sessionTerminals_db991c")}
      >
        {!tabbed ? (
          <div
            role="separator"
            tabIndex={0}
            aria-orientation="horizontal"
            aria-label={copy("session-terminals.resizeDock")}
            aria-valuemin={Math.min(200, geometry.height * 0.7)}
            aria-valuemax={Math.floor(geometry.height)}
            aria-valuenow={Math.round(actualHeight)}
            className="terminal-dock-separator"
            onPointerDown={(event) => {
              const target = event.currentTarget,
                start = event.clientY,
                initial = actualHeight;
              target.setPointerCapture(event.pointerId);
              const move = (next: PointerEvent) => {
                setHeight(initial + start - next.clientY);
              };
              const stop = () => {
                target.removeEventListener("pointermove", move);
                target.removeEventListener("pointerup", stop);
                target.removeEventListener("pointercancel", stop);
              };
              target.addEventListener("pointermove", move);
              target.addEventListener("pointerup", stop, { once: true });
              target.addEventListener("pointercancel", stop, { once: true });
            }}
            onKeyDown={(event) => {
              if (!["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key))
                return;
              event.preventDefault();
              setHeight(
                event.key === "Home"
                  ? 200
                  : event.key === "End"
                    ? geometry.height * 0.7
                    : actualHeight + (event.key === "ArrowUp" ? 20 : -20),
              );
            }}
          />
        ) : null}
        <header className="terminal-dock-header">
          <h3>{copy("session-terminals.terminals_7482c4")}</h3>
          <button
            type="button"
            aria-label={copy("session-terminals.createTerminal_747b98")}
            disabled={blocked}
            onClick={createAdditional}
          >
            +
          </button>
        </header>
        {!recoveryTarget ? recovery : null}
        {discarded ? (
          <p role="status">{copy("session-terminals.unsentDiscarded")}</p>
        ) : null}
        {!tabbed ? (
          <div
            ref={listRoot}
            role={tabbed ? "group" : "tablist"}
            aria-label={copy("session-terminals.terminals_7482c4")}
            className="terminal-tabs"
          >
            {tabs.map((row, index) => (
              <button
                key={row.id}
                id={`terminal-tab-${row.id}`}
                role={tabbed ? undefined : "tab"}
                aria-selected={tabbed ? undefined : selected === row.id}
                aria-controls={
                  selected === row.id ? `terminal-panel-${row.id}` : undefined
                }
                tabIndex={
                  tabbed || selected === row.id || (!selected && index === 0)
                    ? 0
                    : -1
                }
                onKeyDown={(event) => {
                  if (
                    tabbed ||
                    !["ArrowLeft", "ArrowRight", "Home", "End"].includes(
                      event.key,
                    )
                  )
                    return;
                  event.preventDefault();
                  const elements = [
                      ...(listRoot.current?.querySelectorAll<HTMLButtonElement>(
                        "[role=tab]",
                      ) ?? []),
                    ],
                    at = elements.indexOf(event.currentTarget),
                    next =
                      event.key === "Home"
                        ? 0
                        : event.key === "End"
                          ? elements.length - 1
                          : (at +
                              (event.key === "ArrowRight" ? 1 : -1) +
                              elements.length) %
                            elements.length;
                  elements[next]?.focus();
                  elements[next]?.click();
                }}
                onClick={() => {
                  openTerminal?.(row.id);
                  setSelected(row.id);
                  const value =
                    list.data?.resources.find((value) => value.id === row.id) ??
                    (createdTerminal?.id === row.id
                      ? createdTerminal
                      : selectedTerminal?.id === row.id
                        ? selectedTerminal
                        : undefined);
                  if (value) {
                    setSelectedTerminal(value);
                    setCreatedTerminal(undefined);
                  } else {
                    const page = list.pages.find((page) =>
                      page.rows.some((value) => value.id === row.id),
                    );
                    if (page) list.restore(page.token);
                  }
                }}
              >
                {productLabel(PresentationKind.Terminal, row.id)} ·{" "}
                {statusLabel(
                  text(
                    document(
                      list.data?.resources.find(
                        (value) => value.id === row.id,
                      ) ?? (resource?.id === row.id ? resource : undefined),
                    ).state,
                  ),
                )}
              </button>
            ))}
          </div>
        ) : null}
        {!tabbed ? (
          <ScrollContinuation
            showInitial={!(list.loaded && list.loading === ReadStage.Refresh)}
            query={list}
            root={listRoot}
            active={active && supported}
            label={copy("session-terminals.terminalHistoryPages_2ace98")}
          />
        ) : null}
        {supported && list.loaded && !tabs.length && !list.error ? (
          <p role="status">{copy("session-terminals.emptyInventory")}</p>
        ) : null}
        <Problem error={selectedRead.error} />
        <Failure failure={supported ? list.error?.failure : undefined} />
        {active && resource ? (
          <TerminalView
            key={resource.id}
            resource={resource}
            tabbed={tabbed}
            createAdditional={createAdditional}
            createBlocked={blocked}
            discarded={() => setDiscarded(true)}
            refresh={() => {
              if (supported) void list.refresh();
            }}
            observe={(value) => observeRef.current(value)}
          />
        ) : null}
      </aside>
    </>
  );
}

function TerminalView({
  resource,
  tabbed,
  createAdditional,
  createBlocked,
  discarded,
  refresh,
  observe,
}: {
  resource: Resource;
  tabbed: boolean;
  createAdditional: () => void;
  createBlocked: boolean;
  discarded: () => void;
  refresh: () => void;
  observe: (value: Resource) => void;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const tabShortcut = useTerminalTabShortcuts();
  const transport = useTransport(),
    host = useRef<HTMLDivElement>(null),
    screen = useRef<TerminalScreen | undefined>(undefined);
  const creationShortcut = useShortcuts([
    {
      id: ShortcutId.TerminalCreate,
      scope: Surface.Sessions,
      label: "shortcuts.createTerminal",
      bindings: terminalShortcutBindings[ShortcutId.TerminalCreate],
      target: host,
      input: ShortcutInput.Target,
      terminal: true,
      enabled: !createBlocked,
      unavailableReason: "shortcuts.pending",
      run: createAdditional,
    },
  ]);
  const [observed, setObserved] = useState(resource),
    [state, setState] = useState(OutputState.Connecting),
    [error, setError] = useState<unknown>();
  const [ready, setReady] = useState(false);
  const [rendererUnavailable, setRendererUnavailable] = useState(false),
    [gap, setGap] = useState(false),
    [overflow, setOverflow] = useState(false),
    [restart, setRestart] = useState(0),
    [rendererRestart, setRendererRestart] = useState(0),
    [deliveryError, setDeliveryError] = useState(false),
    [wake, setWake] = useState(0);
  const cursor = useRef({ epoch: "", afterSequence: 0n }),
    queue = useRef(new TerminalInputQueue()),
    dispatching = useRef(false),
    seenError = useRef<unknown>(undefined),
    alive = useRef(false);
  const terminal = resource.revision > observed.revision ? resource : observed,
    current = useRef(terminal);
  current.current = terminal;
  const data = document(terminal);
  const control = useRetainedMutation(
    `terminal-control:${resource.id}`,
    TerminalQuery.controlTerminal,
    (value, request) => {
      if (value.terminal) {
        const accepted = value.terminal;
        if (accepted.revision >= current.current.revision)
          current.current = accepted;
        setObserved((previous) =>
          accepted.revision >= previous.revision ? accepted : previous,
        );
      }
      queue.current.acknowledge(
        request.action === TerminalAction.RESIZE
          ? { rows: request.rows, columns: request.columns }
          : undefined,
      );
      dispatching.current = false;
      setWake((value) => value + 1);
      refresh();
    },
    (value, request) =>
      !!value.terminal &&
      value.terminal.id === request.mutation?.id &&
      value.terminal.kind === EntityKind.TERMINAL &&
      value.terminal.sessionId === resource.sessionId &&
      value.terminal.revision >= (request.mutation?.expectedRevision ?? 1n),
  );
  const observeLatest = useRef(observe);
  observeLatest.current = observe;
  useEffect(() => {
    if (!control.busy && !control.uncertain) observeLatest.current(terminal);
  }, [terminal, control.busy, control.uncertain]);
  const pending = Object.keys(object(data.pending)).length > 0;
  const blocked =
    rendererUnavailable ||
    deliveryError ||
    control.busy ||
    control.uncertain ||
    pending ||
    text(data.state) !== "running" ||
    !!data.close_request_id ||
    state !== OutputState.Attached;
  const canType =
    !rendererUnavailable &&
    !deliveryError &&
    !control.uncertain &&
    text(data.state) === "running" &&
    !data.close_request_id &&
    state === OutputState.Attached;
  const focusPending = useRef(true),
    controlRef = useRef(control);
  controlRef.current = control;
  useLayoutEffect(() => {
    alive.current = true;
    let disposed = false;
    const unavailable = () => {
      if (!disposed && alive.current) {
        setRendererUnavailable(true);
        if (queue.current.discard()) discarded();
      }
    };
    setReady(false);
    void import("./terminal-emulator").then(
      ({ openTerminalScreen }) => {
        if (disposed) return;
        try {
          screen.current = openTerminalScreen(
            host.current!,
            (bytes) => {
              if (!queue.current.enqueue(bytes)) setOverflow(true);
              else {
                setOverflow(false);
                setWake((value) => value + 1);
              }
            },
            (rows, columns) => {
              queue.current.resize(rows, columns);
              setWake((value) => value + 1);
            },
            unavailable,
            tabShortcut,
          );
          focusPending.current = true;
          setReady(true);
        } catch {
          console.error({
            event: "terminal-renderer-open-failed",
            category: "renderer-unavailable",
          });
          unavailable();
        }
      },
      () => {
        console.error({
          event: "terminal-renderer-load-failed",
          category: "renderer-unavailable",
        });
        unavailable();
      },
    );
    return () => {
      disposed = true;
      alive.current = false;
      if (queue.current.discard()) discarded();
      screen.current?.dispose();
      screen.current = undefined;
    };
  }, [resource.id, rendererRestart]);
  useEffect(() => {
    screen.current?.enabled(canType);
    if (canType && focusPending.current) {
      focusPending.current = false;
      screen.current?.focus();
    }
  }, [canType, restart, ready]);
  useEffect(() => {
    if (
      control.error &&
      control.error !== seenError.current &&
      !control.busy &&
      !control.uncertain
    ) {
      seenError.current = control.error;
      setDeliveryError(true);
      dispatching.current = false;
      if (queue.current.discard()) discarded();
      return;
    }
    if (blocked || dispatching.current) return;
    const next = queue.current.next();
    if (!next) return;
    dispatching.current = true;
    const record = current.current;
    void control.send({
      mutation: {
        id: record.id,
        expectedRevision: record.revision,
        requestId: newRequestId(),
      },
      action: "input" in next ? TerminalAction.INPUT : TerminalAction.RESIZE,
      input: "input" in next ? next.input : new Uint8Array(),
      rows: "rows" in next ? next.rows : 0,
      columns: "columns" in next ? next.columns : 0,
    });
  }, [
    wake,
    blocked,
    terminal.revision,
    control.busy,
    control.uncertain,
    control.error,
  ]);
  useEffect(() => {
    const abort = new AbortController(),
      client = createClient(TerminalService, transport);
    const run = async () => {
      setState(OutputState.Connecting);
      setError(undefined);
      try {
        let cleanup = false;
        for await (const frame of client.watchTerminalOutput(
          { terminalId: resource.id, ...cursor.current },
          { signal: abort.signal },
        )) {
          if (abort.signal.aborted) return;
          const previous = cursor.current;
          if (
            !frame.epoch ||
            frame.sequence < 0n ||
            (!frame.gap &&
              previous.epoch === frame.epoch &&
              frame.sequence < previous.afterSequence)
          )
            throw new Error("Terminal output cursor regressed.");
          if (
            frame.terminal &&
            (frame.terminal.id !== resource.id ||
              frame.terminal.kind !== EntityKind.TERMINAL ||
              frame.terminal.sessionId !== resource.sessionId ||
              frame.terminal.schemaVersion !== 1 ||
              frame.terminal.revision <= 0n)
          )
            throw new Error("The terminal observation identity changed.");
          if (
            (!frame.gap && previous.epoch && previous.epoch !== frame.epoch) ||
            (!frame.gap &&
              previous.epoch === frame.epoch &&
              frame.data.length &&
              frame.sequence !== previous.afterSequence + 1n)
          )
            throw new Error(
              "Terminal output order changed. Reattach to inspect the gap.",
            );
          if (frame.gap) setGap(true);
          if (frame.gap || frame.data.length)
            await screen.current?.write(frame.data, frame.gap);
          if (abort.signal.aborted) return;
          cursor.current = {
            epoch: frame.epoch,
            afterSequence: frame.sequence,
          };
          if (frame.terminal) {
            setObserved((value) =>
              frame.terminal!.revision >= value.revision
                ? frame.terminal!
                : value,
            );
            cleanup =
              frame.terminal.revision >= current.current.revision &&
              document(frame.terminal).state === "exited" &&
              document(frame.terminal).cleanup_verified === true;
          }
          setState(OutputState.Attached);
        }
        if (!abort.signal.aborted) {
          setState(cleanup ? OutputState.Exited : OutputState.Detached);
          if (queue.current.discard()) discarded();
        }
      } catch (failure) {
        if (!abort.signal.aborted) {
          setError(failure);
          setState(OutputState.Detached);
          if (queue.current.discard()) discarded();
        }
      }
    };
    if (ready && !rendererUnavailable) void run();
    return () => abort.abort();
  }, [resource.id, transport, restart, rendererUnavailable, ready]);
  const reattach = () => {
    if (queue.current.discard()) discarded();
    if (rendererUnavailable) {
      cursor.current = { epoch: "", afterSequence: 0n };
      setGap(true);
      setRendererRestart((value) => value + 1);
    }
    setDeliveryError(false);
    setRendererUnavailable(false);
    setRestart((value) => value + 1);
  };
  return (
    <section
      id={`terminal-panel-${resource.id}`}
      aria-labelledby={tabbed ? undefined : `terminal-tab-${resource.id}`}
      className="terminal-view"
      role={tabbed ? "region" : "tabpanel"}
      aria-label={copy("session-terminals.attachedTerminal_19246e")}
    >
      {gap ? <p role="status">{copy("session-terminals.outputGap")}</p> : null}
      {overflow ? (
        <p role="alert">{copy("session-terminals.inputOverflow")}</p>
      ) : null}
      {rendererUnavailable ? (
        <p role="alert">{copy("session-terminals.rendererUnavailable")}</p>
      ) : null}
      <div
        ref={host}
        className="terminal-screen"
        data-shortcuts="passthrough"
        aria-label={copy("session-terminals.attribute.34ad7d49708a")}
        aria-keyshortcuts={creationShortcut.aria(ShortcutId.TerminalCreate)}
        onKeyDown={(event) => {
          creationShortcut.onKeyDown(event);
          event.stopPropagation();
        }}
      />
      <footer role="status">
        {copy(`session-terminals.${state}`)} · {statusLabel(text(data.state))}
      </footer>
      <Problem error={error || control.error} />
      {state === OutputState.Detached ||
      rendererUnavailable ||
      deliveryError ? (
        <button type="button" onClick={reattach}>
          {copy("session-terminals.reattachOriginalTerminal_b22f08")}
        </button>
      ) : null}
      {control.uncertain ? (
        <>
          <p role="status">{copy("session-terminals.uncertainInput")}</p>
          <button type="button" disabled={control.busy} onClick={control.retry}>
            {copy("session-terminals.retryTheSameTerminalOperation_92eff7")}
          </button>
        </>
      ) : null}
      {object(data.problem).message ? (
        <ServiceProblem
          code={
            text(object(data.problem).code) ||
            text(object(data.problem).problem_code)
          }
        >
          <p role="alert">
            {text(object(data.problem).message)}{" "}
            {text(object(data.problem).guidance)}
          </p>
        </ServiceProblem>
      ) : null}
    </section>
  );
}

/** Hidden attachment recovery shares the original connection-owned request. */
function TerminalControlRecovery({
  sessionId,
  terminalId,
  active,
  observe,
}: {
  sessionId: string;
  terminalId: string;
  active: boolean;
  observe: (value: Resource) => void;
}) {
  const control = useRetainedMutation(
    `terminal-control:${terminalId}`,
    TerminalQuery.controlTerminal,
    (value) => {
      if (value.terminal) observe(value.terminal);
    },
    (value, request) =>
      Boolean(
        value.terminal &&
        value.terminal.id === terminalId &&
        value.terminal.kind === EntityKind.TERMINAL &&
        value.terminal.sessionId === sessionId &&
        value.terminal.schemaVersion === 1 &&
        value.terminal.revision >= (request.mutation?.expectedRevision ?? 1n),
      ),
  );
  return (
    <>
      <p role="status">{copy("session-terminals.uncertainInput")}</p>
      <button
        type="button"
        disabled={!active || control.busy}
        onClick={control.retry}
      >
        {copy("session-terminals.retryTheSameTerminalOperation_92eff7")}
      </button>
    </>
  );
}

/** Shared bounded authenticated read; it has no terminal mutation authority. */
async function readOriginalTerminalInventory(
  transport: Transport,
  sessionId: string,
  signal: AbortSignal,
): Promise<Resource[]> {
  const client = createClient(ResourceService, transport),
    records: Resource[] = [],
    seen = new Set<string>(),
    tokens = new Set<string>();
  let token = "";
  do {
    signal.throwIfAborted();
    const reply = await client.listResources(
      {
        filter: {
          kind: EntityKind.TERMINAL,
          sessionId: sessionId,
          pageSize: 50,
          pageToken: token,
        },
      },
      { signal: signal },
    );
    if (
      reply.resources.length > 50 ||
      reply.nextPageToken.length > 2048 ||
      records.length + reply.resources.length > 128 ||
      (reply.nextPageToken && !reply.resources.length) ||
      tokens.size >= 128
    )
      throw new ConnectError(
        "Terminal inventory is unavailable.",
        Code.DataLoss,
      );
    for (const row of reply.resources) {
      const data = document(row);
      if (
        row.kind !== EntityKind.TERMINAL ||
        row.sessionId !== sessionId ||
        row.schemaVersion !== 1 ||
        !isEntityId(row.id) ||
        row.revision <= 0n ||
        seen.has(row.id) ||
        !["starting", "running", "exited", "closed", "uncertain"].includes(
          text(data.state),
        ) ||
        (data.pending != null &&
          (typeof data.pending !== "object" || Array.isArray(data.pending)))
      )
        throw new ConnectError(
          "Terminal inventory is unavailable.",
          Code.DataLoss,
        );
      if (
        (data.close_request_id != null &&
          (typeof data.close_request_id !== "string" ||
            !isEntityId(data.close_request_id))) ||
        (data.cleanup_verified != null &&
          typeof data.cleanup_verified !== "boolean") ||
        (Object.keys(object(data.pending)).length &&
          !["create", "input", "resize", "close"].includes(
            text(object(data.pending).action),
          ))
      )
        throw new ConnectError(
          "Terminal inventory is unavailable.",
          Code.DataLoss,
        );
      seen.add(row.id);
      records.push(row);
    }
    token = reply.nextPageToken;
    if (token && tokens.has(token))
      throw new ConnectError(
        "Terminal inventory is unavailable.",
        Code.DataLoss,
      );
    tokens.add(token);
  } while (token);
  return records;
}
