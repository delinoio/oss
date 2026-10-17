// SPDX-License-Identifier: Apache-2.0
import { consumeFormSubmit, guardEnterSubmission } from "./form-enter";
import { useProductLabels, PresentationKind } from "./product-identity-labels";
import {
  SettingsActionButton,
  SettingsActionIcon,
  SettingsActionTone,
  actionTone,
} from "./settings-action";
import { useSidebarPaneVisible } from "./sidebar-context";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { Timestamp, TimestampMode } from "./timestamp-display";
import { useRunnerRemediation } from "./runner-remediation";
import {
  RunnerWorkflow,
  useRunnerPreference,
} from "./runner-device-preferences";
import { Code, ConnectError } from "@connectrpc/connect";
import { ScrollContinuation, useScrollRoot } from "./scroll-continuation";
import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import {
  useConnectPaginationReader,
  usePaginationChain,
  usePaginationRefresh,
} from "./scroll-pagination-query";
import { statusLabel } from "./product-status";
import {
  ownedMessage,
  useProductMessage,
  LocalizedText,
  copy,
  useLocale,
} from "./localization";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import {
  EntranceMotion,
  useActivationCount,
  useEntranceActivation,
} from "./entrance-motion";
import { useQuery } from "@connectrpc/connect-query";
import {
  EntityKind,
  ResourceQuery,
  ScheduleAction,
  ScheduleQuery,
  WorkerCapability,
  newRequestId,
  isEntityId,
  supportsResourceSchema,
  type ListSchedulesResponse,
  type ListScheduleOccurrencesResponse,
  type Resource,
} from "@delinoio/delidev-api-client";
import {
  document,
  encode,
  items,
  Mode,
  object,
  text,
  Workspace,
  type Document,
} from "./documents";
import {
  projectRepositoryOption,
  ReferenceFields,
  ResourceChoice,
  TextField,
} from "./configuration-fields";
import { useProjectRepositoryNames } from "./project-repositories";
import { useRetainedMutation } from "./mutation";
import { useLocalWorkerProof, type ReadLocalWorkerProof } from "./local-worker";
import { ServiceProblem, Failure, Problem } from "./ui";
import { Icon as SidebarIcon } from "./sidebar";
import {
  ScheduleCreation,
  type ScheduleCreationProps,
} from "./schedule-creation";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";
import { useDestinationDispatch } from "./destination-window";
import { Surface } from "./surface";
import {
  NavigationIntentSource,
  useNavigationIntent,
} from "./navigation-intent";

enum Overlap {
  Overlap = "overlap",
  Skip = "skip",
  Wait = "wait",
}
enum EnabledFilter {
  All = "all",
  Enabled = "enabled",
  Paused = "paused",
}
const ignoreProtectedChange = (_protectedState: boolean) => undefined;
const scheduleName = (row?: Resource) =>
  text(object(document(row).definition).name) ||
  copy("schedules.extra.d5ef9d155d1e");
const emptyDefinition = (): Document => ({
  name: "",
  prompt: "",
  enabled: true,
  project_id: "",
  agent_id: "",
  machine_id: "",
  workspace: Workspace.Worktree,
  mode: Mode.Execute,
  cron: "0 9 * * 1-5",
  timezone: "UTC",
  overlap: Overlap.Overlap,
});

// A focus target is available only when it is connected, enabled, outside any
// hidden or inert ancestor, and rendered: stylesheet rules such as the compact
// opener's display:none outside the compact breakpoint leave the element
// connected but unfocusable, so focus() on it would silently do nothing.
function isFocusAvailable(
  element: HTMLElement | null | undefined,
): element is HTMLElement {
  if (!element?.isConnected) return false;
  if (element.matches(":disabled") || element.closest("[hidden], [inert]"))
    return false;
  const view = element.ownerDocument.defaultView;
  if (!view) return false;
  const visibility = view.getComputedStyle(element).visibility;
  if (visibility === "hidden" || visibility === "collapse") return false;
  for (let node: Element | null = element; node; node = node.parentElement)
    if (view.getComputedStyle(node).display === "none") return false;
  return true;
}
function hasDuplicateIds(rows: readonly { id: string }[]) {
  return new Set(rows.map((row) => row.id)).size !== rows.length;
}

function schedulePage(response: ListSchedulesResponse) {
  if (
    response.schedules.length > 50 ||
    response.schedules.some((row) => !isEntityId(row.id)) ||
    hasDuplicateIds(response.schedules)
  )
    throw new ConnectError("Invalid schedule page", Code.DataLoss);
  return {
    rows: response.schedules.map((row) => ({
      id: row.id,
      revision: row.revision,
    })),
    payload: response.schedules,
    nextPageToken: response.nextPageToken,
  };
}
function occurrencePage(response: ListScheduleOccurrencesResponse) {
  if (
    response.occurrences.length > 50 ||
    response.occurrences.some((row) => !isEntityId(row.id)) ||
    hasDuplicateIds(response.occurrences)
  )
    throw new ConnectError("Invalid schedule occurrence page", Code.DataLoss);
  return {
    rows: response.occurrences.map((row) => ({
      id: row.id,
      revision: row.revision,
    })),
    payload: response.occurrences,
    nextPageToken: response.nextPageToken,
  };
}

export function StartingReferences({
  project,
  starting,
  change,
  active,
  showRepositoryNames = false,
}: {
  project: string;
  starting: unknown[];
  change: (value: unknown[]) => void;
  active: boolean;
  showRepositoryNames?: boolean;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const [selected, setSelected] = useState("");
  const result = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.PROJECT, id: project },
    { enabled: active && Boolean(project) },
  );
  const references = starting.map(object);
  const repositories = items(document(result.data?.resource).repositories).map(
    text,
  );
  // Retained overrides can outlive the Project's current repository list.
  const nameIds = [
    ...new Set([
      ...repositories,
      ...references.map((row) => text(row.repository_id)),
    ]),
  ];
  const { names, loading, error, retry } = useProjectRepositoryNames(
    nameIds,
    active && showRepositoryNames,
  );
  const label = (id: string, position = nameIds.indexOf(id)) =>
    showRepositoryNames
      ? projectRepositoryOption(id, position, names, productLabel)
      : productLabel(PresentationKind.Repository, id);
  return (
    <fieldset>
      <legend>{copy("schedules.startingReferenceOverrides_58881e")}</legend>
      <p>{copy("schedules.omittedRepositoriesUseTheirSavedStarting_266784")}</p>
      <label>
        {copy("schedules.addRepositoryOverride_3564ca")}
        <select
          value={selected}
          onChange={(event) => setSelected(event.target.value)}
        >
          <option value="">
            {copy("schedules.selectAProjectRepository_404acc")}
          </option>
          {repositories
            .map((id, index) => ({ id, index }))
            .filter(
              ({ id }) => !references.some((row) => row.repository_id === id),
            )
            .map(({ id, index }) => (
              <option key={id} value={id}>
                {label(id, index)}
              </option>
            ))}
        </select>
      </label>
      <button
        type="button"
        disabled={!selected || references.length >= 1000}
        onClick={() => {
          change([
            ...starting,
            {
              repository_id: selected,
              reference: { type: "local-branch", name: "" },
            },
          ]);
          setSelected("");
        }}
      >
        {copy("schedules.addStartingOverride_71ea93")}
      </button>
      <Problem error={result.error} />
      {loading ? (
        <p role="status">{copy("project-creation.loadingNames")}</p>
      ) : null}
      <Problem error={error} />
      {error ? (
        <SettingsActionButton
          icon={SettingsActionIcon.Retry}
          type="button"
          disabled={!active || loading}
          onClick={retry}
        >
          {copy("project-creation.retryRead")}
        </SettingsActionButton>
      ) : null}
      {references.map((row) => (
        <fieldset key={text(row.repository_id)}>
          <legend>{label(text(row.repository_id))}</legend>
          <ReferenceFields
            label={copy("schedules.starting_23cc8e", {
              v0: label(text(row.repository_id)),
            })}
            value={row.reference}
            change={(reference) =>
              change(
                reference.type
                  ? starting.map((value) =>
                      object(value).repository_id === row.repository_id
                        ? { ...row, reference }
                        : value,
                    )
                  : starting.filter(
                      (value) =>
                        object(value).repository_id !== row.repository_id,
                    ),
              )
            }
          />
          <button
            type="button"
            onClick={() =>
              change(
                starting.filter(
                  (value) => object(value).repository_id !== row.repository_id,
                ),
              )
            }
          >
            {copy("schedules.removeStartingOverride_21a3a5")}
          </button>
        </fieldset>
      ))}
    </fieldset>
  );
}

export function ScheduleEditor({
  initial,
  active,
  saved,
  cancel,
  readLocalWorker,
  protectedChange = ignoreProtectedChange,
}: {
  initial?: Resource;
  active: boolean;
  saved: (resource?: Resource) => void;
  cancel: () => void;
  readLocalWorker?: ReadLocalWorkerProof;
  protectedChange?: (protectedState: boolean) => void;
}) {
  useLocale();
  const localProof = useLocalWorkerProof(readLocalWorker);
  const [definition, setDefinition] = useState<Document>(() =>
    initial ? object(document(initial).definition) : emptyDefinition(),
  );
  const runnerProject = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.PROJECT, id: text(definition.project_id) },
    {
      enabled: active && !initial && Boolean(definition.project_id),
      retry: false,
    },
  );
  const projectKnown =
    !definition.project_id ||
    (!runnerProject.isFetching &&
      !runnerProject.error &&
      runnerProject.data?.resource?.id === definition.project_id &&
      supportsResourceSchema(runnerProject.data.resource));
  const runnerEligible = (row: Resource) =>
    projectKnown &&
    (definition.workspace !== Workspace.Worktree ||
      !definition.project_id ||
      !items(document(runnerProject.data?.resource).repositories).length ||
      items(document(row).worker_capabilities).some(
        (capability) =>
          capability === "remote-workspace-clone-v1" ||
          capability === WorkerCapability.REMOTE_WORKSPACE_CLONE_V1,
      ));
  const runner = useRunnerPreference(
    RunnerWorkflow.Schedule,
    active && !initial && projectKnown,
    runnerEligible,
    false,
    `${text(definition.project_id)}:${text(definition.workspace)}:${runnerProject.data?.resource?.revision ?? ""}`,
  );
  const automaticRunner = useRef("");
  const runnerTouched = useRef(Boolean(initial));
  const [limit, setLimit] = useProductMessage("");
  const current = useQuery(
    ScheduleQuery.getSchedule,
    { id: initial?.id ?? "" },
    {
      enabled: active && Boolean(initial),
      refetchInterval: active ? 5000 : false,
    },
  );
  const mutation = useRetainedMutation(
    `schedule-save:${initial?.id ?? "new"}`,
    ScheduleQuery.saveSchedule,
    (response, request) => {
      if (
        response.schedule?.kind === EntityKind.SCHEDULE &&
        response.schedule.revision > 0n &&
        isEntityId(response.schedule.id) &&
        supportsResourceSchema(response.schedule)
      ) {
        const submitted = object(
          JSON.parse(new TextDecoder().decode(request.definitionJson)),
        );
        runner.remember(text(submitted.machine_id));
      }
      saved(response.schedule);
    },
  );
  const automaticRunnerBlocked = Boolean(
    !initial &&
    !runnerTouched.current &&
    automaticRunner.current &&
    definition.machine_id === automaticRunner.current &&
    (runner.reading ||
      !projectKnown ||
      !runner.suggestion ||
      !runnerEligible(runner.suggestion)),
  );
  const inspection = useRunnerRemediation();
  const blocked =
    mutation.busy ||
    mutation.uncertain ||
    localProof.busy ||
    inspection?.pendingFor(text(definition.machine_id)) === true;
  useEffect(() => protectedChange(true), [protectedChange]);
  const stale = Boolean(
    initial &&
    current.data?.schedule &&
    initial.revision !== current.data.schedule.revision,
  );
  const local = definition.workspace === Workspace.Local;
  useEffect(() => {
    if (
      active &&
      !runnerTouched.current &&
      !mutation.busy &&
      !mutation.uncertain &&
      !localProof.busy &&
      definition.workspace !== Workspace.Local &&
      runner.suggestion &&
      runnerEligible(runner.suggestion) &&
      (!definition.machine_id ||
        definition.machine_id === automaticRunner.current)
    ) {
      automaticRunner.current = runner.suggestion.id;
      setDefinition((value) =>
        value.machine_id === runner.suggestion!.id
          ? value
          : { ...value, machine_id: runner.suggestion!.id },
      );
    }
  }, [
    active,
    runner.suggestion,
    blocked,
    projectKnown,
    definition.project_id,
    runnerProject.data?.resource,
    definition.workspace,
    definition.machine_id,
  ]);
  useEffect(() => {
    if (
      active &&
      !runnerTouched.current &&
      !blocked &&
      projectKnown &&
      !runner.reading &&
      !runner.lookupFailed &&
      !runner.suggestion &&
      automaticRunner.current &&
      definition.machine_id === automaticRunner.current
    ) {
      automaticRunner.current = "";
      setDefinition((value) => ({ ...value, machine_id: "" }));
    }
  }, [
    active,
    blocked,
    projectKnown,
    runner.reading,
    runner.lookupFailed,
    runner.suggestion,
    definition.machine_id,
  ]);
  const change = (next: Document) => {
    if (
      next.machine_id !== definition.machine_id ||
      next.workspace !== definition.workspace
    ) {
      runnerTouched.current = true;
      runner.touch();
    }
    if (
      encode(next).byteLength > 1 << 20 ||
      new TextEncoder().encode(text(next.prompt)).byteLength > 256 << 10
    ) {
      setLimit(ownedMessage("schedules.extra.1a9ab790643b"));
      return false;
    }
    setDefinition(next);
    setLimit("");
    return true;
  };
  const field = (key: string) => (value: unknown) =>
    change({ ...definition, [key]: value });
  const submit = async () => {
    if (
      blocked ||
      automaticRunnerBlocked ||
      stale ||
      (initial && current.error)
    )
      return;
    runner.touch();
    const original = object(document(initial).definition);
    const retainedLocal =
      local &&
      original.workspace === Workspace.Local &&
      original.machine_id === definition.machine_id;
    const input = {
      mutation: {
        id: initial?.id ?? "",
        expectedRevision: initial?.revision ?? 0n,
        requestId: newRequestId(),
      },
      schemaVersion: 1,
      definitionJson: encode(definition),
    };
    const proof =
      local && !retainedLocal
        ? await localProof.load(text(definition.machine_id))
        : undefined;
    if (local && !retainedLocal && !proof) return;
    void mutation.send({ ...input, localWorkerToken: proof?.token });
  };
  if (!initial) {
    const props: ScheduleCreationProps = {
      definition,
      change,
      active,
      blocked,
      submitBlocked: automaticRunnerBlocked,
      cancel,
      submit,
      localAvailable: localProof.available,
      selectLocal: () => {
        void localProof.load().then((proof) => {
          if (proof)
            change({
              ...definition,
              workspace: Workspace.Local,
              machine_id: proof.machineId,
              starting: [],
            });
        });
      },
      references: (referencesActive) =>
        local ? null : (
          <StartingReferences
            key={text(definition.project_id)}
            project={text(definition.project_id)}
            starting={items(definition.starting)}
            change={field("starting")}
            active={referencesActive}
            showRepositoryNames
          />
        ),
      errors: (
        <>
          <Problem
            error={runnerProject.error}
            actions={
              <button
                type="button"
                disabled={!active || blocked || runnerProject.isFetching}
                onClick={() => void runnerProject.refetch()}
              >
                {copy("ui.retryCurrentRead")}
              </button>
            }
          />
          {runner.guidance}
          {limit ? <p role="alert">{limit}</p> : null}
          {localProof.problem ? <p role="alert">{localProof.problem}</p> : null}
          <Problem error={mutation.error} />
        </>
      ),
      retry: mutation.uncertain ? (
        <button type="button" disabled={mutation.busy} onClick={mutation.retry}>
          {copy("schedules.retryTheSameSchedule_7702b8")}
        </button>
      ) : null,
    };
    return <ScheduleCreation {...props} />;
  }
  return (
    <section>
      <h3>
        {initial
          ? copy("schedules.editSchedule_559b37")
          : copy("schedules.newSchedule_3bfe90")}
      </h3>
      <form
        onKeyDown={guardEnterSubmission}
        onSubmit={(event) => {
          if (!consumeFormSubmit(event)) return;
          void submit();
        }}
      >
        <fieldset disabled={blocked}>
          <TextField
            label={copy("schedules.scheduleName_60918e")}
            value={definition.name}
            required
            change={field("name")}
          />
          <label className="checkbox">
            <input
              type="checkbox"
              checked={definition.enabled === true}
              onChange={(event) => field("enabled")(event.target.checked)}
            />
            {copy("schedules.enableFutureScheduledRuns_d1eab6")}
          </label>
          <ResourceChoice
            label={copy("schedules.project_985959")}
            kind={EntityKind.PROJECT}
            value={text(definition.project_id)}
            active={active}
            required
            change={(project_id) =>
              setDefinition({ ...definition, project_id, starting: [] })
            }
          />
          <ResourceChoice
            label={copy("schedules.agentWorker_a4caa7")}
            kind={EntityKind.AGENT}
            value={text(definition.agent_id)}
            active={active}
            required
            change={field("agent_id")}
          />
          <ResourceChoice
            label={copy("schedules.runnerDevice_37efe3")}
            kind={EntityKind.MACHINE}
            value={text(definition.machine_id)}
            active={active}
            disabled={local}
            required
            change={field("machine_id")}
          />
          <div className="actions">
            <button
              type="button"
              aria-pressed={!local}
              onClick={() =>
                setDefinition({ ...definition, workspace: Workspace.Worktree })
              }
            >
              {copy("schedules.useSeparateWorktrees_5cd0b6")}
            </button>
            <button
              type="button"
              disabled={!localProof.available}
              aria-pressed={local}
              onClick={() => {
                void localProof.load().then((proof) => {
                  if (proof)
                    setDefinition({
                      ...definition,
                      workspace: Workspace.Local,
                      machine_id: proof.machineId,
                      starting: [],
                    });
                });
              }}
            >
              {copy("schedules.useThisComputerSLocalCheckouts_eadaad")}
            </button>
          </div>
          <p>
            <LocalizedText
              id="schedules.workspace_4eaed8"
              components={{
                s0: (
                  <>
                    {local
                      ? copy(
                          "schedules.localComputerOriginatingWorkerSelected_9357e2",
                        )
                      : copy(
                          "schedules.worktreeSeparateDetachedCheckouts_5318f3",
                        )}
                  </>
                ),
              }}
            />
          </p>
          {local ? (
            <p>
              {copy(
                "schedules.existingLocalSchedulesRetainTheirAuthenticated_538d29",
              )}
            </p>
          ) : (
            <StartingReferences
              key={text(definition.project_id)}
              project={text(definition.project_id)}
              starting={items(definition.starting)}
              change={field("starting")}
              active={active}
              showRepositoryNames
            />
          )}
          <label>
            {copy("schedules.executionMode_c21e7c")}
            <select
              value={text(definition.mode)}
              onChange={(event) => field("mode")(event.target.value)}
            >
              {Object.values(Mode).map((mode) => (
                <option key={mode} value={mode}>
                  {mode}
                </option>
              ))}
            </select>
          </label>
          <label>
            {copy("schedules.scheduledPrompt_209d2b")}
            <textarea
              required
              rows={6}
              maxLength={262144}
              value={text(definition.prompt)}
              onChange={(event) => field("prompt")(event.target.value)}
            />
          </label>
          <TextField
            label={copy("schedules.cronExpression_9e6e7d")}
            value={definition.cron}
            required
            max={512}
            change={field("cron")}
          />
          <p>{copy("schedules.fiveFieldsMinuteHourDayOf_8fec47")}</p>
          <TextField
            label={copy("schedules.ianaTimezone_37cf56")}
            value={definition.timezone}
            required
            change={field("timezone")}
          />
          <label>
            {copy("schedules.whenAPreviousOccurrenceIsStill_851448")}
            <select
              value={text(definition.overlap)}
              onChange={(event) => field("overlap")(event.target.value)}
            >
              <option value={Overlap.Overlap}>
                {copy("schedules.overlapIndependentSessions_df3095")}
              </option>
              <option value={Overlap.Skip}>
                {copy("schedules.skipTheNewOccurrence_e30b7e")}
              </option>
              <option value={Overlap.Wait}>
                {copy("schedules.waitInFifoOrderForConfirmed_f1ce8e")}
              </option>
            </select>
          </label>
          <p>{copy("schedules.theServerContinuesSchedulingWhenThe_116ec3")}</p>
        </fieldset>
        {stale ? (
          <p role="alert">
            {copy("schedules.thisScheduleChangedElsewhereYourDraft_9d939b")}
          </p>
        ) : null}
        {limit ? <p role="alert">{limit}</p> : null}
        {localProof.problem ? <p role="alert">{localProof.problem}</p> : null}
        <Problem
          error={current.error}
          actions={
            <button
              type="button"
              disabled={!active || blocked || current.isFetching}
              onClick={() => void current.refetch()}
            >
              {copy("ui.retryCurrentRead")}
            </button>
          }
        />
        <Problem error={mutation.error} />
        <div className="actions">
          <button
            className="primary"
            disabled={blocked || stale || Boolean(initial && current.error)}
          >
            {copy("schedules.saveSchedule_387f35")}
          </button>
          {mutation.uncertain ? (
            <button
              type="button"
              disabled={mutation.busy}
              onClick={mutation.retry}
            >
              {copy("schedules.retryTheSameSchedule_7702b8")}
            </button>
          ) : null}
          <button type="button" disabled={blocked} onClick={cancel}>
            {copy("schedules.cancelScheduleEdit_efc0c0")}
          </button>
        </div>
      </form>
    </section>
  );
}

function Occurrence({
  resource,
  open,
}: {
  resource: Resource;
  open: (id: string) => void;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const data = document(resource),
    selection = object(data.selection),
    problem = object(data.problem);
  return (
    <article className="result">
      <h4>
        {text(data.trigger)} · {statusLabel(text(data.state))}
      </h4>
      <p>
        <LocalizedText
          id="schedules.dueAccepted_f5f495"
          components={{
            s0: (
              <>
                <Timestamp value={text(data.due_at)} />
              </>
            ),
            s1: (
              <>
                <Timestamp value={text(data.accepted_at)} />
              </>
            ),
          }}
        />
      </p>
      {text(data.reason) ? (
        <p>
          <LocalizedText
            id="schedules.reason_efbcae"
            components={{ s0: <>{text(data.reason)}</> }}
          />
        </p>
      ) : null}
      {text(problem.message) ? (
        <ServiceProblem code={text(problem.code) || text(problem.problem_code)}>
          <p role="alert">
            {text(problem.message)} {text(problem.guidance)}
          </p>
        </ServiceProblem>
      ) : null}
      {text(data.session_id) ? (
        <button onClick={() => open(text(data.session_id))}>
          {copy("schedules.openOccurrenceSession_dceba4")}
        </button>
      ) : (
        <p>{copy("schedules.noSessionHasBeenCreatedFor_782f8a")}</p>
      )}
      <Disclosure>
        <DisclosureSummary>
          {copy("schedules.originalAcceptedSelection_4b454e")}
        </DisclosureSummary>
        <p>
          {text(selection.name)} · {text(selection.workspace)} ·{" "}
          {text(selection.mode)}
        </p>
        <p>
          <LocalizedText
            id="schedules.agentWorker_16fb23"
            components={{
              s0: (
                <>
                  {productLabel(
                    PresentationKind.Agent,
                    text(selection.agent_id),
                  )}
                </>
              ),
              s1: (
                <>
                  {productLabel(
                    PresentationKind.Runner,
                    text(selection.machine_id),
                  )}
                </>
              ),
            }}
          />
        </p>
        <p>
          <LocalizedText
            id="schedules.overlap_eb259a"
            components={{ s0: <>{text(data.overlap)}</> }}
          />
        </p>
        <pre>{text(selection.prompt)}</pre>
        <small>{productLabel(PresentationKind.Occurrence, resource.id)}</small>
      </Disclosure>
    </article>
  );
}
export function ScheduleHistory({
  id,
  active,
  open,
}: {
  id: string;
  active: boolean;
  open: (id: string) => void;
}) {
  useLocale();
  const content = useRef<HTMLElement>(null),
    root = useScrollRoot(content);
  const request = useCallback(
    (token: string) => ({ scheduleId: id, pageSize: 50, pageToken: token }),
    [id],
  );
  const reader = useConnectPaginationReader(
    ScheduleQuery.listScheduleOccurrences,
    request,
    occurrencePage,
  );
  // Inactive presentation (a blocked mutation, a notification review, a hidden
  // Schedules destination) keeps the resident payload and its mounted
  // Occurrence disclosures; reads stay suspended and activation refreshes.
  const result = usePaginationChain(id, active, reader, true, true);
  usePaginationRefresh(
    ScheduleQuery.listScheduleOccurrences,
    request(""),
    active,
    result.refresh,
    5000,
  );
  return (
    <section ref={content}>
      <header>
        <h3>{copy("schedules.occurrenceHistory_4a178f")}</h3>
        <button
          disabled={Boolean(result.loading)}
          onClick={result.refreshExplicit}
        >
          {copy("schedules.refreshHistory_70f3d2")}
        </button>
      </header>
      <p>
        {copy(
          "schedules.historyRemainsAvailableAfterScheduleConfiguration_44b3c0",
        )}
      </p>
      <Failure failure={result.error?.failure} />
      <ScrollPayloadWindow
        identity={paginationIdentity}
        revision={paginationRevision}
        query={result}
        root={root}
        active={active}
      >
        {(payload) =>
          payload.map((row) => (
            <Occurrence resource={row} key={row.id} open={open} />
          ))
        }
      </ScrollPayloadWindow>
      {result.loaded && result.rows.length === 0 ? (
        <p>{copy("schedules.noRetainedOccurrences_dc8c9a")}</p>
      ) : null}
      <ScrollContinuation
        query={result}
        root={root}
        active={active}
        label={copy("schedules.occurrenceHistory_4a178f")}
      />
    </section>
  );
}

export function ScheduleDetails({
  initial,
  active,
  open,
  close,
  edit,
  protectedChange = ignoreProtectedChange,
}: {
  initial: Resource;
  active: boolean;
  open: (id: string) => void;
  close: () => void;
  edit: (row: Resource) => void;
  protectedChange?: (protectedState: boolean) => void;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const [deleted, setDeleted] = useState(false),
    [confirm, setConfirm] = useState<"delete" | "run">();
  const [acknowledged, setAcknowledged] = useState<Resource>(),
    [occurrence, setOccurrence] = useState<Resource>();
  const occurrenceResult = useQuery(
    ScheduleQuery.getScheduleOccurrence,
    { scheduleId: initial.id, id: occurrence?.id ?? "" },
    {
      enabled: active && Boolean(occurrence),
      refetchInterval: active && occurrence ? 5000 : false,
    },
  );
  const result = useQuery(
    ScheduleQuery.getSchedule,
    { id: initial.id },
    {
      enabled: active && !deleted,
      refetchInterval: active && !deleted ? 5000 : false,
    },
  );
  const current = [initial, result.data?.schedule, acknowledged]
    .filter((row): row is Resource => Boolean(row))
    .reduce((a, b) => (a.revision >= b.revision ? a : b));
  const data = document(current),
    definition = object(data.definition),
    problem = object(data.problem);
  // A late accepted response must neither read nor change this view while it is
  // inactive (for example during a notification review): the retained details
  // stay exactly as they were hidden, and the whole response, including its
  // refetch, is applied in order when the view is active again.
  const activeNow = useRef(active);
  activeNow.current = active;
  const latestRefetch = useRef(result.refetch);
  latestRefetch.current = result.refetch;
  const deferredResponses = useRef<(() => void)[]>([]);
  const applyWhenActive = (apply: () => void) => {
    if (activeNow.current) apply();
    else deferredResponses.current.push(apply);
  };
  useEffect(() => {
    if (!active || deferredResponses.current.length === 0) return;
    const queued = deferredResponses.current;
    deferredResponses.current = [];
    for (const apply of queued) apply();
  }, [active]);
  const control = useRetainedMutation(
    `schedule-control:${initial.id}`,
    ScheduleQuery.controlSchedule,
    (response) =>
      applyWhenActive(() => {
        setAcknowledged(response.schedule);
        void latestRefetch.current();
      }),
  );
  const remove = useRetainedMutation(
    `schedule-delete:${initial.id}`,
    ScheduleQuery.deleteSchedule,
    () =>
      applyWhenActive(() => {
        setDeleted(true);
        setConfirm(undefined);
      }),
  );
  const run = useRetainedMutation(
    `schedule-run:${initial.id}`,
    ScheduleQuery.runScheduleNow,
    (response) =>
      applyWhenActive(() => {
        setOccurrence(response.occurrence);
        setConfirm(undefined);
        void latestRefetch.current();
      }),
  );
  const operations = [control, remove, run],
    blocked = operations.some(
      (operation) => operation.busy || operation.uncertain,
    );
  useEffect(() => {
    protectedChange(blocked || Boolean(confirm));
  }, [blocked, confirm, protectedChange]);
  const mutation = () => ({
    id: initial.id,
    expectedRevision: current.revision,
    requestId: newRequestId(),
  });
  return (
    <section>
      <header>
        <h2>{scheduleName(current)}</h2>
        <button disabled={blocked || Boolean(confirm)} onClick={close}>
          {copy("schedules.backToSchedules_975251")}
        </button>
      </header>
      <small>{productLabel(PresentationKind.Schedule, initial.id)}</small>
      {deleted ? (
        <p>
          {copy(
            "schedules.scheduleConfigurationDeletedRetainedOccurrencesAnd_08b290",
          )}
        </p>
      ) : (
        <>
          <p>
            {definition.enabled === true
              ? copy("schedules.enabled_92c1cd")
              : copy("schedules.paused_e159b0")}{" "}
            · {text(definition.cron)} · {text(definition.timezone)}
          </p>
          <p>
            <LocalizedText
              id="schedules.nextRunUtc_eca09c"
              components={{
                s0: (
                  <>
                    <Timestamp
                      mode={TimestampMode.Absolute}
                      timeZone="UTC"
                      value={text(data.next_run_at)}
                      fallback={copy("schedules.extra.170fa2a3d0f0")}
                    />
                  </>
                ),
              }}
            />
          </p>
          <p>
            <LocalizedText
              id="schedules.overlapPolicy_9f02ce"
              components={{ s0: <>{text(definition.overlap)}</> }}
            />
          </p>
          {text(problem.message) ? (
            <ServiceProblem
              code={text(problem.code) || text(problem.problem_code)}
            >
              <p role="alert">
                {text(problem.message)} {text(problem.guidance)}
              </p>
            </ServiceProblem>
          ) : null}
          <div className="actions">
            <button
              disabled={blocked || current.schemaVersion !== 1}
              onClick={() => edit(current)}
            >
              {copy("schedules.editSchedule_559b37")}
            </button>
            <button
              disabled={blocked}
              onClick={() =>
                void control.send({
                  mutation: mutation(),
                  action:
                    definition.enabled === true
                      ? ScheduleAction.PAUSE
                      : ScheduleAction.RESUME,
                })
              }
            >
              {definition.enabled === true
                ? copy("schedules.pauseFutureRuns_bd1b76")
                : copy("schedules.resumeFutureRuns_273685")}
            </button>
            <button disabled={blocked} onClick={() => setConfirm("run")}>
              {copy("schedules.runNow_099139")}
            </button>
            <button
              {...actionTone(SettingsActionTone.Danger)}
              disabled={blocked}
              onClick={() => setConfirm("delete")}
            >
              {copy("schedules.deleteSchedule_d8d0bd")}
            </button>
          </div>
          {confirm ? (
            <div className="notice">
              <p>
                {confirm === "run"
                  ? copy(
                      "schedules.acceptOneIndependentOccurrenceNowUsing_09cf1a",
                    )
                  : copy(
                      "schedules.deleteFutureSchedulingConfigurationAlreadyAccepted_eb8d35",
                    )}
              </p>
              <button
                {...actionTone(
                  confirm === "delete"
                    ? SettingsActionTone.Danger
                    : SettingsActionTone.Default,
                )}
                disabled={blocked}
                onClick={() =>
                  confirm === "run"
                    ? void run.send({ mutation: mutation() })
                    : void remove.send({ mutation: mutation() })
                }
              >
                {confirm === "run"
                  ? copy("schedules.confirmRunNow_e9bc9d")
                  : copy("schedules.confirmScheduleDeletion_27d4ca")}
              </button>
              <button disabled={blocked} onClick={() => setConfirm(undefined)}>
                {copy("schedules.cancel_19766e")}
              </button>
            </div>
          ) : null}
          <Problem error={result.error} />
        </>
      )}
      {operations.map((operation, index) => (
        <div key={index}>
          <Problem error={operation.error} />
          {operation.uncertain ? (
            <button
              {...actionTone(
                index === 1
                  ? SettingsActionTone.Danger
                  : SettingsActionTone.Default,
              )}
              disabled={operation.busy}
              onClick={operation.retry}
            >
              <LocalizedText
                id="schedules.retryTheSame_4cb78a"
                components={{
                  s0: (
                    <>
                      {index === 0
                        ? copy("schedules.scheduleControl_8ea17b")
                        : index === 1
                          ? copy("schedules.scheduleDeletion_183079")
                          : copy("schedules.runNow_099139")}
                    </>
                  ),
                }}
              />
            </button>
          ) : null}
        </div>
      ))}
      {occurrence ? (
        <section>
          <h3>{copy("schedules.runNowAccepted_e83ab1")}</h3>
          <Problem error={occurrenceResult.error} />
          <Occurrence
            resource={
              occurrenceResult.data?.occurrence &&
              occurrenceResult.data.occurrence.revision >= occurrence.revision
                ? occurrenceResult.data.occurrence
                : occurrence
            }
            open={open}
          />
        </section>
      ) : null}
      <ScheduleHistory
        id={initial.id}
        active={active && !blocked}
        open={open}
      />
    </section>
  );
}

export function Schedules({
  active,
  open,
  readLocalWorker,
  notificationOccurrence,
  clearNotificationOccurrence,
  restore,
  selectionChanged,
}: {
  active: boolean;
  open: (id: string) => void;
  /** Schedule reopened by the product route from a fresh read after a reload. */
  restore?: { resource: Resource; generation: string };
  /** Reports the selected Schedule ID ("" when none) for the product route. */
  selectionChanged?: (id: string) => void;
  readLocalWorker?: ReadLocalWorkerProof;
  notificationOccurrence?: {
    id: string;
    scheduleId: string;
    generation: string;
  };
  clearNotificationOccurrence?: () => void;
}) {
  useLocale();
  const originalNotificationOccurrence = useQuery(
    ScheduleQuery.getScheduleOccurrence,
    {
      scheduleId: notificationOccurrence?.scheduleId ?? "",
      id: notificationOccurrence?.id ?? "",
    },
    {
      enabled: active && Boolean(notificationOccurrence),
      retry: false,
      refetchInterval: active && notificationOccurrence ? 5000 : false,
    },
  );
  const [filter, setFilter] = useState(EnabledFilter.All);
  const content = useRef<HTMLDivElement>(null),
    root = useScrollRoot(content);
  const [selected, setSelected] = useState<Resource>(),
    [editing, setEditing] = useState<{ initial?: Resource; key: string }>();
  const [projectId, setProjectId] = useState("");
  const [protectedWorkflow, setProtectedWorkflow] = useState(false);
  const restored = useRef("");
  useEffect(() => {
    if (!restore || restored.current === restore.generation) return;
    restored.current = restore.generation;
    setSelected(restore.resource);
  }, [restore]);
  const selectedScheduleId = selected?.id ?? "";
  useEffect(() => {
    selectionChanged?.(selectedScheduleId);
  }, [selectedScheduleId, selectionChanged]);
  // One page-root entrance serves every explicit entry, so only one 4px
  // transform is ever active. Destination activation, New schedule, selecting a
  // schedule and opening an original notification occurrence each advance the
  // key; recurrence edits, occurrence and job updates, saves, close, refetch and
  // polling do not. Nothing waits for the motion and Run is never triggered by it.
  const [entries, setEntries] = useState({ count: 0, generation: "" });
  const occurrenceGeneration = notificationOccurrence?.generation ?? "";
  if (occurrenceGeneration && occurrenceGeneration !== entries.generation)
    setEntries({ count: entries.count + 1, generation: occurrenceGeneration });
  const destinationActivations = useActivationCount(active);
  const pageEntrance = useEntranceActivation(
    `schedules:${destinationActivations}:${entries.count}`,
    active,
    EntranceMotion.Y4,
  );
  const closeDrawer = useCloseSidebarDrawer();
  const navigationIntent = useNavigationIntent();
  const dispatch = useDestinationDispatch();
  const reviewingNotification = Boolean(notificationOccurrence);
  // The notification occurrence destination is exclusive, so the sidebar
  // interactions and list reads stop together with the hidden workspace.
  const locked = protectedWorkflow || Boolean(editing) || reviewingNotification;
  const workspaceActive = active && !reviewingNotification;
  // Hiding the inert workspace blurs its focused control and the review's Back
  // button unmounts on return, so remember the control and restore it then.
  const workspace = useRef<HTMLDivElement>(null);
  const deferredSave = useRef<{ row?: Resource } | undefined>(undefined);
  const focusAfterDeferredSave = useRef(false);
  const newScheduleButton = useRef<HTMLButtonElement>(null);
  const focusOpenerAfterCancel = useRef(false);
  const wasReviewing = useRef(false);
  const focusBeforeReview = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    if (reviewingNotification === wasReviewing.current) return;
    wasReviewing.current = reviewingNotification;
    if (reviewingNotification) {
      const focused = globalThis.document.activeElement;
      focusBeforeReview.current =
        focused instanceof HTMLElement && workspace.current?.contains(focused)
          ? focused
          : null;
      return;
    }
    const target = focusBeforeReview.current;
    focusBeforeReview.current = null;
    // A save accepted during the review unmounts the retained editor right
    // after this effect, so focusing it would be lost; the deferred save hands
    // focus to the resulting view instead.
    if (deferredSave.current) return;
    const current = globalThis.document.activeElement;
    if (
      target?.isConnected &&
      (!current || current === globalThis.document.body || !current.isConnected)
    )
      target.focus({ preventScroll: true });
  }, [reviewingNotification]);
  const request = useCallback(
    (token: string) => ({
      projectId,
      pageSize: 50,
      pageToken: token,
      ...(filter === EnabledFilter.All
        ? {}
        : { enabled: filter === EnabledFilter.Enabled }),
    }),
    [projectId, filter],
  );
  const reader = useConnectPaginationReader(
    ScheduleQuery.listSchedules,
    request,
    schedulePage,
  );
  const paneVisible = useSidebarPaneVisible();
  const navigationActive = active && paneVisible;
  const result = usePaginationChain(
    JSON.stringify([projectId, filter]),
    navigationActive && !locked,
    reader,
  );
  usePaginationRefresh(
    ScheduleQuery.listSchedules,
    request(""),
    navigationActive && !locked,
    result.refresh,
  );
  // An accepted save that arrives during a notification review must not clear
  // the retained editor or read the list; it applies when the review clears.
  const reviewingNow = useRef(reviewingNotification);
  reviewingNow.current = reviewingNotification;
  const applySave = (row?: Resource) => {
    setEditing(undefined);
    setProtectedWorkflow(false);
    if (row) setSelected(row);
    result.refresh();
  };
  const latestApplySave = useRef(applySave);
  latestApplySave.current = applySave;
  useEffect(() => {
    if (reviewingNotification || !deferredSave.current) return;
    const { row } = deferredSave.current;
    deferredSave.current = undefined;
    focusAfterDeferredSave.current = true;
    latestApplySave.current(row);
  }, [reviewingNotification]);
  // The applied save unmounts the focused retained editor, so move focus to the
  // first enabled control of the resulting view unless focus already moved.
  useLayoutEffect(() => {
    if (!focusAfterDeferredSave.current || editing) return;
    focusAfterDeferredSave.current = false;
    const current = globalThis.document.activeElement;
    if (current && current !== globalThis.document.body && current.isConnected)
      return;
    workspace.current
      ?.querySelector<HTMLElement>("button:not(:disabled)")
      ?.focus({ preventScroll: true });
  }, [editing, selected]);
  // Cancelling the creation wizard (button or Escape) unmounts the focused
  // control and re-enables the New schedule opener in the same commit. Return
  // focus to that opener, or to a visible Schedules control when the sidebar is
  // a closed compact drawer, unless focus already moved somewhere else.
  useLayoutEffect(() => {
    if (!focusOpenerAfterCancel.current || editing) return;
    focusOpenerAfterCancel.current = false;
    const current = globalThis.document.activeElement;
    if (
      current &&
      current !== globalThis.document.body &&
      current !== globalThis.document.documentElement &&
      current.isConnected
    )
      return;
    const opener = newScheduleButton.current;
    const main = workspace.current?.closest("main");
    const compact = (main ?? globalThis.document).querySelector<HTMLElement>(
      ".sidebar-context-trigger",
    );
    // Try each candidate in order; one that is unavailable, or that does not
    // actually take focus, falls through to the next candidate.
    for (const candidate of [opener, compact, main]) {
      if (!isFocusAvailable(candidate)) continue;
      candidate.focus({ preventScroll: true });
      if (globalThis.document.activeElement === candidate) return;
    }
  }, [editing]);
  const selectSchedule = (row: Resource) => {
    if (locked) return;
    navigationIntent(NavigationIntentSource.ScheduleSelect);
    if (row.id !== selected?.id)
      setEntries((current) => ({ ...current, count: current.count + 1 }));
    setSelected(row);
    closeDrawer();
  };
  const newSchedule = () => {
    if (locked) return;
    navigationIntent(NavigationIntentSource.ScheduleNew);
    setEntries((current) => ({ ...current, count: current.count + 1 }));
    setSelected(undefined);
    setEditing({ key: newRequestId() });
    closeDrawer();
  };
  return (
    <>
      <SidebarSurface
        active={active}
        title={copy("schedules.schedules_221ff1")}
        className="schedules-sidebar"
      >
        <button
          ref={newScheduleButton}
          className="primary sidebar-action"
          disabled={locked}
          onClick={newSchedule}
        >
          <SidebarIcon name="plus" />
          {copy("schedules.newSchedule_3bfe90")}
        </button>
        <div
          className="sidebar-filter-options"
          aria-label={copy("schedules.scheduleState_dd8c03")}
        >
          {(
            [
              [EnabledFilter.All, copy("schedules.extra.1c1c1bfaa5af")],
              [EnabledFilter.Enabled, copy("schedules.extra.92c1cdfdf4cb")],
              [EnabledFilter.Paused, copy("schedules.extra.e159b06187d3")],
            ] as const
          ).map(([value, label]) => (
            <button
              key={value}
              type="button"
              aria-pressed={filter === value}
              disabled={locked}
              onClick={() => {
                setFilter(value);
              }}
            >
              {label}
            </button>
          ))}
        </div>
        <ResourceChoice
          label={copy("schedules.filterByProject_9e2a0a")}
          emptyLabel={copy("schedules.allProjects_4b8727")}
          kind={EntityKind.PROJECT}
          value={projectId}
          change={(id) => {
            if (!locked) {
              setProjectId(id);
            }
          }}
          active={active}
          disabled={locked}
        />
        <header className="sidebar-list-heading">
          <h3>{copy("schedules.savedSchedules_97f381")}</h3>
          <button
            type="button"
            disabled={locked || Boolean(result.loading)}
            onClick={result.refreshExplicit}
          >
            <SidebarIcon name="refresh" />
            {copy("schedules.refresh_0e9161")}
          </button>
        </header>
        <Failure failure={result.error?.failure} />
        {!result.loaded && Boolean(result.loading) && active ? (
          <p role="status">{copy("schedules.loadingSchedules_77307d")}</p>
        ) : null}
        {result.error && result.loaded ? (
          <p className="sidebar-help">
            {copy("schedules.refreshFailedShowingThePreviousPage_24e457")}
          </p>
        ) : null}
        <div ref={content}>
          <ScrollPayloadWindow
            identity={paginationIdentity}
            revision={paginationRevision}
            query={result}
            root={root}
            active={active && !locked}
          >
            {(payload) =>
              payload.map((row) => (
                <article key={row.id} className="sidebar-schedule-row">
                  <button
                    type="button"
                    disabled={locked}
                    aria-current={selected?.id === row.id ? "true" : undefined}
                    onClick={(event) =>
                      dispatch(
                        event,
                        { surface: Surface.Schedules, item: row.id },
                        () => selectSchedule(row),
                      )
                    }
                  >
                    <span className="schedule-row-title">
                      {scheduleName(row)}
                    </span>
                    <span className="schedule-row-state">
                      {object(document(row).definition).enabled === true
                        ? copy("schedules.enabled_92c1cd")
                        : copy("schedules.paused_e159b0")}
                    </span>
                    <span className="schedule-row-next">
                      <LocalizedText
                        id="schedules.nextRunUtc_eca09c"
                        components={{
                          s0: (
                            <span>
                              {
                                <Timestamp
                                  mode={TimestampMode.Absolute}
                                  timeZone="UTC"
                                  value={text(document(row).next_run_at)}
                                  fallback={copy(
                                    "schedules.extra.170fa2a3d0f0",
                                  )}
                                />
                              }
                            </span>
                          ),
                        }}
                      />
                    </span>
                  </button>
                </article>
              ))
            }
          </ScrollPayloadWindow>
          {!result.error && result.loaded && result.rows.length === 0 ? (
            <p className="sidebar-help schedules-empty">
              <SidebarIcon name="schedules" />
              {copy("schedules.noSavedSchedules_3aa66d")}
            </p>
          ) : null}
          <ScrollContinuation
            query={result}
            root={root}
            active={active && !locked}
            label={copy("schedules.savedSchedules_97f381")}
          />
        </div>
      </SidebarSurface>
      <div hidden={!active} className="page schedule-page" {...pageEntrance}>
        {notificationOccurrence ? (
          <section
            key={notificationOccurrence.generation}
            aria-label={copy("inbox.operational.originalOccurrence")}
          >
            <h2>{copy("inbox.operational.originalOccurrence")}</h2>
            <button type="button" onClick={clearNotificationOccurrence}>
              {copy("schedules.backToSchedules_975251")}
            </button>
            <Problem error={originalNotificationOccurrence.error} />
            {!originalNotificationOccurrence.error &&
            originalNotificationOccurrence.data?.occurrence?.id ===
              notificationOccurrence.id &&
            originalNotificationOccurrence.data.occurrence.kind ===
              EntityKind.OCCURRENCE ? (
              <Occurrence
                resource={originalNotificationOccurrence.data.occurrence}
                open={open}
              />
            ) : (
              <p role="status">{copy("inbox.operational.unavailable")}</p>
            )}
          </section>
        ) : null}
        {/* The notification occurrence destination is exclusive: the normal
            workspace stays mounted (draft, focus and request identity) but is
            hidden, inert and inactive until the user explicitly returns. */}
        <div
          ref={workspace}
          hidden={Boolean(notificationOccurrence)}
          inert={Boolean(notificationOccurrence)}
          className="schedule-workspace"
        >
          {editing ? (
            <ScheduleEditor
              readLocalWorker={readLocalWorker}
              key={editing.key}
              initial={editing.initial}
              active={workspaceActive}
              saved={(row) => {
                if (reviewingNow.current) deferredSave.current = { row };
                else latestApplySave.current(row);
              }}
              cancel={() => {
                // Only dismissing the creation wizard (no initial resource)
                // returns focus to the opener; cancelling the edit of an
                // existing schedule keeps its previous focus behavior.
                const focused = globalThis.document.activeElement;
                focusOpenerAfterCancel.current =
                  editing.initial === undefined &&
                  (!focused ||
                    focused === globalThis.document.body ||
                    Boolean(workspace.current?.contains(focused)));
                setEditing(undefined);
                setProtectedWorkflow(false);
              }}
              protectedChange={setProtectedWorkflow}
            />
          ) : selected ? (
            <ScheduleDetails
              key={selected.id}
              initial={selected}
              active={workspaceActive}
              open={open}
              close={() => {
                navigationIntent(NavigationIntentSource.ScheduleClose);
                setSelected(undefined);
                setProtectedWorkflow(false);
                result.refresh();
              }}
              edit={(initial) => {
                navigationIntent(NavigationIntentSource.ScheduleEdit);
                setSelected(undefined);
                setEditing({ initial, key: newRequestId() });
              }}
              protectedChange={setProtectedWorkflow}
            />
          ) : (
            <section>
              <h2>{copy("schedules.schedules_221ff1")}</h2>
              <p>{copy("schedules.selectAScheduleFromTheSidebar_6800ae")}</p>
            </section>
          )}
        </div>
      </div>
    </>
  );
}
