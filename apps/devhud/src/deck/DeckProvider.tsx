import { DeckViewService } from "@delinoio/devhud-deck-connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
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
import { isTauri } from "@tauri-apps/api/core";
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
  type DeckView,
  type DeckViewInput,
} from "./contracts";
import type { ManualRefreshWarning } from "./refreshController";
import { createUuidV7 } from "./uuid";

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

export interface DeckContextValue {
  readonly state: DeckState;
  readonly actions: DeckActions;
  readonly meta: DeckMeta;
}

const DeckContext = createContext<DeckContextValue | null>(null);

function isStaleRevision(error: unknown): boolean {
  return error instanceof DeckProductError && error.code === DeckFailureCode.StaleRevision;
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
  const [busy, setBusy] = useState(false);
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
    if (selectedOwner === null && owners.length > 0) setSelectedOwner(owners[0]!);
  }, [owners, selectedOwner]);

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
    if (selectedView === null) return;
    const current = views.find((view) => view.viewId === selectedView.viewId);
    if (current === undefined) setSelectedView(null);
    else if (current !== selectedView) setSelectedView(current);
  }, [selectedView, views]);

  useEffect(() => {
    if (selectedViewId !== null) gateway.recordViewOpened(selectedViewId);
  }, [gateway, selectedViewId]);

  useEffect(() => {
    void gateway.synchronizeShortcuts(views).catch(() => undefined);
  }, [gateway, views]);
  useEffect(
    () => () => { void gateway.synchronizeShortcuts([]).catch(() => undefined); },
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
    setBusy(true);
    setOperationError(null);
    try {
      await operation();
      return true;
    } catch (error) {
      setOperationError(error);
      return false;
    } finally {
      setBusy(false);
    }
  }, []);

  const createView = useCallback(
    (owner: DeckOwner, input: DeckViewInput) => run(async () => {
      const created = await gateway.createView(owner, input, createUuidV7());
      await invalidateViews();
      setSelectedView(created);
    }),
    [gateway, invalidateViews, run],
  );
  const saveView = useCallback(
    (view: DeckView, input: DeckViewInput) => run(async () => {
      try {
        const updated = await gateway.updateView(view, input);
        setConflict(null);
        setSelectedView(updated);
        await invalidateViews();
      } catch (error) {
        if (isStaleRevision(error)) {
          const current = await gateway.getView(view.viewId);
          setConflict({ attempted: input, current });
        }
        throw error;
      }
    }),
    [gateway, invalidateViews, run],
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
      } else if (result.pullRequest !== undefined) {
        await invalidatePullRequests();
      }
    }),
    [gateway, invalidatePullRequests, run, selectedView],
  );
  const openOnGitHub = useCallback(
    (pullRequest: DeckPullRequest) => run(() => gateway.openPullRequest(pullRequest)),
    [gateway, run],
  );
  useEffect(() => {
    if (!isTauri()) return;
    const cleanups = [
      listen("devhud://deck-open", () => {
        document.getElementById("deck-workspace-title")?.focus();
      }),
      listen("devhud://deck-refresh", () => void refresh()),
      listen<string>("devhud://deck-shortcut", (event) => {
        const view = views.find((candidate) => candidate.viewId === event.payload);
        if (view !== undefined) setSelectedView(view);
      }),
    ];
    return () => { void Promise.all(cleanups).then((unlisten) => unlisten.forEach((remove) => remove())); };
  }, [refresh, views]);
  const retry = useCallback(async () => {
    setOperationError(null);
    await Promise.all([ownersQuery.refetch(), viewsQuery.refetch(), pullRequestsQuery.refetch()]);
  }, [ownersQuery, pullRequestsQuery, viewsQuery]);

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
    busy,
    manualRefreshWarning,
  }), [
    busy,
    conflict,
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
      setSelectedOwner(owner);
      setSelectedView(null);
      setConflict(null);
    },
    selectView: (view) => {
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
    resolveManualRefresh: (confirmed) => {
      manualRefreshResolver?.(confirmed);
      setManualRefreshResolver(null);
      setManualRefreshWarning(null);
    },
    searchMutationCandidates,
    mutate,
    openOnGitHub,
    retry,
  }), [
    createView,
    deleteView,
    mutate,
    openOnGitHub,
    pullRequestsQuery,
    reapplyConflict,
    refresh,
    manualRefreshResolver,
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
