import { Code, ConnectError } from "@connectrpc/connect";
import { createConnectQueryKey, useMutation, useQuery, useTransport, TransportProvider } from "@connectrpc/connect-query";
import { createConnectTransport } from "@connectrpc/connect-web";
import { QueryClient, QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import {
  AccountFailureReason,
  AccountDeletionState,
  AccountQuery,
  AdministrativeBlockState,
  BootstrapQuery,
  mapDevHudError,
  PermissionFailureReason,
  SettingsQuery,
  type Account,
  type DevHudClientError,
} from "@delinoio/devhud-api-client";
import { createContext, use, useEffect, useMemo, useRef, useState, type PropsWithChildren, type RefObject } from "react";
import { createIdentitySession, isTerminalAccessTokenError, sessionProfileId, validateBootstrap, type IdentitySession, type ValidatedBootstrap } from "./identity-client";
import { clearDeckCaches } from "./deck.ts";
import { invalidateDeckPolling } from "./deck-polling-cancellation.ts";
import { assertDeviceLocalSettingsPersistable, clearAllContractedLocalData, clearAuthenticatedOriginData, clearAuthenticatedSettingsCache, clearGuestImportMarker, deviceLocalSettingsEqual, hasGuestSettings, readAuthenticatedSettingsCache, readCachedIdentityBootstrap, readGuestSettings, writeAuthenticatedSettingsCache, writeCachedIdentityBootstrap, writeGuestSettings } from "./local-data";
import { SecureSettingKind, type NativeBridgeV1, type RuntimePlatform } from "./native-bridge";
import { profileRequiresSetup } from "./profile-secrets";
import { AgentPromptSettingsSchemaVersion, canonicalDevHudSettings, CollidingSettingsSchemaVersion, defaultDevHudSettings, decodeVersionedDevHudSettings, encodeDevHudSettings, LegacySettingsSchemaVersion, parseDevHudSettings, PreviousSettingsSchemaVersion, R2SettingsSchemaVersion, SettingsSchemaVersion, StructuredSettingsSchemaVersion, withDeviceLocalSettings, type DevHudSettingsV1 } from "./settings-contract";
import { diffSettings, type SettingsDiffEntry } from "./settings-diff";
import { inactiveDesktopShortcutBindings } from "./shortcuts";
import { getLocalStorage, isValidApiOrigin } from "./shell";
import { appendDiagnosticCorrelation, beginDiagnosticWriteSuppression } from "./diagnostics";

export type IdentityStatus = "guest" | "starting" | "signed-out" | "authenticated" | "blocked" | "deletion-pending" | "error";

export interface SettingsConflict {
  readonly local: DevHudSettingsV1;
  readonly server: DevHudSettingsV1;
  readonly currentRevision: bigint;
  readonly currentContentSHA256: Uint8Array;
  readonly diff: readonly SettingsDiffEntry[];
}

export interface IdentitySettingsValue {
  readonly status: IdentityStatus;
  readonly bootstrap: ValidatedBootstrap | null;
  readonly account: Account | null;
  readonly settings: DevHudSettingsV1;
  readonly revision: bigint;
  readonly readOnly: boolean;
  readonly shortcutHydrationReady: boolean;
  readonly activeShortcutBindings: DevHudSettingsV1["shortcuts"]["desktop"];
  readonly setActiveShortcutBindings: (bindings: DevHudSettingsV1["shortcuts"]["desktop"]) => void;
  readonly offline: boolean;
  readonly error: string | null;
  readonly accountError: DevHudClientError | null;
  readonly settingsError: DevHudClientError | null;
  readonly deletionCleanupFailed: boolean;
  readonly deckAccessSuspended: boolean;
  readonly importDiff: readonly SettingsDiffEntry[] | null;
  readonly conflict: SettingsConflict | null;
  readonly signInPending: boolean;
  readonly identityResetAvailable: boolean;
  readonly githubPatScopeId: Promise<string>;
  readonly githubPatCleanupPending: boolean;
  readonly reconcileGitHubPats: () => Promise<boolean>;
  readonly signIn: () => Promise<void>;
  readonly retryIdentity: () => void;
  readonly resetIdentity: () => Promise<void>;
  readonly retryAccount: () => Promise<void>;
  readonly retrySettings: () => Promise<void>;
  readonly continueLocally: () => void;
  readonly uploadLocal: () => Promise<boolean>;
  readonly replaceLocal: () => Promise<boolean>;
  readonly replaceSettings: (settings: DevHudSettingsV1 | ((current: DevHudSettingsV1) => DevHudSettingsV1)) => Promise<boolean>;
  readonly replaceSettingsAt: (settings: DevHudSettingsV1 | ((current: DevHudSettingsV1) => DevHudSettingsV1), expectedRevision: bigint) => Promise<boolean>;
  readonly adoptConflictServer: () => Promise<boolean>;
  readonly reapplyConflictLocal: () => Promise<boolean>;
  readonly logout: () => Promise<void>;
  readonly deleteAccount: () => Promise<void>;
  readonly restoreAccount: () => Promise<void>;
  readonly retryDeletionCleanup: () => Promise<void>;
  readonly profileRequiresSetup: (kind: "github" | "r2", profileId: string) => Promise<boolean>;
}

const IdentitySettingsContext = createContext<IdentitySettingsValue | null>(null);

export function useIdentitySettings(): IdentitySettingsValue {
  const value = use(IdentitySettingsContext);
  if (value === null) throw new Error("IdentitySettingsProvider is missing");
  return value;
}

interface BoundaryProps extends PropsWithChildren {
  readonly apiOrigin: string;
  readonly active: boolean;
  readonly online: boolean;
  readonly callbackUrl: string | null;
  readonly platform: RuntimePlatform;
  readonly bridge: NativeBridgeV1;
  readonly onCallbackConsumed: (url: string) => void;
  readonly onDeckLinkPolicyReady?: () => void;
  readonly onContinueLocally: () => void;
  readonly onLoggedOut: () => void;
  readonly initialAppearance?: DevHudSettingsV1["appearance"];
  readonly identitySessionRef?: RefObject<IdentitySession | null>;
}

export function DevHudServiceBoundary(props: BoundaryProps) {
  const internalSessionRef = useRef<IdentitySession | null>(null);
  const sessionRef = props.identitySessionRef ?? internalSessionRef;
  const [identityEpoch, setIdentityEpoch] = useState(0);
  const queryClient = useMemo(() => new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } }), [identityEpoch, props.apiOrigin]);
  const transport = useMemo(() => createConnectTransport({
    baseUrl: props.apiOrigin,
    interceptors: [(next) => async (request) => {
      if (!request.url.endsWith("/GetBootstrap") && sessionRef.current !== null) {
        try {
          const token = await sessionRef.current.getAccessToken();
          request.header.set("Authorization", `Bearer ${token}`);
        } catch (reason) {
          if (isTerminalAccessTokenError(reason)) throw ConnectError.from(reason, Code.Unauthenticated);
          throw reason;
        }
      }
      const startedAt = performance.now();
      try {
        const response = await next(request);
        appendDiagnosticCorrelation(getLocalStorage(), response.header.get("x-devhud-correlation-id"), request.url, performance.now() - startedAt);
        return response;
      } catch (reason) {
        appendDiagnosticCorrelation(getLocalStorage(), reason instanceof ConnectError ? reason.metadata.get("x-devhud-correlation-id") : null, request.url, performance.now() - startedAt);
        throw reason;
      }
    }],
  }), [props.apiOrigin]);

  useEffect(() => () => queryClient.clear(), [queryClient]);

  return <TransportProvider transport={transport}><QueryClientProvider client={queryClient}>
    <IdentitySettingsProvider key={identityEpoch} {...props} sessionRef={sessionRef} onIdentityReset={() => setIdentityEpoch((current) => current + 1)}>
      {props.children}
    </IdentitySettingsProvider>
  </QueryClientProvider></TransportProvider>;
}

function IdentitySettingsProvider({ apiOrigin, active, online, callbackUrl, platform, bridge, onCallbackConsumed, onDeckLinkPolicyReady, onContinueLocally, onLoggedOut, initialAppearance, children, sessionRef, onIdentityReset }: BoundaryProps & { readonly sessionRef: RefObject<IdentitySession | null>; readonly onIdentityReset: () => void }) {
  const storage = getLocalStorage();
  const queryClient = useQueryClient();
  const transport = useTransport();
  const [status, setStatus] = useState<IdentityStatus>("guest");
  const [session, setSession] = useState<IdentitySession | null>(null);
  const [bootstrap, setBootstrap] = useState<ValidatedBootstrap | null>(null);
  const [settings, setSettings] = useState<DevHudSettingsV1>(() => {
    const guest = readGuestSettings(storage);
    return !hasGuestSettings(storage) && initialAppearance ? { ...guest, appearance: initialAppearance } : guest;
  });
  const settingsRef = useRef(settings);
  const deviceLocalSettingsGenerationRef = useRef(0);
  const [revision, setRevision] = useState(0n);
  const revisionRef = useRef(revision);
  const contentSHA256Ref = useRef(new Uint8Array());
  const githubPatScopeId = useMemo(() => sessionProfileId(apiOrigin), [apiOrigin]);
  const [error, setError] = useState<string | null>(null);
  const [accountError, setAccountError] = useState<DevHudClientError | null>(null);
  const [settingsError, setSettingsError] = useState<DevHudClientError | null>(null);
  const [deletionCleanupFailed, setDeletionCleanupFailed] = useState(false);
  const [deckAccessSuspended, setDeckAccessSuspended] = useState(false);
  const [importDiff, setImportDiff] = useState<readonly SettingsDiffEntry[] | null>(null);
  const [conflict, setConflict] = useState<SettingsConflict | null>(null);
  const [account, setAccount] = useState<Account | null>(null);
  const [networkReady, setNetworkReady] = useState(false);
  const [identityReady, setIdentityReady] = useState(false);
  const [settingsReady, setSettingsReady] = useState(false);
  const [shortcutSettingsReady, setShortcutSettingsReady] = useState(false);
  const [activeShortcutBindings, setActiveShortcutBindings] = useState(() => settings.shortcuts.desktop);
  const [bootstrapAttempt, setBootstrapAttempt] = useState(0);
  const [signInPending, setSignInPending] = useState(false);
  const [identityResetAvailable, setIdentityResetAvailable] = useState(false);
  const [continuedLocally, setContinuedLocally] = useState(false);
  const [githubPatCleanupPending, setGitHubPatCleanupPending] = useState(false);
  const [logoutCleanupPending, setLogoutCleanupPending] = useState(false);
  const signInPendingRef = useRef(false);
  const callbackHandled = useRef<string | null>(null);
  const invalidSessionCleanupRef = useRef<Promise<void> | null>(null);
  const irrecoverableCleanupPendingRef = useRef(false);
  const continueLocallyRef = useRef(false);
  const githubPatReconciliationRef = useRef<Promise<boolean> | null>(null);
  const lastReconciledGitHubPatKeyRef = useRef<string | null>(null);
  const settingsWritableRef = useRef(false);
  const replaceSettingsRef = useRef<IdentitySettingsValue["replaceSettings"]>(async () => false);

  useEffect(() => {
    if (session !== null || continuedLocally && networkReady) onDeckLinkPolicyReady?.();
  }, [continuedLocally, networkReady, onDeckLinkPolicyReady, session]);

  function applySettings(next: DevHudSettingsV1): void {
    settingsRef.current = next;
    setSettings(next);
  }

  function applyRevision(next: bigint, contentSHA256: Uint8Array = next === 0n ? new Uint8Array() : contentSHA256Ref.current): void {
    revisionRef.current = next;
    contentSHA256Ref.current = Uint8Array.from(contentSHA256);
    setRevision(next);
  }

  const accountQueryKey = useMemo(() => createConnectQueryKey({ schema: AccountQuery.getAccount, transport, input: {}, cardinality: "finite" }), [transport]);
  const settingsQueryKey = useMemo(() => createConnectQueryKey({ schema: SettingsQuery.getSettings, transport, input: {}, cardinality: "finite" }), [transport]);

  const bootstrapQuery = useQuery(BootstrapQuery.getBootstrap, {}, { enabled: active && online && networkReady && isValidApiOrigin(apiOrigin) });
  const accountQuery = useQuery(AccountQuery.getAccount, {}, { enabled: status === "authenticated" && online && !logoutCleanupPending });
  const settingsQuery = useQuery(SettingsQuery.getSettings, {}, { enabled: status === "authenticated" && online && !logoutCleanupPending });
  const replaceMutation = useMutation(SettingsQuery.replaceSettings);
  const deleteMutation = useMutation(AccountQuery.deleteAccount);
  const restoreMutation = useMutation(AccountQuery.restoreAccount);

  async function clearIdentityQueryCache(): Promise<void> {
    await Promise.all([
      queryClient.cancelQueries({ queryKey: accountQueryKey }),
      queryClient.cancelQueries({ queryKey: settingsQueryKey }),
    ]);
    queryClient.removeQueries({ queryKey: accountQueryKey });
    queryClient.removeQueries({ queryKey: settingsQueryKey });
  }

  function resetDesktopShortcuts() {
    setShortcutSettingsReady(false);
    setActiveShortcutBindings(inactiveDesktopShortcutBindings);
  }

  function clearInvalidSession(): Promise<void> {
    if (invalidSessionCleanupRef.current !== null) return invalidSessionCleanupRef.current;
    invalidateDeckPolling();
    const current = sessionRef.current;
    sessionRef.current = null;
    setSession(null);
    let cleanup: Promise<void>;
    cleanup = (async () => {
      try {
        await current?.clear();
      } finally {
        setAccount(null);
        setAccountError(null);
        applySettings(defaultDevHudSettings);
        applyRevision(0n);
        setSettingsReady(false);
        resetDesktopShortcuts();
        setSettingsError(null);
        clearAuthenticatedSettingsCache(storage, apiOrigin);
        setStatus("signed-out");
        setDeckAccessSuspended(false);
        clearDeckCaches(storage, await sessionProfileId(apiOrigin));
        await clearIdentityQueryCache();
        onIdentityReset();
      }
    })().then(
      () => { if (invalidSessionCleanupRef.current === cleanup) invalidSessionCleanupRef.current = null; },
      (reason: unknown) => {
        if (invalidSessionCleanupRef.current === cleanup) invalidSessionCleanupRef.current = null;
        throw reason;
      },
    );
    invalidSessionCleanupRef.current = cleanup;
    return cleanup;
  }

  async function cleanPendingDeletion(): Promise<void> {
    invalidateDeckPolling();
    setDeckAccessSuspended(true);
    const releaseDiagnosticWrites = beginDiagnosticWriteSuppression(storage);
    try {
      const localCleanupComplete = clearAllContractedLocalData(storage);
      try {
        await bridge.request({ operation: "secure.purge", scope: "account-deletion", profileId: await sessionProfileId(apiOrigin) });
        setDeletionCleanupFailed(!localCleanupComplete);
      } catch {
        setDeletionCleanupFailed(true);
      }
    } finally {
      releaseDiagnosticWrites();
    }
  }

  async function clearIrrecoverableAccount(): Promise<void> {
    invalidateDeckPolling();
    setDeckAccessSuspended(true);
    irrecoverableCleanupPendingRef.current = true;
    const releaseDiagnosticWrites = beginDiagnosticWriteSuppression(storage);
    try {
      setStatus("error");
      const localCleanupComplete = clearAllContractedLocalData(storage);
      if (!localCleanupComplete) throw new Error("local-data-cleanup-incomplete");
      await bridge.request({ operation: "secure.purge", scope: "logout" });
      sessionRef.current = null;
      setSession(null);
      setAccount(null);
      setAccountError(null);
      applySettings(defaultDevHudSettings);
      applyRevision(0n);
      setSettingsReady(false);
      resetDesktopShortcuts();
      setSettingsError(null);
      setStatus("signed-out");
      setDeckAccessSuspended(false);
      setError(null);
      irrecoverableCleanupPendingRef.current = false;
      await clearIdentityQueryCache();
      onIdentityReset();
    } catch (reason) {
      setError(safeError(reason));
      throw reason;
    } finally {
      releaseDiagnosticWrites();
    }
  }

  useEffect(() => {
    if (!active || !isValidApiOrigin(apiOrigin)) { setNetworkReady(false); return; }
    let cancelled = false;
    void bridge.request({ operation: "session.configure-origins", apiOrigin }).then((response) => {
      if (cancelled || response.kind !== "session-network-policy") return;
      if (response.changed) location.reload();
      else setNetworkReady(true);
    }).catch((reason) => {
      if (!cancelled && !continueLocallyRef.current) { setStatus("error"); setError(safeError(reason)); }
    });
    return () => { cancelled = true; };
  }, [active, apiOrigin, bootstrapAttempt, bridge]);

  useEffect(() => {
    if (!active || !online || !networkReady || !bootstrapQuery.data) return;
    let cancelled = false;
    if (!continueLocallyRef.current) setStatus((current) => current === "guest" ? "starting" : current);
    void (async () => {
      try {
        const validated = validateBootstrap(bootstrapQuery.data, platform);
        writeCachedIdentityBootstrap(storage, apiOrigin, validated);
        const policy = await bridge.request({ operation: "session.configure-origins", apiOrigin, logtoIssuer: validated.issuer });
        if (policy.kind === "session-network-policy" && policy.changed) {
          location.reload();
          return;
        }
        const session = await createIdentitySession(validated, apiOrigin, bridge);
        let authenticated: boolean;
        try {
          authenticated = await session.isAuthenticated();
        } catch (reason) {
          if (!cancelled && !continueLocallyRef.current) setIdentityResetAvailable(true);
          throw reason;
        }
        if (cancelled) return;
        if (!authenticated) {
          clearAuthenticatedSettingsCache(storage, apiOrigin);
          clearDeckCaches(storage, await sessionProfileId(apiOrigin));
        }
        sessionRef.current = session;
        setSession(session);
        setBootstrap(validated);
        setIdentityResetAvailable(false);
        setStatus(continueLocallyRef.current ? "guest" : authenticated ? "authenticated" : "signed-out");
        setIdentityReady(true);
        setError(null);
      } catch (reason) {
        if (!cancelled && !continueLocallyRef.current) {
          setStatus("error");
          setError(safeError(reason));
        }
      }
    })();
    return () => { cancelled = true; };
  }, [active, apiOrigin, bootstrapQuery.data, bridge, networkReady, online, platform, sessionRef, storage]);

  useEffect(() => {
    if (!active || online || !networkReady) return;
    const cached = readCachedIdentityBootstrap(storage, apiOrigin);
    if (cached === null) {
      setIdentityReady(true);
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const policy = await bridge.request({ operation: "session.configure-origins", apiOrigin, logtoIssuer: cached.issuer });
        if (policy.kind === "session-network-policy" && policy.changed) {
          location.reload();
          return;
        }
        const session = await createIdentitySession(cached, apiOrigin, bridge);
        let authenticated: boolean;
        try {
          authenticated = await session.isAuthenticated();
        } catch (reason) {
          if (!cancelled && !continueLocallyRef.current) setIdentityResetAvailable(true);
          throw reason;
        }
        if (cancelled) return;
        if (!authenticated) {
          clearAuthenticatedSettingsCache(storage, apiOrigin);
          clearDeckCaches(storage, await sessionProfileId(apiOrigin));
        }
        sessionRef.current = session;
        setSession(session);
        setBootstrap(cached);
        setIdentityResetAvailable(false);
        setStatus(continueLocallyRef.current ? "guest" : authenticated ? "authenticated" : "signed-out");
        setIdentityReady(true);
        setError(null);
      } catch (reason) {
        if (!cancelled && !continueLocallyRef.current) {
          setStatus("error");
          setError(safeError(reason));
        }
      }
    })();
    return () => { cancelled = true; };
  }, [active, apiOrigin, bridge, networkReady, online, sessionRef, storage]);

  useEffect(() => {
    if (!callbackUrl || callbackHandled.current === callbackUrl || session === null) return;
    callbackHandled.current = callbackUrl;
    void (async () => {
      await session.handleCallback(callbackUrl);
      const pending = await bridge.request({ operation: "auth.take-pending-callback" });
      if (pending.kind !== "auth-callback" || pending.url !== callbackUrl) throw new Error("auth-callback-unavailable");
      onCallbackConsumed(callbackUrl);
      setStatus("authenticated");
      setError(null);
    })().catch((reason) => {
      callbackHandled.current = null;
      setStatus("error");
      setError(safeError(reason));
    });
  }, [bridge, callbackUrl, onCallbackConsumed, session]);

  useEffect(() => {
    if (!networkReady || !bootstrapQuery.error) return;
    if (continueLocallyRef.current) return;
    if (bootstrap !== null || session !== null) return;
    setStatus("error");
    setError("bootstrap-unavailable");
  }, [bootstrap, bootstrapQuery.error, networkReady, session]);

  useEffect(() => {
    if (!accountQuery.data?.account) return;
    const next = accountQuery.data.account;
    setAccount(next);
    setAccountError(null);
    if (next.deletionState === AccountDeletionState.PURGE_CLAIMED) {
      void clearIrrecoverableAccount().catch(() => {});
    } else if (next.deletionState === AccountDeletionState.PENDING) {
      setStatus("deletion-pending");
      void cleanPendingDeletion();
    } else if (next.administrativeBlockState === AdministrativeBlockState.BLOCKED) setStatus("blocked");
  }, [accountQuery.data]);

  useEffect(() => {
    if (!accountQuery.error) return;
    const mapped = mapDevHudError(accountQuery.error);
    if (mapped.kind === "unauthenticated") {
      setAccountError(null);
      void clearInvalidSession().catch((reason) => setError(safeError(reason)));
    } else if (mapped.kind === "accountPrecondition" && mapped.detail.reason === AccountFailureReason.PURGE_CLAIMED) {
      setAccountError(null);
      void clearIrrecoverableAccount().catch(() => {});
    } else if (mapped.kind === "permissionDenied") {
      if (mapped.detail.reason === PermissionFailureReason.ACCOUNT_DELETION_PENDING) {
        setAccountError(null);
        setStatus("deletion-pending");
        void cleanPendingDeletion();
      } else if (mapped.detail.reason === PermissionFailureReason.USER_BLOCKED) {
        setAccountError(null);
        setStatus("blocked");
      } else {
        setAccount(null);
        setAccountError(mapped);
      }
    } else {
      setAccount(null);
      setAccountError(mapped);
    }
  }, [accountQuery.error]);

  useEffect(() => {
    if (logoutCleanupPending) return;
    if (status === "blocked") {
      if (!settingsReady) {
        const cached = readAuthenticatedSettingsCache(storage, apiOrigin);
        if (cached) { applySettings(cached.settings); applyRevision(cached.revision, cached.contentSHA256); }
        setShortcutSettingsReady((ready) => ready || settingsReady || cached !== null);
      }
      return;
    }
    if (status !== "authenticated") return;
    if (!online) {
      setSettingsReady(false);
      const cached = readAuthenticatedSettingsCache(storage, apiOrigin);
      setShortcutSettingsReady((ready) => ready || settingsReady || cached !== null);
      if (cached) { applySettings(cached.settings); applyRevision(cached.revision, cached.contentSHA256); }
      return;
    }
    if (!settingsQuery.data) return;
    const snapshot = settingsQuery.data.snapshot;
    let cancelled = false;
    void (async () => {
      let validated: ValidatedSettingsSnapshot;
      try {
        validated = await validatedSettingsSnapshot(snapshot);
      } catch {
        if (!cancelled && (snapshot === undefined || snapshot.revision >= revisionRef.current)) markSettingsContractInvalid();
        return;
      }
      if (cancelled || sessionRef.current === null || validated.revision < revisionRef.current) return;
      let server = validated.settings;
      let local: DevHudSettingsV1 | null = null;
      if (hasGuestSettings(storage)) {
        local = readGuestSettings(storage);
        server = withDeviceLocalSettings(server, local);
      } else {
        const cached = readAuthenticatedSettingsCache(storage, apiOrigin);
        server = withDeviceLocalSettings(server, cached?.settings ?? settingsRef.current);
        try {
          assertDeviceLocalSettingsPersistable(server);
        } catch {
          if (!cancelled) markSettingsContractInvalid();
          return;
        }
      }
      setError((current) => current === "settings-contract-invalid" ? null : current);
      setSettingsError(null);
      setSettingsReady(true);
      setShortcutSettingsReady(true);
      applyRevision(validated.revision, validated.contentSHA256);
      if (local !== null) {
        applySettings(local);
        setImportDiff(diffSettings(local, server));
      } else {
        applySettings(server);
        writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: server, revision: validated.revision, contentSHA256: validated.contentSHA256, cachedAt: new Date().toISOString() });
      }
    })();
    return () => { cancelled = true; };
  }, [apiOrigin, logoutCleanupPending, online, settingsQuery.data, status, storage]);

  useEffect(() => {
    if (status !== "authenticated" || logoutCleanupPending || !online || !settingsQuery.error) return;
    const mapped = mapDevHudError(settingsQuery.error);
    setSettingsReady(false);
    setSettingsError(mapped);
    const cached = readAuthenticatedSettingsCache(storage, apiOrigin);
    if (cached) {
      applySettings(cached.settings);
      applyRevision(cached.revision, cached.contentSHA256);
    }
    setShortcutSettingsReady(cached !== null);
    if (mapped.kind === "unauthenticated") {
      void clearInvalidSession().catch((reason) => setError(safeError(reason)));
    } else if (mapped.kind === "permissionDenied") {
      if (mapped.detail.reason === PermissionFailureReason.ACCOUNT_DELETION_PENDING) {
        setStatus("deletion-pending");
        void cleanPendingDeletion();
      }
      if (mapped.detail.reason === PermissionFailureReason.USER_BLOCKED) setStatus("blocked");
    }
  }, [apiOrigin, logoutCleanupPending, online, settingsQuery.error, status, storage]);

  useEffect(() => {
    if (status !== "authenticated" && status !== "blocked") setShortcutSettingsReady(false);
  }, [status]);

  async function replaceAt(local: DevHudSettingsV1, expectedRevision: bigint, expectedContentSHA256: Uint8Array = contentSHA256Ref.current): Promise<boolean> {
    if (!online) throw new Error("offline-read-only");
    const deviceLocalSettingsGeneration = deviceLocalSettingsGenerationRef.current;
    setSettingsError(null);
    let canonicalJson: Uint8Array;
    try {
      assertDeviceLocalSettingsPersistable(local);
      canonicalJson = encodeDevHudSettings(local);
    } catch (reason) {
      setError("settings-contract-invalid");
      throw reason;
    }
    try {
      const response = await replaceMutation.mutateAsync({ schemaVersion: SettingsSchemaVersion, canonicalJson: Uint8Array.from(canonicalJson), expectedRevision, expectedContentSha256: expectedRevision === 0n ? new Uint8Array() : Uint8Array.from(expectedContentSHA256) });
      let validated: ValidatedSettingsSnapshot;
      try {
        if (!response.snapshot) throw new SettingsSnapshotError("settings response is missing its snapshot");
        validated = await validatedSettingsSnapshot(response.snapshot);
      } catch (reason) {
        markSettingsContractInvalid();
        throw reason;
      }
      const hasNewerDeviceLocalSettings = deviceLocalSettingsGenerationRef.current !== deviceLocalSettingsGeneration;
      const latestDeviceLocalSettings = hasNewerDeviceLocalSettings ? settingsRef.current : local;
      const requiresDeviceLocalPersistence = hasGuestSettings(storage) || !hasNewerDeviceLocalSettings && !deviceLocalSettingsEqual(local, settingsRef.current);
      const next = withDeviceLocalSettings(validated.settings, latestDeviceLocalSettings);
      const persisted = writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: next, revision: validated.revision, contentSHA256: validated.contentSHA256, cachedAt: new Date().toISOString() });
      applySettings(next);
      applyRevision(validated.revision, validated.contentSHA256);
      setSettingsReady(true);
      setError((current) => current === "settings-contract-invalid" ? null : current);
      setImportDiff(null);
      setConflict(null);
      if (persisted || !requiresDeviceLocalPersistence) clearGuestImportMarker(storage);
      if (requiresDeviceLocalPersistence && !persisted) throw new Error("device-local-settings-persistence-failed");
      return true;
    } catch (reason) {
      if (reason instanceof SettingsSnapshotError) throw reason;
      const mapped = mapDevHudError(reason);
      if (mapped.kind === "revisionConflict") {
        const serverSnapshot = mapped.detail.currentSnapshot;
        let validated: ValidatedSettingsSnapshot;
        try {
          validated = await validatedSettingsSnapshot(serverSnapshot);
        } catch (snapshotReason) {
          setConflict(null);
          markSettingsContractInvalid();
          throw snapshotReason;
        }
        setImportDiff(null);
        const latestDeviceLocalSettings = deviceLocalSettingsGenerationRef.current !== deviceLocalSettingsGeneration ? settingsRef.current : local;
        const latestLocal = withDeviceLocalSettings(local, latestDeviceLocalSettings);
        const server = withDeviceLocalSettings(validated.settings, latestDeviceLocalSettings);
        setConflict({ local: latestLocal, server, currentRevision: validated.revision, currentContentSHA256: validated.contentSHA256, diff: diffSettings(latestLocal, server) });
        return false;
      }
      setSettingsError(mapped);
      throw reason;
    }
  }

  function retryIdentity(): void {
    if (irrecoverableCleanupPendingRef.current) {
      setError(null);
      void clearIrrecoverableAccount().catch(() => {});
      return;
    }
    continueLocallyRef.current = false;
    setContinuedLocally(false);
    setStatus("starting");
    setError(null);
    setIdentityResetAvailable(false);
    setNetworkReady(false);
    setBootstrapAttempt((current) => current + 1);
  }

  async function resetIdentity(): Promise<void> {
    clearAuthenticatedSettingsCache(storage, apiOrigin);
    setStatus("starting");
    setError(null);
    setIdentityResetAvailable(false);
    try {
      await bridge.request({
        operation: "secure.remove",
        setting: { kind: SecureSettingKind.LogtoSession, profileId: await sessionProfileId(apiOrigin) },
      });
      sessionRef.current = null;
      setSession(null);
      setBootstrap(null);
      setAccount(null);
      setAccountError(null);
      setIdentityReady(false);
      setSettingsReady(false);
      setSettingsError(null);
      await clearIdentityQueryCache();
      retryIdentity();
    } catch (reason) {
      setStatus("error");
      setError(safeError(reason));
      setIdentityResetAvailable(true);
      throw reason;
    }
  }

  const localSettingsSession = status === "guest" || status === "signed-out";
  const localSettingsWritable = identityReady && localSettingsSession;
  const settingsReadOnly = logoutCleanupPending || replaceMutation.isPending || (!localSettingsWritable
    && (status !== "authenticated" || !online || !settingsReady || importDiff !== null || conflict !== null));
  const shortcutHydrationReady = identityReady && (localSettingsSession || ((status === "authenticated" || status === "blocked") && shortcutSettingsReady && importDiff === null && conflict === null));
  const githubPatSettingsReady = identityReady && !settingsReadOnly && conflict === null;

  const replaceSettings: IdentitySettingsValue["replaceSettings"] = async (update) => {
    const next = typeof update === "function" ? update(settingsRef.current) : update;
    const parsed = parseDevHudSettings(next);
    assertDeviceLocalSettingsPersistable(parsed);
    if (canonicalDevHudSettings(parsed) === canonicalDevHudSettings(settingsRef.current)) {
      const changesDeviceLocalSettings = !deviceLocalSettingsEqual(parsed, settingsRef.current);
      if (!shortcutHydrationReady) throw new Error("device-local-settings-not-ready");
      if (localSettingsSession) {
        if (!writeGuestSettings(storage, parsed)) throw new Error("device-local-settings-persistence-failed");
      } else {
        if (!writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: parsed, revision: revisionRef.current, contentSHA256: contentSHA256Ref.current, cachedAt: new Date().toISOString() })) throw new Error("device-local-settings-persistence-failed");
      }
      if (changesDeviceLocalSettings) deviceLocalSettingsGenerationRef.current += 1;
      applySettings(parsed);
      return true;
    }
    if (localSettingsSession) {
      const changesDeviceLocalSettings = !deviceLocalSettingsEqual(parsed, settingsRef.current);
      encodeDevHudSettings(parsed);
      if (!writeGuestSettings(storage, parsed)) throw new Error("device-local-settings-persistence-failed");
      if (changesDeviceLocalSettings) deviceLocalSettingsGenerationRef.current += 1;
      applySettings(parsed);
      return true;
    }
    if (settingsReadOnly) throw new Error("settings-read-only");
    return replaceAt(parsed, revisionRef.current);
  };
  const replaceSettingsAt: IdentitySettingsValue["replaceSettingsAt"] = async (update, expectedRevision) => {
    const next = typeof update === "function" ? update(settingsRef.current) : update;
    if (localSettingsSession) return replaceSettings(next);
    if (settingsReadOnly) throw new Error("settings-read-only");
    return replaceAt(next, expectedRevision);
  };
  settingsWritableRef.current = githubPatSettingsReady;
  replaceSettingsRef.current = replaceSettings;

  function githubPatSnapshotKey(snapshot: DevHudSettingsV1): string {
    return `${apiOrigin}|${snapshot.github.profiles.map((profile) => profile.id).join(":")}|${snapshot.github.pendingPatRemovals.join(":")}`;
  }

  function reconcileGitHubPats(): Promise<boolean> {
    if (githubPatReconciliationRef.current !== null) return githubPatReconciliationRef.current;
    const reconciliation = (async () => {
      while (settingsWritableRef.current) {
        const snapshot = settingsRef.current;
        const snapshotKey = githubPatSnapshotKey(snapshot);
        const activeProfileIds = snapshot.github.profiles.map((profile) => profile.id);
        const pendingProfileIds = snapshot.github.pendingPatRemovals;
        try {
          await bridge.request({ operation: "secure.reconcile-github-pats", scopeId: await githubPatScopeId, profileIds: activeProfileIds });
          if (pendingProfileIds.length > 0) {
            if (!settingsWritableRef.current) return false;
            const processed = new Set(pendingProfileIds);
            const committed = await replaceSettingsRef.current((current) => ({
              ...current,
              github: { ...current.github, pendingPatRemovals: current.github.pendingPatRemovals.filter((profileId) => !processed.has(profileId)) },
            }));
            if (!committed) return false;
          }
          lastReconciledGitHubPatKeyRef.current = snapshotKey;
          setGitHubPatCleanupPending(false);
        } catch (error) {
          setGitHubPatCleanupPending(true);
          throw error;
        }
        if (githubPatSnapshotKey(settingsRef.current) === snapshotKey) return true;
      }
      return false;
    })();
    githubPatReconciliationRef.current = reconciliation;
    void reconciliation.finally(() => {
      if (githubPatReconciliationRef.current === reconciliation) githubPatReconciliationRef.current = null;
    }).catch(() => {});
    return reconciliation;
  }

  const activeGitHubProfileKey = settings.github.profiles.map((profile) => profile.id).join(":");
  const pendingGitHubPatRemovalKey = settings.github.pendingPatRemovals.join(":");
  useEffect(() => {
    if (!githubPatSettingsReady) return;
    const snapshotKey = githubPatSnapshotKey(settings);
    if (lastReconciledGitHubPatKeyRef.current === snapshotKey) return;
    void reconcileGitHubPats().catch(() => {});
  }, [activeGitHubProfileKey, githubPatSettingsReady, pendingGitHubPatRemovalKey]);

  const value: IdentitySettingsValue = {
    status,
    bootstrap,
    account,
    settings,
    revision,
    readOnly: settingsReadOnly,
    shortcutHydrationReady,
    activeShortcutBindings,
    setActiveShortcutBindings,
    offline: !online,
    error,
    accountError,
    settingsError,
    deletionCleanupFailed,
    deckAccessSuspended,
    importDiff,
    conflict,
    signInPending,
    identityResetAvailable,
    githubPatScopeId,
    githubPatCleanupPending,
    reconcileGitHubPats,
    signIn: async () => {
      if (signInPendingRef.current) return;
      continueLocallyRef.current = false;
      setContinuedLocally(false);
      const current = sessionRef.current;
      if (current === null) throw new Error("bootstrap-not-ready");
      signInPendingRef.current = true;
      setSignInPending(true);
      try {
        await current.signIn();
      } finally {
        signInPendingRef.current = false;
        setSignInPending(false);
      }
    },
    retryIdentity,
    resetIdentity,
    retryAccount: async () => {
      setAccountError(null);
      await accountQuery.refetch();
    },
    retrySettings: async () => {
      setSettingsError(null);
      setError((current) => current === "settings-contract-invalid" ? null : current);
      await settingsQuery.refetch();
    },
    continueLocally: () => {
      continueLocallyRef.current = true;
      setContinuedLocally(true);
      setDeckAccessSuspended(false);
      setStatus("guest");
      setIdentityReady(true);
      setError(null);
      onContinueLocally();
    },
    uploadLocal: () => replaceAt(settings, revision),
    replaceLocal: async () => {
      let validated: ValidatedSettingsSnapshot;
      try {
        validated = await validatedSettingsSnapshot(settingsQuery.data?.snapshot);
      } catch {
        markSettingsContractInvalid();
        return false;
      }
      const next = withDeviceLocalSettings(validated.settings, settingsRef.current);
      if (!writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: next, revision: validated.revision, contentSHA256: validated.contentSHA256, cachedAt: new Date().toISOString() })) throw new Error("device-local-settings-persistence-failed");
      applySettings(next);
      applyRevision(validated.revision, validated.contentSHA256);
      setImportDiff(null);
      clearGuestImportMarker(storage);
      return true;
    },
    replaceSettings,
    replaceSettingsAt,
    adoptConflictServer: async () => {
      if (!conflict) return false;
      if (!writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: conflict.server, revision: conflict.currentRevision, contentSHA256: conflict.currentContentSHA256, cachedAt: new Date().toISOString() })) throw new Error("device-local-settings-persistence-failed");
      lastReconciledGitHubPatKeyRef.current = null;
      applySettings(conflict.server);
      applyRevision(conflict.currentRevision, conflict.currentContentSHA256);
      setConflict(null);
      clearGuestImportMarker(storage);
      return true;
    },
    reapplyConflictLocal: async () => conflict ? replaceAt(conflict.local, conflict.currentRevision, conflict.currentContentSHA256) : false,
    logout: async () => {
      invalidateDeckPolling();
      setDeckAccessSuspended(true);
      const releaseDiagnosticWrites = beginDiagnosticWriteSuppression(storage);
      try {
        setLogoutCleanupPending(true);
        const localCleanupComplete = clearAllContractedLocalData(storage);
        if (!localCleanupComplete) throw new Error("local-data-cleanup-incomplete");
        await bridge.request({ operation: "secure.purge", scope: "logout" });
        await sessionRef.current?.clear();
        sessionRef.current = null;
        setSession(null);
        clearAuthenticatedSettingsCache(storage, apiOrigin);
        setStatus("signed-out");
        setAccount(null);
        setAccountError(null);
        applySettings(defaultDevHudSettings);
        applyRevision(0n);
        setSettingsReady(false);
        resetDesktopShortcuts();
        setSettingsError(null);
        setDeckAccessSuspended(false);
        await clearIdentityQueryCache();
        onLoggedOut();
        onIdentityReset();
        setLogoutCleanupPending(false);
      } finally {
        releaseDiagnosticWrites();
      }
    },
    deleteAccount: async () => {
      const response = await deleteMutation.mutateAsync({});
      setAccount(response.account ?? null);
      setStatus("deletion-pending");
      await cleanPendingDeletion();
    },
    restoreAccount: async () => {
      try {
        const response = await restoreMutation.mutateAsync({});
        setAccount(response.account ?? null);
        setDeletionCleanupFailed(false);
        setDeckAccessSuspended(false);
        const blocked = response.account?.administrativeBlockState === AdministrativeBlockState.BLOCKED;
        setStatus(blocked ? "blocked" : "authenticated");
        if (!blocked) await settingsQuery.refetch();
      } catch (reason) {
        const mapped = mapDevHudError(reason);
        if (mapped.kind === "accountPrecondition" && mapped.detail.reason === AccountFailureReason.PURGE_CLAIMED) {
          await clearIrrecoverableAccount();
          return;
        }
        throw reason;
      }
    },
    retryDeletionCleanup: cleanPendingDeletion,
    profileRequiresSetup: async (kind, profileId) => kind === "github"
      ? profileRequiresSetup(bridge, kind, profileId, await githubPatScopeId)
      : profileRequiresSetup(bridge, kind, profileId),
  };

  function markSettingsContractInvalid(): void {
    setSettingsReady(false);
    setSettingsError(null);
    setError("settings-contract-invalid");
  }

  return <IdentitySettingsContext value={value}>{children}</IdentitySettingsContext>;
}

interface ValidatedSettingsSnapshot {
  readonly settings: DevHudSettingsV1;
  readonly revision: bigint;
  readonly contentSHA256: Uint8Array;
}

class SettingsSnapshotError extends TypeError {}

async function validatedSettingsSnapshot(snapshot: { readonly schemaVersion: number; readonly canonicalJson: Uint8Array; readonly revision: bigint; readonly contentSha256: Uint8Array } | undefined): Promise<ValidatedSettingsSnapshot> {
  if (!snapshot) return { settings: defaultDevHudSettings, revision: 0n, contentSHA256: new Uint8Array() };
  if (snapshot.schemaVersion !== LegacySettingsSchemaVersion && snapshot.schemaVersion !== PreviousSettingsSchemaVersion && snapshot.schemaVersion !== CollidingSettingsSchemaVersion && snapshot.schemaVersion !== StructuredSettingsSchemaVersion && snapshot.schemaVersion !== R2SettingsSchemaVersion && snapshot.schemaVersion !== AgentPromptSettingsSchemaVersion && snapshot.schemaVersion !== SettingsSchemaVersion) throw new SettingsSnapshotError("unsupported settings schema version");
  try {
    if (snapshot.schemaVersion === SettingsSchemaVersion) {
      if (snapshot.contentSha256.byteLength !== 32) throw new SettingsSnapshotError("settings snapshot content digest is invalid");
      const canonicalBuffer = new ArrayBuffer(snapshot.canonicalJson.byteLength);
      new Uint8Array(canonicalBuffer).set(snapshot.canonicalJson);
      const actual = new Uint8Array(await crypto.subtle.digest("SHA-256", canonicalBuffer));
      if (actual.some((byte, index) => byte !== snapshot.contentSha256[index])) throw new SettingsSnapshotError("settings snapshot content digest does not match canonical JSON");
    }
    return { settings: decodeVersionedDevHudSettings(snapshot.canonicalJson, snapshot.schemaVersion), revision: snapshot.revision, contentSHA256: Uint8Array.from(snapshot.contentSha256) };
  } catch (reason) {
    throw new SettingsSnapshotError("invalid settings snapshot", { cause: reason });
  }
}

export async function clearIdentityForApiChange(
  bridge: NativeBridgeV1,
  storage: Storage,
  oldApiOrigin: string,
  sessionRef?: RefObject<IdentitySession | null>,
): Promise<void> {
  const session = sessionRef?.current ?? null;
  if (sessionRef) sessionRef.current = null;
  const discardedCallback = await bridge.request({ operation: "auth.take-pending-callback" });
  if (discardedCallback.kind !== "auth-callback") throw new Error("auth-callback-discard-failed");
  await session?.clear();
  const scopeId = await sessionProfileId(oldApiOrigin);
  await bridge.request({ operation: "secure.purge", scope: "api-change", profileId: scopeId });
  clearAuthenticatedOriginData(storage, oldApiOrigin);
  clearDeckCaches(storage, scopeId);
}

export function saveGuestSettings(storage: Storage, settings: DevHudSettingsV1): boolean {
  return writeGuestSettings(storage, settings);
}

function safeError(reason: unknown): string {
  if (reason instanceof Error && /^[a-z0-9-]{1,64}$/u.test(reason.message)) return reason.message;
  if (reason instanceof Error && /^[A-Za-z]{1,64}$/u.test(reason.name)) return `identity-${reason.name.toLowerCase()}`;
  return "identity-boundary-failed";
}
