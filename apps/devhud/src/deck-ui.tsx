import { createContext, use, useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent as ReactKeyboardEvent, type PropsWithChildren } from "react";
import { applyDeckBuilder, classifyDeckFailure, clearDeckCache, DeckLimit, deckTransitionKeys, nextDeckRefresh, parseDeckBuilder, readDeckCache, validateDeckQuery, writeDeckCache, type DeckCache, type DeckFailure, type DeckPendingNotification } from "./deck.ts";
import { createGitHubProvider, GitHubErrorCode, GitHubOperation, GitHubProviderError, readGitHubCredential, type GitHubCredential, type GitHubDeckPullRequest, type GitHubProvider } from "./github-provider.ts";
import type { Copy } from "./localization.ts";
import { DeckNotificationKind, type NativeBridgeV1 } from "./native-bridge.ts";
import { deckPollingCancellationGeneration } from "./deck-polling-cancellation.ts";
import { useIdentitySettings } from "./service-boundary.tsx";
import { deckRepositories, hasDeckBooleanQuerySyntax, hasRepositoryQualifier, type DeckBuilder, type DevHudSettingsV1 } from "./settings-contract.ts";
import { getLocalStorage } from "./shell.ts";
import { EmptyState, LoadingState, OfflineState } from "./surface-state.tsx";
import { WidgetContractVersion, widgetDeckCounts, widgetPullRequests, widgetRefreshState, type WidgetDeckConfiguration, type WidgetDeckSnapshot } from "./widget-contract.ts";

type Deck = DevHudSettingsV1["decks"][number];
interface DeckPollingConfiguration { readonly signature: string; readonly refreshMinutes: Deck["refreshMinutes"]; readonly profileRef: Deck["profileRef"]; }

interface DeckRefreshState { readonly cache: DeckCache | null; readonly loading: boolean; readonly failure: DeckFailure | null; }
interface DeckPollingContextValue {
  readonly states: Readonly<Record<string, DeckRefreshState>>;
  readonly canPoll: boolean;
  readonly online: boolean;
  readonly refresh: (deckId: string, manual?: boolean) => Promise<void>;
  readonly validate: (deck: Deck) => Promise<void>;
  readonly clear: (deck: Deck) => Promise<void>;
}

interface WidgetAccessContextValue {
  readonly supported: boolean;
  readonly enabledDeckIds: ReadonlySet<string>;
  readonly failedDeckIds: ReadonlySet<string>;
  readonly enable: (deck: Deck, snapshot?: WidgetDeckSnapshot) => Promise<void>;
  readonly disable: (deckId: string) => Promise<void>;
}

const DeckPollingContext = createContext<DeckPollingContextValue | null>(null);
const WidgetAccessContext = createContext<WidgetAccessContextValue | null>(null);
const emptyDeckRefreshState: DeckRefreshState = { cache: null, loading: false, failure: null };
const noDecks: readonly Deck[] = [];
const DeckRepositoryValidationConcurrency = 2;

class DeckPollingCancelledError extends Error {}

function createRepositoryValidationQueue(limit: number): <Value>(operation: () => Promise<Value>) => Promise<Value> {
  let active = 0;
  const pending: Array<() => void> = [];
  const start = (task: () => void) => {
    active += 1;
    task();
  };
  return function enqueue<Value>(operation: () => Promise<Value>): Promise<Value> {
    return new Promise((resolve, reject) => {
      const task = () => {
        void Promise.resolve().then(operation).then(resolve, reject).finally(() => {
          active -= 1;
          const next = pending.shift();
          if (next !== undefined) start(next);
        });
      };
      if (active < limit) start(task);
      else pending.push(task);
    });
  };
}

function retainedPendingNotifications(cache: DeckCache | null, notifications: readonly DeckNotificationKind[]): readonly DeckPendingNotification[] {
  return cache?.pendingNotifications?.filter((notification) => notifications.includes(notification.kind)) ?? [];
}

function useDeckPolling(): DeckPollingContextValue {
  const value = use(DeckPollingContext);
  if (value === null) throw new Error("DeckPollingBoundary is missing");
  return value;
}

function useWidgetAccess(): WidgetAccessContextValue {
  const value = use(WidgetAccessContext);
  if (value === null) throw new Error("WidgetAccessBoundary is missing");
  return value;
}

function WidgetSnapshotSynchronization({ deck, enabled, state, synchronize }: { readonly deck: Deck; readonly enabled: boolean; readonly state: DeckRefreshState; readonly synchronize: (deck: Deck, snapshot?: WidgetDeckSnapshot) => Promise<void> }) {
  useEffect(() => {
    if (!enabled || state.cache === null || state.cache.query !== deck.query) return;
    void synchronize(deck, widgetSnapshot(deck, state.cache, state.failure)).catch(() => undefined);
  }, [deck, enabled, state.cache, state.failure, synchronize]);
  return null;
}

interface DeckPollingBoundaryProps extends PropsWithChildren { readonly bridge: NativeBridgeV1; readonly active: boolean; readonly online: boolean; readonly language?: "en" | "ko"; readonly provider?: GitHubProvider; }

/** Keeps every configured Deck current while the client is able to poll, independent of the visible surface. */
export function DeckPollingBoundary({ bridge, active, online, language = "en", provider: suppliedProvider, children }: DeckPollingBoundaryProps) {
  const identity = useIdentitySettings();
  const storage = useMemo(() => getLocalStorage(), []);
  const defaultProvider = useMemo(() => createGitHubProvider({ fetch: globalThis.fetch }), []);
  const provider = suppliedProvider ?? defaultProvider;
  const [states, setStates] = useState<Record<string, DeckRefreshState>>({});
  const [widgetSupported, setWidgetSupported] = useState(false);
  const [enabledWidgetDeckIds, setEnabledWidgetDeckIds] = useState<ReadonlySet<string>>(() => new Set());
  const [failedWidgetDeckIds, setFailedWidgetDeckIds] = useState<ReadonlySet<string>>(() => new Set());
  const widgetMutationQueues = useRef(new Map<string, Promise<void>>());
  const widgetReconciliationGeneration = useRef(0);
  const widgetSynchronizationGenerations = useRef(new Map<string, number>());
  const manuallyDisabledWidgetDeckIds = useRef(new Set<string>());
  const caches = useRef(new Map<string, DeckCache | null>());
  const configurations = useRef(new Map<string, DeckPollingConfiguration>());
  const loading = useRef(new Set<string>());
  const queued = useRef(new Set<string>());
  const validatedRepositories = useRef(new Map<string, { readonly token: string; readonly validation: Promise<void> }>());
  const repositoryValidationQueue = useMemo(() => createRepositoryValidationQueue(DeckRepositoryValidationConcurrency), []);
  const browserNotifications = useRef(new Map<string, Set<Notification>>());
  const decks = useRef<readonly Deck[]>(identity.settings.decks);
  const deckAccessAllowed = useRef(!identity.deckAccessSuspended);
  const activeRef = useRef(active);
  const onlineRef = useRef(online);

  deckAccessAllowed.current = !identity.deckAccessSuspended;
  decks.current = identity.deckAccessSuspended ? [] : identity.settings.decks;
  useEffect(() => { activeRef.current = active; }, [active]);
  useEffect(() => { onlineRef.current = online; }, [online]);

  const setDeckState = useCallback((deckId: string, update: (current: DeckRefreshState) => DeckRefreshState) => {
    setStates((current) => ({ ...current, [deckId]: update(current[deckId] ?? emptyDeckRefreshState) }));
  }, []);

  const validateRepositories = useCallback(async (credential: GitHubCredential, repositories: ReturnType<typeof deckRepositories>, force = false, canContinue?: () => boolean) => {
    if (repositories === null || repositories.length === 0) throw new GitHubProviderError(GitHubErrorCode.InvalidQuery, GitHubOperation.SearchPullRequests);
    const validateRepository = async (repository: NonNullable<typeof repositories>[number]) => {
      const key = `${credential.profileId}\u0000${repository.owner}/${repository.name}`.toLowerCase();
      const existing = validatedRepositories.current.get(key);
      if (!force && existing?.token === credential.token) return existing.validation;
      const validation = repositoryValidationQueue(async () => {
        if (canContinue !== undefined && !canContinue()) throw new DeckPollingCancelledError();
        await provider.validateRepository(credential, repository);
      });
      validatedRepositories.current.set(key, { token: credential.token, validation });
      try { await validation; } catch (error) { if (validatedRepositories.current.get(key)?.validation === validation) validatedRepositories.current.delete(key); throw error; }
    };
    await Promise.all(repositories.map(validateRepository));
  }, [provider, repositoryValidationQueue]);

  const validate = useCallback(async (currentDeck: Deck) => {
    const profile = identity.settings.github.profiles.find((item) => item.id === currentDeck.profileRef);
    if (profile === undefined) throw new GitHubProviderError(GitHubErrorCode.MissingToken, GitHubOperation.ValidateRepository);
    const repositories = deckRepositories(currentDeck.query);
    const scopeId = await identity.githubPatScopeId;
    const credential = await readGitHubCredential(bridge, profile, scopeId);
    await validateRepositories(credential, repositories, true);
  }, [bridge, identity.githubPatScopeId, identity.settings.github.profiles, validateRepositories]);

  const refresh = useCallback(async (deckId: string, manual = false) => {
    const currentDeck = decks.current.find((deck) => deck.id === deckId);
    if (currentDeck === undefined || !deckAccessAllowed.current || !onlineRef.current || !activeRef.current) return;
    if (loading.current.has(deckId)) { queued.current.add(deckId); return; }
    const cancellationGeneration = deckPollingCancellationGeneration();
    const signature = `${currentDeck.name}\u0000${currentDeck.profileRef}\u0000${currentDeck.query}\u0000${currentDeck.refreshMinutes}\u0000${currentDeck.notifications.join(",")}`;
    const isCurrentDeck = () => cancellationGeneration === deckPollingCancellationGeneration() && deckAccessAllowed.current && decks.current.some((deck) => deck.id === deckId && `${deck.name}\u0000${deck.profileRef}\u0000${deck.query}\u0000${deck.refreshMinutes}\u0000${deck.notifications.join(",")}` === signature);
    const canContinue = () => isCurrentDeck() && activeRef.current && onlineRef.current;
    loading.current.add(deckId);
    setDeckState(deckId, (current) => ({ ...current, loading: true, failure: null }));
    const attemptedAt = new Date().toISOString();
    let scopeId: string | null = null;
    let currentCache: DeckCache | null = null;
    try {
      const profile = identity.settings.github.profiles.find((item) => item.id === currentDeck.profileRef);
      if (profile === undefined) throw new GitHubProviderError(GitHubErrorCode.MissingToken, GitHubOperation.ValidateRepository);
      const repositories = deckRepositories(currentDeck.query);
      scopeId = await identity.githubPatScopeId;
      if (!canContinue()) return;
      const hydratedCache = caches.current.get(deckId);
      currentCache = hydratedCache?.query === currentDeck.query ? hydratedCache : readDeckCache(storage, `${scopeId}.${currentDeck.profileRef}`, deckId, currentDeck.query);
      const retainedNotifications = retainedPendingNotifications(currentCache, currentDeck.notifications);
      if (currentCache !== null && retainedNotifications.length !== (currentCache.pendingNotifications?.length ?? 0)) {
        currentCache = { ...currentCache, pendingNotifications: retainedNotifications };
        writeDeckCache(storage, `${scopeId}.${currentDeck.profileRef}`, currentCache);
        caches.current.set(deckId, currentCache);
      }
      if (!manual && currentCache?.nextRefreshAt !== null && currentCache?.nextRefreshAt !== undefined && Date.parse(currentCache.nextRefreshAt) > Date.now()) return;
      const credential = await readGitHubCredential(bridge, profile, scopeId);
      if (!canContinue()) return;
      await validateRepositories(credential, repositories, false, canContinue);
      if (!canContinue()) return;
      const search = await provider.searchPullRequests(credential, currentDeck.query, { etag: currentCache?.totalCount === undefined ? undefined : currentCache.queryEtag ?? undefined });
      if (!canContinue()) return;
      if (search.notModified && currentCache?.totalCount === undefined) throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, GitHubOperation.SearchPullRequests);
      if (search.incompleteResults) {
        const failures = (currentCache?.failures ?? 0) + 1;
        const next: DeckCache = { version: 2, deckId: currentDeck.id, query: currentDeck.query, queryEtag: currentCache?.queryEtag ?? null, totalCount: currentCache?.totalCount, results: currentCache?.results ?? [], lastSuccessfulAt: currentCache?.lastSuccessfulAt ?? null, lastAttemptedAt: attemptedAt, rate: search.metadata.rate, failures, nextRefreshAt: nextDeckRefresh(Date.now(), currentDeck.refreshMinutes, failures, search.metadata.rate), transitionKeys: currentCache?.transitionKeys ?? [], pendingNotifications: currentCache?.pendingNotifications ?? [] };
        writeDeckCache(storage, `${scopeId}.${currentDeck.profileRef}`, next);
        caches.current.set(deckId, next);
        setDeckState(deckId, (current) => ({ ...current, cache: next, failure: "incomplete-results" }));
        return;
      }
      const resultNodeIds = search.notModified ? currentCache?.results.map((item) => item.nodeId) ?? [] : search.items.slice(0, 100).map((item) => item.nodeId);
      const missingNodeIds = currentCache !== null && currentDeck.notifications.length > 0 && !search.notModified ? currentCache.results.map((item) => item.nodeId).filter((nodeId) => !resultNodeIds.includes(nodeId)) : [];
      const enriched = await enrichPullRequestBatches(provider, credential, [...resultNodeIds, ...missingNodeIds], canContinue);
      if (!canContinue()) return;
      const enrichedById = new Map(enriched.map((item) => [item.nodeId, item]));
      const results = resultNodeIds.flatMap((nodeId) => enrichedById.get(nodeId) ?? []);
      const reconciled = missingNodeIds.flatMap((nodeId) => enrichedById.get(nodeId) ?? []);
      let transitionKeys = currentCache?.transitionKeys ?? [];
      let pendingNotifications = retainedPendingNotifications(currentCache, currentDeck.notifications);
      if (isCurrentDeck() && activeRef.current && onlineRef.current && currentCache !== null && currentDeck.notifications.length > 0) {
        const transitions: readonly DeckPendingNotification[] = [
          ...pendingNotifications,
          ...deckTransitionKeys(currentCache.results, [...results, ...reconciled])
            .filter((transition) => currentDeck.notifications.includes(transition.kind) && !transitionKeys.includes(transition.key) && !pendingNotifications.some((notification) => notification.key === transition.key))
            .map((transition) => ({ key: transition.key, kind: transition.kind, body: transition.pullRequest.title })),
        ];
        for (const transition of transitions) {
          if (!isCurrentDeck() || !activeRef.current || !onlineRef.current) break;
          if (!await publishDeckNotification(bridge, browserNotifications.current, currentDeck.id, transition.key, transition.kind, currentDeck.name, transition.body)) {
            if (!pendingNotifications.some((notification) => notification.key === transition.key)) pendingNotifications = [...pendingNotifications, transition];
            continue;
          }
          transitionKeys = [...transitionKeys, transition.key];
          pendingNotifications = pendingNotifications.filter((notification) => notification.key !== transition.key);
          const retainedCache = caches.current.get(deckId) ?? currentCache;
          if (isCurrentDeck() && retainedCache !== null) {
            const retained = { ...retainedCache, transitionKeys, pendingNotifications };
            writeDeckCache(storage, `${scopeId}.${currentDeck.profileRef}`, retained);
            caches.current.set(deckId, retained);
          }
        }
      }
      if (!canContinue()) return;
      const next: DeckCache = { version: 2, deckId: currentDeck.id, query: currentDeck.query, queryEtag: search.metadata.etag ?? currentCache?.queryEtag ?? null, totalCount: search.notModified ? currentCache?.totalCount ?? results.length : search.totalCount, results, lastSuccessfulAt: attemptedAt, lastAttemptedAt: attemptedAt, rate: search.metadata.rate, failures: 0, nextRefreshAt: null, transitionKeys, pendingNotifications };
      writeDeckCache(storage, `${scopeId}.${currentDeck.profileRef}`, next);
      if (isCurrentDeck()) {
        caches.current.set(deckId, next);
        setDeckState(deckId, (current) => ({ ...current, cache: next, failure: null }));
      }
    } catch (error) {
      if (error instanceof DeckPollingCancelledError || !canContinue()) return;
      const failures = (currentCache?.failures ?? 0) + 1;
      const rate = error instanceof GitHubProviderError ? error.rate : null;
      const cacheScopeId = scopeId ?? await identity.githubPatScopeId;
      if (!canContinue()) return;
      const next: DeckCache = { version: 2, deckId: currentDeck.id, query: currentDeck.query, queryEtag: currentCache?.queryEtag ?? null, totalCount: currentCache?.totalCount, results: currentCache?.results ?? [], lastSuccessfulAt: currentCache?.lastSuccessfulAt ?? null, lastAttemptedAt: attemptedAt, rate, failures, nextRefreshAt: nextDeckRefresh(Date.now(), currentDeck.refreshMinutes, failures, rate), transitionKeys: currentCache?.transitionKeys ?? [], pendingNotifications: currentCache?.pendingNotifications ?? [] };
      writeDeckCache(storage, `${cacheScopeId}.${currentDeck.profileRef}`, next);
      caches.current.set(deckId, next);
      setDeckState(deckId, (current) => ({ ...current, cache: next, failure: classifyDeckFailure(error) }));
    } finally {
      loading.current.delete(deckId);
      if (isCurrentDeck()) setDeckState(deckId, (current) => ({ ...current, loading: false }));
      if (queued.current.delete(deckId) && deckAccessAllowed.current && activeRef.current && onlineRef.current) void refresh(deckId);
    }
  }, [bridge, identity.githubPatScopeId, identity.settings.github.profiles, setDeckState, storage, validateRepositories]);

  const cancelDeckNotifications = useCallback(async (deckId: string) => {
    const request = bridge.request({ operation: "notifications.cancel-deck", deckId });
    closeBrowserDeckNotifications(browserNotifications.current, deckId);
    try { await request; } catch {}
  }, [bridge]);

  const clear = useCallback(async (deck: Deck) => {
    clearDeckCache(storage, `${await identity.githubPatScopeId}.${deck.profileRef}`, deck.id);
    await cancelDeckNotifications(deck.id);
    caches.current.delete(deck.id);
    configurations.current.delete(deck.id);
    setStates((current) => {
      const { [deck.id]: _removed, ...remaining } = current;
      return remaining;
    });
  }, [cancelDeckNotifications, identity.githubPatScopeId, storage]);

  const setWidgetFailure = useCallback((deckId: string, failed: boolean) => {
    setFailedWidgetDeckIds((current) => {
      if (current.has(deckId) === failed) return current;
      const next = new Set(current);
      if (failed) next.add(deckId); else next.delete(deckId);
      return next;
    });
  }, []);

  const enqueueWidgetMutation = useCallback((deckId: string, operation: () => Promise<void>) => {
    const previous = widgetMutationQueues.current.get(deckId) ?? Promise.resolve();
    const next = previous.catch(() => undefined).then(operation);
    widgetMutationQueues.current.set(deckId, next);
    const cleanup = () => { if (widgetMutationQueues.current.get(deckId) === next) widgetMutationQueues.current.delete(deckId); };
    void next.then(cleanup, cleanup);
    return next;
  }, []);

  const widgetConfiguration = useCallback(async (deck: Deck): Promise<WidgetDeckConfiguration | null> => {
    const profile = identity.settings.github.profiles.find((item) => item.id === deck.profileRef);
    const repositories = deckRepositories(deck.query);
    if (profile === undefined || repositories === null || repositories.length === 0) return null;
    return { version: WidgetContractVersion, deckId: deck.id, name: deck.name, query: deck.query, repositories, profileId: profile.id, profileKind: profile.kind, scopeId: await identity.githubPatScopeId, language };
  }, [identity.githubPatScopeId, identity.settings.github.profiles, language]);

  const synchronizeWidgetDeck = useCallback(async (deck: Deck, snapshot?: WidgetDeckSnapshot) => {
    const generation = widgetReconciliationGeneration.current;
    const synchronizationGeneration = widgetSynchronizationGenerations.current.get(deck.id) ?? 0;
    const canSynchronize = () => generation === widgetReconciliationGeneration.current && synchronizationGeneration === (widgetSynchronizationGenerations.current.get(deck.id) ?? 0) && !manuallyDisabledWidgetDeckIds.current.has(deck.id);
    try {
      const configuration = await widgetConfiguration(deck);
      if (configuration === null) throw new Error("missing-widget-configuration");
      let synchronized = false;
      await enqueueWidgetMutation(deck.id, async () => {
        if (!canSynchronize()) return;
        await bridge.request({ operation: "widgets.enable-deck", configuration });
        if (!canSynchronize()) return;
        if (snapshot !== undefined && snapshot.query === configuration.query) await bridge.request({ operation: "widgets.replace-deck-snapshot", snapshot });
        synchronized = canSynchronize();
      });
      if (!synchronized) return;
      setEnabledWidgetDeckIds((current) => current.has(deck.id) ? current : new Set(current).add(deck.id));
      setWidgetFailure(deck.id, false);
    } catch (error) {
      setWidgetFailure(deck.id, true);
      throw error;
    }
  }, [bridge, enqueueWidgetMutation, setWidgetFailure, widgetConfiguration]);

  const disableWidgetDeck = useCallback(async (deckId: string) => {
    try {
      await enqueueWidgetMutation(deckId, async () => { await bridge.request({ operation: "widgets.disable-deck", deckId }); });
      setEnabledWidgetDeckIds((current) => { if (!current.has(deckId)) return current; const next = new Set(current); next.delete(deckId); return next; });
      setWidgetFailure(deckId, false);
    } catch (error) {
      setWidgetFailure(deckId, true);
      throw error;
    }
  }, [bridge, enqueueWidgetMutation, setWidgetFailure]);

  const enableWidgetDeck = useCallback(async (deck: Deck, snapshot?: WidgetDeckSnapshot) => {
    manuallyDisabledWidgetDeckIds.current.delete(deck.id);
    widgetSynchronizationGenerations.current.set(deck.id, (widgetSynchronizationGenerations.current.get(deck.id) ?? 0) + 1);
    await synchronizeWidgetDeck(deck, snapshot);
  }, [synchronizeWidgetDeck]);

  const disableWidgetDeckManually = useCallback(async (deckId: string) => {
    manuallyDisabledWidgetDeckIds.current.add(deckId);
    widgetSynchronizationGenerations.current.set(deckId, (widgetSynchronizationGenerations.current.get(deckId) ?? 0) + 1);
    try { await disableWidgetDeck(deckId); }
    catch (error) { manuallyDisabledWidgetDeckIds.current.delete(deckId); throw error; }
  }, [disableWidgetDeck]);

  const configuredDecks = identity.deckAccessSuspended ? noDecks : identity.settings.decks;
  const scheduleKey = configuredDecks.map((deck) => `${deck.id}\u0000${deck.name}\u0000${deck.profileRef}\u0000${deck.query}\u0000${deck.refreshMinutes}\u0000${deck.notifications.join(",")}`).join("\u0001");
  const scheduledDecks = useMemo(() => configuredDecks, [identity.deckAccessSuspended, scheduleKey]);
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  useEffect(() => {
    let cancelled = false;
    validatedRepositories.current.clear();
    if (identity.deckAccessSuspended) {
      for (const deckId of new Set([...caches.current.keys(), ...configurations.current.keys()])) void cancelDeckNotifications(deckId);
      caches.current.clear();
      configurations.current.clear();
      loading.current.clear();
      queued.current.clear();
      setStates({});
      return () => { cancelled = true; };
    }
    const configuredIds = new Set(scheduledDecks.map((deck) => deck.id));
    const removedConfigurations = [...configurations.current.entries()].filter(([deckId]) => !configuredIds.has(deckId));
    const removedDeckIds = new Set([...caches.current.keys(), ...removedConfigurations.map(([deckId]) => deckId)]);
    for (const deckId of removedDeckIds) void cancelDeckNotifications(deckId);
    for (const deckId of [...caches.current.keys()]) {
      if (!configuredIds.has(deckId)) {
        caches.current.delete(deckId);
      }
    }
    for (const deckId of [...configurations.current.keys()]) {
      if (!configuredIds.has(deckId)) configurations.current.delete(deckId);
    }
    setStates((current) => Object.fromEntries(Object.entries(current).filter(([deckId]) => configuredIds.has(deckId))));
    void identity.githubPatScopeId.then((scopeId) => {
      if (cancelled) return;
      for (const [deckId, configuration] of removedConfigurations) clearDeckCache(storage, `${scopeId}.${configuration.profileRef}`, deckId);
      for (const deck of scheduledDecks) {
        const signature = `${deck.name}\u0000${deck.profileRef}\u0000${deck.query}\u0000${deck.refreshMinutes}\u0000${deck.notifications.join(",")}`;
        const previous = configurations.current.get(deck.id);
        if (previous?.signature === signature) continue;
        if (previous !== undefined && previous.profileRef !== deck.profileRef) clearDeckCache(storage, `${scopeId}.${previous.profileRef}`, deck.id);
        let cache = readDeckCache(storage, `${scopeId}.${deck.profileRef}`, deck.id, deck.query);
        const retainedNotifications = retainedPendingNotifications(cache, deck.notifications);
        if (cache !== null && retainedNotifications.length !== (cache.pendingNotifications?.length ?? 0)) {
          cache = { ...cache, pendingNotifications: retainedNotifications };
          writeDeckCache(storage, `${scopeId}.${deck.profileRef}`, cache);
        }
        if (previous !== undefined && previous.refreshMinutes !== deck.refreshMinutes && cache !== null && cache.failures > 0 && cache.nextRefreshAt !== null) {
          cache = { ...cache, nextRefreshAt: nextDeckRefresh(Date.now(), deck.refreshMinutes, cache.failures, cache.rate) };
          writeDeckCache(storage, `${scopeId}.${deck.profileRef}`, cache);
        }
        configurations.current.set(deck.id, { signature, refreshMinutes: deck.refreshMinutes, profileRef: deck.profileRef });
        caches.current.set(deck.id, cache);
        setDeckState(deck.id, (current) => ({ ...current, cache, failure: null }));
      }
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [cancelDeckNotifications, identity.deckAccessSuspended, identity.githubPatScopeId, scheduledDecks, setDeckState, storage]);
  useEffect(() => {
    let cancelled = false;
    let retry: ReturnType<typeof setTimeout> | undefined;
    const generation = widgetReconciliationGeneration.current + 1;
    widgetReconciliationGeneration.current = generation;
    const reconcile = async () => {
      let failed = false;
      try {
        const synchronizationGenerations = new Map(widgetSynchronizationGenerations.current);
        const wasInvalidated = (deckId: string) => (synchronizationGenerations.get(deckId) ?? 0) !== (widgetSynchronizationGenerations.current.get(deckId) ?? 0);
        const response = await bridge.request({ operation: "widgets.status" });
        if (cancelled || widgetReconciliationGeneration.current !== generation || response.kind !== "widget-status") return;
        setWidgetSupported(true);
        const reportedEnabledDeckIds = new Set(response.enabledDeckIds);
        setEnabledWidgetDeckIds((current) => {
          const next = new Set(current);
          for (const deckId of new Set([...current, ...reportedEnabledDeckIds])) {
            if (wasInvalidated(deckId)) continue;
            if (reportedEnabledDeckIds.has(deckId) && !manuallyDisabledWidgetDeckIds.current.has(deckId)) next.add(deckId);
            else next.delete(deckId);
          }
          return next;
        });
        for (const deckId of [...manuallyDisabledWidgetDeckIds.current]) if (!wasInvalidated(deckId) && !reportedEnabledDeckIds.has(deckId)) manuallyDisabledWidgetDeckIds.current.delete(deckId);
        for (const deckId of response.enabledDeckIds) {
          if (cancelled || widgetReconciliationGeneration.current !== generation) return;
          if (wasInvalidated(deckId) || manuallyDisabledWidgetDeckIds.current.has(deckId)) continue;
          const deck = scheduledDecks.find((item) => item.id === deckId);
          try {
            if (deck === undefined) await disableWidgetDeck(deckId);
            else await synchronizeWidgetDeck(deck);
          } catch { failed = true; }
        }
      } catch { failed = true; }
      if (!cancelled && widgetReconciliationGeneration.current === generation && failed) retry = setTimeout(() => void reconcile(), 5_000);
    };
    void reconcile();
    return () => {
      cancelled = true;
      if (retry !== undefined) clearTimeout(retry);
    };
  }, [bridge, disableWidgetDeck, scheduledDecks, synchronizeWidgetDeck]);
  useEffect(() => () => {
    const deckIds = new Set([...decks.current.map((deck) => deck.id), ...caches.current.keys(), ...configurations.current.keys()]);
    widgetReconciliationGeneration.current += 1;
    deckAccessAllowed.current = false;
    decks.current = [];
    activeRef.current = false;
    onlineRef.current = false;
    loading.current.clear();
    queued.current.clear();
    validatedRepositories.current.clear();
    caches.current.clear();
    configurations.current.clear();
    for (const deckId of deckIds) void cancelDeckNotifications(deckId);
    closeBrowserDeckNotifications(browserNotifications.current);
  }, [cancelDeckNotifications]);
  useEffect(() => {
    if (!active || !online || identity.deckAccessSuspended) return;
    const timers = scheduledDecks.map((deck) => {
      const timeout = setTimeout(() => void refreshRef.current(deck.id), 0);
      const interval = setInterval(() => void refreshRef.current(deck.id), deck.refreshMinutes * 60_000);
      return { timeout, interval };
    });
    return () => { for (const timer of timers) { clearTimeout(timer.timeout); clearInterval(timer.interval); } };
  }, [active, identity.deckAccessSuspended, online, scheduledDecks]);

  const value = useMemo<DeckPollingContextValue>(() => ({ states, canPoll: active && online && !identity.deckAccessSuspended, online, refresh, validate, clear }), [active, clear, identity.deckAccessSuspended, online, refresh, states, validate]);
  const widgetValue = useMemo<WidgetAccessContextValue>(() => ({ supported: widgetSupported, enabledDeckIds: enabledWidgetDeckIds, failedDeckIds: failedWidgetDeckIds, enable: enableWidgetDeck, disable: disableWidgetDeckManually }), [disableWidgetDeckManually, enableWidgetDeck, enabledWidgetDeckIds, failedWidgetDeckIds, widgetSupported]);
  return <DeckPollingContext value={value}><WidgetAccessContext value={widgetValue}>{scheduledDecks.map((deck) => <WidgetSnapshotSynchronization key={deck.id} deck={deck} enabled={enabledWidgetDeckIds.has(deck.id)} state={states[deck.id] ?? emptyDeckRefreshState} synchronize={synchronizeWidgetDeck} />)}{children}</WidgetAccessContext></DeckPollingContext>;
}

interface DeckSurfaceProps { readonly copy: Copy; readonly bridge: NativeBridgeV1; readonly language?: "en" | "ko"; readonly selectedDeckId?: string | null; readonly onDismissMissingLink?: () => void; }

export function DeckSurface({ copy, selectedDeckId = null, onDismissMissingLink }: DeckSurfaceProps) {
  const identity = useIdentitySettings();
  const polling = useDeckPolling();
  const widgetAccess = useWidgetAccess();
  const [selected, setSelected] = useState<string | null>(selectedDeckId);
  const [creating, setCreating] = useState(false);
  const [deleteFailed, setDeleteFailed] = useState(false);
  const missingLinkedDeck = selectedDeckId !== null && !identity.settings.decks.some((item) => item.id === selectedDeckId);
  const selectedDeck = missingLinkedDeck ? null : identity.settings.decks.find((item) => item.id === selected) ?? identity.settings.decks[0] ?? null;
  const deck = creating ? null : selectedDeck;
  const refreshState = deck === null ? emptyDeckRefreshState : polling.states[deck.id] ?? emptyDeckRefreshState;
  useEffect(() => {
    if (selectedDeckId && identity.settings.decks.some((item) => item.id === selectedDeckId)) {
      setSelected(selectedDeckId);
      setCreating(false);
    }
  }, [identity.settings.decks, selectedDeckId]);
  const deleteDeck = async (value: Deck) => {
    setDeleteFailed(false);
    try {
      const committed = await identity.replaceSettings((current) => ({ ...current, decks: current.decks.filter((item) => item.id !== value.id) }));
      if (!committed) { setDeleteFailed(true); return; }
      void widgetAccess.disable(value.id).catch(() => undefined);
      void polling.clear(value).catch(() => undefined);
    } catch { setDeleteFailed(true); }
  };
  if (missingLinkedDeck) return <section className="deck" aria-labelledby="deck-title"><h2 id="deck-title">{copy.deckTitle}</h2><p role="alert">{copy.deckNotFound}</p><button type="button" onClick={onDismissMissingLink}>{copy.deckReturnToList}</button></section>;
  if (identity.settings.github.profiles.length === 0) return <>{polling.online ? <EmptyState copy={copy} /> : <OfflineState copy={copy} />}<p role="status">{copy.deckNoProfiles}</p></>;
  const creationDisabled = identity.readOnly || identity.settings.decks.length >= DeckLimit;
  return <section className="deck" aria-labelledby="deck-title"><div className="deck-head"><h2 id="deck-title">{copy.deckTitle}</h2><button type="button" disabled={creationDisabled} onClick={() => { onDismissMissingLink?.(); setSelected(null); setCreating(true); }}>{copy.deckCreate}</button></div>
    <div className="deck-layout"><nav aria-label={copy.deck}><ul>{identity.settings.decks.map((item) => <li key={item.id}><button className={deck?.id === item.id ? "active" : ""} onClick={() => { onDismissMissingLink?.(); setSelected(item.id); setCreating(false); }}>{item.name}</button></li>)}</ul></nav>
      {deck === null ? <DeckEditor key="create" copy={copy} disabled={creationDisabled} onSave={async (next) => { await polling.validate(next); const committed = await identity.replaceSettings((current) => ({ ...current, decks: [...current.decks, next] })); if (committed) { setSelected(next.id); setCreating(false); } return committed; }} profiles={identity.settings.github.profiles} /> : <div><DeckEditor key={deck.id} copy={copy} value={deck} profiles={identity.settings.github.profiles} disabled={identity.readOnly} onSave={async (next) => { await polling.validate(next); return identity.replaceSettings((current) => ({ ...current, decks: current.decks.map((item) => item.id === deck.id ? next : item) })); }} />
        <div className="actions"><button type="button" disabled={refreshState.loading || !polling.canPoll} onClick={() => void polling.refresh(deck.id, true)}>{copy.deckRefresh}</button><button type="button" disabled={identity.readOnly} onClick={() => void deleteDeck(deck)}>{copy.deckDelete}</button></div>
        <WidgetAccess key={deck.id} cache={refreshState.cache} copy={copy} deck={deck} failure={refreshState.failure} />
        {refreshState.cache?.lastSuccessfulAt && <p>{copy.lastSuccessfulRefresh}: <time dateTime={refreshState.cache.lastSuccessfulAt}>{refreshState.cache.lastSuccessfulAt}</time></p>}{refreshState.cache?.rate?.resetAt && <p>{copy.deckRateReset}: <time dateTime={refreshState.cache.rate.resetAt}>{refreshState.cache.rate.resetAt}</time></p>}{!polling.canPoll && refreshState.cache && <p className="notice">{copy.deckStale}</p>}{refreshState.failure && <p role="alert">{failureCopy(copy, refreshState.failure)}</p>}
        {deleteFailed && <p role="alert">{copy.deckDeleteFailed}</p>}{!polling.online && refreshState.cache === null ? <OfflineState copy={copy} /> : refreshState.loading && refreshState.cache === null ? <LoadingState copy={copy} /> : <DeckResults copy={copy} groupBy={deck.display.groupBy} results={deck.display.showDrafts ? refreshState.cache?.results ?? [] : (refreshState.cache?.results ?? []).filter((pullRequest) => !pullRequest.draft)} />}
      </div>}</div></section>;
}

function WidgetAccess({ cache, copy, deck, failure }: { readonly cache: DeckCache | null; readonly copy: Copy; readonly deck: Deck; readonly failure: DeckFailure | null }) {
  const identity = useIdentitySettings();
  const widgetAccess = useWidgetAccess();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const enableTrigger = useRef<HTMLButtonElement>(null);
  const disableButton = useRef<HTMLButtonElement>(null);
  const dialog = useRef<HTMLElement>(null);
  const cancelButton = useRef<HTMLButtonElement>(null);
  const profile = identity.settings.github.profiles.find((item) => item.id === deck.profileRef);
  const enabled = widgetAccess.enabledDeckIds.has(deck.id);
  const failed = widgetAccess.failedDeckIds.has(deck.id);

  const closeConfirmation = useCallback(() => {
    setConfirming(false);
    requestAnimationFrame(() => enableTrigger.current?.focus());
  }, []);
  useEffect(() => {
    if (!confirming) return;
    cancelButton.current?.focus();
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === "Escape" && !busy) closeConfirmation(); };
    addEventListener("keydown", closeOnEscape);
    return () => removeEventListener("keydown", closeOnEscape);
  }, [busy, closeConfirmation, confirming]);
  const trapDialogFocus = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (event.key !== "Tab") return;
    const focusable = dialog.current?.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled]), select:not([disabled]), [href]");
    if (!focusable?.length) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };

  const enable = async () => {
    setBusy(true);
    try {
      await widgetAccess.enable(deck, cache !== null && cache.query === deck.query ? widgetSnapshot(deck, cache, failure) : undefined);
      setConfirming(false);
      requestAnimationFrame(() => disableButton.current?.focus());
    } catch {}
    finally { setBusy(false); }
  };
  const disable = async () => {
    setBusy(true);
    try { await widgetAccess.disable(deck.id); }
    catch {}
    finally { setBusy(false); }
  };

  if (!widgetAccess.supported) return null;
  return <section className="widget-access" aria-labelledby={`widget-title-${deck.id}`}><h3 id={`widget-title-${deck.id}`}>{copy.widgetTitle}</h3>
    {enabled ? <><p role="status">{copy.widgetEnabled}</p><button ref={disableButton} type="button" disabled={busy} onClick={() => void disable()}>{copy.widgetDisable}</button></> : <button ref={enableTrigger} type="button" disabled={busy || profile === undefined} onClick={() => setConfirming(true)}>{copy.widgetEnable}</button>}
    {confirming && <section ref={dialog} className="widget-privacy" role="alertdialog" aria-modal="true" aria-labelledby={`widget-privacy-title-${deck.id}`} aria-describedby={`widget-privacy-warning-${deck.id}`} onKeyDown={trapDialogFocus}><h4 id={`widget-privacy-title-${deck.id}`}>{copy.widgetPrivacyTitle}</h4><p id={`widget-privacy-warning-${deck.id}`}>{copy.widgetPrivacyWarning}</p><div className="actions"><button type="button" disabled={busy} onClick={() => void enable()}>{copy.widgetPrivacyConfirm}</button><button ref={cancelButton} type="button" disabled={busy} onClick={closeConfirmation}>{copy.widgetPrivacyCancel}</button></div></section>}
    {failed && <p role="alert">{copy.widgetActionFailed}</p>}
  </section>;
}

function widgetSnapshot(deck: Deck, cache: DeckCache, failure: DeckFailure | null): WidgetDeckSnapshot | undefined {
  if (cache.totalCount === undefined) return undefined;
  const base: WidgetDeckSnapshot = {
    version: WidgetContractVersion,
    deckId: deck.id,
    query: deck.query,
    counts: widgetDeckCounts(cache.totalCount, cache.results),
    results: widgetPullRequests(cache.results),
    state: widgetRefreshState(cache.lastSuccessfulAt, failure === "token" ? "missing-token" : failure === "rate-limit" ? "rate-limit" : failure === "permission" ? "permission" : failure !== null ? "error" : null),
    lastSuccessfulAt: cache.lastSuccessfulAt,
    lastAttemptedAt: cache.lastAttemptedAt ?? cache.lastSuccessfulAt ?? new Date(0).toISOString(),
    rate: cache.rate,
  };
  return base;
}

function DeckEditor({ copy, value, profiles, disabled = false, onSave }: { readonly copy: Copy; readonly value?: DevHudSettingsV1["decks"][number]; readonly profiles: DevHudSettingsV1["github"]["profiles"]; readonly disabled?: boolean; readonly onSave: (deck: DevHudSettingsV1["decks"][number]) => Promise<boolean> }) {
  const [name, setName] = useState(value?.name ?? ""); const [profileRef, setProfileRef] = useState(value?.profileRef ?? profiles[0]?.id ?? ""); const [query, setQuery] = useState(value?.query ?? "is:pr"); const [builder, setBuilder] = useState<DeckBuilder | null>(value?.builder ?? parseDeckBuilder(value?.query ?? "")); const [refreshMinutes, setRefreshMinutes] = useState<1 | 5 | 15 | 30>(value?.refreshMinutes ?? 5); const [groupBy, setGroupBy] = useState<Deck["display"]["groupBy"]>(value?.display.groupBy ?? "none"); const [showDrafts, setShowDrafts] = useState(value?.display.showDrafts ?? true); const [notifications, setNotifications] = useState<readonly DeckNotificationKind[]>(value?.notifications ?? []); const [invalid, setInvalid] = useState<"query" | "repository" | null>(null); const [saveFailure, setSaveFailure] = useState<DeckFailure | null>(null); const [saving, setSaving] = useState(false);
  useEffect(() => { if (value) { setName(value.name); setProfileRef(value.profileRef); setQuery(value.query); setBuilder(value.builder ?? parseDeckBuilder(value.query)); setRefreshMinutes(value.refreshMinutes); setGroupBy(value.display.groupBy); setShowDrafts(value.display.showDrafts); setNotifications(value.notifications); } }, [value]);
  const submit = (event: FormEvent) => { event.preventDefault(); if (!validateDeckQuery(query) || !profileRef || !name.trim()) { setInvalid("query"); return; } if (!hasRepositoryQualifier(query) || deckRepositories(query) === null) { setInvalid("repository"); return; } setInvalid(null); setSaveFailure(null); setSaving(true); void onSave({ id: value?.id ?? createUuidV7(), name: name.trim(), profileRef, query, builder, display: { groupBy, showDrafts }, refreshMinutes, notifications }).then((committed) => { if (!committed) setSaveFailure("unknown"); }).catch((error) => setSaveFailure(classifyDeckFailure(error))).finally(() => setSaving(false)); };
  const setBuilderValue = (field: keyof DeckBuilder, next: string) => { const trimmed = next.trim(); const builderValue = trimmed === "" ? null : trimmed as DeckBuilder[typeof field]; const nextQuery = applyDeckBuilder(query, field, builderValue); setQuery(nextQuery); setBuilder(parseDeckBuilder(nextQuery)); };
  return <form onSubmit={submit} className="deck-editor"><fieldset disabled={disabled || saving}><label>{copy.deckName}<input required value={name} onChange={(event) => setName(event.target.value)} /></label><label>{copy.deckProfile}<select required value={profileRef} onChange={(event) => setProfileRef(event.target.value)}>{profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}</select></label><label>{copy.deckQuery}<input required value={query} onChange={(event) => { setQuery(event.target.value); setBuilder(parseDeckBuilder(event.target.value)); }} /></label><fieldset disabled={hasDeckBooleanQuerySyntax(query)}><legend>{copy.deckBuilder}</legend><label>{copy.deckBuilderRepository}<input value={builder?.repository ?? ""} onChange={(event) => setBuilderValue("repository", event.target.value)} /></label><label>{copy.deckBuilderAuthor}<input value={builder?.author ?? ""} onChange={(event) => setBuilderValue("author", event.target.value)} /></label><label>{copy.deckBuilderLabel}<input value={builder?.label ?? ""} onChange={(event) => setBuilderValue("label", event.target.value)} /></label><label>{copy.deckBuilderReview}<select value={builder?.review ?? ""} onChange={(event) => setBuilderValue("review", event.target.value)}><option value="">{copy.deckAny}</option><option value="approved">{copy.deckApproved}</option><option value="changes-requested">{copy.deckChangesRequested}</option><option value="required">{copy.deckRequired}</option></select></label><label>{copy.deckBuilderState}<select value={builder?.state ?? ""} onChange={(event) => setBuilderValue("state", event.target.value)}><option value="">{copy.deckAny}</option><option value="open">{copy.deckOpen}</option><option value="closed">{copy.deckClosed}</option><option value="merged">{copy.deckMerged}</option></select></label></fieldset><label>{copy.deckRefreshInterval}<select value={refreshMinutes} onChange={(event) => setRefreshMinutes(Number(event.target.value) as 1 | 5 | 15 | 30)}>{[1, 5, 15, 30].map((minutes) => <option key={minutes} value={minutes}>{minutes} {copy.deckMinutes}</option>)}</select></label><label>{copy.deckGroupBy}<select value={groupBy} onChange={(event) => setGroupBy(event.target.value as Deck["display"]["groupBy"])}><option value="none">{copy.deckGroupNone}</option><option value="repository">{copy.deckGroupRepository}</option><option value="author">{copy.deckGroupAuthor}</option></select></label><label><input type="checkbox" checked={showDrafts} onChange={(event) => setShowDrafts(event.target.checked)} />{copy.deckShowDrafts}</label><label><input type="checkbox" checked={notifications.length > 0} onChange={(event) => setNotifications(event.target.checked ? ["review", "checks", "merged", "closed"] : [])} />{copy.deckNotifications}</label><button type="submit">{copy.saved}</button>{invalid && <p role="alert">{invalid === "repository" ? copy.deckRequireRepository : copy.deckRequirePullRequests}</p>}{saveFailure && <p role="alert">{failureCopy(copy, saveFailure)}</p>}</fieldset></form>;
}

function DeckResults({ copy, groupBy, results }: { readonly copy: Copy; readonly groupBy: DevHudSettingsV1["decks"][number]["display"]["groupBy"]; readonly results: readonly GitHubDeckPullRequest[] }) {
  const groups = groupDeckResults(results, groupBy);
  return <section aria-labelledby="deck-results-title"><h3 id="deck-results-title">{copy.deckResults}</h3>{results.length === 0 ? <p>{copy.empty}</p> : groups.map((group) => <section key={group.key}><>{group.label && <h4>{group.label}</h4>}</><ul className="deck-results">{group.results.map((pullRequest) => <li key={pullRequest.nodeId}><strong>{pullRequest.repository.owner}/{pullRequest.repository.name}#{pullRequest.number}: {pullRequest.title}</strong><span>{deckStateCopy(copy, pullRequest.state)}{pullRequest.draft ? ` · ${copy.deckDraft}` : ""} · {reviewCopy(copy, pullRequest.reviewDecision)} · {checkCopy(copy, pullRequest.checkRollup.state)}</span><span>{pullRequest.author} · {pullRequest.labels.join(", ")} · <time dateTime={pullRequest.updatedAt}>{pullRequest.updatedAt}</time></span></li>)}</ul></section>)}</section>;
}

function groupDeckResults(results: readonly GitHubDeckPullRequest[], groupBy: DevHudSettingsV1["decks"][number]["display"]["groupBy"]): readonly { readonly key: string; readonly label: string | null; readonly results: readonly GitHubDeckPullRequest[] }[] {
  if (groupBy === "none") return [{ key: "all", label: null, results }];
  const groups = new Map<string, GitHubDeckPullRequest[]>();
  for (const pullRequest of results) {
    const key = groupBy === "repository" ? `${pullRequest.repository.owner}/${pullRequest.repository.name}` : pullRequest.author;
    const current = groups.get(key) ?? [];
    current.push(pullRequest);
    groups.set(key, current);
  }
  return [...groups].map(([key, grouped]) => ({ key, label: key, results: grouped }));
}

function deckStateCopy(copy: Copy, state: GitHubDeckPullRequest["state"]): string { return state === "open" ? copy.deckOpen : state === "closed" ? copy.deckClosed : copy.deckMerged; }
function reviewCopy(copy: Copy, review: GitHubDeckPullRequest["reviewDecision"]): string { return review === "approved" ? copy.deckApproved : review === "changes-requested" ? copy.deckChangesRequested : review === "required" ? copy.deckRequired : copy.deckNoReview; }
function checkCopy(copy: Copy, state: string | null): string { return state === "PENDING" ? copy.deckChecksPending : state === "SUCCESS" ? copy.deckChecksSuccess : state === "FAILURE" ? copy.deckChecksFailure : state === "ERROR" ? copy.deckChecksError : state === "EXPECTED" ? copy.deckChecksExpected : state === "TIMED_OUT" ? copy.deckChecksTimedOut : state === null ? copy.deckNoChecks : copy.deckChecksUnknown; }
function failureCopy(copy: Copy, failure: DeckFailure): string { return failure === "token" ? copy.deckErrorToken : failure === "secure-storage" ? copy.githubErrorSecureStorage : failure === "permission" ? copy.deckErrorPermission : failure === "query" ? copy.deckErrorQuery : failure === "rate-limit" ? copy.deckErrorRate : failure === "incomplete-results" ? copy.deckErrorIncomplete : copy.deckErrorNetwork; }
function closeBrowserDeckNotifications(notifications: Map<string, Set<Notification>>, deckId?: string): void { const entries = deckId === undefined ? [...notifications] : [[deckId, notifications.get(deckId) ?? new Set<Notification>()] as const]; for (const [id, items] of entries) { for (const notification of items) notification.close(); notifications.delete(id); } }
async function publishDeckNotification(bridge: NativeBridgeV1, browserNotifications: Map<string, Set<Notification>>, deckId: string, key: string, kind: "review" | "checks" | "merged" | "closed", title: string, body: string): Promise<boolean> { try { await bridge.request({ operation: "notifications.publish-deck-change", notification: { id: `deck:${deckId}:${key}`, deckId, kind: kind as DeckNotificationKind, title, body } }); return true; } catch { try { if (typeof Notification !== "undefined" && Notification.permission === "granted") { const notification = new Notification(title, { body, tag: `deck:${deckId}:${key}` }); const items = browserNotifications.get(deckId) ?? new Set<Notification>(); items.add(notification); browserNotifications.set(deckId, items); notification.addEventListener("close", () => items.delete(notification), { once: true }); return true; } } catch {} return false; } }
async function enrichPullRequestBatches(provider: GitHubProvider, credential: GitHubCredential, nodeIds: readonly string[], canContinue: () => boolean = () => true): Promise<readonly GitHubDeckPullRequest[]> { const items: GitHubDeckPullRequest[] = []; for (let index = 0; index < nodeIds.length; index += 100) { if (!canContinue()) return items; items.push(...(await provider.enrichPullRequests(credential, nodeIds.slice(index, index + 100))).items); } return items; }
function createUuidV7(now = Date.now()): string { const bytes = crypto.getRandomValues(new Uint8Array(16)); let timestamp = BigInt(now); for (let index = 5; index >= 0; index -= 1) { bytes[index] = Number(timestamp & 0xffn); timestamp >>= 8n; } bytes[6] = (bytes[6] & 0x0f) | 0x70; bytes[8] = (bytes[8] & 0x3f) | 0x80; const hex = [...bytes].map((value) => value.toString(16).padStart(2, "0")).join(""); return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`; }
