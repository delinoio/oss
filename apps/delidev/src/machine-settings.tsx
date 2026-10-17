import { consumeFormSubmit, guardEnterSubmission } from "./form-enter";
import {
  useProductLabels,
  productResourceName,
} from "./product-identity-labels";
import {
  SettingsActionButton,
  SettingsActionIcon,
  SettingsActionPresentation,
} from "./settings-action";
import { SettingsTaskDismissButton } from "./settings-task";
import { statusLabel } from "./product-status";
import {
  LocalizedText,
  copy,
  useLocale,
  type MessageKey,
} from "./localization";
import { SettingsTaskActions } from "./settings-task";
import { useCloseSettingsTask } from "./settings-task-context";
import {
  useState,
  useId,
  useMemo,
  useRef,
  useEffect,
  useSyncExternalStore,
} from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  installationObservation,
  validRunnerObservation,
} from "./runner-observation";
import {
  createConnectQueryKey,
  useQuery,
  useTransport,
} from "@connectrpc/connect-query";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import {
  EntityKind,
  ResourceQuery,
  WorkerQuery,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document, encode, items, object, text } from "./documents";
import { installationVersionAllowed } from "./harness-version";
import {
  OpenCodeInstallationCause,
  openCodeInstallationCause,
  type RunnerExclusion,
} from "./runner-observation";
import { Harness, TextField } from "./configuration-fields";
import { ObservedJob, useJobObservation } from "./jobs";
import { useRetainedMutation } from "./mutation";
import {
  GrokDiagnosticNotice,
  grokDiagnosticFromDocument,
  safeGrokDiagnostic,
} from "./grok-diagnostic";
import { InlineRemediation, Problem } from "./ui";
import { Updates } from "./updates";
import { NetworkSettings } from "./network-settings";
import type { PairingAuthority } from "./pairing-grant";

export function useMachineSettingsController(
  initial: Resource,
  active: boolean,
) {
  const transport = useTransport();
  const queryClient = useQueryClient();
  const input = { kind: EntityKind.MACHINE, id: initial.id };
  const queryKey = useMemo(
    () =>
      createConnectQueryKey({
        schema: ResourceQuery.getResource,
        transport,
        input,
        cardinality: "finite",
      }),
    [initial.id, transport],
  );
  const result = useQuery(ResourceQuery.getResource, input, {
    enabled: active,
    refetchInterval: active ? 5000 : false,
  });
  const queryCache = queryClient.getQueryCache();
  const queryHash = queryCache.find({ queryKey, exact: true })?.queryHash;
  const subscribeToReadCount = useMemo(
    () => (notify: () => void) =>
      queryCache.subscribe((event) => {
        if (event.query.queryHash === queryHash) notify();
      }),
    [queryCache, queryHash],
  );
  const getReadUpdateCount = useMemo(
    () => () => queryClient.getQueryState(queryKey)?.dataUpdateCount ?? 0,
    [queryClient, queryKey],
  );
  const readUpdateCount = useSyncExternalStore(
    subscribeToReadCount,
    getReadUpdateCount,
    getReadUpdateCount,
  );
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const exact = (row?: Resource): row is Resource =>
    validRunnerObservation(row) && row.id === initial.id;
  // Same-identity reconnects replace the transport but retain this QueryClient.
  const [observation, setObservation] = useState<{
    id: string;
    queryClient: QueryClient;
    current?: Resource;
    readUpdateCount: number;
    acknowledged?: Resource;
  }>(() => ({
    id: initial.id,
    queryClient,
    readUpdateCount,
  }));
  const retained =
    observation.id === initial.id && observation.queryClient === queryClient
      ? observation
      : {
          id: initial.id,
          queryClient,
          readUpdateCount,
        };
  let current = [retained?.current, initial, acknowledged]
    .filter(exact)
    .reduce<Resource | undefined>(
      (a, b) => (!a || b.revision > a.revision ? b : a),
      undefined,
    );
  const newAcknowledgement = acknowledged !== retained?.acknowledged;
  if (
    newAcknowledgement &&
    exact(acknowledged) &&
    (!current || acknowledged.revision >= current.revision)
  )
    current = acknowledged;
  const freshRead =
    result.isSuccess &&
    !result.error &&
    readUpdateCount > retained.readUpdateCount;
  const readCounterReset = readUpdateCount < retained.readUpdateCount;
  const readResource = result.data?.resource;
  const readExceedsAcknowledgement =
    exact(readResource) &&
    exact(acknowledged) &&
    readResource.revision > acknowledged.revision;
  // Heartbeats advance without a business revision. Consume each successful
  // read once, and fence cached or equal/older reads when a mutation is
  // acknowledged while still accepting a simultaneously delivered newer read.
  if (
    freshRead &&
    (!newAcknowledgement || readExceedsAcknowledgement) &&
    exact(result.data?.resource) &&
    (!current || result.data.resource.revision >= current.revision)
  )
    current = result.data.resource;
  if (
    !retained ||
    current !== retained.current ||
    freshRead ||
    readCounterReset ||
    newAcknowledgement
  )
    setObservation({
      id: initial.id,
      queryClient,
      current,
      readUpdateCount,
      acknowledged,
    });
  const data = useMemo(() => document(current), [current]);
  const [edit, setEdit] = useState<{
    revision: bigint;
    paths: Record<string, string>;
  }>();
  const [verify, setVerifyValue] = useState(false);
  const verifyGeneration = useRef(0);
  const [job, setJob] = useState<Resource | "unknown">();
  const submitted = useRef<
    | {
        edit: typeof edit;
        verify: boolean;
        verifyGeneration: number;
      }
    | undefined
  >(undefined);
  const [accepted, setAccepted] = useState<{
    jobId: string;
    edit: typeof edit;
    verify: boolean;
    verifyGeneration: number;
  }>();
  const verifyEdited = accepted ? verify !== accepted.verify : verify;
  const setVerify = useMemo(
    () => (next: boolean) => {
      setVerifyValue(next);
      verifyGeneration.current += 1;
    },
    [],
  );
  const draft = useRef({
    edit,
    verify,
    verifyGeneration: verifyGeneration.current,
  });
  draft.current = { edit, verify, verifyGeneration: verifyGeneration.current };
  const discovery = useRetainedMutation(
    `machine-discovery:${initial.id}`,
    WorkerQuery.discoverHarnesses,
    (response) => {
      if (exact(response.machine)) setAcknowledged(response.machine);
      setJob(response.job ?? "unknown");
      setAccepted(
        response.job && submitted.current
          ? { jobId: response.job.id, ...submitted.current }
          : undefined,
      );
    },
  );
  const jobObservation = useJobObservation(
    job && job !== "unknown" ? job : undefined,
    active,
    "harness-discovery",
    initial.id,
  );
  const pending =
    discovery.busy ||
    discovery.uncertain ||
    Boolean(job && !jobObservation.terminal);
  useEffect(() => {
    if (
      !accepted ||
      !jobObservation.terminal ||
      jobObservation.current?.id !== accepted.jobId
    )
      return;
    setEdit((currentEdit) =>
      currentEdit === accepted.edit ? undefined : currentEdit,
    );
  }, [accepted, jobObservation.terminal, jobObservation.current?.id]);
  const stale = Boolean(edit && edit.revision !== current?.revision);
  const readError = useMemo(
    () =>
      result.error ||
      (result.data && !exact(result.data.resource)
        ? new ConnectError("Runner observation is unavailable.", Code.DataLoss)
        : undefined),
    [result.error, result.data, initial.id],
  );
  const mutation = useRef(discovery);
  mutation.current = discovery;
  const actions = useMemo(
    () => ({
      send: (...args: Parameters<typeof discovery.send>) => {
        if (mutation.current.busy || mutation.current.uncertain) return;
        submitted.current = draft.current;
        return mutation.current.send(...args);
      },
      retry: () => mutation.current.retry(),
    }),
    [],
  );
  const retainedDiscovery = useMemo(
    () => ({
      ...actions,
      busy: discovery.busy,
      uncertain: discovery.uncertain,
      error: discovery.error,
    }),
    [actions, discovery.busy, discovery.uncertain, discovery.error],
  );
  return useMemo(
    () => ({
      initial,
      current,
      data,
      edit,
      setEdit,
      verify,
      setVerify,
      job,
      setJob,
      jobObservation,
      discovery: retainedDiscovery,
      pending,
      stale,
      readError,
      refetch: result.refetch,
      loading: result.isFetching,
      locked: Boolean(edit || pending || verifyEdited),
    }),
    [
      initial,
      current,
      data,
      edit,
      verify,
      verifyEdited,
      setVerify,
      job,
      jobObservation,
      retainedDiscovery,
      pending,
      stale,
      readError,
      result.refetch,
      result.isFetching,
    ],
  );
}
/**
 * Copy key for an installation exclusion cause. Claude and OpenCode have
 * harness-specific guidance; every other harness keeps the shared copy. OpenCode
 * update guidance applies only to a detected under-baseline version, and a
 * protocol profile mismatch never asks for an update. No key names a numeric
 * version baseline.
 */
export function installationCauseCopyKey(
  harness: string,
  cause: RunnerExclusion,
  detectedVersion?: string,
): MessageKey {
  if (harness === Harness.Claude)
    return `claude-subscription.excluded.${cause}`;
  if (harness === Harness.OpenCode) {
    switch (openCodeInstallationCause(cause, detectedVersion)) {
      case OpenCodeInstallationCause.BelowBaseline:
        return "machine-settings.opencode.excluded.version";
      case OpenCodeInstallationCause.ProtocolProfileMismatch:
        return "machine-settings.opencode.excluded.protocolUnsupported";
      case OpenCodeInstallationCause.InvalidInstallation:
      case OpenCodeInstallationCause.Other:
        break;
    }
  }
  return `machine-settings.excluded.${cause}`;
}
export type MachineSettingsController = ReturnType<
  typeof useMachineSettingsController
>;
export function MachineSettings({
  initial,
  active,
  close,
  authority,
}: {
  initial: Resource;
  active: boolean;
  close: () => void;
  authority?: PairingAuthority;
}) {
  const controller = useMachineSettingsController(initial, active);
  return (
    <MachineSettingsView
      controller={controller}
      active={active}
      close={close}
      authority={authority}
    />
  );
}
/** All actions belong to the original retained controller; this view owns no reads or mutations. */
export function MachineSettingsView({
  controller,
  active,
  close,
  authority,
  compact = false,
}: {
  controller: MachineSettingsController;
  active: boolean;
  close: () => void;
  authority?: PairingAuthority;
  compact?: boolean;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const formId = useId(),
    closeTask = useCloseSettingsTask(close);
  const {
    initial,
    current,
    data,
    edit,
    setEdit,
    verify,
    setVerify,
    job,
    setJob,
    jobObservation,
    discovery,
    pending,
    stale,
    readError,
    refetch,
    loading,
  } = controller;
  if (!current)
    return (
      <InlineRemediation
        summary={copy("machine-settings.observationUnavailable")}
        actions={
          <SettingsActionButton
            icon={SettingsActionIcon.Refresh}
            presentation={SettingsActionPresentation.Icon}
            disabled={!active || loading}
            onClick={() => void refetch()}
          >
            {copy("claude-subscription.refreshRunners")}
          </SettingsActionButton>
        }
      />
    );
  return (
    <section>
      <header>
        <h3>{productResourceName(productLabel, current)}</h3>
      </header>
      <p>
        <LocalizedText
          id="machine-settings.worker_5b4b08"
          components={{
            s0: (
              <>
                {["darwin", "linux", "windows"].includes(text(data.os))
                  ? text(data.os)
                  : copy("machine-settings.extra.b764cdc0eab7")}
              </>
            ),
            s1: (
              <>
                {["amd64", "arm64", "386", "arm"].includes(
                  text(data.architecture),
                )
                  ? text(data.architecture)
                  : copy("machine-settings.extra.b764cdc0eab7")}
              </>
            ),
            s2: (
              <>
                {/^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]{1,64})?$/.test(
                  text(data.version),
                )
                  ? text(data.version)
                  : copy("machine-settings.extra.b764cdc0eab7")}
              </>
            ),
          }}
        />
      </p>
      <p>
        <LocalizedText
          id="machine-settings.lastObserved_f1bd8c"
          components={{
            s0: (
              <>
                {/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(
                  text(data.last_seen),
                )
                  ? text(data.last_seen)
                  : copy("machine-settings.extra.b764cdc0eab7")}
              </>
            ),
            s1: (
              <>
                {data.disabled === true
                  ? copy("machine-settings.disabled_3c6ef5")
                  : ""}
              </>
            ),
          }}
        />
      </p>
      <p>{copy("machine-settings.checksRunOnThisWorkerDelidev_36310f")}</p>
      {!Array.isArray(data.installations) ||
      items(data.installations).length > 4 ? (
        <InlineRemediation
          summary={copy("machine-settings.observationUnavailable")}
        />
      ) : (
        items(data.installations)
          .map(object)
          .map((installation, index) => {
            const harness = text(installation.harness),
              known = Object.values(Harness).includes(harness as Harness),
              duplicate =
                items(data.installations)
                  .map(object)
                  .filter((row) => row.harness === harness).length !== 1;
            const observation =
              !known || duplicate
                ? { cause: "unavailable" as const }
                : installationObservation(
                    installation,
                    installationVersionAllowed(harness),
                  );
            // A failed Grok Build validation keeps the same safe phase, versions
            // and guidance that Settings presents for the original operation.
            const reported =
              harness === Harness.Grok && known && !duplicate
                ? grokDiagnosticFromDocument(
                    object(installation.protocol).grok_diagnostic,
                  )
                : undefined;
            const grok = safeGrokDiagnostic(reported) ? reported : undefined;
            return (
              <article className="result" key={index}>
                <h4>
                  {known
                    ? harness
                    : copy("machine-settings.observationUnavailable")}
                </h4>
                <p>
                  <LocalizedText
                    id="machine-settings.nativeProtocol_4de6c7"
                    components={{
                      s0: (
                        <>
                          {statusLabel(
                            ["verified", "unsupported", "failed"].includes(
                              text(object(installation.protocol).state),
                            )
                              ? text(object(installation.protocol).state)
                              : "",
                          ) || copy("machine-settings.extra.d16948e73a68")}
                        </>
                      ),
                    }}
                  />
                </p>
                {observation.detectedVersion ? (
                  <p>
                    {copy("machine-settings.detectedVersion", {
                      version: observation.detectedVersion,
                    })}
                  </p>
                ) : null}
                {observation.cause && grok ? (
                  <GrokDiagnosticNotice diagnostic={grok} />
                ) : observation.cause ? (
                  <InlineRemediation
                    summary={copy(
                      installationCauseCopyKey(
                        harness,
                        observation.cause,
                        observation.detectedVersion,
                      ),
                    )}
                  />
                ) : (
                  <p>{copy("machine-settings.protocolVerified")}</p>
                )}
              </article>
            );
          })
      )}
      {job ? (
        job === "unknown" ? (
          <p role="alert">
            {copy(
              "machine-settings.discoveryWasAcknowledgedWithoutAReadable_b1200f",
            )}
          </p>
        ) : (
          <ObservedJob observation={jobObservation}>
            {() =>
              jobObservation.terminal ? (
                <SettingsTaskActions>
                  <SettingsActionButton
                    icon={SettingsActionIcon.Cancel}
                    disabled={!active}
                    onClick={() => {
                      setJob(undefined);
                      void refetch();
                    }}
                  >
                    {copy("machine-settings.finishInspection_c3611c")}
                  </SettingsActionButton>
                </SettingsTaskActions>
              ) : null
            }
          </ObservedJob>
        )
      ) : (
        <form
          onKeyDown={guardEnterSubmission}
          id={formId}
          onSubmit={(event) => {
            if (!consumeFormSubmit(event)) return;
            if (!active || pending || stale || readError) return;
            void discovery.send({
              mutation: {
                id: initial.id,
                expectedRevision: edit?.revision ?? current.revision,
                requestId: newRequestId(),
              },
              verifyProtocol: verify,
              ...(edit
                ? {
                    selectionsJson: encode({
                      executables: Object.values(Harness).map((harness) => ({
                        harness,
                        path: edit.paths[harness] ?? "",
                      })),
                    }),
                  }
                : {}),
            });
          }}
        >
          <fieldset disabled={!active || pending || Boolean(readError)}>
            {edit ? (
              <>
                <p>
                  {copy(
                    "machine-settings.theseSelectionsReplaceAllFourHarness_a99040",
                  )}
                </p>
                {Object.values(Harness).map((harness) => (
                  <TextField
                    key={harness}
                    label={copy("machine-settings.executablePath_327ca6", {
                      v0: harness,
                    })}
                    value={edit.paths[harness]}
                    max={4096}
                    change={(path) =>
                      setEdit({
                        ...edit,
                        paths: { ...edit.paths, [harness]: path },
                      })
                    }
                  />
                ))}
                <SettingsActionButton
                  icon={SettingsActionIcon.Cancel}
                  type="button"
                  onClick={() => setEdit(undefined)}
                >
                  {copy("machine-settings.discardPathEdits_906d0c")}
                </SettingsActionButton>
              </>
            ) : (
              <SettingsActionButton
                icon={SettingsActionIcon.Edit}
                presentation={SettingsActionPresentation.Icon}
                type="button"
                onClick={() =>
                  setEdit({
                    revision: current.revision,
                    paths: Object.fromEntries(
                      items(data.installations)
                        .map(object)
                        .map((row) => [
                          text(row.harness),
                          text(row.explicit_path),
                        ]),
                    ),
                  })
                }
              >
                {copy("machine-settings.editExecutablePaths_5868fb")}
              </SettingsActionButton>
            )}
            <label className="checkbox">
              <input
                type="checkbox"
                checked={verify}
                onChange={(event) => setVerify(event.target.checked)}
              />
              {copy(
                "machine-settings.verifyTheInstalledNativeProtocolWithout_0ea550",
              )}
            </label>
          </fieldset>
          <SettingsTaskActions form={formId}>
            <SettingsTaskDismissButton
              type="button"
              data-settings-task-cancel
              onClick={closeTask}
            >
              {copy("machine-settings.backToRunnerDevices_3af74a")}
            </SettingsTaskDismissButton>
            <SettingsActionButton
              icon={SettingsActionIcon.Inspect}
              className="primary"
              disabled={pending || stale || Boolean(readError) || !active}
            >
              {copy("machine-settings.checkInstalledHarnesses_c38636")}
            </SettingsActionButton>
          </SettingsTaskActions>
          {stale ? (
            <p role="alert">
              {copy(
                "machine-settings.workerConfigurationChangedElsewhereYourPath_34c382",
              )}
            </p>
          ) : null}
        </form>
      )}
      <Problem
        error={readError}
        summary={copy("machine-settings.observationReadFailed")}
        actions={
          <SettingsActionButton
            icon={SettingsActionIcon.Refresh}
            presentation={SettingsActionPresentation.Icon}
            disabled={!active || loading}
            onClick={() => void refetch()}
          >
            {copy("claude-subscription.refreshRunners")}
          </SettingsActionButton>
        }
      />
      <Problem error={discovery.error} />
      {discovery.uncertain ? (
        <SettingsActionButton
          icon={SettingsActionIcon.Retry}
          disabled={discovery.busy}
          onClick={discovery.retry}
        >
          {copy("machine-settings.retryTheSameHarnessCheck_7aa9ae")}
        </SettingsActionButton>
      ) : null}
      {!compact ? (
        <>
          <Updates active={active} machine={current} />
          <NetworkSettings
            active={active}
            machine={initial.id}
            authority={authority}
          />
        </>
      ) : null}
    </section>
  );
}
