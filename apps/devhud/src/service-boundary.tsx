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
import { createIdentitySession, sessionProfileId, validateBootstrap, type IdentitySession, type ValidatedBootstrap } from "./identity-client";
import { clearAllContractedLocalData, clearAuthenticatedOriginData, clearGuestImportMarker, hasGuestSettings, readAuthenticatedSettingsCache, readCachedIdentityBootstrap, readGuestSettings, writeAuthenticatedSettingsCache, writeCachedIdentityBootstrap, writeGuestSettings } from "./local-data";
import type { NativeBridgeV1, RuntimePlatform } from "./native-bridge";
import { profileRequiresSetup } from "./profile-secrets";
import { defaultDevHudSettings, decodeDevHudSettings, encodeDevHudSettings, SettingsSchemaVersion, type DevHudSettingsV1 } from "./settings-contract";
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
  readonly settingsError: DevHudClientError | null;
  readonly importDiff: readonly SettingsDiffEntry[] | null;
  readonly conflict: SettingsConflict | null;
  readonly signIn: () => Promise<void>;
  readonly retryIdentity: () => void;
  readonly continueLocally: () => void;
  readonly uploadLocal: () => Promise<void>;
  readonly replaceLocal: () => void;
  readonly replaceSettings: (settings: DevHudSettingsV1) => Promise<void>;
  readonly adoptConflictServer: () => void;
  readonly reapplyConflictLocal: () => Promise<void>;
  readonly logout: () => Promise<void>;
  readonly deleteAccount: () => Promise<void>;
  readonly restoreAccount: () => Promise<void>;
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
  readonly onContinueLocally: () => void;
  readonly onLoggedOut: () => void;
}

export function DevHudServiceBoundary(props: BoundaryProps) {
  const sessionRef = useRef<IdentitySession | null>(null);
  const [identityEpoch, setIdentityEpoch] = useState(0);
  const queryClient = useMemo(() => new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } }), [identityEpoch, props.apiOrigin]);
  const transport = useMemo(() => createConnectTransport({
    baseUrl: props.apiOrigin,
    interceptors: [(next) => async (request) => {
      if (!request.url.endsWith("/GetBootstrap") && sessionRef.current !== null) {
        const token = await sessionRef.current.getAccessToken();
        request.header.set("Authorization", `Bearer ${token}`);
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

function IdentitySettingsProvider({ apiOrigin, active, online, callbackUrl, platform, bridge, onContinueLocally, onLoggedOut, children, sessionRef, onIdentityReset }: BoundaryProps & { readonly sessionRef: RefObject<IdentitySession | null>; readonly onIdentityReset: () => void }) {
  const storage = getLocalStorage();
  const queryClient = useQueryClient();
  const transport = useTransport();
  const [status, setStatus] = useState<IdentityStatus>("guest");
  const [session, setSession] = useState<IdentitySession | null>(null);
  const [bootstrap, setBootstrap] = useState<ValidatedBootstrap | null>(null);
  const [settings, setSettings] = useState<DevHudSettingsV1>(() => readGuestSettings(storage));
  const [revision, setRevision] = useState(0n);
  const [error, setError] = useState<string | null>(null);
  const [settingsError, setSettingsError] = useState<DevHudClientError | null>(null);
  const [importDiff, setImportDiff] = useState<readonly SettingsDiffEntry[] | null>(null);
  const [conflict, setConflict] = useState<SettingsConflict | null>(null);
  const [account, setAccount] = useState<Account | null>(null);
  const [networkReady, setNetworkReady] = useState(false);
  const [settingsReady, setSettingsReady] = useState(false);
  const [bootstrapAttempt, setBootstrapAttempt] = useState(0);
  const callbackHandled = useRef<string | null>(null);

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

  async function clearInvalidSession(): Promise<void> {
    const current = sessionRef.current;
    sessionRef.current = null;
    setSession(null);
    try {
      await current?.clear();
    } finally {
      setAccount(null);
      setSettings(defaultDevHudSettings);
      setRevision(0n);
      setSettingsReady(false);
      setSettingsError(null);
      setStatus("signed-out");
      await clearIdentityQueryCache();
      onIdentityReset();
    }
  }

  async function cleanPendingDeletion(): Promise<void> {
    clearAllContractedLocalData(storage);
    await bridge.request({ operation: "secure.purge", scope: "account-deletion", profileId: await sessionProfileId(apiOrigin) });
  }

  async function clearIrrecoverableAccount(): Promise<void> {
    setStatus("error");
    clearAllContractedLocalData(storage);
    try {
      await bridge.request({ operation: "secure.purge", scope: "logout" });
      sessionRef.current = null;
      setSession(null);
      setAccount(null);
      setSettings(defaultDevHudSettings);
      setRevision(0n);
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
      if (!cancelled) { setStatus("error"); setError(safeError(reason)); }
    });
    return () => { cancelled = true; };
  }, [active, apiOrigin, bootstrapAttempt, bridge]);

  useEffect(() => {
    if (!active || !online || !networkReady || !bootstrapQuery.data) return;
    let cancelled = false;
    setStatus((current) => current === "guest" ? "starting" : current);
    void (async () => {
      try {
        const validated = validateBootstrap(bootstrapQuery.data, platform);
        try {
          writeCachedIdentityBootstrap(storage, apiOrigin, validated);
        } catch {
          // Bootstrap caching is optional; the online session remains usable when Web Storage rejects writes.
        }
        const policy = await bridge.request({ operation: "session.configure-origins", apiOrigin, logtoIssuer: validated.issuer });
        if (policy.kind === "session-network-policy" && policy.changed) {
          location.reload();
          return;
        }
        const session = await createIdentitySession(validated, apiOrigin, bridge);
        const authenticated = await session.isAuthenticated();
        if (cancelled) return;
        sessionRef.current = session;
        setSession(session);
        setBootstrap(validated);
        setStatus(authenticated ? "authenticated" : "signed-out");
        setError(null);
      } catch (reason) {
        if (!cancelled) {
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
    if (cached === null) return;
    let cancelled = false;
    void (async () => {
      try {
        const policy = await bridge.request({ operation: "session.configure-origins", apiOrigin, logtoIssuer: cached.issuer });
        if (policy.kind === "session-network-policy" && policy.changed) {
          location.reload();
          return;
        }
        const session = await createIdentitySession(cached, apiOrigin, bridge);
        const authenticated = await session.isAuthenticated();
        if (cancelled) return;
        sessionRef.current = session;
        setSession(session);
        setBootstrap(cached);
        setStatus(authenticated ? "authenticated" : "signed-out");
        setError(null);
      } catch (reason) {
        if (!cancelled) {
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
      const pending = await bridge.request({ operation: "auth.take-pending-callback" });
      if (pending.kind !== "auth-callback" || pending.url !== callbackUrl) throw new Error("auth-callback-unavailable");
      await session.handleCallback(callbackUrl);
      setStatus("authenticated");
      setError(null);
    })().catch((reason) => {
      callbackHandled.current = null;
      setStatus("error");
      setError(safeError(reason));
    });
  }, [bridge, callbackUrl, session]);

  useEffect(() => {
    if (!networkReady || !bootstrapQuery.error) return;
    setStatus("error");
    setError("bootstrap-unavailable");
  }, [bootstrapQuery.error, networkReady]);

  useEffect(() => {
    if (!accountQuery.data?.account) return;
    const next = accountQuery.data.account;
    setAccount(next);
    if (next.deletionState === AccountDeletionState.PURGE_CLAIMED) {
      void clearIrrecoverableAccount().catch(() => {});
    } else if (next.deletionState === AccountDeletionState.PENDING) {
      setStatus("deletion-pending");
      void cleanPendingDeletion().catch((reason) => setError(safeError(reason)));
    } else if (next.administrativeBlockState === AdministrativeBlockState.BLOCKED) setStatus("blocked");
  }, [accountQuery.data]);

  useEffect(() => {
    if (!accountQuery.error) return;
    const mapped = mapDevHudError(accountQuery.error);
    if (mapped.kind === "unauthenticated") {
      void clearInvalidSession().catch((reason) => setError(safeError(reason)));
    } else if (mapped.kind === "accountPrecondition" && mapped.detail.reason === AccountFailureReason.PURGE_CLAIMED) {
      void clearIrrecoverableAccount().catch(() => {});
    } else if (mapped.kind === "permissionDenied") {
      if (mapped.detail.reason === PermissionFailureReason.ACCOUNT_DELETION_PENDING) setStatus("deletion-pending");
      if (mapped.detail.reason === PermissionFailureReason.USER_BLOCKED) setStatus("blocked");
    }
  }, [accountQuery.error]);

  useEffect(() => {
    if (status !== "authenticated") return;
    if (!online) {
      setSettingsReady(false);
      const cached = readAuthenticatedSettingsCache(storage, apiOrigin);
      if (cached) { setSettings(cached.settings); setRevision(cached.revision); }
      return;
    }
    if (!settingsQuery.data) return;
    let server = defaultDevHudSettings;
    let currentRevision = 0n;
    try {
      if (settingsQuery.data.snapshot) {
        if (settingsQuery.data.snapshot.schemaVersion !== SettingsSchemaVersion) throw new TypeError("unsupported settings schema version");
        server = decodeDevHudSettings(settingsQuery.data.snapshot.canonicalJson);
        currentRevision = settingsQuery.data.snapshot.revision;
      }
    } catch {
      setSettingsReady(false);
      setError("settings-contract-invalid");
      return;
    }
    setError((current) => current === "settings-contract-invalid" ? null : current);
    setSettingsError(null);
    setSettingsReady(true);
    setRevision(currentRevision);
    if (hasGuestSettings(storage)) {
      const local = readGuestSettings(storage);
      setSettings(local);
      setImportDiff(diffSettings(local, server));
    } else {
      setSettings(server);
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
      setSettings(cached.settings);
      setRevision(cached.revision);
    }
    if (mapped.kind === "unauthenticated") {
      void clearInvalidSession().catch((reason) => setError(safeError(reason)));
    } else if (mapped.kind === "permissionDenied") {
      if (mapped.detail.reason === PermissionFailureReason.ACCOUNT_DELETION_PENDING) setStatus("deletion-pending");
      if (mapped.detail.reason === PermissionFailureReason.USER_BLOCKED) setStatus("blocked");
    }
  }, [apiOrigin, online, settingsQuery.error, status, storage]);

  async function replaceAt(local: DevHudSettingsV1, expectedRevision: bigint): Promise<void> {
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
      const response = await replaceMutation.mutateAsync({ schemaVersion: 1, canonicalJson: Uint8Array.from(canonicalJson), expectedRevision });
      if (!response.snapshot) throw new Error("settings-response-missing-snapshot");
      if (response.snapshot.schemaVersion !== SettingsSchemaVersion) {
        setSettingsReady(false);
        setError("settings-contract-invalid");
        throw new TypeError("unsupported settings schema version");
      }
      const next = decodeDevHudSettings(response.snapshot.canonicalJson);
      setSettings(next);
      setRevision(response.snapshot.revision);
      setSettingsReady(true);
      setError((current) => current === "settings-contract-invalid" ? null : current);
      setImportDiff(null);
      setConflict(null);
      clearGuestImportMarker(storage);
      writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: next, revision: response.snapshot.revision, cachedAt: new Date().toISOString() });
    } catch (reason) {
      const mapped = mapDevHudError(reason);
      if (mapped.kind === "revisionConflict") {
        const serverSnapshot = mapped.detail.currentSnapshot;
        const server = serverSnapshot ? decodeDevHudSettings(serverSnapshot.canonicalJson) : defaultDevHudSettings;
        const currentRevision = serverSnapshot?.revision ?? 0n;
        setImportDiff(null);
        setConflict({ local, server, currentRevision, diff: diffSettings(local, server) });
        return;
      }
      setSettingsError(mapped);
      throw reason;
    }
  }

  const value: IdentitySettingsValue = {
    status,
    bootstrap,
    account,
    settings,
    revision,
    readOnly: status !== "authenticated" || !online || !settingsReady || importDiff !== null || conflict !== null,
    offline: !online,
    error,
    settingsError,
    importDiff,
    conflict,
    signIn: async () => {
      if (sessionRef.current === null) throw new Error("bootstrap-not-ready");
      await sessionRef.current.signIn();
    },
    retryIdentity: () => {
      setStatus("starting");
      setError(null);
      setNetworkReady(false);
      setBootstrapAttempt((current) => current + 1);
    },
    continueLocally: () => { setStatus("guest"); onContinueLocally(); },
    uploadLocal: () => replaceAt(settings, revision),
    replaceLocal: () => {
      const server = settingsQuery.data?.snapshot ? decodeDevHudSettings(settingsQuery.data.snapshot.canonicalJson) : defaultDevHudSettings;
      const serverRevision = settingsQuery.data?.snapshot?.revision ?? 0n;
      setSettings(server);
      setRevision(serverRevision);
      setImportDiff(null);
      clearGuestImportMarker(storage);
      writeAuthenticatedSettingsCache(storage, apiOrigin, { settings: server, revision: serverRevision, cachedAt: new Date().toISOString() });
    },
    replaceSettings: (next) => replaceAt(next, revision),
    adoptConflictServer: () => {
      if (!conflict) return;
      setSettings(conflict.server);
      setRevision(conflict.currentRevision);
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
      clearAllContractedLocalData(storage);
      setStatus("signed-out");
      setAccount(null);
      setSettings(defaultDevHudSettings);
      setRevision(0n);
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
    profileRequiresSetup: (kind, profileId) => profileRequiresSetup(bridge, kind, profileId),
  };

  return <IdentitySettingsContext value={value}>{children}</IdentitySettingsContext>;
}

export async function clearIdentityForApiChange(bridge: NativeBridgeV1, storage: Storage, oldApiOrigin: string): Promise<void> {
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
