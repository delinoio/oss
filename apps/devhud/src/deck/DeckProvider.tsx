import { DeckViewService } from "@delinoio/devhud-deck-connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
  type InfiniteData,
} from "@tanstack/react-query";
import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";

import {
  DeckFailureCode,
  DeckFreshness,
  DeckProductError,
  type DeckConflict,
  type DeckGateway,
  type DeckMutationCandidatePage,
  type DeckMutationInput,
  type DeckMutationKind,
  type DeckOwner,
  type DeckPullRequest,
  type DeckPullRequestPage,
  type DeckView,
  type DeckViewInput,
} from "./contracts";
import type { ManualRefreshWarning } from "./refreshController";
import { createUuidV7 } from "./uuid";
import { openDeckPullRequest } from "./transport";
import type { DeckWidgetFamily, DeckWidgetPrivacy } from "../persistence/contracts";

// Gateway injection keeps component tests native-free, while Connect Query's
// generated-service key keeps every server-state cache operation namespaced to
// the canonical RPC descriptor.
const viewServiceKey = createConnectQueryKey({
  schema: DeckViewService,
  cardinality: undefined,
});

interface DeckState {
  readonly owners: readonly DeckOwner[];
  readonly selectedOwner: DeckOwner | null;
  readonly views: readonly DeckView[];
  readonly selectedView: DeckView | null;
  readonly pullRequests: readonly DeckPullRequest[];
  readonly conflict: DeckConflict<DeckViewInput> | null;
  readonly online: boolean;
  readonly loadingOwners: boolean;
  readonly loadingViews: boolean;
  readonly loadingPullRequests: boolean;
  readonly viewCursor: string;
  readonly pullRequestCursor: string;
  readonly truncated: boolean;
  readonly resultLimit: number;
  readonly freshness: string | null;
  readonly error: unknown;
  readonly busy: boolean;
  readonly manualRefreshWarning: ManualRefreshWarning | null;
}

interface DeckActions {
  selectOwner(owner: DeckOwner): void;
  selectView(view: DeckView | null): void;
  createView(owner: DeckOwner, input: DeckViewInput): Promise<boolean>;
  saveView(view: DeckView, input: DeckViewInput): Promise<boolean>;
  deleteView(view: DeckView): Promise<boolean>;
  reloadConflict(): Promise<void>;
  reapplyConflict(): Promise<boolean>;
  dismissConflict(): void;
  loadMoreViews(): Promise<void>;
  loadMorePullRequests(): Promise<void>;
  refresh(): Promise<boolean>;
  createWidgetConfiguration(
    viewId: string,
    family: DeckWidgetFamily,
    privacy: DeckWidgetPrivacy,
  ): Promise<boolean>;
  resolveManualRefresh(confirmed: boolean): void;
  searchMutationCandidates(
    pullRequest: DeckPullRequest,
    kind: DeckMutationKind,
    query: string,
    cursor: string,
  ): Promise<DeckMutationCandidatePage | null>;
  mutate(pullRequest: DeckPullRequest, input: DeckMutationInput): Promise<boolean>;
  openOnGitHub(pullRequest: DeckPullRequest): Promise<boolean>;
  retry(): Promise<void>;
}

interface DeckMeta {
  readonly gateway: DeckGateway;
}

type DeckWidgetActionEvent =
  | { readonly action: "open-view"; readonly viewId: string }
  | { readonly action: "refresh"; readonly viewId: string }
  | {
      readonly action: "open-pr";
      readonly viewId: string;
      readonly owner: string;
      readonly repository: string;
      readonly number: number;
    }
  | { readonly action: "resolve-event"; readonly eventId: string };

export interface DeckContextValue {
  readonly state: DeckState;
  readonly actions: DeckActions;
  readonly meta: DeckMeta;
}

const DeckContext = createContext<DeckContextValue | null>(null);

function isStaleRevision(error: unknown): boolean {
  return error instanceof DeckProductError && error.code === DeckFailureCode.StaleRevision;
}

function shouldClearShortcuts(error: unknown): boolean {
  return error instanceof DeckProductError && (
    error.code === DeckFailureCode.AuthenticationRequired ||
    error.code === DeckFailureCode.PermissionDenied ||
    error.code === DeckFailureCode.GitHubPermissionDenied ||
    error.code === DeckFailureCode.Disconnected
  );
}

export function DeckProvider({
  children,
  gateway,
}: {
  readonly children: ReactNode;
  readonly gateway: DeckGateway;
}) {
  const queryClient = useQueryClient();
  const [selectedOwner, setSelectedOwner] = useState<DeckOwner | null>(null);
  const [selectedView, setSelectedView] = useState<DeckView | null>(null);
  const [conflict, setConflict] = useState<DeckConflict<DeckViewInput> | null>(null);
  const [online, setOnline] = useState(() => navigator.onLine);
  const [inFlightOperations, setInFlightOperations] = useState(0);
  const [operationError, setOperationError] = useState<unknown>(null);
  const [manualRefreshWarning, setManualRefreshWarning] = useState<ManualRefreshWarning | null>(null);
  const [manualRefreshResolver, setManualRefreshResolver] = useState<((confirmed: boolean) => void) | null>(null);

  useEffect(() => {
    const update = () => setOnline(navigator.onLine);
    window.addEventListener("online", update);
    window.addEventListener("offline", update);
    return () => {
      window.removeEventListener("online", update);
      window.removeEventListener("offline", update);
    };
  }, []);

  const ownersQuery = useQuery({
    queryKey: [...viewServiceKey, "ListOwners"],
    queryFn: () => gateway.listOwners(),
    enabled: online,
    gcTime: 0,
    retry: false,
    staleTime: 0,
  });
  const owners = useMemo(
    () => online && ownersQuery.error === null ? (ownersQuery.data ?? []) : [],
    [online, ownersQuery.data, ownersQuery.error],
  );

  useEffect(() => {
    if (!online || !ownersQuery.isSuccess) return;
    const current = selectedOwner === null
      ? undefined
      : owners.find((owner) =>
        owner.ownerId === selectedOwner.ownerId && owner.kind === selectedOwner.kind
      );
    if (current !== undefined) {
      if (current !== selectedOwner) setSelectedOwner(current);
      return;
    }
    setSelectedOwner(owners[0] ?? null);
    setSelectedView(null);
    setConflict(null);
  }, [online, owners, ownersQuery.isSuccess, selectedOwner]);

  const viewsQuery = useInfiniteQuery({
    queryKey: [...viewServiceKey, "ListViews", selectedOwner?.ownerId ?? ""],
    queryFn: ({ pageParam }) => gateway.listViews(selectedOwner!, pageParam),
    enabled: online && selectedOwner !== null,
    initialPageParam: "",
    getNextPageParam: (page) => page.nextCursor || undefined,
    gcTime: 0,
    retry: false,
    staleTime: 0,
  });
  const views = useMemo(
    () => online && viewsQuery.error === null
      ? (viewsQuery.data?.pages.flatMap((page) => page.items) ?? [])
      : [],
    [online, viewsQuery.data?.pages, viewsQuery.error],
  );
  const selectedViewId = selectedView?.viewId ?? null;

  useEffect(() => {
    if (selectedViewId === null) return;
    const current = views.find((view) => view.viewId === selectedViewId);
    if (current !== undefined) {
      setSelectedView(current);
      return;
    }
    if (viewsQuery.isPending) return;
    let cancelled = false;
    void gateway.getView(selectedViewId).then((view) => {
      if (cancelled) return;
      const owner = owners.find((candidate) =>
        candidate.ownerId === view.owner.ownerId && candidate.kind === view.owner.kind
      );
      setSelectedView(owner === undefined ? null : { ...view, owner });
    }).catch(() => {
      if (!cancelled) setSelectedView(null);
    });
    return () => { cancelled = true; };
  }, [gateway, owners, selectedViewId, views, viewsQuery.isPending]);

  useEffect(() => {
    if (selectedViewId !== null) gateway.recordViewOpened(selectedViewId);
  }, [gateway, selectedViewId]);

  useEffect(() => {
    void gateway.synchronizeShortcuts().catch((error: unknown) => {
      if (!shouldClearShortcuts(error)) return;
      return gateway.clearShortcuts().catch(() => undefined);
    });
  }, [gateway, views]);
  useEffect(
    () => () => { void gateway.clearShortcuts().catch(() => undefined); },
    [gateway],
  );
  useEffect(
    () => gateway.startEligibleRefreshes(views, (viewId) => {
      void queryClient.invalidateQueries({
        queryKey: [...viewServiceKey, "ListPullRequests", viewId],
      });
    }),
    [gateway, queryClient, views],
  );

  const pullRequestsQuery = useInfiniteQuery({
    queryKey: [...viewServiceKey, "ListPullRequests", selectedView?.viewId ?? ""],
    queryFn: ({ pageParam }) => gateway.listPullRequests(selectedView!.viewId, pageParam),
    enabled: online && selectedView !== null && selectedView.connection === "connected",
    initialPageParam: "",
    getNextPageParam: (page) => page.nextCursor || undefined,
    gcTime: 0,
    retry: false,
    staleTime: 0,
  });
  const queriedPullRequestPages =
    online && pullRequestsQuery.error === null ? pullRequestsQuery.data?.pages : undefined;
  const queriedLastPullRequestPage = queriedPullRequestPages?.at(-1);
  const unavailableFreshness =
    queriedLastPullRequestPage?.freshness === DeckFreshness.Offline ||
    queriedLastPullRequestPage?.freshness === DeckFreshness.Disconnected;
  const pullRequestPages = unavailableFreshness ? undefined : queriedPullRequestPages;
  const pullRequests = useMemo(
    () => pullRequestPages?.flatMap((page) => page.items) ?? [],
    [pullRequestPages],
  );
  const lastPullRequestPage = pullRequestPages?.at(-1);

  useEffect(() => {
    if (!online || pullRequestsQuery.error !== null) {
      queryClient.removeQueries({
        queryKey: [...viewServiceKey, "ListPullRequests", selectedView?.viewId ?? ""],
      });
    }
  }, [online, pullRequestsQuery.error, queryClient, selectedView?.viewId]);

  const invalidateViews = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: [...viewServiceKey, "ListViews"] });
  }, [queryClient]);
  const invalidatePullRequests = useCallback(async () => {
    if (selectedView === null) return;
    await queryClient.invalidateQueries({
      queryKey: [...viewServiceKey, "ListPullRequests", selectedView.viewId],
    });
  }, [queryClient, selectedView]);

  const run = useCallback(async (operation: () => Promise<void>) => {
    setInFlightOperations((current) => current + 1);
    setOperationError(null);
    try {
      await operation();
      return true;
    } catch (error) {
      setOperationError(error);
      return false;
    } finally {
      setInFlightOperations((current) => Math.max(0, current - 1));
    }
  }, []);

  const syncNativeNotificationPreference = useCallback(
    async (viewId: string, input: DeckViewInput) => {
      try {
        await gateway.updateNativeNotificationPreference(
          viewId,
          input.notificationPreference,
        );
      } catch (error) {
        // The view mutation is already committed. Report this device-local
        // failure without making the create/edit form retry the server write.
        setOperationError(error);
      }
    },
    [gateway],
  );

  const createView = useCallback(
    (owner: DeckOwner, input: DeckViewInput) => run(async () => {
      await gateway.ensureNativeNotificationPermission(input.notificationPreference);
      const created = await gateway.createView(owner, input, createUuidV7());
      setSelectedView(created);
      await invalidateViews();
      await syncNativeNotificationPreference(created.viewId, input);
    }),
    [gateway, invalidateViews, run, syncNativeNotificationPreference],
  );
  const saveView = useCallback(
    (view: DeckView, input: DeckViewInput) => run(async () => {
      await gateway.ensureNativeNotificationPermission(input.notificationPreference);
      let updated: DeckView;
      try {
        updated = await gateway.updateView(view, input);
      } catch (error) {
        if (isStaleRevision(error)) {
          const current = await gateway.getView(view.viewId);
          setConflict({ attempted: input, current });
        }
        throw error;
      }
      setConflict(null);
      setSelectedView(updated);
      await invalidateViews();
      await syncNativeNotificationPreference(updated.viewId, input);
    }),
    [gateway, invalidateViews, run, syncNativeNotificationPreference],
  );
  const deleteView = useCallback(
    (view: DeckView) => run(async () => {
      await gateway.deleteView(view);
      setSelectedView(null);
      await invalidateViews();
    }),
    [gateway, invalidateViews, run],
  );
  const reloadConflict = useCallback(async () => {
    if (conflict === null) return;
    const current = await gateway.getView(conflict.current.viewId);
    setConflict({ ...conflict, current });
  }, [conflict, gateway]);
  const reapplyConflict = useCallback(async () => {
    if (conflict === null) return false;
    return saveView(conflict.current, conflict.attempted);
  }, [conflict, saveView]);
  const refresh = useCallback(
    () => run(async () => {
      if (selectedView === null) return;
      await gateway.refreshView(selectedView.viewId, (warning) => new Promise<boolean>((resolve) => {
        setManualRefreshWarning(warning);
        setManualRefreshResolver(() => resolve);
      }));
      await invalidatePullRequests();
    }),
    [gateway, invalidatePullRequests, run, selectedView],
  );
  const createWidgetConfiguration = useCallback(
    (viewId: string, family: DeckWidgetFamily, privacy: DeckWidgetPrivacy) =>
      run(() => gateway.createWidgetConfiguration(viewId, family, privacy)),
    [gateway, run],
  );
  const resolveManualRefresh = useCallback((confirmed: boolean) => {
    manualRefreshResolver?.(confirmed);
    setManualRefreshResolver(null);
    setManualRefreshWarning(null);
  }, [manualRefreshResolver]);
  useEffect(() => {
    if (!online) resolveManualRefresh(false);
  }, [online, resolveManualRefresh]);
  const searchMutationCandidates = useCallback(async (
    pullRequest: DeckPullRequest,
    kind: DeckMutationKind,
    query: string,
    cursor: string,
  ) => {
    let page: DeckMutationCandidatePage | null = null;
    await run(async () => {
      if (selectedView === null) return;
      page = await gateway.listMutationCandidates(
        selectedView.viewId,
        pullRequest,
        kind,
        query,
        cursor,
      );
    });
    return page;
  }, [gateway, run, selectedView]);
  const mutate = useCallback(
    (pullRequest: DeckPullRequest, input: DeckMutationInput) => run(async () => {
      if (selectedView === null) return;
      const result = await gateway.mutatePullRequest(selectedView.viewId, pullRequest, input);
      // A provider-accepted mutation is never repeated when its reload failed.
      // refresh_required deliberately carries no potentially stale detail, so
      // dispatch a new view refresh and never retry the provider mutation.
      if (result.refreshRequired) {
        try {
          await gateway.refreshAfterMutation(selectedView.viewId);
        } finally {
          await invalidatePullRequests();
        }
      } else {
        const updated = result.pullRequest;
        if (updated === undefined) return;
        queryClient.setQueryData<InfiniteData<DeckPullRequestPage, string>>(
          [...viewServiceKey, "ListPullRequests", selectedView.viewId],
          (current) => current === undefined ? current : {
            ...current,
            pages: current.pages.map((page) => ({
              ...page,
              items: page.items.map((candidate) =>
                candidate.repositoryOwner === updated.repositoryOwner &&
                candidate.repositoryName === updated.repositoryName &&
                candidate.number === updated.number
                  ? updated
                  : candidate
              ),
            })),
          },
        );
      }
    }),
    [gateway, invalidatePullRequests, queryClient, run, selectedView],
  );
  const openOnGitHub = useCallback(
    (pullRequest: DeckPullRequest) => run(() => gateway.openPullRequest(pullRequest)),
    [gateway, run],
  );
  const openShortcut = useCallback(
    (viewId: string) => run(async () => {
      const view = await gateway.getView(viewId);
      let owner = owners.find((candidate) =>
        candidate.ownerId === view.owner.ownerId && candidate.kind === view.owner.kind
      );
      if (owner === undefined) {
        const refreshedOwners = await gateway.listOwners();
        queryClient.setQueryData([...viewServiceKey, "ListOwners"], refreshedOwners);
        owner = refreshedOwners.find((candidate) =>
          candidate.ownerId === view.owner.ownerId && candidate.kind === view.owner.kind
        );
      }
      if (owner === undefined) {
        throw new DeckProductError(DeckFailureCode.PermissionDenied);
      }
      resolveManualRefresh(false);
      setSelectedOwner(owner);
      setSelectedView({ ...view, owner });
      setConflict(null);
    }),
    [gateway, owners, queryClient, resolveManualRefresh, run],
  );
  const handleWidgetAction = useCallback(
    (action: DeckWidgetActionEvent) => run(async () => {
      if (action.action === "open-view") {
        await openShortcut(action.viewId);
        return;
      }
      if (action.action === "refresh") {
        await gateway.requestWidgetRefresh(action.viewId);
        await openShortcut(action.viewId);
        await queryClient.invalidateQueries({
          queryKey: [...viewServiceKey, "ListPullRequests", action.viewId],
        });
        return;
      }
      if (action.action === "open-pr") {
        await openDeckPullRequest(action.owner, action.repository, action.number);
        return;
      }
      const destination = await gateway.resolveNotificationEvent(action.eventId);
      if (destination.viewId !== undefined) await openShortcut(destination.viewId);
      if (destination.pullRequest !== undefined) {
        await openDeckPullRequest(
          destination.pullRequest.repositoryOwner,
          destination.pullRequest.repositoryName,
          destination.pullRequest.number,
        );
      }
    }),
    [gateway, openShortcut, queryClient, run],
  );
  useEffect(() => {
    if (!isTauri()) return;
    let active = true;
    let drainRequested = false;
    let draining = false;
    const requestWidgetActionDrain = () => {
      drainRequested = true;
      if (draining) return;
      draining = true;
      void (async () => {
        while (active && drainRequested) {
          drainRequested = false;
          while (active) {
            const action = await invoke<DeckWidgetActionEvent | null>(
              "take_pending_deck_widget_action",
            );
            if (action === null) break;
            await handleWidgetAction(action);
          }
        }
      })().finally(() => {
        draining = false;
        if (active && drainRequested) requestWidgetActionDrain();
      });
    };
    const widgetListener = listen("devhud://deck-widget-action", requestWidgetActionDrain);
    void widgetListener.then(requestWidgetActionDrain);
    const cleanups = [
      listen("devhud://deck-open", () => {
        document.getElementById("deck-workspace-title")?.focus();
      }),
      listen("devhud://deck-refresh", () => void refresh()),
      listen<string>("devhud://deck-shortcut", (event) => void openShortcut(event.payload)),
      widgetListener,
    ];
    return () => {
      active = false;
      void Promise.all(cleanups).then((unlisten) => unlisten.forEach((remove) => remove()));
    };
  }, [handleWidgetAction, openShortcut, refresh]);
  const retry = useCallback(async () => {
    setOperationError(null);
    const requests: Promise<unknown>[] = [ownersQuery.refetch()];
    if (selectedOwner !== null) requests.push(viewsQuery.refetch());
    if (selectedView !== null && selectedView.connection === "connected") {
      requests.push(pullRequestsQuery.refetch());
    }
    await Promise.all(requests);
  }, [ownersQuery, pullRequestsQuery, selectedOwner, selectedView, viewsQuery]);

  const state = useMemo<DeckState>(() => ({
    owners,
    selectedOwner,
    views,
    selectedView,
    pullRequests,
    conflict,
    online,
    loadingOwners: ownersQuery.isPending,
    loadingViews: viewsQuery.isPending,
    loadingPullRequests: pullRequestsQuery.isPending,
    viewCursor: viewsQuery.hasNextPage ? viewsQuery.data?.pages.at(-1)?.nextCursor ?? "" : "",
    pullRequestCursor: pullRequestsQuery.hasNextPage ? lastPullRequestPage?.nextCursor ?? "" : "",
    truncated: lastPullRequestPage?.truncated ?? false,
    resultLimit: lastPullRequestPage?.resultLimit ?? 0,
    freshness: queriedLastPullRequestPage?.freshness ?? null,
    error:
      operationError ?? ownersQuery.error ?? viewsQuery.error ?? pullRequestsQuery.error,
    busy: inFlightOperations > 0,
    manualRefreshWarning,
  }), [
    conflict,
    inFlightOperations,
    lastPullRequestPage,
    queriedLastPullRequestPage,
    online,
    operationError,
    manualRefreshWarning,
    owners,
    ownersQuery.error,
    ownersQuery.isPending,
    pullRequests,
    pullRequestsQuery.error,
    pullRequestsQuery.hasNextPage,
    pullRequestsQuery.isPending,
    selectedOwner,
    selectedView,
    views,
    viewsQuery.data?.pages,
    viewsQuery.error,
    viewsQuery.hasNextPage,
    viewsQuery.isPending,
  ]);

  const actions = useMemo<DeckActions>(() => ({
    selectOwner: (owner) => {
      resolveManualRefresh(false);
      setOperationError(null);
      setSelectedOwner(owner);
      setSelectedView(null);
      setConflict(null);
    },
    selectView: (view) => {
      resolveManualRefresh(false);
      setOperationError(null);
      setSelectedView(view);
      setConflict(null);
    },
    createView,
    saveView,
    deleteView,
    reloadConflict,
    reapplyConflict,
    dismissConflict: () => setConflict(null),
    loadMoreViews: async () => { await viewsQuery.fetchNextPage(); },
    loadMorePullRequests: async () => { await pullRequestsQuery.fetchNextPage(); },
    refresh,
    createWidgetConfiguration,
    resolveManualRefresh,
    searchMutationCandidates,
    mutate,
    openOnGitHub,
    retry,
  }), [
    createView,
    createWidgetConfiguration,
    deleteView,
    mutate,
    openOnGitHub,
    pullRequestsQuery,
    reapplyConflict,
    refresh,
    resolveManualRefresh,
    reloadConflict,
    retry,
    saveView,
    searchMutationCandidates,
    viewsQuery,
  ]);

  const value = useMemo<DeckContextValue>(
    () => ({ state, actions, meta: { gateway } }),
    [actions, gateway, state],
  );
  return <DeckContext value={value}>{children}</DeckContext>;
}

export function useDeck(): DeckContextValue {
  const value = use(DeckContext);
  if (value === null) throw new Error("Deck components must be used inside DeckProvider.");
  return value;
}
