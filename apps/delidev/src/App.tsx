import {
  ProductIdentityProvider,
  useProductLabels,
} from "./product-identity-labels";
import { PresentationKind } from "./product-identity";
import { DraftEditGenerations } from "./draft-edit-generations";
import { SessionInboxReadBoundary } from "./session-inbox-read";
import { SubscriptionQuotaAutoRefresh } from "./subscription-quota-refresh";
import { InboxBadgePresentation } from "./inbox-badge";
import { SessionNameEditorProvider } from "./session-name-editor";
import {
  SidebarPreference,
  SidebarPreferenceBoundary,
  useSidebarPreference,
  useWideSidebar,
} from "./sidebar-preference";
import { CommandMenu, applicationCommands } from "./command-menu";

import {
  SessionTabsProvider,
  useSessionTabsStore,
  useSessionTabs,
  SessionTabKind,
} from "./session-tabs";
import { SessionSubmissionsProvider } from "./session-submissions";
import { ImageDraftProvider } from "./image-drafts";
import { RunnerRemediationProvider } from "./runner-remediation";
import { RunnerPreferenceProvider } from "./runner-device-preferences";
import { SessionControlProvider } from "./session-control";
import { LocalizedText, copy, useLocale } from "./localization";
import {
  EntranceMotion,
  useActivationCount,
  useEntranceActivation,
} from "./entrance-motion";
import type { UsageEntry } from "./usage-entry";
import { LocalConnectionPresentationProvider } from "./local-connection-presentation";
import {
  ConnectionEpochProvider,
  useConnectionEpoch,
} from "./connection-epoch";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { type Transport } from "@connectrpc/connect";
import {
  TransportProvider,
  useQuery,
  useTransport,
} from "@connectrpc/connect-query";
import { QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import {
  SessionQuery,
  SystemQuery,
  SystemCapability,
  newRequestId,
  EntityKind,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import { SettingsCategory } from "./settings-category";
import { SessionView } from "./session";
import {
  resolveSearchSession,
  SearchOpenStatus,
  SearchResolutionLogging,
  SearchSessionRoute,
} from "./search-session-routing";
import { Search, Settings, Surface } from "./views";
import {
  type DestinationClick,
  DestinationWindowNotice,
  DestinationWindowProvider,
  destinationSurface,
  useDestinationWindowController,
} from "./destination-window";
import { openDestinationWindow } from "./navigation-disposition";
import { NewSession, NewSessionKind } from "./new-session";
import { Inbox } from "./inbox";
import { Usage } from "./usage";
import { UsageTab } from "./usage-tab";
import {
  NavigationIntentProvider,
  NavigationIntentSource,
} from "./navigation-intent";
import {
  commitInitialRouteScope,
  readInitialRoute,
  writeProductRoute,
  type ProductRoute,
} from "./product-route";
import {
  readRouteInboxEntry,
  readRouteRepository,
  readRouteSchedule,
  readRouteSession,
  RouteReadOutcome,
} from "./product-route-restore";
import {
  deriveProductRoute,
  MirrorRestoreState,
  SchedulesMode,
} from "./product-route-mirror";
import { Schedules } from "./schedules";
import type { ControlLocalWorker } from "./local-worker-controls";
import type { ReadLocalWorkerProof } from "./local-worker";
import { Problem } from "./ui";
import type { SkillTokenBinding } from "./skill-completion";
import { MutationIntents, useRetainedMutationAccepted } from "./mutation";
import { connectionQueryClient } from "./cache";
import type { PairingAuthority } from "./pairing-grant";
import { TrayPresentation } from "./tray-presentation";
import { TrayDestination } from "./tray";
import { NotificationPresentation } from "./notification-presentation";
import { NotificationProvider } from "./toast-notifications";
import { refreshCreatedSessionNavigation } from "./home-navigation-query";
import { Sidebar } from "./sidebar";
import type { SidebarPresentation } from "./sidebar-motion";
import type { ChooseRepositoryFolder } from "./repository-registration";
import {
  SettingsEntryDestination,
  type SettingsNavigationEntry,
} from "./settings";
import { SidebarOutletProvider } from "./sidebar-context";
import { PullRequests } from "./pull-requests";
import { SessionForkProvider } from "./session-fork";
import { SessionStorageProvider } from "./session-storage";
import { PRWorkflowProvider } from "./pr-workflow";
import { ProjectCreationDialog } from "./project-creation";
import type { PublishRunnerTailscaleSlots } from "./runner-tailscale-slots";
import {
  ShortcutProvider,
  useShortcutHelp,
  useHeldShortcutHelp,
  useShortcutSurface,
  useShortcuts,
} from "./shortcut-provider";
import {
  ShortcutExecution,
  ShortcutId,
  ShortcutInput,
  ShortcutScope,
  globalShortcutBindings,
} from "./shortcuts";

/** Selection from the product route that still needs a fresh verification read. */
interface PendingSelection {
  surface: Surface;
  id: string;
  sidechat?: string;
}
/** Per-screen selections reported by the mounted screens for the product route. */
interface RouteSelection {
  inbox: string;
  schedule: string;
  repository: string;
  category?: SettingsCategory;
  tab?: UsageTab;
}
function pendingSelectionOf(
  route: ProductRoute | undefined,
): PendingSelection | undefined {
  if (!route) return undefined;
  if (route.surface === Surface.Sessions)
    return route.session
      ? { surface: route.surface, id: route.session, sidechat: route.sidechat }
      : undefined;
  return route.item ? { surface: route.surface, id: route.item } : undefined;
}

function Shell({
  routeScope,
  localServer,
  connectionReady,
  connectionSettings,
  appInformationSettings,
  runnerTailscale,
  connectionTarget,
  connectionPageOpen = false,
  onConnectionHelp,
  readLocalWorker,
  controlLocalWorker,
  chooseRepositoryFolder,
  currentDeviceId,
  pairingAuthority,
}: {
  pairingAuthority?: PairingAuthority;
  currentDeviceId?: string;
  controlLocalWorker?: ControlLocalWorker;
  chooseRepositoryFolder?: ChooseRepositoryFolder;
  /** Connection identity that owns the document's initial product route. */
  routeScope: string;
  localServer?: ReactNode;
  connectionReady: boolean;
  connectionSettings?: ReactNode;
  appInformationSettings?: ReactNode;
  runnerTailscale?: PublishRunnerTailscaleSlots;
  connectionTarget?: HTMLElement;
  /** True while the local window's Connections page is mounted. */
  connectionPageOpen?: boolean;
  onConnectionHelp?: (target: HTMLElement | undefined) => void;
  readLocalWorker?: ReadLocalWorkerProof;
}) {
  const queryClient = useQueryClient();
  const productLabel = useProductLabels();
  const transport = useTransport();
  const latestTransport = useRef(transport);
  useLayoutEffect(() => {
    latestTransport.current = transport;
  }, [transport]);
  // A same-identity reconnect keeps the transport and query client while the
  // peer behind them can be replaced or downgraded, so work that started before
  // it is current only for the epoch it started in.
  const connectionEpoch = useConnectionEpoch();
  const latestEpoch = useRef(connectionEpoch);
  useLayoutEffect(() => {
    latestEpoch.current = connectionEpoch;
  }, [connectionEpoch]);
  const sessionCreated = useCallback(
    (session: Resource) => {
      // A retained creation can settle after a same-identity reconnect replaces its transport.
      void refreshCreatedSessionNavigation(
        queryClient,
        latestTransport.current,
        session,
      );
    },
    [queryClient],
  );
  useLocale();
  // The product route in the URL (screen plus selection) survives a full webview
  // reload. Screens without a selection open immediately; a selection waits for
  // a fresh read through this connection before it is reopened.
  // The read is pure (StrictMode double-invokes lazy initializers); the identity
  // takes ownership of the URL only when this Shell commits.
  const [initialRoute] = useState(() => readInitialRoute(routeScope));
  useLayoutEffect(() => {
    commitInitialRouteScope(routeScope);
  }, [routeScope]);
  const [surface, setSurfaceState] = useState(
    initialRoute?.surface ?? Surface.Sessions,
  );
  useShortcutSurface(surface);
  // Home activation (a rising edge of the Sessions surface) owns one 160ms Y4
  // entrance on the welcome content. Selecting a session, reads and retries keep
  // the key unchanged, and the hook lives here so the welcome section mounting
  // later never starts a replay.
  const homeActivations = useActivationCount(surface === Surface.Sessions);
  const welcomeEntrance = useEntranceActivation(
    `welcome:${homeActivations}`,
    surface === Surface.Sessions,
    EntranceMotion.Y4,
  );
  const openHelp = useShortcutHelp();
  const holdHelp = useHeldShortcutHelp();
  const [commandMenuOpen, setCommandMenuOpen] = useState(false);
  const toggleCommandMenu = () => setCommandMenuOpen((value) => !value);
  const pendingFocusDestination = useRef<Surface>(undefined);
  const main = useRef<HTMLElement>(null);
  const contextOpener = useRef<HTMLButtonElement>(null);
  const [sidebarTarget, setSidebarTarget] = useState<HTMLElement | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const sidebarPreference = useSidebarPreference();
  const wideSidebar = useWideSidebar();
  const sidebarCollapsed =
    sidebarPreference.snapshot.sidebar_preference ===
    SidebarPreference.Collapsed;
  const sidebarPaneVisible = wideSidebar ? !sidebarCollapsed : drawerOpen;
  const [sidebarPresentation, setSidebarPresentation] =
    useState<SidebarPresentation>({
      generation: 0,
      settled: true,
      collapsed: false,
      focusAllowed: false,
    });
  const toggleSidebar = () => {
    // The product window renders no sidebar notice, so the toggle shortcut is
    // the recovery action. While a problem fences saves, one press inspects the
    // stored preference without saving or queueing the requested change; the
    // next press after a healthy inspection saves it.
    if (sidebarPreference.snapshot.problem) {
      console.info("delidev.sidebar_preference.recovery_requested", {
        problem: sidebarPreference.snapshot.problem,
      });
      sidebarPreference.reload();
      return;
    }
    sidebarPreference.select(
      sidebarCollapsed
        ? SidebarPreference.Expanded
        : SidebarPreference.Collapsed,
    );
  };
  const openSidebar = () => {
    if (wideSidebar)
      return (
        !sidebarCollapsed ||
        sidebarPreference.select(SidebarPreference.Expanded)
      );
    setDrawerOpen(true);
    return true;
  };

  const [pendingSelection, setPendingSelection] = useState(() =>
    pendingSelectionOf(initialRoute),
  );
  // The one monotonic navigation-intent token. Every handler that changes the
  // surface, the selection or the tab, or opens a new-item, editor, review or
  // project destination, bumps it synchronously, in the handler itself and never
  // from an effect. Shell handlers call `userNavigated` directly; child screens
  // call it through `useNavigationIntent` from their own navigation handlers.
  // Retry, reconnect, refresh, dismiss and copy controls never bump it, so they
  // cannot supersede a selection that waits for its connection. A pending
  // route restore captures the token when it starts and compares it when its
  // read settles, before any state change, Session opening or URL write, so a
  // slow read can never override a choice made after it began, even when it
  // settles before the choice has rendered or any passive effect has run.
  const navigationIntent = useRef(0);
  const restoreAbort = useRef<AbortController>(undefined);
  const restorePending = useRef(pendingSelection !== undefined);
  const userNavigated = useCallback((source: NavigationIntentSource) => {
    navigationIntent.current += 1;
    console.info("delidev.product_route.navigation_intent", {
      source,
      restorePending: restorePending.current,
    });
    restoreAbort.current?.abort();
    restoreAbort.current = undefined;
    if (!restorePending.current) return;
    restorePending.current = false;
    console.info("delidev.product_route.restore", {
      outcome: "superseded",
    });
    setPendingSelection(undefined);
  }, []);

  const destinationWindow = useDestinationWindowController();
  const searchIntent = useRef(0);
  const searchAbort = useRef<AbortController>(undefined);
  const searchMounted = useRef(false);
  // Invalidates the latest opening intent and cancels its in-flight read so a
  // superseded GetResource cannot linger, accumulate or run after reconnection.
  const supersedeSearchOpening = () => {
    searchIntent.current += 1;
    searchAbort.current?.abort();
    searchAbort.current = undefined;
  };
  const [searchOpening, setSearchOpening] = useState<{
    id: string;
    status: SearchOpenStatus;
  }>();
  useEffect(() => {
    searchMounted.current = true;
    return () => {
      searchMounted.current = false;
      supersedeSearchOpening();
    };
  }, []);
  // Leaving Search supersedes a pending result opening synchronously, in the
  // navigation path itself: a read can settle after the new surface commits but
  // before passive effects flush, and must not override the chosen destination.
  const showSurface = (next: Surface) => {
    if (next !== Surface.Search) supersedeSearchOpening();
    setSurfaceState(next);
  };
  // Every surface change a handler requests is a navigation intent.
  const setSurface = (next: Surface) => {
    userNavigated(NavigationIntentSource.Surface);
    showSurface(next);
  };
  useEffect(() => {
    if (surface === Surface.Search) return;
    supersedeSearchOpening();
    setSearchOpening(undefined);
  }, [surface]);
  // A new search replaces the result set, so an opening chosen from the
  // previous results is cancelled and its pending or unavailable status cleared.
  const resetSearchOpening = () => {
    supersedeSearchOpening();
    setSearchOpening(undefined);
  };
  // A same-identity reconnect keeps this shell but replaces the transport that
  // owns the pending read, which would otherwise leave its status stuck.
  useLayoutEffect(() => {
    resetSearchOpening();
  }, [transport]);
  const [selected, setSelected] = useState("");
  const [sessionNavigationActivation, setSessionNavigationActivation] =
    useState(0);
  const sessionNavigationGeneration = useRef(0);
  const [visited, setVisited] = useState<readonly string[]>([]);
  const tabs = useSessionTabsStore();
  const selectedTabs = useSessionTabs(selected);
  const sidebarSession =
    selectedTabs.tab.kind === SessionTabKind.Sidechat
      ? selectedTabs.tab.id
      : selected;
  const [selectedInbox, setSelectedInbox] = useState("");
  const [inboxActivation, setInboxActivation] = useState(0);
  const [newSessionEntry, setNewSessionEntry] = useState<{
    activation: number;
    projectId?: string;
  }>({ activation: 0 });
  const [newSessionProjectBlocked, setNewSessionProjectBlocked] =
    useState(false);
  const [newGeneralChatActivation, setNewGeneralChatActivation] = useState(
    initialRoute?.surface === Surface.NewGeneralChat ? 1 : 0,
  );
  const [notificationOccurrence, setNotificationOccurrence] = useState<{
    scheduleId: string;
    id: string;
    generation: string;
  }>();
  const [usageEntry, setUsageEntry] = useState<UsageEntry>();
  const [settingsEntry, setSettingsEntry] = useState<SettingsNavigationEntry>();
  const [routeSelection, setRouteSelection] = useState<RouteSelection>({
    inbox: "",
    schedule: "",
    repository: "",
    category: initialRoute?.category,
    tab: initialRoute?.tab,
  });
  const [restoreCategory, setRestoreCategory] = useState(
    initialRoute?.category,
  );
  const [inboxRestore, setInboxRestore] = useState<{
    id: string;
    activation: number;
  }>();
  const [scheduleRestore, setScheduleRestore] = useState<{
    resource: Resource;
    generation: string;
  }>();
  const [repositoryRestore, setRepositoryRestore] = useState<{
    id: string;
    generation: string;
  }>();
  // Screens report their selection for the URL mirror. Reporting is
  // informational: restore supersession is decided only by `navigationIntent`.
  const inboxSelected = useCallback(
    (inbox: string) => setRouteSelection((current) => ({ ...current, inbox })),
    [],
  );
  const scheduleSelected = useCallback(
    (schedule: string) =>
      setRouteSelection((current) => ({ ...current, schedule })),
    [],
  );
  const repositorySelected = useCallback(
    (repository: string) =>
      setRouteSelection((current) => ({ ...current, repository })),
    [],
  );
  const categorySelected = useCallback(
    (category: SettingsCategory) =>
      setRouteSelection((current) => ({ ...current, category })),
    [],
  );
  const usageTabSelected = useCallback(
    (tab: UsageTab) => setRouteSelection((current) => ({ ...current, tab })),
    [],
  );
  // Settings remounts for every visit and reports its own category, so the
  // category of an earlier visit is dropped when the visit ends instead of
  // describing the next one.
  useEffect(() => {
    if (surface === Surface.Settings) return;
    setRestoreCategory(undefined);
    setRouteSelection((current) =>
      current.category ? { ...current, category: undefined } : current,
    );
  }, [surface]);
  const [projectCreation, setProjectCreation] = useState<{
    id: string;
    activation: number;
  }>();
  type SessionDraft = { prompt: string; bindings: SkillTokenBinding[] };
  const [draftState, setDraftState] = useState<{
    drafts: ReadonlyMap<string, SessionDraft>;
    error?: string;
  }>({ drafts: new Map() });
  const draftOwner = useRef(draftState);
  const [draftEdits] = useState(() => new DraftEditGenerations());
  const { drafts } = draftState;
  // Keep text and typed invocation ownership atomic, including before unmount.
  const saveDraft = (
    id: string,
    prompt?: string,
    bindings?: SkillTokenBinding[],
    userEdit = false,
  ) => {
    const current = draftOwner.current,
      previous = current.drafts.get(id);
    const next = {
      prompt: prompt ?? previous?.prompt ?? "",
      bindings: bindings ?? previous?.bindings ?? [],
    };
    if (
      !userEdit &&
      ((previous?.prompt === next.prompt &&
        previous.bindings === next.bindings) ||
        (!previous && !next.prompt && !next.bindings.length))
    )
      return true;
    const size = new TextEncoder().encode(next.prompt).byteLength;
    const total = [...current.drafts].reduce(
      (bytes, [key, draft]) =>
        bytes +
        (key === id
          ? 0
          : new TextEncoder().encode(JSON.stringify(draft)).byteLength),
      new TextEncoder().encode(JSON.stringify(next)).byteLength,
    );
    if (
      size > 256 << 10 ||
      total > 4 << 20 ||
      ((next.prompt || next.bindings.length) &&
        !previous &&
        current.drafts.size >= 1000) ||
      (userEdit && !draftEdits.advance(id))
    ) {
      draftOwner.current = {
        ...current,
        error: copy("App.extra.979e130130a9"),
      };
      setDraftState(draftOwner.current);
      return false;
    }
    const drafts = new Map(current.drafts);
    if (next.prompt || next.bindings.length) drafts.set(id, next);
    else drafts.delete(id);
    draftOwner.current = { drafts };
    setDraftState(draftOwner.current);
    return true;
  };
  const sessions = useQuery(SessionQuery.listSessions, {
    projectId: "",
    includeArchived: false,
    pageSize: 50,
    pageToken: "",
  });
  const status = useQuery(
    SystemQuery.getStatus,
    {},
    { refetchInterval: 30000 },
  );
  const leaveSurface = (destination: Surface) => {
    pendingFocusDestination.current =
      surface === Surface.Settings ? destination : undefined;
    setSettingsEntry(undefined);
    if (destination !== surface) setProjectCreation(undefined);
  };
  // Shows a Session without registering a navigation intent: the route restore
  // uses it after it has verified that no user navigation happened since it began.
  const showSession = (id: string, originalParent?: string, name?: string) => {
    sessionNavigationGeneration.current += 1;
    setSessionNavigationActivation((value) => value + 1);
    const parent = tabs.parent(id) ?? originalParent;
    if (parent) {
      tabs.open(
        parent,
        tabs.sidechats(parent).find((tab) => tab.id === id) ??
          (name
            ? { kind: SessionTabKind.Sidechat, id, name }
            : {
                // Never expose the internal ID: show a localized product label
                // until the pane verifies the Session and supplies its name.
                kind: SessionTabKind.Sidechat,
                id,
                name: productLabel(PresentationKind.Session, id),
                provisional: true,
              }),
      );
      id = parent;
    }
    setVisited((previous) =>
      previous.includes(id) ? previous : [...previous, id],
    );
    leaveSurface(Surface.Sessions);
    if (id !== selected) setProjectCreation(undefined);
    setSelected(id);
    showSurface(Surface.Sessions);
    setDrawerOpen(false);
  };
  const openSession = (id: string, originalParent?: string, name?: string) => {
    userNavigated(NavigationIntentSource.SessionOpen);
    showSession(id, originalParent, name);
  };
  const openSessionRef = useRef(openSession);
  const showSessionRef = useRef(showSession);
  useLayoutEffect(() => {
    openSessionRef.current = openSession;
    showSessionRef.current = showSession;
  });
  // The top-level Session (and optional Sidechat under it) a retryable failure
  // restored without verification. The mounted top-level pane judges it through
  // `verifyRestored`; that pane keeps its metadata read active for as long as
  // the restore is unverified, whichever tab is selected, and the Sidechat's own
  // pane defers its judgment until this verification settles. An invalid
  // top-level Session (a Sidechat, malformed provenance, a definitively
  // rejected read) ends the whole provisional pair, so a malformed URL cannot
  // leave an unverified Session or Sidechat selected.
  const unverifiedRestore = useRef<{ id: string; sidechat?: string }>(
    undefined,
  );
  const [unverifiedRestoreId, setUnverifiedRestoreId] = useState("");
  const verifyRestoredSession = useCallback(
    (valid: boolean) => {
      const pending = unverifiedRestore.current;
      if (!pending) return;
      unverifiedRestore.current = undefined;
      setUnverifiedRestoreId("");
      if (valid) return;
      console.warn("delidev.product_route.restore_rejected", {
        surface: Surface.Sessions,
        reason: "unverified-session-invalid",
      });
      // The provisional pair is the restore's own state, so it ends whatever
      // the user did since; only a different selection is left alone.
      if (pending.sidechat)
        tabs.discardProvisionalSidechat(pending.id, pending.sidechat);
      setVisited((previous) => previous.filter((id) => id !== pending.id));
      setSelected((current) => (current === pending.id ? "" : current));
    },
    [tabs],
  );
  // Verifies the route selection with fresh reads through the current
  // connection. It waits for connection readiness, never dispatches a mutation
  // and reopens only what this connection can read. The restore captures the
  // navigation-intent token when it starts; any user navigation since then
  // aborts it and fails `current()`, so it applies nothing.
  useEffect(() => {
    if (!pendingSelection || !connectionReady) return;
    const pending = pendingSelection;
    const owner = transport;
    const ownerEpoch = connectionEpoch;
    const controller = new AbortController();
    restoreAbort.current = controller;
    const intent = navigationIntent.current;
    const current = () =>
      !controller.signal.aborted &&
      latestTransport.current === owner &&
      latestEpoch.current === ownerEpoch &&
      navigationIntent.current === intent;
    const settle = (outcome: RouteReadOutcome | "superseded") => {
      console.info("delidev.product_route.restore", {
        surface: pending.surface,
        outcome,
      });
      restorePending.current = false;
      if (restoreAbort.current === controller) restoreAbort.current = undefined;
      setPendingSelection(undefined);
    };
    void (async () => {
      switch (pending.surface) {
        case Surface.Sessions: {
          const read = await readRouteSession(
            queryClient,
            owner,
            pending,
            controller.signal,
          );
          if (!current()) return;
          settle(read.outcome);
          if (read.outcome === RouteReadOutcome.Missing) return;
          // A retryable failure still opens the deepest requested selection;
          // the mounted view owns its existing retry and error state.
          showSessionRef.current(
            read.open.id,
            read.open.parent,
            read.open.name,
          );
          // A retryable failure opened a Session that was never verified as a
          // top-level Session: its pane judges it once it loads.
          if (read.outcome === RouteReadOutcome.Failed) {
            const top = read.open.parent ?? read.open.id;
            unverifiedRestore.current = {
              id: top,
              sidechat: read.open.parent ? read.open.id : undefined,
            };
            setUnverifiedRestoreId(top);
          }
          return;
        }
        case Surface.Inbox: {
          const read = await readRouteInboxEntry(
            queryClient,
            owner,
            pending.id,
            controller.signal,
          );
          if (!current()) return;
          settle(read.outcome);
          if (read.outcome === RouteReadOutcome.Missing) return;
          setRouteSelection((previous) => ({ ...previous, inbox: pending.id }));
          setInboxRestore((previous) => ({
            id: pending.id,
            activation: (previous?.activation ?? 0) + 1,
          }));
          return;
        }
        case Surface.Schedules: {
          const read = await readRouteSchedule(
            queryClient,
            owner,
            pending.id,
            controller.signal,
          );
          if (!current()) return;
          settle(read.outcome);
          if (read.outcome !== RouteReadOutcome.Found) return;
          setRouteSelection((previous) => ({
            ...previous,
            schedule: pending.id,
          }));
          setScheduleRestore({
            resource: read.value,
            generation: newRequestId(),
          });
          return;
        }
        case Surface.PullRequests: {
          const read = await readRouteRepository(
            queryClient,
            owner,
            pending.id,
            controller.signal,
          );
          if (!current()) return;
          settle(read.outcome);
          // The mounted screen owns the retry and error state of a failed read.
          if (read.outcome === RouteReadOutcome.Missing) return;
          setRouteSelection((previous) => ({
            ...previous,
            repository: pending.id,
          }));
          setRepositoryRestore({ id: pending.id, generation: newRequestId() });
          return;
        }
        default:
          settle("superseded");
      }
    })();
    return () => {
      controller.abort();
      if (restoreAbort.current === controller) restoreAbort.current = undefined;
    };
  }, [
    pendingSelection,
    connectionReady,
    transport,
    connectionEpoch,
    queryClient,
  ]);
  // Mirror committed navigation into the URL query with history.replaceState.
  // The URL keeps the original route until its selection has been verified. The
  // route derives only from the visible surface's own state through the table in
  // `product-route-mirror.ts`: a selection retained by a hidden workspace (the
  // Schedule behind an occurrence review, a category of an earlier Settings
  // visit) never describes another destination.
  const sidechatRoute =
    selectedTabs.tab.kind === SessionTabKind.Sidechat
      ? selectedTabs.tab.id
      : undefined;
  const mirrorRestore = pendingSelection
    ? MirrorRestoreState.Pending
    : unverifiedRestoreId
      ? MirrorRestoreState.Provisional
      : MirrorRestoreState.Settled;
  const mirrorSchedules = notificationOccurrence
    ? SchedulesMode.OccurrenceReview
    : SchedulesMode.Workspace;
  useEffect(() => {
    const route = deriveProductRoute({
      surface,
      restore: mirrorRestore,
      schedules: mirrorSchedules,
      session: selected,
      sidechat: sidechatRoute,
      inbox: routeSelection.inbox,
      schedule: routeSelection.schedule,
      repository: routeSelection.repository,
      category: routeSelection.category,
      tab: routeSelection.tab,
    });
    if (route) writeProductRoute(route);
  }, [
    mirrorRestore,
    mirrorSchedules,
    surface,
    selected,
    sidechatRoute,
    routeSelection,
  ]);
  // Any deliberate navigation supersedes a pending Search-result resolution.
  const open = (id: string, originalParent?: string, name?: string) => {
    supersedeSearchOpening();
    openSession(id, originalParent, name);
  };
  // A never-visited Sidechat result carries only its child ID, so its original
  // parent is read from retained metadata before the host is chosen.
  // A modified click opens the result in a new window on this connection. The
  // opening shares the Search status presentation and cancellation rules, but
  // never changes this window's selection, surface or drawer.
  const openSearchResultInWindow = (id: string, surface: string) =>
    destinationWindow.run(async () => {
      supersedeSearchOpening();
      const token = searchIntent.current;
      const parent = tabs.parent(id);
      let route: ProductRoute;
      if (parent) {
        route = { surface: Surface.Sessions, session: parent, sidechat: id };
      } else {
        const controller = new AbortController();
        searchAbort.current = controller;
        const owner = transport;
        setSearchOpening({ id, status: SearchOpenStatus.Pending });
        // Destination window code paths log no identifiers, so this read
        // reports only its closed unavailable reason.
        const resolution = await resolveSearchSession(
          queryClient,
          owner,
          id,
          controller.signal,
          SearchResolutionLogging.Anonymous,
        );
        if (searchAbort.current === controller) searchAbort.current = undefined;
        if (
          !searchMounted.current ||
          searchIntent.current !== token ||
          latestTransport.current !== owner
        )
          return;
        if (resolution.route === SearchSessionRoute.Unavailable) {
          setSearchOpening({ id, status: SearchOpenStatus.Unavailable });
          return;
        }
        setSearchOpening(undefined);
        route =
          resolution.route === SearchSessionRoute.Sidechat
            ? {
                surface: Surface.Sessions,
                session: resolution.parent,
                sidechat: id,
              }
            : { surface: Surface.Sessions, session: id };
      }
      await openDestinationWindow(route);
    }, surface);
  const openSearchResult = (id: string, event?: DestinationClick) => {
    if (event && destinationWindow.claim(event)) {
      void openSearchResultInWindow(id, destinationSurface(event));
      return;
    }
    userNavigated(NavigationIntentSource.SearchResult);
    if (tabs.parent(id)) {
      open(id);
      return;
    }
    supersedeSearchOpening();
    const token = searchIntent.current;
    const controller = new AbortController();
    searchAbort.current = controller;
    const owner = transport;
    setSearchOpening({ id, status: SearchOpenStatus.Pending });
    void resolveSearchSession(queryClient, owner, id, controller.signal).then(
      (resolution) => {
        if (searchAbort.current === controller) searchAbort.current = undefined;
        if (
          !searchMounted.current ||
          searchIntent.current !== token ||
          latestTransport.current !== owner
        ) {
          console.info("delidev.search.session_route_discarded", {
            sessionId: id,
          });
          return;
        }
        if (resolution.route === SearchSessionRoute.Unavailable) {
          setSearchOpening({ id, status: SearchOpenStatus.Unavailable });
          return;
        }
        setSearchOpening(undefined);
        if (resolution.route === SearchSessionRoute.Sidechat)
          openSessionRef.current(id, resolution.parent, resolution.name);
        else openSessionRef.current(id);
      },
    );
  };
  const navigateTray = (destination: TrayDestination, inboxId?: string) => {
    if (destination === TrayDestination.Settings) {
      openSettings();
      return;
    }
    if (destination === TrayDestination.ConnectionDiagnostics) {
      openSettings(SettingsEntryDestination.ConnectionDiagnostics);
      return;
    }
    const next =
      destination === TrayDestination.Inbox
        ? Surface.Inbox
        : destination === TrayDestination.Usage
          ? Surface.Usage
          : Surface.Sessions;
    leaveSurface(next);
    if (destination === TrayDestination.Inbox) {
      setSelectedInbox(inboxId ?? "");
      setInboxActivation((value) => value + 1);
    }
    setDrawerOpen(false);
    setSurface(next);
  };
  const openSettings = (destination?: SettingsNavigationEntry) => {
    setProjectCreation(undefined);
    // Selecting the active rail item keeps the current visit and deferred entry.
    if (surface !== Surface.Settings || destination) {
      pendingFocusDestination.current = Surface.Settings;
      setSettingsEntry(destination);
    }
    setDrawerOpen(false);
    setSurface(Surface.Settings);
  };
  const consumeSettingsEntry = useCallback(
    () => setSettingsEntry(undefined),
    [],
  );
  const openNewProject = () => {
    userNavigated(NavigationIntentSource.ProjectCreation);
    setDrawerOpen(false);
    setProjectCreation((current) =>
      current
        ? { ...current, activation: current.activation + 1 }
        : { id: newRequestId(), activation: 0 },
    );
  };
  const closeProjectCreation = useCallback(
    () => setProjectCreation(undefined),
    [],
  );
  const projectFallbackFocus = useCallback(
    () =>
      typeof window.matchMedia === "function" &&
      window.matchMedia("(max-width: 759px)").matches
        ? contextOpener.current
        : main.current,
    [],
  );
  const openOperationalNotification = (resource: Resource) => {
    if (
      resource.kind === EntityKind.MACHINE ||
      resource.kind === EntityKind.ACCOUNT
    ) {
      openSettings({
        category:
          resource.kind === EntityKind.MACHINE
            ? SettingsCategory.ExecutionWorkers
            : SettingsCategory.SubscriptionAccounts,
        generation: newRequestId(),
        resourceId: resource.id,
        resourceKind: resource.kind,
      });
      return;
    }
    if (resource.kind === EntityKind.OCCURRENCE) {
      setNotificationOccurrence({
        id: resource.id,
        scheduleId: text(document(resource).schedule_id),
        generation: newRequestId(),
      });
      leaveSurface(Surface.Schedules);
      setSurface(Surface.Schedules);
      setDrawerOpen(false);
    }
  };
  const surfaceName =
    surface === Surface.Sessions ||
    surface === Surface.NewSession ||
    surface === Surface.NewGeneralChat
      ? copy("App.extra.2998edd080d1")
      : surface === Surface.Settings
        ? copy("App.extra.a1de4eceaa3b")
        : surface === Surface.PullRequests
          ? copy("App.extra.23533b15bc29")
          : surface === Surface.Usage
            ? copy("App.extra.34d76f3f7da4")
            : surface === Surface.Schedules
              ? copy("App.extra.a6a986427e87")
              : surface === Surface.Inbox
                ? copy("App.extra.a1de2be5c09b")
                : copy("App.extra.1f73d5f3eac5");
  const startNewSession = (projectId?: string) => {
    if (projectId && newSessionProjectBlocked) return;
    leaveSurface(Surface.NewSession);
    setDrawerOpen(false);
    setNewSessionEntry((current) => ({
      activation: current.activation + 1,
      projectId,
    }));
    setSurface(Surface.NewSession);
  };
  const startNewGeneralChat = () => {
    leaveSurface(Surface.NewGeneralChat);
    setDrawerOpen(false);
    setNewGeneralChatActivation((value) => value + 1);
    setSurface(Surface.NewGeneralChat);
  };
  const navigate = (destination: Surface) => {
    leaveSurface(destination);
    setDrawerOpen(false);
    setSurface(destination);
    if (destination === Surface.Inbox) setSelectedInbox("");
  };
  const openAccountUsage = (entry: UsageEntry) => {
    setUsageEntry(entry);
    navigate(Surface.Usage);
  };
  const navigateHeader = (destination: Surface.Inbox | Surface.Search) => {
    navigate(destination);
    pendingFocusDestination.current = destination;
  };
  const sidebarShortcutEnabled = !sidebarPreference.operation;
  const shortcutDeclarations = useShortcuts([
    {
      id: ShortcutId.ToggleSidebar,
      scope: ShortcutScope.Global,
      label: "sidebar-preference.toggle",
      bindings: globalShortcutBindings[ShortcutId.ToggleSidebar],
      input: ShortcutInput.Allow,
      active: wideSidebar,
      enabled: sidebarShortcutEnabled,
      run: toggleSidebar,
    },
    {
      id: ShortcutId.CommandMenu,
      scope: ShortcutScope.Global,
      label: "command-menu.title",
      bindings: globalShortcutBindings[ShortcutId.CommandMenu],
      input: ShortcutInput.Allow,
      run: toggleCommandMenu,
    },
    {
      id: ShortcutId.OpenSettings,
      scope: ShortcutScope.Global,
      label: "shortcuts.openSettings",
      bindings: globalShortcutBindings[ShortcutId.OpenSettings],
      input: ShortcutInput.Allow,
      run: () => openSettings(),
    },
    {
      id: ShortcutId.Help,
      scope: ShortcutScope.Global,
      label: "shortcuts.help",
      bindings: globalShortcutBindings[ShortcutId.Help],
      helpKeydown: holdHelp,
    },
    {
      id: ShortcutId.NewSession,
      scope: ShortcutScope.Global,
      label: "shortcuts.newSession",
      bindings: globalShortcutBindings[ShortcutId.NewSession],
      input: ShortcutInput.Allow,
      run: startNewSession,
    },
    {
      // Help and ARIA listing only; the window-level ReloadShortcutOwner above
      // App executes the reload before ordinary dispatch can see the event.
      id: ShortcutId.ReloadPage,
      scope: ShortcutScope.Global,
      label: "shortcuts.reloadPage",
      bindings: globalShortcutBindings[ShortcutId.ReloadPage],
      input: ShortcutInput.Allow,
      execution: ShortcutExecution.Native,
    },
  ]);
  useLayoutEffect(() => {
    const destination = pendingFocusDestination.current;
    if (!destination) return;
    if (destination !== surface) {
      pendingFocusDestination.current = undefined;
      return;
    }
    // The compact drawer closes in a child layout effect. Keep this request
    // until that state update lets the wide destination receive focus.
    if (drawerOpen) return;
    pendingFocusDestination.current = undefined;
    // Search's existing first-entry animation frame remains authoritative, with
    // no later focus handoff after the visible destination receives focus.
    const compact =
      typeof window.matchMedia === "function" &&
      window.matchMedia("(max-width: 759px)").matches;
    const target = compact ? contextOpener.current : main.current;
    if (target && !target.closest("[hidden], [inert]"))
      target.focus({ preventScroll: true });
  }, [surface, drawerOpen, settingsEntry]);
  return (
    <NavigationIntentProvider value={userNavigated}>
      <DestinationWindowProvider value={destinationWindow}>
        <LocalConnectionPresentationProvider
          target={connectionTarget}
          inline={!connectionReady}
          pageOpen={connectionPageOpen}
          onRequest={onConnectionHelp}
        >
          <RunnerRemediationProvider
            active
            connectionReady={connectionReady}
            authority={pairingAuthority}
          >
            <RunnerPreferenceProvider
              readLocalWorker={readLocalWorker}
              scope={
                pairingAuthority && currentDeviceId
                  ? {
                      server_id: pairingAuthority.serverId,
                      device_id: currentDeviceId,
                    }
                  : undefined
              }
            >
              <SessionControlProvider>
                <SessionForkProvider
                  openSession={open}
                  sessionNavigationGeneration={sessionNavigationGeneration}
                  readLocalWorker={readLocalWorker}
                >
                  <SessionStorageProvider>
                    <SidebarOutletProvider
                      target={sidebarTarget}
                      closeDrawer={() => setDrawerOpen(false)}
                      drawerOpen={drawerOpen}
                      paneVisible={sidebarPaneVisible}
                      paneSettled={
                        !wideSidebar ||
                        (sidebarPresentation.settled &&
                          sidebarPresentation.collapsed === sidebarCollapsed)
                      }
                      paneGeneration={sidebarPresentation.generation}
                      paneFocusAllowed={sidebarPresentation.focusAllowed}
                      paneProblem={Boolean(sidebarPreference.snapshot.problem)}
                      openDrawer={openSidebar}
                    >
                      <div
                        className={`app${wideSidebar && sidebarCollapsed ? " sidebar-collapsed" : ""}`}
                      >
                        <a className="skip" href="#main">
                          {copy("App.skipToContent_ac576a")}
                        </a>
                        <Sidebar
                          collapsed={sidebarCollapsed}
                          motionReady={
                            sidebarPreference.snapshot.revision > 0 &&
                            !sidebarPreference.snapshot.problem
                          }
                          motionScope={transport}
                          presentationChanged={setSidebarPresentation}
                          compactFocusRef={contextOpener}
                          focusHandoffPending={
                            pendingFocusDestination.current !== undefined
                          }
                          openCommandMenu={toggleCommandMenu}
                          connectionReady={connectionReady}
                          surface={surface}
                          selectedSessionId={sidebarSession}
                          selectedSessionActivation={
                            sessionNavigationActivation
                          }
                          navigate={navigate}
                          navigateHeader={navigateHeader}
                          openSession={open}
                          newSession={startNewSession}
                          newGeneralChat={startNewGeneralChat}
                          newProject={openNewProject}
                          projectSelectionBlocked={newSessionProjectBlocked}
                          openSettings={openSettings}
                          setContextTarget={setSidebarTarget}
                          drawerOpen={drawerOpen}
                          setDrawerOpen={setDrawerOpen}
                        />
                        {commandMenuOpen ? (
                          <CommandMenu
                            openSession={open}
                            close={() => setCommandMenuOpen(false)}
                            commands={applicationCommands(
                              {
                                navigate,
                                navigateHeader,
                                openSettings,
                                newSession: startNewSession,
                                newGeneralChat: startNewGeneralChat,
                                newProject: openNewProject,
                                help: openHelp,
                              },
                              { tailscale: runnerTailscale !== undefined },
                            )}
                          />
                        ) : null}
                        <main
                          ref={main}
                          id="main"
                          tabIndex={-1}
                          aria-keyshortcuts={
                            wideSidebar && sidebarShortcutEnabled
                              ? shortcutDeclarations.aria(
                                  ShortcutId.ToggleSidebar,
                                )
                              : undefined
                          }
                        >
                          <DestinationWindowNotice />
                          <button
                            ref={contextOpener}
                            type="button"
                            className="sidebar-context-trigger"
                            aria-haspopup="dialog"
                            aria-expanded={drawerOpen}
                            onClick={() => setDrawerOpen(true)}
                          >
                            <LocalizedText
                              id="App.open_a007d6"
                              components={{ s0: <>{surfaceName}</> }}
                            />
                          </button>
                          <TrayPresentation navigate={navigateTray} />
                          <NotificationPresentation />
                          <InboxBadgePresentation
                            ready={connectionReady}
                            supported={
                              Boolean(
                                status.data?.capabilities.includes(
                                  SystemCapability.UNREAD_INBOX_COUNT_V1,
                                ),
                              ) && !status.isError
                            }
                          />
                          {draftState.error ? (
                            <p role="alert">{draftState.error}</p>
                          ) : null}
                          {[...drafts.keys()].map((id) => (
                            <SessionDraftSettlement
                              key={id}
                              id={id}
                              clear={() => {
                                saveDraft(id, "", []);
                              }}
                            />
                          ))}
                          {projectCreation ? (
                            <ProjectCreationDialog
                              registrationAdapters={{
                                readLocalWorker,
                                controlLocalWorker,
                                chooseFolder: chooseRepositoryFolder,
                              }}
                              key={projectCreation.id}
                              activation={projectCreation.activation}
                              close={closeProjectCreation}
                              fallbackFocus={projectFallbackFocus}
                            />
                          ) : null}
                          <div
                            hidden={surface !== Surface.Sessions}
                            className="session-container"
                          >
                            {visited.map((id) => (
                              <div
                                key={id}
                                hidden={id !== selected}
                                inert={id !== selected}
                              >
                                <SessionView
                                  active={
                                    surface === Surface.Sessions &&
                                    id === selected
                                  }
                                  connectionReady={connectionReady}
                                  id={id}
                                  draft={drafts.get(id)?.prompt ?? ""}
                                  initialSkills={drafts.get(id)?.bindings}
                                  draftGeneration={() => draftEdits.read(id)}
                                  setDraft={(value, bindings, userEdit) =>
                                    saveDraft(id, value, bindings, userEdit)
                                  }
                                  changeSkills={(bindings) => {
                                    saveDraft(id, undefined, bindings);
                                  }}
                                  openRunnerSettings={() =>
                                    openSettings(
                                      SettingsEntryDestination.RunnerDevices,
                                    )
                                  }
                                  verifyRestored={
                                    id === unverifiedRestoreId
                                      ? verifyRestoredSession
                                      : undefined
                                  }
                                />
                              </div>
                            ))}
                            {!selected &&
                            pendingSelection?.surface !== Surface.Sessions ? (
                              <section
                                className="page welcome"
                                {...welcomeEntrance}
                              >
                                <h2>
                                  {copy("App.yourSessionsInOnePlace_5dad94")}
                                </h2>
                                <Problem
                                  error={status.error}
                                  actions={
                                    <button
                                      type="button"
                                      disabled={
                                        status.isFetching || !connectionReady
                                      }
                                      onClick={() => void status.refetch()}
                                    >
                                      {copy("ui.retryCurrentRead")}
                                    </button>
                                  }
                                />
                              </section>
                            ) : null}
                          </div>
                          <NewSession
                            preferenceScope={
                              pairingAuthority && currentDeviceId
                                ? {
                                    server_id: pairingAuthority.serverId,
                                    device_id: currentDeviceId,
                                  }
                                : undefined
                            }
                            active={surface === Surface.NewSession}
                            ownsActivation={surface === Surface.NewSession}
                            activation={newSessionEntry.activation}
                            entryProjectId={newSessionEntry.projectId}
                            projectSelectionBlockedChanged={
                              setNewSessionProjectBlocked
                            }
                            readLocalWorker={readLocalWorker}
                            back={() => {
                              navigate(Surface.Sessions);
                              void sessions.refetch();
                            }}
                            openSettings={openSettings}
                            newProject={openNewProject}
                            open={open}
                            created={sessionCreated}
                          />
                          {newGeneralChatActivation > 0 ? (
                            <NewSession
                              preferenceScope={
                                pairingAuthority && currentDeviceId
                                  ? {
                                      server_id: pairingAuthority.serverId,
                                      device_id: currentDeviceId,
                                    }
                                  : undefined
                              }
                              kind={NewSessionKind.GeneralChat}
                              active={surface === Surface.NewGeneralChat}
                              ownsActivation={
                                surface === Surface.NewGeneralChat
                              }
                              activation={newGeneralChatActivation}
                              back={() => {
                                navigate(Surface.Sessions);
                                void sessions.refetch();
                              }}
                              openSettings={openSettings}
                              open={open}
                              created={sessionCreated}
                            />
                          ) : null}
                          <Search
                            active={surface === Surface.Search}
                            open={openSearchResult}
                            opening={searchOpening}
                            submitted={resetSearchOpening}
                          />
                          <div
                            className="inbox-container"
                            hidden={surface !== Surface.Inbox}
                          >
                            <Inbox
                              active={surface === Surface.Inbox}
                              open={open}
                              notificationId={selectedInbox}
                              notificationActivation={inboxActivation}
                              openOperational={openOperationalNotification}
                              restoreId={inboxRestore?.id}
                              restoreActivation={inboxRestore?.activation}
                              selectionChanged={inboxSelected}
                            />
                          </div>
                          <Usage
                            active={surface === Surface.Usage}
                            open={open}
                            entry={usageEntry}
                            initialTab={initialRoute?.tab}
                            tabChanged={usageTabSelected}
                          />
                          <Schedules
                            readLocalWorker={readLocalWorker}
                            active={surface === Surface.Schedules}
                            open={open}
                            notificationOccurrence={notificationOccurrence}
                            clearNotificationOccurrence={() =>
                              setNotificationOccurrence(undefined)
                            }
                            restore={scheduleRestore}
                            selectionChanged={scheduleSelected}
                          />
                          <PullRequests
                            active={surface === Surface.PullRequests}
                            openSettings={openSettings}
                            restore={repositoryRestore}
                            selectionChanged={repositorySelected}
                          />
                          <Settings
                            openUsage={openAccountUsage}
                            readLocalWorker={readLocalWorker}
                            connectionSettings={connectionSettings}
                            appInformationSettings={appInformationSettings}
                            runnerTailscale={runnerTailscale}
                            pairingAuthority={pairingAuthority}
                            currentDeviceId={currentDeviceId}
                            controlLocalWorker={controlLocalWorker}
                            chooseRepositoryFolder={chooseRepositoryFolder}
                            visible={surface === Surface.Settings}
                            entryDestination={settingsEntry}
                            destinationConsumed={consumeSettingsEntry}
                            initialCategory={restoreCategory}
                            categoryChanged={categorySelected}
                          />
                          {localServer}
                        </main>
                      </div>
                    </SidebarOutletProvider>
                  </SessionStorageProvider>
                </SessionForkProvider>
              </SessionControlProvider>
            </RunnerPreferenceProvider>
          </RunnerRemediationProvider>
        </LocalConnectionPresentationProvider>
      </DestinationWindowProvider>
    </NavigationIntentProvider>
  );
}

const transportScopes = new WeakMap<Transport, string>();
/**
 * The product-route identity scope of a connection identity. A saved identity is
 * its own scope; an unpaired connection is scoped by its transport object, so
 * two different transports never share a route. The lookup is idempotent for a
 * given transport, which keeps it safe to call during render.
 */
function routeScopeOf(identity: string | Transport): string {
  if (typeof identity === "string") return identity;
  let scope = transportScopes.get(identity);
  if (!scope) {
    scope = `transport:${newRequestId()}`;
    transportScopes.set(identity, scope);
  }
  return scope;
}

// Reconnects for one server/device retain this memory and its mutation receipts
// even when authentication creates a replacement transport. Selecting another
// identity creates a fresh query, draft and mutation scope.
export function App({
  transport,
  localServer,
  connectionSettings,
  appInformationSettings,
  runnerTailscale,
  connectionTarget,
  connectionPageOpen = false,
  onConnectionHelp,
  connectionReady = true,
  connectionEpoch = 0,
  readLocalWorker,
  controlLocalWorker,
  chooseRepositoryFolder,
  currentDeviceId,
  pairingAuthority,
}: {
  pairingAuthority?: PairingAuthority;
  currentDeviceId?: string;
  controlLocalWorker?: ControlLocalWorker;
  chooseRepositoryFolder?: ChooseRepositoryFolder;
  readLocalWorker?: ReadLocalWorkerProof;
  transport: Transport;
  localServer?: ReactNode;
  connectionSettings?: ReactNode;
  appInformationSettings?: ReactNode;
  runnerTailscale?: PublishRunnerTailscaleSlots;
  connectionTarget?: HTMLElement;
  /** True while the local window's Connections page is mounted. */
  connectionPageOpen?: boolean;
  onConnectionHelp?: (target: HTMLElement | undefined) => void;
  connectionReady?: boolean;
  connectionEpoch?: number;
}) {
  useLocale();
  const connectionIdentity =
    pairingAuthority && currentDeviceId
      ? JSON.stringify([
          pairingAuthority.endpoint,
          pairingAuthority.serverId,
          currentDeviceId,
        ])
      : transport;
  const connection = useMemo(
    () => ({ id: newRequestId(), ...connectionQueryClient() }),
    [connectionIdentity],
  );
  const client = connection.client;
  useEffect(() => connection.activate(), [connection]);
  useEffect(() => {
    if (connectionReady)
      void client.invalidateQueries({ refetchType: "active" });
  }, [client, connectionReady, connectionEpoch]);
  return (
    <SidebarPreferenceBoundary>
      <TransportProvider transport={transport}>
        <QueryClientProvider key={connection.id} client={client}>
          <ConnectionEpochProvider epoch={connectionEpoch}>
            <ProductIdentityProvider>
              <NotificationProvider>
                <MutationIntents>
                  <SubscriptionQuotaAutoRefresh ready={connectionReady} />
                  <SessionTabsProvider>
                    <SessionSubmissionsProvider>
                      <ImageDraftProvider>
                        <PRWorkflowProvider>
                          <ShortcutProvider>
                            <SessionNameEditorProvider>
                              <SessionInboxReadBoundary ready={connectionReady}>
                                <Shell
                                  routeScope={routeScopeOf(connectionIdentity)}
                                  connectionReady={connectionReady}
                                  pairingAuthority={pairingAuthority}
                                  currentDeviceId={currentDeviceId}
                                  controlLocalWorker={controlLocalWorker}
                                  chooseRepositoryFolder={
                                    chooseRepositoryFolder
                                  }
                                  connectionSettings={connectionSettings}
                                  appInformationSettings={
                                    appInformationSettings
                                  }
                                  runnerTailscale={runnerTailscale}
                                  connectionTarget={connectionTarget}
                                  connectionPageOpen={connectionPageOpen}
                                  onConnectionHelp={onConnectionHelp}
                                  localServer={localServer}
                                  readLocalWorker={readLocalWorker}
                                />
                              </SessionInboxReadBoundary>
                            </SessionNameEditorProvider>
                          </ShortcutProvider>
                        </PRWorkflowProvider>
                      </ImageDraftProvider>
                    </SessionSubmissionsProvider>
                  </SessionTabsProvider>
                </MutationIntents>
              </NotificationProvider>
            </ProductIdentityProvider>
          </ConnectionEpochProvider>
        </QueryClientProvider>
      </TransportProvider>
    </SidebarPreferenceBoundary>
  );
}

// The connection owns submitted drafts even while another Session is mounted.
function SessionDraftSettlement({
  id,
  clear,
}: {
  id: string;
  clear: () => void;
}) {
  useRetainedMutationAccepted(`enqueue:${id}`, clear);
  return null;
}
