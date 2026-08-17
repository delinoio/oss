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
import { clearAllContractedLocalData, clearAuthenticatedOriginData, clearAuthenticatedSettingsCache, clearGuestImportMarker, hasGuestSettings, readAuthenticatedSettingsCache, readCachedIdentityBootstrap, readGuestSettings, writeAuthenticatedSettingsCache, writeCachedIdentityBootstrap, writeGuestSettings } from "./local-data";
import { SecureSettingKind, type NativeBridgeV1, type RuntimePlatform } from "./native-bridge";
import { profileRequiresSetup } from "./profile-secrets";
import { defaultDevHudSettings, decodeVersionedDevHudSettings, encodeDevHudSettings, LegacySettingsSchemaVersion, parseDevHudSettings, SettingsSchemaVersion, type DevHudSettingsV1 } from "./settings-contract";
import { diffSettings, type SettingsDiffEntry } from "./settings-diff";
import { getLocalStorage, isValidApiOrigin } from "./shell";

export type IdentityStatus = "guest" | "starting" | "signed-out" | "authenticated" | "blocked" | "deletion-pending" | "error";

export interface SettingsConflict {
  readonly local: DevHudSettingsV1;
  readonly server: DevHudSettingsV1;
  readonly currentRevision: bigint;
  readonly diff: readonly SettingsDiffEntry[];
}

export interface IdentitySettingsValue {
  readonly status: IdentityStatus;
  readonly bootstrap: ValidatedBootstrap | null;
  readonly account: Account | null;
  readonly settings: DevHudSettingsV1;
  readonly revision: bigint;
  readonly readOnly: boolean;
  readonly offline: boolean;
  readonly error: string | null;
  readonly accountError: DevHudClientError | null;
  readonly settingsError: DevHudClientError | null;
  readonly deletionCleanupFailed: boolean;
  readonly importDiff: readonly SettingsDiffEntry[] | null;
  readonly conflict: SettingsConflict | null;
  readonly signInPending: boolean;
  readonly identityResetAvailable: boolean;
  readonly githubPatScopeId: Promise<string>;
  readonly signIn: () => Promise<void>;
  readonly retryIdentity: () => void;
  readonly resetIdentity: () => Promise<void>;
  readonly retryAccount: () => Promise<void>;
  readonly retrySettings: () => Promise<void>;
  readonly continueLocally: () => void;
  readonly uploadLocal: () => Promise<void>;
  readonly replaceLocal: () => void;
  readonly replaceSettings: (settings: DevHudSettingsV1 | ((current: DevHudSettingsV1) => DevHudSettingsV1)) => Promise<boolean>;
  readonly adoptConflictServer: () => void;
  readonly reapplyConflictLocal: () => Promise<void>;
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
      return next(request);
    }],
  }), [props.apiOrigin]);

  useEffect(() => () => queryClient.clear(), [queryClient]);

  return <TransportProvider transport={transport}><QueryClientProvider client={queryClient}>
    <IdentitySettingsProvider key={identityEpoch} {...props} sessionRef={sessionRef} onIdentityReset={() => setIdentityEpoch((current) => current + 1)}>
      {props.children}
    </IdentitySettingsProvider>
  </QueryClientProvider></TransportProvider>;
}

function IdentitySettingsProvider({ apiOrigin, active, online, callbackUrl, platform, bridge, onCallbackConsumed, onContinueLocally, onLoggedOut, initialAppearance, children, sessionRef, onIdentityReset }: BoundaryProps & { readonly sessionRef: RefObject<IdentitySession | null>; readonly onIdentityReset: () => void }) {
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
  const [revision, setRevision] = useState(0n);
  const revisionRef = useRef(revision);
  const githubPatScopeId = useMemo(() => sessionProfileId(apiOrigin), [apiOrigin]);
  const [error, setError] = useState<string | null>(null);
  const [accountError, setAccountError] = useState<DevHudClientError | null>(null);
  const [settingsError, setSettingsError] = useState<DevHudClientError | null>(null);
  const [deletionCleanupFailed, setDeletionCleanupFailed] = useState(false);
  const [importDiff, setImportDiff] = useState<readonly SettingsDiffEntry[] | null>(null);
  const [conflict, setConflict] = useState<SettingsConflict | null>(null);
  const [account, setAccount] = useState<Account | null>(null);
  const [networkReady, setNetworkReady] = useState(false);
  const [identityReady, setIdentityReady] = useState(false);
  const [settingsReady, setSettingsReady] = useState(false);
  const [bootstrapAttempt, setBootstrapAttempt] = useState(0);
  const [signInPending, setSignInPending] = useState(false);
  const [identityResetAvailable, setIdentityResetAvailable] = useState(false);
  const signInPendingRef = useRef(false);
  const callbackHandled = useRef<string | null>(null);
  const invalidSessionCleanupRef = useRef<Promise<void> | null>(null);
  const continueLocallyRef = useRef(false);

  function applySettings(next: DevHudSettingsV1): void {
    settingsRef.current = next;
    setSettings(next);
  }

  function applyRevision(next: bigint): void {
    revisionRef.current = next;
    setRevision(next);
  }

  const accountQueryKey = useMemo(() => createConnectQueryKey({ schema: AccountQuery.getAccount, transport, input: {}, cardinality: "finite" }), [transport]);
  const settingsQueryKey = useMemo(() => createConnectQueryKey({ schema: SettingsQuery.getSettings, transport, input: {}, cardinality: "finite" }), [transport]);

  const bootstrapQuery = useQuery(BootstrapQuery.getBootstrap, {}, { enabled: active && online && networkReady && isValidApiOrigin(apiOrigin) });
  const accountQuery = useQuery(AccountQuery.getAccount, {}, { enabled: status === "authenticated" && online });
  const settingsQuery = useQuery(SettingsQuery.getSettings, {}, { enabled: status === "authenticated" && online });
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

  function clearInvalidSession(): Promise<void> {
    if (invalidSessionCleanupRef.current !== null) return invalidSessionCleanupRef.current;
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
        setSettingsError(null);
        setStatus("signed-out");
        clearAuthenticatedSettingsCache(storage, apiOrigin);
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
    clearAllContractedLocalData(storage);
    try {
      await bridge.request({ operation: "secure.purge", scope: "account-deletion", profileId: await sessionProfileId(apiOrigin) });
      setDeletionCleanupFailed(false);
    } catch {
      setDeletionCleanupFailed(true);
    }
  }

  async function clearIrrecoverableAccount(): Promise<void> {
    setStatus("error");
    clearAllContractedLocalData(storage);
    try {
      await bridge.request({ operation: "secure.purge", scope: "logout" });
      sessionRef.current = null;
      setSession(null);
      setAccount(null);
      setAccountError(null);
      applySettings(defaultDevHudSettings);
      applyRevision(0n);
      setSettingsReady(false);
      setSettingsError(null);
      setStatus("signed-out");
      setError(null);
      await clearIdentityQueryCache();
      onIdentityReset();
    } catch (reason) {
      setError(safeError(reason));
      throw reason;
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
        if (!authenticated) clearAuthenticatedSettingsCache(storage, apiOrigin);
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
        if (!authenticated) clearAuthenticatedSettingsCache(storage, apiOrigin);
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
    if (status !== "authenticated") return;
    if (!online) {
      setSettingsReady(false);
      const cached = readAuthenticatedSettingsCache(storage, apiOrigin);
      if (cached) { applySettings(cached.settings); applyRevision(cached.revision); }
      return;
    }
    if (!settingsQuery.data) return;
    let server: DevHudSettingsV1;
    let currentRevision: bigint;
    try {
      ({ settings: server, revision: currentRevision } = validatedSettingsSnapshot(settingsQuery.data.snapshot));
    } catch {
      markSettingsContractInvalid();
      return;
    }
    setError((current) => current === "settings-contract-invalid" ? null : current);
    setSettingsError(null);
    setSettingsReady(true);
    applyRevision(currentRevision);
    if (hasGuestSettings(storage)) {
      const local = readGuestSettings(storage);
      applySettings(local);
      setImportDiff(diffSettings(local, server));
    } else {
      applySettings(server);
      writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: server, revision: currentRevision, cachedAt: new Date().toISOString() });
    }
  }, [apiOrigin, online, settingsQuery.data, status, storage]);

  useEffect(() => {
    if (status !== "authenticated" || !online || !settingsQuery.error) return;
    const mapped = mapDevHudError(settingsQuery.error);
    setSettingsReady(false);
    setSettingsError(mapped);
    const cached = readAuthenticatedSettingsCache(storage, apiOrigin);
    if (cached) {
      applySettings(cached.settings);
      applyRevision(cached.revision);
    }
    if (mapped.kind === "unauthenticated") {
      void clearInvalidSession().catch((reason) => setError(safeError(reason)));
    } else if (mapped.kind === "permissionDenied") {
      if (mapped.detail.reason === PermissionFailureReason.ACCOUNT_DELETION_PENDING) {
        setStatus("deletion-pending");
        void cleanPendingDeletion();
      }
      if (mapped.detail.reason === PermissionFailureReason.USER_BLOCKED) setStatus("blocked");
    }
  }, [apiOrigin, online, settingsQuery.error, status, storage]);

  async function replaceAt(local: DevHudSettingsV1, expectedRevision: bigint): Promise<boolean> {
    if (!online) throw new Error("offline-read-only");
    setSettingsError(null);
    let canonicalJson: Uint8Array;
    try {
      canonicalJson = encodeDevHudSettings(local);
    } catch (reason) {
      setError("settings-contract-invalid");
      throw reason;
    }
    try {
      const response = await replaceMutation.mutateAsync({ schemaVersion: SettingsSchemaVersion, canonicalJson: Uint8Array.from(canonicalJson), expectedRevision });
      let validated: ValidatedSettingsSnapshot;
      try {
        if (!response.snapshot) throw new SettingsSnapshotError("settings response is missing its snapshot");
        validated = validatedSettingsSnapshot(response.snapshot);
      } catch (reason) {
        markSettingsContractInvalid();
        throw reason;
      }
      const next = validated.settings;
      applySettings(next);
      applyRevision(validated.revision);
      setSettingsReady(true);
      setError((current) => current === "settings-contract-invalid" ? null : current);
      setImportDiff(null);
      setConflict(null);
      clearGuestImportMarker(storage);
      writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: next, revision: validated.revision, cachedAt: new Date().toISOString() });
      return true;
    } catch (reason) {
      if (reason instanceof SettingsSnapshotError) throw reason;
      const mapped = mapDevHudError(reason);
      if (mapped.kind === "revisionConflict") {
        const serverSnapshot = mapped.detail.currentSnapshot;
        let validated: ValidatedSettingsSnapshot;
        try {
          validated = validatedSettingsSnapshot(serverSnapshot);
        } catch (snapshotReason) {
          setConflict(null);
          markSettingsContractInvalid();
          throw snapshotReason;
        }
        setImportDiff(null);
        setConflict({ local, server: validated.settings, currentRevision: validated.revision, diff: diffSettings(local, validated.settings) });
        return false;
      }
      setSettingsError(mapped);
      throw reason;
    }
  }

  function retryIdentity(): void {
    continueLocallyRef.current = false;
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

  const localSettingsWritable = identityReady && (status === "guest" || status === "signed-out");
  const settingsReadOnly = replaceMutation.isPending || (!localSettingsWritable
    && (status !== "authenticated" || !online || !settingsReady || importDiff !== null || conflict !== null));

  const value: IdentitySettingsValue = {
    status,
    bootstrap,
    account,
    settings,
    revision,
    readOnly: settingsReadOnly,
    offline: !online,
    error,
    accountError,
    settingsError,
    deletionCleanupFailed,
    importDiff,
    conflict,
    signInPending,
    identityResetAvailable,
    githubPatScopeId,
    signIn: async () => {
      if (signInPendingRef.current) return;
      continueLocallyRef.current = false;
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
      setStatus("guest");
      setIdentityReady(true);
      setError(null);
      onContinueLocally();
    },
    uploadLocal: async () => { await replaceAt(settings, revision); },
    replaceLocal: () => {
      let validated: ValidatedSettingsSnapshot;
      try {
        validated = validatedSettingsSnapshot(settingsQuery.data?.snapshot);
      } catch {
        markSettingsContractInvalid();
        return;
      }
      applySettings(validated.settings);
      applyRevision(validated.revision);
      setImportDiff(null);
      clearGuestImportMarker(storage);
      writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: validated.settings, revision: validated.revision, cachedAt: new Date().toISOString() });
    },
    replaceSettings: async (update) => {
      const next = typeof update === "function" ? update(settingsRef.current) : update;
      if (status === "guest" || status === "signed-out") {
        const parsed = parseDevHudSettings(next);
        writeGuestSettings(storage, parsed);
        applySettings(parsed);
        return true;
      }
      if (settingsReadOnly) throw new Error("settings-read-only");
      return replaceAt(next, revisionRef.current);
    },
    adoptConflictServer: () => {
      if (!conflict) return;
      applySettings(conflict.server);
      applyRevision(conflict.currentRevision);
      setConflict(null);
      clearGuestImportMarker(storage);
      writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: conflict.server, revision: conflict.currentRevision, cachedAt: new Date().toISOString() });
    },
    reapplyConflictLocal: async () => { if (conflict) await replaceAt(conflict.local, conflict.currentRevision); },
    logout: async () => {
      await bridge.request({ operation: "secure.purge", scope: "logout" });
      await sessionRef.current?.clear();
      sessionRef.current = null;
      setSession(null);
      clearAuthenticatedSettingsCache(storage, apiOrigin);
      clearAllContractedLocalData(storage);
      setStatus("signed-out");
      setAccount(null);
      setAccountError(null);
      applySettings(defaultDevHudSettings);
      applyRevision(0n);
      setSettingsReady(false);
      setSettingsError(null);
      await clearIdentityQueryCache();
      onIdentityReset();
      onLoggedOut();
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
}

class SettingsSnapshotError extends TypeError {}

function validatedSettingsSnapshot(snapshot: { readonly schemaVersion: number; readonly canonicalJson: Uint8Array; readonly revision: bigint } | undefined): ValidatedSettingsSnapshot {
  if (!snapshot) return { settings: defaultDevHudSettings, revision: 0n };
  if (snapshot.schemaVersion !== LegacySettingsSchemaVersion && snapshot.schemaVersion !== SettingsSchemaVersion) throw new SettingsSnapshotError("unsupported settings schema version");
  try {
    return { settings: decodeVersionedDevHudSettings(snapshot.canonicalJson, snapshot.schemaVersion), revision: snapshot.revision };
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
  await bridge.request({ operation: "secure.purge", scope: "api-change", profileId: await sessionProfileId(oldApiOrigin) });
  clearAuthenticatedOriginData(storage, oldApiOrigin);
}

export function saveGuestSettings(storage: Storage, settings: DevHudSettingsV1): void {
  writeGuestSettings(storage, settings);
}

function safeError(reason: unknown): string {
  if (reason instanceof Error && /^[a-z0-9-]{1,64}$/u.test(reason.message)) return reason.message;
  if (reason instanceof Error && /^[A-Za-z]{1,64}$/u.test(reason.name)) return `identity-${reason.name.toLowerCase()}`;
  return "identity-boundary-failed";
}
