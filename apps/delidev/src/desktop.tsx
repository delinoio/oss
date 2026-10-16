// SPDX-License-Identifier: Apache-2.0
import {
  AppInformation,
  AppInformationSlot,
  retainedDesktopContext,
  type OpenAppInformationLink,
} from "./app-information";
import { QuitConnectionProvider, useQuitConnection } from "./quit-confirmation";
import { createDesktopFetch } from "./desktop-runtime";
import { copy, useLocale } from "./localization";
import { OAuthNativeProvider, type OAuthNativeControl } from "./account-oauth";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { type Transport } from "@connectrpc/connect";
import { createDeliDevTransport } from "@delinoio/delidev-api-client";
import { Updates, type DesktopUpdateControls } from "./updates";
import { App } from "./App";
import { TailscaleConnections } from "./tailscale-connections";
import type { RunnerTailscaleSlots } from "./runner-tailscale-slots";
import {
  PersistentConnectionView,
  ConnectionsPage,
  type ConnectionPageSlots,
} from "./connections-page";
import { LocalConnectionPresentation } from "./local-connection-presentation";
import { Modal, Problem } from "./ui";
import {
  EntranceMotion,
  useEntranceActivation,
  type EntranceProps,
} from "./entrance-motion";
import {
  SavedConnections,
  SavedConnectionProblem,
  type SavedConnection,
  type SavedConnectionActions,
} from "./saved-connections";
import {
  LocalServerControls,
  LocalServerState,
  LocalServerStatusText,
  type LocalServerStatus,
} from "./local-server";
import type {
  ControlLocalWorker,
  LocalWorkerStatus,
} from "./local-worker-controls";
import {
  WorkerNetworkControlProvider,
  type ControlWorkerNetwork,
} from "./worker-network-native";
import { verifyLocalServer } from "./connection";
import { CredentialAccessGate } from "./credential-access";

import {
  LocalRegistrationRecovery,
  localPermissionProblem,
  type NativeConnection,
} from "./local-registration";
import { localStartupProblem } from "./local-startup-problem";
import { ReloadShortcutOwner } from "./reload-shortcut";
const nativeProblems: Record<string, string> = {
  get "service-managed"() {
    return copy("desktop.aNativeServiceOwnsThisConnection_413f9b");
  },
  get stopped() {
    return copy("desktop.theServerWasExplicitlyStoppedStart_5d2c3b");
  },
  get "ownership-conflict"() {
    return localStartupProblem("ownership-conflict")!;
  },
  get "startup-conflict"() {
    return localStartupProblem("startup-conflict")!;
  },
  get busy() {
    return copy("desktop.aLocalConnectionAttemptIsAlready_2efbe3");
  },
  get "sidecar-missing"() {
    return copy("desktop.theBundledDelidevExecutableIsMissing_2b5194");
  },
  get "sidecar-failed"() {
    return copy("desktop.theLocalServerCouldNotConnect_88c6fd");
  },
  get "timed-out"() {
    return copy("desktop.localStartupHasNotCompletedCheck_90d29e");
  },
  get incompatible() {
    return copy("desktop.theRunningServerUsesADifferent_327644");
  },
  get "credential-unavailable"() {
    return copy("desktop.thisDesktopCredentialIsUnavailableOr_55cfb5");
  },
  get "permission-denied"() {
    return localPermissionProblem();
  },
  get "invalid-evidence"() {
    return copy("desktop.theRetainedLocalConnectionRequiresInspection_c0ad7a");
  },
  get "storage-unavailable"() {
    return copy("desktop.thePrivateDelidevConfigurationDirectoryIs_3958ae");
  },
};
function LocalDesktop({ entrance }: { entrance: EntranceProps }) {
  useLocale();
  const [showSaved, setShowSaved] = useState(false);
  const [inlineRecovery, setInlineRecovery] = useState(false);
  const [inlineRecoveryTarget, setInlineRecoveryTarget] =
    useState<HTMLElement>();
  const [showConnection, setShowConnection] = useState(false);
  const [pageSlots, setPageSlots] = useState<ConnectionPageSlots>();
  const [runnerSlots, setRunnerSlots] = useState<RunnerTailscaleSlots>();
  const [savedRefresh, setSavedRefresh] = useState(0);
  const [appInformationTarget, setAppInformationTarget] =
    useState<HTMLElement>();
  const [connectionTarget, setConnectionTarget] =
    useState<HTMLDivElement | null>(null);
  const [recoveryTarget, setRecoveryTarget] = useState<HTMLDivElement | null>(
    null,
  );
  const previous = useRef<NativeConnection>(undefined);
  const connectionReadGeneration = useRef(0);
  const [connectionVerified, setConnectionVerified] = useState(true);
  const [status, setStatus] = useState<LocalServerStatus>();
  const [connectionEpoch, setConnectionEpoch] = useState(0);
  useEffect(() => {
    if (!isTauri()) return;
    let canceled = false;
    let timer: ReturnType<typeof setTimeout>;
    const read = async () => {
      try {
        const value = await invoke<LocalServerStatus>("local_server_status");
        if (!canceled) setStatus(value);
      } catch {
        if (!canceled) setStatus(undefined);
      } finally {
        if (!canceled) timer = setTimeout(() => void read(), 2000);
      }
    };
    void read();
    return () => {
      canceled = true;
      clearTimeout(timer);
    };
  }, []);
  const [transport, setTransport] = useState<Transport>();
  useQuitConnection(
    transport,
    connectionVerified && status?.state === LocalServerState.Ready,
  );
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(isTauri());
  const connecting = useRef(false);
  const acceptConnection = async (
    connection: NativeConnection,
    current: () => boolean = () => true,
  ) => {
    if (!current()) return;
    const generation = connectionReadGeneration.current;
    if (previous.current && previous.current.endpoint !== connection.endpoint)
      throw "invalid-evidence";
    const candidate = createDeliDevTransport({
      origin: connection.endpoint,
      getToken: () => connection.token,
      fetch: createDesktopFetch(connection),
    });
    await verifyLocalServer(candidate, connection.server_id);
    if (!current() || generation !== connectionReadGeneration.current) return;
    const old = previous.current;
    if (
      !old ||
      old.server_id !== connection.server_id ||
      old.device_id !== connection.device_id ||
      old.endpoint !== connection.endpoint ||
      old.token !== connection.token ||
      old.runtime_generation !== connection.runtime_generation
    )
      setTransport(candidate);
    previous.current = connection;
    setConnectionVerified(true);
    setConnectionEpoch((epoch) => epoch + 1);
    setError(undefined);
  };
  const acceptRecovery = async (
    connection: NativeConnection,
    admissionGeneration?: number,
  ) => {
    let generation = admissionGeneration ?? connectionReadGeneration.current;
    if (generation !== connectionReadGeneration.current) {
      // Recovery can publish its own connection-change event. Reconcile only
      // the exact returned native outcome, never adopt a sibling replacement.
      generation = connectionReadGeneration.current;
      const observed = await invoke<NativeConnection | null>("launch_local");
      if (
        generation !== connectionReadGeneration.current ||
        !observed ||
        observed.server_id !== connection.server_id ||
        observed.device_id !== connection.device_id ||
        observed.endpoint !== connection.endpoint ||
        observed.runtime_generation !== connection.runtime_generation ||
        observed.runtime_key !== connection.runtime_key ||
        observed.token !== connection.token
      )
        return false;
    }
    await acceptConnection(
      connection,
      () => generation === connectionReadGeneration.current,
    );
    return (
      generation === connectionReadGeneration.current &&
      previous.current === connection
    );
  };
  const connect = async (
    action: "connect_local" | "retry_local" = "connect_local",
  ) => {
    if (busy || connecting.current) return;
    connecting.current = true;
    setBusy(true);
    setError(undefined);
    const generation = connectionReadGeneration.current;
    try {
      const connection = await invoke<NativeConnection>(action);
      await acceptConnection(
        connection,
        () => generation === connectionReadGeneration.current,
      );
    } catch (reason) {
      if (generation === connectionReadGeneration.current) setError(reason);
    } finally {
      connecting.current = false;
      if (generation === connectionReadGeneration.current) setBusy(false);
    }
  };
  useEffect(() => {
    if (!isTauri()) return;
    let canceled = false;
    let timer: ReturnType<typeof setTimeout>;
    const observe = async () => {
      const generation = connectionReadGeneration.current;
      try {
        // Native owns the operation across every renderer lifecycle. A null
        // observation is pending; reading it never starts another attempt.
        const connection = await invoke<NativeConnection | null>(
          "launch_local",
        );
        if (canceled || generation !== connectionReadGeneration.current) return;
        if (!connection) {
          timer = setTimeout(() => void observe(), 250);
          return;
        }
        await acceptConnection(
          connection,
          () => !canceled && generation === connectionReadGeneration.current,
        );
        if (!canceled && generation === connectionReadGeneration.current)
          setBusy(false);
      } catch (reason) {
        if (!canceled && generation === connectionReadGeneration.current) {
          setError(reason);
          setBusy(false);
        }
      }
    };
    void observe();
    return () => {
      canceled = true;
      clearTimeout(timer);
    };
  }, []);
  useEffect(() => {
    if (!isTauri()) return;
    let canceled = false;
    const reread = async () => {
      const generation = ++connectionReadGeneration.current;
      setConnectionVerified(false);
      try {
        // Native publishes only a change signal. Observe its adopted outcome;
        // a sibling registration change never authorizes startup or pairing.
        const connection = await invoke<NativeConnection | null>(
          "launch_local",
        );
        if (canceled || generation !== connectionReadGeneration.current) return;
        if (!connection) throw "credential-unavailable";
        await acceptConnection(
          connection,
          () => !canceled && generation === connectionReadGeneration.current,
        );
        if (!canceled && generation === connectionReadGeneration.current)
          setBusy(false);
      } catch (reason) {
        if (!canceled && generation === connectionReadGeneration.current) {
          setError(reason);
          setBusy(false);
        }
      }
    };
    const subscription = listen("local-connection-changed", () => {
      if (!canceled) void reread();
    }).catch(() => () => {});
    return () => {
      canceled = true;
      connectionReadGeneration.current++;
      void subscription.then((unlisten) => unlisten()).catch(() => {});
    };
  }, []);
  const chooseRepositoryFolder = async () => {
    const selected = previous.current;
    const path = await invoke<string | null>("choose_repository_folder");
    if (!selected || previous.current !== selected)
      throw new Error("Folder selection connection changed");
    return path;
  };
  // Keyed to the accepted connection epoch so unrelated renders keep the
  // reader identity stable and do not re-trigger identity verification.
  const readLocalWorker = useCallback(async () => {
    const selected = previous.current;
    const proof = await invoke<{
      endpoint: string;
      server_id: string;
      machine_id: string;
      token: string;
    }>("local_worker_proof");
    if (
      !selected ||
      previous.current !== selected ||
      proof.endpoint !== selected.endpoint ||
      proof.server_id !== selected.server_id
    )
      throw new Error("Local Worker authority changed");
    return { machineId: proof.machine_id, token: proof.token };
  }, [connectionEpoch]);
  const controlWorkerNetwork: ControlWorkerNetwork = async (
    machine,
    action,
    ciphertext,
    digest,
  ) => {
    const selected = previous.current;
    const result = await invoke<unknown>("worker_network_control", {
      machine,
      action,
      ciphertext: Array.from(ciphertext),
      digest,
    });
    if (!selected || previous.current !== selected)
      throw new Error("Worker network connection changed");
    return result;
  };
  const controlLocalWorker: ControlLocalWorker = Object.assign(
    async (action: Parameters<ControlLocalWorker>[0], generation?: string) => {
      const selected = previous.current;
      const value = await invoke<LocalWorkerStatus>("local_worker_control", {
        action,
        generation,
      });
      if (!selected || previous.current !== selected)
        throw new Error("Local Worker connection changed");
      return value;
    },
    { automatic: true },
  );
  const problem =
    typeof error === "string" && Object.hasOwn(nativeProblems, error) ? (
      <p role="alert">{nativeProblems[error]}</p>
    ) : (
      <Problem error={error} />
    );
  const connectionSettings = <ConnectionsPage local onSlots={setPageSlots} />;
  const updateControls = useMemo<DesktopUpdateControls>(
    () => ({
      readContext: retainedDesktopContext(() =>
        invoke("desktop_update_context"),
      ),
      control: async (action, id, revision) => {
        const selected = previous.current;
        if (!selected) throw "invalid-evidence";
        const result = await invoke<import("./updates").NativeUpdateResult>(
          "desktop_update_native",
          {
            server: selected.server_id,
            id,
            revision: revision.toString(),
            action,
          },
        );
        if (previous.current !== selected) throw "invalid-evidence";
        return result;
      },
    }),
    [],
  );
  const controls = (
    <>
      <TailscaleConnections
        current={runnerSlots?.access}
        target={runnerSlots?.devices}
        requests={runnerSlots?.requests}
        onPaired={() => setSavedRefresh((value) => value + 1)}
      />
      <LocalServerControls
        endpoint={previous.current?.endpoint}
        connected={connectionVerified}
        status={status}
        restart={() => void connect()}
        busy={busy}
        problem={problem}
      />
      <PersistentConnectionView target={appInformationTarget} hidden>
        <AppInformation
          active={Boolean(appInformationTarget)}
          controls={isTauri() ? updateControls : undefined}
          openLink={isTauri() ? openAppInformationLink : undefined}
        >
          <Updates
            active={Boolean(appInformationTarget)}
            controls={isTauri() ? updateControls : undefined}
          />
        </AppInformation>
      </PersistentConnectionView>
    </>
  );
  const oauthServer = previous.current?.server_id ?? "";
  const controlOAuth = useCallback<OAuthNativeControl>(
    (opening, action, generation, attempt, authorization) =>
      invoke("account_oauth_native", {
        server: oauthServer,
        opening,
        action,
        generation,
        attempt,
        authorization,
      }),
    [oauthServer],
  );
  return (
    <>
      {transport ? (
        <CredentialAccessGate
          connection={previous.current!}
          registration={() => setInlineRecovery(true)}
          diagnostics={() => setShowConnection(true)}
        >
          <OAuthNativeProvider control={controlOAuth}>
            <WorkerNetworkControlProvider control={controlWorkerNetwork}>
              <App
                onConnectionHelp={(target) => {
                  setInlineRecoveryTarget(target);
                  setInlineRecovery(Boolean(target));
                }}
                pairingAuthority={
                  previous.current
                    ? {
                        endpoint: previous.current.endpoint,
                        serverId: previous.current.server_id,
                      }
                    : undefined
                }
                currentDeviceId={previous.current?.device_id}
                controlLocalWorker={controlLocalWorker}
                chooseRepositoryFolder={chooseRepositoryFolder}
                readLocalWorker={readLocalWorker}
                transport={transport}
                connectionReady={
                  connectionVerified && status?.state === LocalServerState.Ready
                }
                connectionEpoch={connectionEpoch}
                connectionSettings={connectionSettings}
                runnerTailscale={setRunnerSlots}
                appInformationSettings={
                  <AppInformationSlot onTarget={setAppInformationTarget} />
                }
                // The Connections page never hosts the local controls. Only the
                // diagnostics modal receives their view; otherwise it stays hidden
                // while that page is open and uses the inline fallback elsewhere.
                connectionTarget={
                  showConnection ? (connectionTarget ?? undefined) : undefined
                }
                connectionPageOpen={Boolean(pageSlots)}
                localServer={controls}
              />
            </WorkerNetworkControlProvider>
          </OAuthNativeProvider>
        </CredentialAccessGate>
      ) : (
        <main className="connect-page" {...entrance}>
          <h1>{copy("desktop.delidev_44fcad")}</h1>
          {isTauri() ? (
            <>
              <p role="status">
                {busy
                  ? copy("desktop.startingDelidev_e37cda")
                  : copy("desktop.delidevCouldNotConnect_ed9a4c")}
              </p>
              {error ? (
                <>
                  <p role="alert">
                    {error === "stopped"
                      ? copy(
                          "desktop.delidevIsDisconnectedOnThisComputer_12b4ca",
                        )
                      : copy(
                          "desktop.delidevCouldNotFinishStartingRetry_c06c30",
                        )}
                  </p>
                  <button
                    className="primary"
                    disabled={busy}
                    onClick={() =>
                      void connect(
                        error === "stopped" ? undefined : "retry_local",
                      )
                    }
                  >
                    {copy(
                      error === "stopped"
                        ? "desktop.startLocalServer_4d64e3"
                        : "desktop.retry_942087",
                    )}
                  </button>
                  {!showConnection ? problem : null}
                </>
              ) : null}
              <button onClick={() => setShowConnection(true)}>
                {copy("desktop.troubleshooting_c3af07")}
              </button>
            </>
          ) : (
            <p>{copy("desktop.openTheDelidevDesktopAppTo_5990b4")}</p>
          )}
        </main>
      )}
      <Modal
        title={copy("desktop.connectionDiagnostics_b30b0d")}
        visible={showConnection}
        close={() => setShowConnection(false)}
      >
        <section aria-label={copy("desktop.connection_639a40")}>
          <h3>{copy("desktop.connectionOnThisComputer_dbc9be")}</h3>
          <div ref={setConnectionTarget} />
          {!transport ? (
            <>
              <LocalServerStatusText status={status} />
              <button disabled={busy} onClick={() => void connect()}>
                {copy("desktop.startLocalServer_4d64e3")}
              </button>
              {showConnection ? problem : null}
            </>
          ) : null}
          <div ref={setRecoveryTarget} />
          <button onClick={() => setShowSaved(true)}>
            {copy("desktop.savedServers_4bf084")}
          </button>
        </section>
      </Modal>
      <LocalRegistrationRecovery
        busy={busy}
        setBusy={setBusy}
        recovered={acceptRecovery}
        readGeneration={() => connectionReadGeneration.current}
        active={
          showConnection ||
          Boolean(pageSlots) ||
          inlineRecovery ||
          error === "permission-denied" ||
          error === "credential-unavailable"
        }
        target={
          inlineRecoveryTarget ??
          (showConnection ? (recoveryTarget ?? undefined) : pageSlots?.advanced)
        }
        inline={
          inlineRecovery ||
          error === "permission-denied" ||
          error === "credential-unavailable"
        }
      />
      <SavedConnections
        target={showSaved ? undefined : pageSlots?.saved}
        advancedTarget={showSaved ? undefined : pageSlots?.advanced}
        visible={showSaved || Boolean(pageSlots)}
        close={() => setShowSaved(false)}
        actions={savedActions}
        refreshKey={savedRefresh}
      />
    </>
  );
}
const savedActions: SavedConnectionActions = {
  list: () => invoke<SavedConnection[]>("saved_connections"),
  removed: (after) => invoke("removed_connections", { after }),
  remove: (id, requestId, revision) =>
    invoke("remove_connection", { id, requestId, revision }),
  retainedWorker: (id, action, generation) =>
    invoke("retained_worker_control", { id, action, generation }),
  pair: (id, name, grant) =>
    invoke<SavedConnection>("pair_connection", { id, name, grant }),
  retry: (id) => invoke<SavedConnection>("retry_connection", { id }),
  rename: (id, requestId, revision, name) =>
    invoke<SavedConnection>("rename_connection", {
      id,
      requestId,
      revision,
      name,
    }),
  open: (id) => invoke<void>("open_connection", { id }),
};
function SavedDesktop({
  profile,
  entrance,
}: {
  profile: SavedConnection;
  entrance: EntranceProps;
}) {
  useLocale();
  const [showConnection, setShowConnection] = useState(false);
  const [pageSlots, setPageSlots] = useState<ConnectionPageSlots>();
  const [appInformationTarget, setAppInformationTarget] =
    useState<HTMLElement>();
  const [connectionTarget, setConnectionTarget] =
    useState<HTMLDivElement | null>(null);
  const [transport, setTransport] = useState<Transport>();
  const [error, setError] = useState<unknown>();
  useQuitConnection(transport, Boolean(transport) && !error);
  const [busy, setBusy] = useState(false);
  const [epoch, setEpoch] = useState(0);
  const previous = useRef<NativeConnection>(undefined);
  const connecting = useRef(false);
  const connect = async () => {
    if (connecting.current) return;
    connecting.current = true;
    setBusy(true);
    setError(undefined);
    try {
      const connection = await invoke<NativeConnection>("connect_saved");
      if (
        connection.server_id !== profile.server_id ||
        connection.device_id !== profile.device_id ||
        connection.endpoint !== profile.endpoint
      )
        throw "invalid-evidence";
      const candidate = createDeliDevTransport({
        origin: connection.endpoint,
        getToken: () => connection.token,
      });
      await verifyLocalServer(candidate, connection.server_id);
      if (!previous.current || previous.current.token !== connection.token)
        setTransport(candidate);
      previous.current = connection;
      setEpoch((value) => value + 1);
    } catch (error) {
      setError(error);
    } finally {
      connecting.current = false;
      setBusy(false);
    }
  };
  // Opening this native window is the explicit selection. Reconnecting verifies
  // its same saved identity; it never retries pairing or starts the server.
  useEffect(() => {
    void connect();
  }, []);
  const chooseRepositoryFolder = async () => {
    const selected = previous.current;
    const path = await invoke<string | null>("choose_repository_folder");
    if (!selected || previous.current !== selected)
      throw new Error("Folder selection connection changed");
    return path;
  };
  // Keyed to the accepted connection epoch so unrelated renders keep the
  // reader identity stable and do not re-trigger identity verification.
  const readLocalWorker = useCallback(async () => {
    const selected = previous.current;
    const proof = await invoke<{
      endpoint: string;
      server_id: string;
      machine_id: string;
      token: string;
    }>("saved_worker_proof");
    if (
      !selected ||
      previous.current !== selected ||
      proof.endpoint !== selected.endpoint ||
      proof.server_id !== selected.server_id
    )
      throw new Error("Saved Worker authority changed");
    return { machineId: proof.machine_id, token: proof.token };
  }, [epoch]);
  const controlWorkerNetwork: ControlWorkerNetwork = async (
    machine,
    action,
    ciphertext,
    digest,
  ) => {
    const selected = previous.current;
    const result = await invoke<unknown>("worker_network_control", {
      machine,
      action,
      ciphertext: Array.from(ciphertext),
      digest,
    });
    if (!selected || previous.current !== selected)
      throw new Error("Worker network connection changed");
    return result;
  };
  const controlLocalWorker: ControlLocalWorker = async (action, generation) => {
    const selected = previous.current;
    const value = await invoke<LocalWorkerStatus>("saved_worker_control", {
      action,
      generation,
    });
    if (!selected || previous.current !== selected)
      throw new Error("Saved Worker connection changed");
    return value;
  };
  const updateControls = useMemo<DesktopUpdateControls>(
    () => ({
      readContext: retainedDesktopContext(() =>
        invoke("desktop_update_context"),
      ),
      control: async (action, id, revision) => {
        const selected = previous.current;
        if (!selected) throw "invalid-evidence";
        const result = await invoke<import("./updates").NativeUpdateResult>(
          "desktop_update_native",
          {
            server: selected.server_id,
            id,
            revision: revision.toString(),
            action,
          },
        );
        if (previous.current !== selected) throw "invalid-evidence";
        return result;
      },
    }),
    [],
  );
  const controls = (
    <LocalConnectionPresentation>
      <p>{profile.name}</p>
      <small>{profile.endpoint}</small>
      <button disabled={busy} onClick={() => void connect()}>
        {copy("desktop.verifySavedConnection_9161de")}
      </button>
      <button
        onClick={() => void invoke("show_connection_manager").catch(setError)}
      >
        {copy("desktop.showLocalWindow_746012")}
      </button>
      <SavedConnectionProblem error={error} />
    </LocalConnectionPresentation>
  );
  const updateView = (
    <PersistentConnectionView target={appInformationTarget} hidden>
      <AppInformation
        active={Boolean(appInformationTarget)}
        controls={updateControls}
        openLink={openAppInformationLink}
      >
        <Updates
          active={Boolean(appInformationTarget)}
          controls={updateControls}
        />
      </AppInformation>
    </PersistentConnectionView>
  );
  const oauthServer = profile.server_id;
  const controlOAuth = useCallback<OAuthNativeControl>(
    (opening, action, generation, attempt, authorization) =>
      invoke("account_oauth_native", {
        server: oauthServer,
        opening,
        action,
        generation,
        attempt,
        authorization,
      }),
    [oauthServer],
  );
  return (
    <>
      {transport ? (
        <CredentialAccessGate
          connection={previous.current!}
          diagnostics={() => setShowConnection(true)}
        >
          <OAuthNativeProvider control={controlOAuth}>
            <WorkerNetworkControlProvider control={controlWorkerNetwork}>
              <App
                localServer={
                  <>
                    {controls}
                    {updateView}
                  </>
                }
                connectionTarget={
                  showConnection
                    ? (connectionTarget ?? undefined)
                    : pageSlots?.current
                }
                pairingAuthority={{
                  endpoint: profile.endpoint,
                  serverId: profile.server_id,
                }}
                transport={transport}
                readLocalWorker={readLocalWorker}
                chooseRepositoryFolder={chooseRepositoryFolder}
                controlLocalWorker={controlLocalWorker}
                currentDeviceId={profile.device_id}
                connectionEpoch={epoch}
                connectionSettings={<ConnectionsPage onSlots={setPageSlots} />}
                appInformationSettings={
                  <AppInformationSlot onTarget={setAppInformationTarget} />
                }
              />
            </WorkerNetworkControlProvider>
          </OAuthNativeProvider>
        </CredentialAccessGate>
      ) : (
        <main className="connect-page" {...entrance}>
          <h1>{copy("desktop.delidev_44fcad")}</h1>
          <p role="status">
            {busy
              ? copy("desktop.connecting_72021e")
              : copy("desktop.delidevCouldNotConnect_ed9a4c")}
          </p>
          {error ? (
            <p role="alert">
              {copy("desktop.delidevCouldNotVerifyThisConnection_862b8e")}
            </p>
          ) : null}
          <button disabled={busy} onClick={() => void connect()}>
            {copy("desktop.retry_942087")}
          </button>
          <button onClick={() => setShowConnection(true)}>
            {copy("desktop.troubleshooting_c3af07")}
          </button>
        </main>
      )}
      <Modal
        title={copy("desktop.connectionDiagnostics_b30b0d")}
        visible={showConnection}
        close={() => setShowConnection(false)}
      >
        <section aria-label={copy("desktop.savedConnection_4df074")}>
          <h3>{copy("desktop.savedConnection_4df074")}</h3>
          <div ref={setConnectionTarget} />
          {!transport ? <SavedConnectionProblem error={error} /> : null}
        </section>
      </Modal>
    </>
  );
}
function DesktopContent() {
  useLocale();
  const [context, setContext] = useState<{
    ready: boolean;
    profile?: SavedConnection;
    error?: unknown;
  }>({ ready: !isTauri() });
  const read = async () => {
    try {
      const profile = await invoke<SavedConnection | null>(
        "connection_context",
      );
      setContext({ ready: true, profile: profile ?? undefined });
    } catch (error) {
      setContext({ ready: false, error });
    }
  };
  useEffect(() => {
    if (isTauri()) void read();
  }, []);
  // One window-lifetime owner of the Startup and connection entrance. The key
  // stays constant once the window context is read, so the first pre-transport
  // presentation plays once and later status, retry or reconnect changes never
  // replay it. The identity-read page stays static; its "pending" key settles
  // on its own and nothing waits for it.
  const entrance = useEntranceActivation(
    context.ready ? "startup" : "pending",
    true,
    EntranceMotion.Y2,
  );
  useEffect(() => {
    if (!isTauri()) return;
    let canceled = false;
    const subscription = listen("saved-connection-label", () => {
      if (canceled) return;
      void invoke<SavedConnection | null>("connection_context")
        .then((profile) => {
          if (!canceled && profile)
            setContext((previous) =>
              previous.profile?.id === profile.id &&
              profile.revision >= previous.profile.revision
                ? { ...previous, profile }
                : previous,
            );
        })
        .catch(() => {
          /* The original window, authorization and drafts remain active if label refresh fails. */
        });
    }).catch(() => () => {});
    return () => {
      canceled = true;
      void subscription.then((unlisten) => unlisten()).catch(() => {});
    };
  }, []);
  if (!context.ready)
    return (
      <main className="connect-page">
        <h1>{copy("desktop.delidev_44fcad")}</h1>
        <p>{copy("desktop.readingThisWindowSServerIdentity_9fc24f")}</p>
        <SavedConnectionProblem error={context.error} />
        {context.error ? (
          <button onClick={() => void read()}>
            {copy("desktop.retryWindowContext_6efa5e")}
          </button>
        ) : null}
      </main>
    );
  return context.profile ? (
    <SavedDesktop profile={context.profile} entrance={entrance} />
  ) : (
    <LocalDesktop entrance={entrance} />
  );
}

export function Desktop() {
  return (
    <QuitConnectionProvider>
      <ReloadShortcutOwner />
      <DesktopContent />
    </QuitConnectionProvider>
  );
}

const openAppInformationLink: OpenAppInformationLink = (action) =>
  invoke("open_app_information_link", { action });
