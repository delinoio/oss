// SPDX-License-Identifier: Apache-2.0
import { consumeFormSubmit, guardEnterSubmission } from "./form-enter";
import { useProductLabels, PresentationKind } from "./product-identity-labels";
import { PersistentConnectionView } from "./connections-page";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { Disclosure, DisclosureSummary, DisclosureDensity } from "./disclosure";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  InstallationQuery,
  SystemQuery,
  SystemCapability,
  UpdateComponent,
  UpdateTarget,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
export enum NativeUpdateAction {
  Prepare = "prepare",
  Install = "install",
  Inspect = "inspect",
}
export enum NativeUpdatePhase {
  Prepared = "prepared",
  Installing = "installing",
  Installed = "installed",
  Failed = "failed",
  Uncertain = "uncertain",
}
export type NativeUpdateResult = {
  operation_id: string;
  release_version: string;
  phase: NativeUpdatePhase;
};
export type DesktopUpdateControls = {
  readContext: () => Promise<{ current_version: string; target: string }>;
  control: (
    action: NativeUpdateAction,
    id: string,
    revision: bigint,
  ) => Promise<NativeUpdateResult>;
};
const targets: Record<string, UpdateTarget> = {
  "darwin-amd64": UpdateTarget.DARWIN_AMD64,
  "darwin-arm64": UpdateTarget.DARWIN_ARM64,
  "windows-amd64": UpdateTarget.WINDOWS_AMD64,
  "windows-arm64": UpdateTarget.WINDOWS_ARM64,
  "linux-amd64": UpdateTarget.LINUX_AMD64,
  "linux-arm64": UpdateTarget.LINUX_ARM64,
};
function UpdateCandidate({
  current,
  active,
  worker,
  controls,
  recoveryTarget,
  onRecoveryStateChange,
}: {
  recoveryTarget?: HTMLElement;
  current: Resource;
  active: boolean;
  worker?: boolean;
  controls?: DesktopUpdateControls;
  onRecoveryStateChange?: (id: string, required: boolean) => void;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const [local, setLocal] = useState<NativeUpdateResult>(),
    [busy, setBusy] = useState(false),
    [nativeAction, setNativeAction] = useState<NativeUpdateAction>(),
    [uncertain, setUncertain] = useState(false),
    [error, setError] = useState<unknown>();
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const read = useQuery(
    InstallationQuery.getUpdate,
    { id: current.id },
    { enabled: active, refetchInterval: active && worker ? 3000 : false },
  );
  const row =
      read.data?.update && read.data.update.revision >= current.revision
        ? read.data.update
        : current,
    data = document(row);
  const request = useRetainedMutation(
    `update:request:${row.id}`,
    InstallationQuery.requestWorkerUpdate,
    () => {
      void read.refetch();
    },
  );
  const cancel = useRetainedMutation(
    `update:cancel:${row.id}`,
    InstallationQuery.cancelUpdate,
    () => {
      void read.refetch();
    },
  );
  const recoveryRequired = Boolean(
    busy ||
    uncertain ||
    request.busy ||
    request.uncertain ||
    cancel.busy ||
    cancel.uncertain ||
    (!worker &&
      (local?.phase === NativeUpdatePhase.Prepared ||
        local?.phase === NativeUpdatePhase.Installing ||
        local?.phase === NativeUpdatePhase.Failed ||
        local?.phase === NativeUpdatePhase.Uncertain)),
  );
  useEffect(() => {
    onRecoveryStateChange?.(current.id, recoveryRequired);
    return () => onRecoveryStateChange?.(current.id, false);
  }, [current.id, onRecoveryStateChange, recoveryRequired]);
  const native = async (action: NativeUpdateAction) => {
    if (
      !active ||
      !controls ||
      busy ||
      (uncertain && action !== NativeUpdateAction.Inspect)
    )
      return;
    setBusy(true);
    setNativeAction(action);
    setError(undefined);
    try {
      const result = await controls.control(action, row.id, row.revision);
      if (
        result.operation_id !== row.id ||
        result.release_version !== text(data.version)
      )
        throw new Error("Update identity changed");
      if (alive.current) {
        setLocal(result);
        setUncertain(false);
      }
    } catch (error) {
      if (alive.current) {
        setError(error);
        setUncertain(true);
      }
    } finally {
      if (alive.current) setBusy(false);
    }
  };
  const mutation = () => ({
    id: row.id,
    expectedRevision: row.revision,
    requestId: newRequestId(),
  });
  return (
    <section
      data-connection-attention={Boolean(
        busy ||
        uncertain ||
        error ||
        request.busy ||
        request.uncertain ||
        cancel.busy ||
        cancel.uncertain ||
        local?.phase === NativeUpdatePhase.Failed ||
        local?.phase === NativeUpdatePhase.Uncertain ||
        local?.phase === NativeUpdatePhase.Installing,
      )}
      aria-label={copy("updates.originalUpdate_9f056a")}
    >
      <p>
        <LocalizedText
          id="updates.delidev_a214b0"
          components={{
            s0: <>{text(data.version)}</>,
            s1: <>{text(data.target)}</>,
            s2: <>{statusLabel(text(data.state))}</>,
          }}
        />
      </p>
      {worker ? (
        <>
          <p>{copy("updates.updatesWaitForActiveWorkTerminals_ac7bf1")}</p>
          {text(data.state) === "OBSERVED" ? (
            <SettingsActionButton
              icon={SettingsActionIcon.Inspect}
              disabled={!active || request.busy || request.uncertain}
              onClick={() => void request.send({ mutation: mutation() })}
            >
              {copy("updates.updateWorkerWhenIdle_50f590")}
            </SettingsActionButton>
          ) : null}
          {["OBSERVED", "WAITING_FOR_IDLE"].includes(text(data.state)) ? (
            <SettingsActionButton
              icon={SettingsActionIcon.Cancel}
              disabled={!active || cancel.busy || cancel.uncertain}
              onClick={() => void cancel.send({ mutation: mutation() })}
            >
              {copy("updates.cancelUnclaimedUpdate_d10b89")}
            </SettingsActionButton>
          ) : null}
        </>
      ) : (
        <>
          <p>
            {copy(
              "updates.installationRequiresConfirmationOnThisComputer_828091",
            )}
          </p>
          {!local && !uncertain ? (
            <SettingsActionButton
              icon={SettingsActionIcon.Download}
              disabled={busy || !active}
              onClick={() => void native(NativeUpdateAction.Prepare)}
            >
              {copy("updates.downloadVerifiedDesktopUpdate_0dd9ad")}
            </SettingsActionButton>
          ) : null}
          {local?.phase === NativeUpdatePhase.Prepared && !uncertain ? (
            <SettingsActionButton
              icon={SettingsActionIcon.Inspect}
              disabled={busy || !active}
              onClick={() => void native(NativeUpdateAction.Install)}
            >
              {copy("updates.reviewAndInstallDesktopUpdate_2f844d")}
            </SettingsActionButton>
          ) : null}
          {busy ? (
            <p role="status">
              {copy(
                nativeAction === NativeUpdateAction.Prepare
                  ? "updates.preparing"
                  : nativeAction === NativeUpdateAction.Install
                    ? "updates.installing"
                    : "updates.inspecting",
              )}
            </p>
          ) : null}
          {local ? (
            <p role="status">
              <LocalizedText
                id="updates.desktopInstallation_cc279d"
                components={{
                  s0: <>{nativePhaseLabel(local.phase)}</>,
                  s1: (
                    <>
                      {local.phase === NativeUpdatePhase.Installed
                        ? copy("updates.restartDelidevWhenReady_f8939d")
                        : ""}
                    </>
                  ),
                }}
              />
            </p>
          ) : null}
          {uncertain ? (
            <p role="alert">
              {copy(
                "updates.theOriginalInstallationResponseIsUnconfirmed_34e5ba",
              )}
            </p>
          ) : null}
          <PersistentConnectionView target={recoveryTarget} hidden>
            <dl>
              <dt>{copy("updates.retainedDesktopUpdateId_acb1d6")}</dt>
              <dd>{productLabel(PresentationKind.Resource, row.id)}</dd>
              <dt>{copy("updates.retainedDesktopUpdateRevision_a8ee1a")}</dt>
              <dd>{row.revision.toString()}</dd>
            </dl>
            <SettingsActionButton
              icon={SettingsActionIcon.Retry}
              disabled={busy || !active}
              onClick={() => void native(NativeUpdateAction.Inspect)}
            >
              {copy("updates.inspectOriginalDesktopInstallation_5849fa")}
            </SettingsActionButton>
          </PersistentConnectionView>
        </>
      )}
      {request.uncertain ? (
        <SettingsActionButton
          icon={SettingsActionIcon.Inspect}
          disabled={request.busy || !active}
          onClick={request.retry}
        >
          {copy("updates.inspectTheSameWorkerUpdateRequest_9c4a50")}
        </SettingsActionButton>
      ) : null}
      {cancel.uncertain ? (
        <SettingsActionButton
          icon={SettingsActionIcon.Cancel}
          disabled={cancel.busy || !active}
          onClick={cancel.retry}
        >
          {copy("updates.inspectTheSameCancellation_10c4f8")}
        </SettingsActionButton>
      ) : null}
      <Problem error={error || read.error || request.error || cancel.error} />
    </section>
  );
}
// Read-only recovery stays available independently of live server negotiation.
// The native owner compares the exact local journal and trusted connection; this
// form cannot prepare, install, restart or select an artifact.
function LocalInstallationInspection({
  active,
  controls,
}: {
  active: boolean;
  controls: DesktopUpdateControls;
}) {
  useLocale();
  const [id, setId] = useState(""),
    [revision, setRevision] = useState(""),
    [result, setResult] = useState<NativeUpdateResult>(),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>();
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const valid =
    /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(
      id,
    ) &&
    /^[1-9][0-9]{0,18}$/.test(revision) &&
    BigInt(revision) < 1n << 63n;
  const inspect = async () => {
    if (!active || busy || !valid) return;
    const original = id;
    setBusy(true);
    setError(undefined);
    setResult(undefined);
    try {
      const observed = await controls.control(
        NativeUpdateAction.Inspect,
        original,
        BigInt(revision),
      );
      if (observed.operation_id !== original)
        throw new Error("Update identity changed");
      if (alive.current) setResult(observed);
    } catch (error) {
      if (alive.current) setError(error);
    } finally {
      if (alive.current) setBusy(false);
    }
  };
  return (
    <section
      data-connection-attention={Boolean(
        busy ||
        error ||
        result?.phase === NativeUpdatePhase.Uncertain ||
        result?.phase === NativeUpdatePhase.Failed ||
        result?.phase === NativeUpdatePhase.Installing,
      )}
      aria-label={copy("updates.localInstallationRecovery_41dcc5")}
    >
      <p>{copy("updates.inspectARetainedInstallationOnThis_28e7a3")}</p>
      <form
        onKeyDown={guardEnterSubmission}
        onSubmit={(event) => {
          if (!consumeFormSubmit(event)) return;
          void inspect();
        }}
      >
        <label>
          {copy("updates.retainedDesktopUpdateId_acb1d6")}
          <input
            value={id}
            maxLength={36}
            disabled={busy}
            onChange={(event) => {
              setId(event.target.value);
              setResult(undefined);
            }}
          />
        </label>
        <label>
          {copy("updates.retainedDesktopUpdateRevision_a8ee1a")}
          <input
            value={revision}
            maxLength={19}
            inputMode="numeric"
            disabled={busy}
            onChange={(event) => {
              setRevision(event.target.value);
              setResult(undefined);
            }}
          />
        </label>
        <SettingsActionButton
          icon={SettingsActionIcon.Inspect}
          disabled={!active || busy || !valid}
        >
          {copy("updates.inspectRetainedLocalInstallation_3d589d")}
        </SettingsActionButton>
      </form>
      {result ? (
        <p role="status">
          <LocalizedText
            id="updates.retainedDesktop_312269"
            components={{
              s0: <>{result.release_version}</>,
              s1: <>{nativePhaseLabel(result.phase)}</>,
              s2: (
                <>
                  {result.phase === NativeUpdatePhase.Installed
                    ? copy("updates.restartDelidevWhenReady_f8939d")
                    : ""}
                </>
              ),
            }}
          />
        </p>
      ) : null}
      <Problem error={error} />
    </section>
  );
}
export function Updates({
  active,
  machine,
  controls,
}: {
  active: boolean;
  machine?: Resource;
  controls?: DesktopUpdateControls;
}) {
  useLocale();
  const [expanded, setExpanded] = useState(!machine),
    [checked, setChecked] = useState(false),
    [recoveryTarget, setRecoveryTarget] = useState<HTMLElement>(),
    [candidate, setCandidate] = useState<Resource>(),
    [recoveryCandidateId, setRecoveryCandidateId] = useState(""),
    [id, setId] = useState(""),
    [nativeContext, setNativeContext] = useState<{
      current_version: string;
      target: string;
    }>(),
    [error, setError] = useState<unknown>();
  const recoverySlot = useCallback(
    (node: HTMLDivElement | null) => setRecoveryTarget(node ?? undefined),
    [],
  );
  const desktopRoot = useRef<HTMLDivElement>(null);
  const recovery = useRef<HTMLDetailsElement>(null);
  const reportCandidateRecovery = useCallback(
    (candidateId: string, required: boolean) => {
      setRecoveryCandidateId((current) =>
        required ? candidateId : current === candidateId ? "" : current,
      );
    },
    [],
  );
  useEffect(() => {
    if (machine || !desktopRoot.current) return;
    const root = desktopRoot.current;
    const reveal = () => {
      if (
        recovery.current &&
        (root.dataset.connectionAttention === "true" ||
          root.querySelector(
            '[role="alert"], [data-connection-attention="true"]',
          ))
      )
        recovery.current.open = true;
    };
    const observer = new MutationObserver(reveal);
    observer.observe(root, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ["data-connection-attention", "role"],
    });
    reveal();
    return () => observer.disconnect();
  }, [machine]);
  const status = useQuery(
    SystemQuery.getStatus,
    {},
    { enabled: active && expanded },
  );
  const supported =
    status.data?.capabilities.includes(SystemCapability.SIGNED_UPDATES_V1) ===
    true;
  // Explicit false means this server build has no production signing root. An
  // absent field is a legacy server and keeps the explicit check behavior.
  const rootUnavailable =
    !machine && supported && status.data?.productionRootReady === false;
  const rootHelpId = useId(),
    rootStatusId = useId();
  useEffect(() => {
    let alive = true;
    if (expanded && active && controls)
      void controls
        .readContext()
        .then((value) => {
          if (alive) setNativeContext(value);
        })
        .catch((error) => {
          if (alive) setError(error);
        });
    return () => {
      alive = false;
    };
  }, [expanded, active, controls]);
  const check = useRetainedMutation(
    `update:check:${machine?.id ?? "desktop"}`,
    InstallationQuery.checkUpdate,
    (response) => {
      if (!machine) {
        setChecked(true);
        setCandidate(response.update);
        setId(response.update?.id ?? "");
      } else if (response.update) {
        setCandidate(response.update);
        setId(response.update.id);
      }
    },
  );
  const read = useQuery(
    InstallationQuery.getUpdate,
    { id },
    { enabled: active && expanded && supported && Boolean(id) },
  );
  const data = machine ? document(machine) : undefined;
  const target = data
      ? `${text(data.os)}-${text(data.architecture)}`
      : (nativeContext?.target ?? ""),
    version = data
      ? text(data.version)
      : (nativeContext?.current_version ?? "");
  const selected =
    read.data?.update?.id === id
      ? read.data.update
      : candidate?.id === id
        ? candidate
        : undefined;
  const knownWorker =
    !machine ||
    (Array.isArray(data?.worker_capabilities) &&
      data.worker_capabilities.includes("signed-worker-updates-v1"));
  const content = (
    <>
      {!supported || !knownWorker ? (
        <p>
          {status.isPending
            ? copy("updates.checkingUpdateSupport_9f8283")
            : copy("updates.thisServerOrWorkerRequiresAn_e35658")}
        </p>
      ) : (
        <>
          <p>{copy("updates.onlySignedStableDelidevReleasesAre_fdf83e")}</p>
          <div
            className={!machine ? "app-update-status" : "worker-update-check"}
          >
            {!machine ? (
              <p role="status" id={rootStatusId}>
                {copy(
                  check.busy
                    ? "updates.checking"
                    : check.uncertain
                      ? "updates.uncertain"
                      : check.error
                        ? "updates.failed"
                        : selected
                          ? "updates.available"
                          : checked
                            ? "updates.current"
                            : rootUnavailable
                              ? "updates.rootUnavailable"
                              : "updates.unchecked",
                )}
              </p>
            ) : null}
            <SettingsActionButton
              icon={
                machine
                  ? SettingsActionIcon.Confirm
                  : SettingsActionIcon.Refresh
              }
              aria-describedby={
                rootUnavailable ? `${rootStatusId} ${rootHelpId}` : undefined
              }
              disabled={
                rootUnavailable ||
                !active ||
                !targets[target] ||
                !version ||
                check.busy ||
                check.uncertain ||
                (Boolean(recoveryCandidateId) && recoveryCandidateId === id)
              }
              onClick={() => {
                if (rootUnavailable) return;
                void check.send({
                  requestId: newRequestId(),
                  component: machine
                    ? UpdateComponent.WORKER
                    : UpdateComponent.DESKTOP,
                  target: targets[target],
                  currentVersion: version,
                  machineId: machine?.id ?? "",
                  expectedMachineRevision: machine?.revision ?? 0n,
                });
              }}
            >
              {!machine ? (
                copy("updates.check")
              ) : (
                <LocalizedText
                  id="updates.checkForUpdate_8ac71d"
                  components={{
                    s0: (
                      <>
                        {machine
                          ? copy("updates.worker_a67b04")
                          : copy("updates.desktop_68693d")}
                      </>
                    ),
                  }}
                />
              )}
            </SettingsActionButton>
          </div>
          {rootUnavailable ? (
            <p id={rootHelpId}>{copy("updates.rootUnavailableHelp")}</p>
          ) : null}
          {check.uncertain ? (
            <SettingsActionButton
              icon={SettingsActionIcon.Retry}
              disabled={check.busy || !active}
              onClick={check.retry}
            >
              {copy("updates.retryTheSameUpdateCheck_919af6")}
            </SettingsActionButton>
          ) : null}
          {machine ? (
            <form
              onKeyDown={guardEnterSubmission}
              onSubmit={(event) => {
                if (!consumeFormSubmit(event)) return;
                void read.refetch();
              }}
            >
              <label>
                {copy("updates.originalUpdateId_51ae1b")}
                <input
                  value={id}
                  maxLength={36}
                  onChange={(event) => {
                    setId(event.target.value);
                    setCandidate(undefined);
                  }}
                />
              </label>
              <SettingsActionButton
                icon={SettingsActionIcon.Retry}
                disabled={!active || !id}
              >
                {copy("updates.inspectOriginalUpdate_3b2c2e")}
              </SettingsActionButton>
            </form>
          ) : null}
          {selected ? (
            <UpdateCandidate
              key={selected.id}
              current={selected}
              active={active && expanded}
              worker={Boolean(machine)}
              controls={controls}
              recoveryTarget={recoveryTarget}
              onRecoveryStateChange={reportCandidateRecovery}
            />
          ) : null}
          <Problem error={check.error || read.error || error} />
        </>
      )}
      {!machine ? (
        <Disclosure
          ref={recovery}
          density={DisclosureDensity.Settings}
          className="app-update-recovery"
        >
          <DisclosureSummary data-settings-search-target="app-recovery">
            {copy("updates.recovery")}
          </DisclosureSummary>
          <div>
            {supported ? (
              <>
                {" "}
                <form
                  onKeyDown={guardEnterSubmission}
                  onSubmit={(event) => {
                    if (!consumeFormSubmit(event)) return;
                    void read.refetch();
                  }}
                >
                  <label>
                    {copy("updates.originalUpdateId_51ae1b")}
                    <input
                      value={id}
                      maxLength={36}
                      onChange={(event) => {
                        setId(event.target.value);
                        setCandidate(undefined);
                      }}
                    />
                  </label>
                  <SettingsActionButton
                    icon={SettingsActionIcon.Retry}
                    disabled={!active || !id}
                  >
                    {copy("updates.inspectOriginalUpdate_3b2c2e")}
                  </SettingsActionButton>
                </form>
              </>
            ) : null}
            <div ref={recoverySlot} />
            {controls ? (
              <LocalInstallationInspection
                active={active}
                controls={controls}
              />
            ) : null}
          </div>
        </Disclosure>
      ) : null}
    </>
  );
  const attention = Boolean(
    check.busy || check.uncertain || check.error || read.error || error,
  );
  return machine ? (
    <Disclosure
      density={DisclosureDensity.Settings}
      data-connection-attention={attention}
      onToggle={(event) => setExpanded(event.currentTarget.open)}
    >
      <DisclosureSummary>
        {copy("updates.workerUpdates_86b3ca")}
      </DisclosureSummary>
      {expanded ? content : null}
    </Disclosure>
  ) : (
    <div ref={desktopRoot} data-connection-attention={attention}>
      {content}
    </div>
  );
}

function nativePhaseLabel(phase: NativeUpdatePhase) {
  switch (phase) {
    case NativeUpdatePhase.Prepared:
      return copy("updates.prepared");
    case NativeUpdatePhase.Installing:
      return copy("updates.installing");
    case NativeUpdatePhase.Installed:
      return copy("updates.installed");
    case NativeUpdatePhase.Failed:
      return copy("updates.failed");
    case NativeUpdatePhase.Uncertain:
      return copy("updates.uncertain");
  }
}
