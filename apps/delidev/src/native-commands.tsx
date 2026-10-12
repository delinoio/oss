// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  EntityKind,
  NativeShellAccess,
  ResourceQuery,
  SessionQuery,
  SystemCapability,
  SystemQuery,
  isEntityId,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
import { copy, useLocale } from "./localization";
import {
  useRetainedMutation,
  useRetainedMutationPresentation,
} from "./mutation";
import { Modal, Problem } from "./ui";
import "./native-commands.css";

export enum NativeCommandState {
  Queued = "queued",
  Claimed = "claimed",
  Running = "running",
  Exited = "exited",
  Canceled = "canceled",
  Uncertain = "uncertain",
}
const states = Object.values(NativeCommandState);
const maxOutput = 262144;
const maxCommand = 65536;
const maxDocument = 1048576;
export function nativeCommandRecord(
  row: Resource | undefined,
  session: string,
  action: string,
): Document | undefined {
  if (
    !row ||
    row.kind !== EntityKind.NATIVE_ACTION ||
    row.schemaVersion !== 1 ||
    row.revision <= 0n ||
    row.sessionId !== session ||
    row.id !== action ||
    !isEntityId(session) ||
    !isEntityId(action) ||
    row.documentJson.byteLength > maxDocument
  )
    return;
  const value = document(row),
    command = object(value.command),
    observation = object(value.observation),
    claim = object(value.claim);
  if (
    value.version !== 1 ||
    value.session_id !== session ||
    value.action_id !== action ||
    !isEntityId(text(value.execution_id)) ||
    !isEntityId(text(value.job_id)) ||
    !states.includes(value.state as NativeCommandState) ||
    command.full_access !== true ||
    typeof command.command !== "string" ||
    new TextEncoder().encode(command.command).byteLength > maxCommand ||
    !command.command.trim() ||
    typeof value.disclosure !== "string" ||
    (command.timeout_ms !== undefined &&
      (!Number.isInteger(command.timeout_ms) ||
        Number(command.timeout_ms) < 0 ||
        Number(command.timeout_ms) > 3600000)) ||
    (value.claim !== undefined &&
      ![
        claim.request_id,
        claim.machine_id,
        claim.instance_id,
        claim.device_id,
      ].every((id) => isEntityId(text(id)))) ||
    (value.state === NativeCommandState.Queued &&
      (value.claim !== undefined || value.observation !== undefined)) ||
    (value.state === NativeCommandState.Canceled &&
      (value.claim === undefined) !== (value.observation === undefined)) ||
    ([
      NativeCommandState.Claimed,
      NativeCommandState.Running,
      NativeCommandState.Exited,
    ].includes(value.state as NativeCommandState) &&
      (value.claim === undefined || value.observation === undefined))
  )
    return;
  if (
    value.observation !== undefined &&
    (observation.action_id !== action ||
      observation.session_id !== session ||
      observation.execution_id !== value.execution_id ||
      observation.state !== value.state ||
      !/^[1-9][0-9]{0,19}$/.test(text(observation.sequence)) ||
      BigInt(text(observation.sequence)) > 18446744073709551615n ||
      typeof observation.output !== "string" ||
      new TextEncoder().encode(observation.output).byteLength > maxOutput ||
      (observation.aggregated_output !== undefined &&
        (typeof observation.aggregated_output !== "string" ||
          new TextEncoder().encode(observation.aggregated_output).byteLength >
            maxOutput)) ||
      (observation.exit_code !== undefined &&
        (!Number.isInteger(observation.exit_code) ||
          Number(observation.exit_code) < -2147483648 ||
          Number(observation.exit_code) > 2147483647)) ||
      typeof observation.cleanup_verified !== "boolean")
  )
    return;
  return value;
}
export function nativeCommandEligible(
  row: Resource,
  supported: boolean,
): boolean {
  const value = document(row),
    execution = object(value.execution);
  return (
    supported &&
    row.kind === EntityKind.SESSION &&
    row.schemaVersion === 1 &&
    row.revision > 0n &&
    object(object(value.initial_execution).configuration).harness === "codex" &&
    !object(value.fork).sidechat_parent_snapshot &&
    value.archive === "active" &&
    value.recovery === "none" &&
    value.dispatch === "ready" &&
    value.outcome === "succeeded" &&
    !value.active_execution_id &&
    !value.compaction_job_id &&
    !value.pending_steer_id &&
    !value.pending_inputs &&
    !value.pending_input_bytes &&
    execution.cleanup_verified === true &&
    !execution.unconfirmed_responses &&
    Object.values(object(execution.native_compactions)).every(
      (stage) => stage === "completed",
    ) &&
    Object.values(object(execution.auto_reviews)).every(
      (review) => object(review).status !== "inProgress",
    ) &&
    !Object.values(object(execution.waiting)).some(Boolean) &&
    !Object.keys(object(execution.subagents)).length
  );
}
export function commandTimeout(value: string): bigint | undefined | null {
  if (!value.trim()) return undefined;
  if (!/^(0|[1-9][0-9]{0,3})$/.test(value) || BigInt(value) > 3600n)
    return null;
  return BigInt(value) * 1000n;
}
interface CommandPresentation {
  action?: string;
  pending?: {
    action: string;
    command: string;
    timeout?: bigint;
    revision: bigint;
  };
}
const terminal = (value: Document | undefined) =>
  value &&
  ((value.state === NativeCommandState.Canceled && value.claim === undefined) ||
    ([NativeCommandState.Exited, NativeCommandState.Canceled].includes(
      value.state as NativeCommandState,
    ) &&
      object(value.observation).cleanup_verified === true));

export function NativeCommands({
  session,
  active,
  close,
}: {
  session: Resource;
  active: boolean;
  close: () => void;
}) {
  useLocale();
  const formId = useId();
  const commandInput = useRef<HTMLTextAreaElement>(null);
  const confirmButton = useRef<HTMLButtonElement>(null);
  const [command, setCommand] = useState("");
  const [timeout, setTimeout] = useState("");
  const [confirmation, setConfirmation] = useState<{
    command: string;
    timeout?: bigint;
    revision: bigint;
    action: string;
  }>();
  const key = `native-command:${session.id}`;
  const presentation =
    useRetainedMutationPresentation<CommandPresentation>(key);
  const save = (value: CommandPresentation) =>
    presentation.save(value, 128 << 10);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const machineId = text(document(session).machine_id);
  const machine = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.MACHINE, id: machineId },
    { enabled: active && Boolean(machineId) },
  );
  const machineResource = machine.data?.resource;
  const supported =
    machineResource?.id === machineId &&
    machineResource.kind === EntityKind.MACHINE &&
    machineResource.schemaVersion === 1 &&
    machineResource.revision > 0n &&
    !status.error &&
    !machine.error &&
    status.data?.capabilities.includes(
      SystemCapability.CODEX_NATIVE_SHELL_V1,
    ) === true &&
    Array.isArray(document(machine.data?.resource).worker_capabilities) &&
    (
      document(machine.data?.resource).worker_capabilities as unknown[]
    ).includes("codex-native-shell-v1");
  const lastJob = text(document(session).last_native_shell_job_id);
  const job = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.JOB, id: lastJob },
    { enabled: active && Boolean(lastJob) },
  );
  const jobResource = job.data?.resource;
  const restoredAction =
    jobResource?.id === lastJob &&
    jobResource.kind === EntityKind.JOB &&
    jobResource.revision > 0n &&
    jobResource.schemaVersion === 1 &&
    jobResource.sessionId === session.id &&
    document(jobResource).type === "compact-session" &&
    object(document(jobResource).input).version === 5 &&
    typeof object(object(document(jobResource).input).native_shell).command ===
      "string"
      ? text(object(document(jobResource).input).action_id)
      : "";
  const actionId =
    presentation.value?.pending?.action ??
    presentation.value?.action ??
    restoredAction;
  const action = useQuery(
    SessionQuery.getNativeShell,
    { sessionId: session.id, actionId: actionId || "" },
    {
      enabled: active && isEntityId(actionId || ""),
      refetchInterval: (query) =>
        terminal(
          nativeCommandRecord(
            query.state.data?.action,
            session.id,
            actionId || "",
          ),
        )
          ? false
          : 1000,
    },
  );
  const row = action.data?.action;
  const observed = !action.error
    ? nativeCommandRecord(row, session.id, actionId || "")
    : undefined;
  const pending = presentation.value?.pending;
  const runKey = `native-command-run:${session.id}:${confirmation?.action ?? pending?.action ?? actionId ?? ""}`;
  const run = useRetainedMutation(
    runKey,
    SessionQuery.runNativeShell,
    (result, request) => {
      save({ action: request.mutation!.requestId });
      setCommand("");
      setTimeout("");
      void action.refetch();
    },
    (result, request) => {
      const value = nativeCommandRecord(
        result.action,
        request.mutation?.id ?? "",
        request.mutation?.requestId ?? "",
      );
      const original = object(value?.command);
      return Boolean(
        value &&
        result.requestId === request.mutation?.requestId &&
        original.command === request.command &&
        original.full_access === true &&
        (request.timeoutMs === undefined
          ? original.timeout_ms === undefined
          : original.timeout_ms === Number(request.timeoutMs)),
      );
    },
    false,
    () => save({ action: presentation.read()?.action }),
  );
  const cancel = useRetainedMutation(
    `native-command-cancel:${session.id}:${actionId || ""}`,
    SessionQuery.cancelNativeShell,
    () => {
      void action.refetch();
    },
    (result, request) =>
      result.requestId === request.mutation?.requestId &&
      Boolean(
        nativeCommandRecord(
          result.action,
          request.sessionId,
          request.mutation?.id ?? "",
        ),
      ),
  );
  const eligible = nativeCommandEligible(session, supported);
  const blocked =
    run.busy ||
    Boolean(pending) ||
    Boolean(actionId && (!observed || !terminal(observed)));
  const parsedTimeout = commandTimeout(timeout);
  const validCommand =
    command.trim().length > 0 &&
    new TextEncoder().encode(command).byteLength <= maxCommand;
  useEffect(() => {
    if (active) commandInput.current?.focus();
    else setConfirmation(undefined);
  }, [active]);
  // Read-only recovery pins the original accepted action. The old mutation
  // remains immutable in its own registry entry; a later command gets a fresh
  // key only after this action independently proves terminal cleanup.
  useEffect(() => {
    if (!pending || !observed || pending.action !== actionId) return;
    const original = object(observed.command);
    if (
      original.command === pending.command &&
      (pending.timeout === undefined
        ? original.timeout_ms === undefined
        : original.timeout_ms === Number(pending.timeout))
    )
      save({ action: pending.action });
  }, [pending, observed, actionId]);
  const observation = object(observed?.observation);
  const resolved = Boolean(terminal(observed));
  const state = observed?.state as NativeCommandState | undefined;
  const stateLabel = state
    ? copy(`native-commands.state.${state}`)
    : copy("native-commands.awaitingStatus");
  const canCancel =
    row &&
    observed &&
    !terminal(observed) &&
    state !== NativeCommandState.Uncertain &&
    !cancel.busy &&
    !cancel.uncertain;
  return (
    <section className="native-commands">
      <header>
        <h2>{copy("native-commands.title")}</h2>
        <button type="button" onClick={close}>
          {copy("native-commands.close")}
        </button>
      </header>
      <p className="native-command-disclosure">
        {copy("native-commands.disclosure")}
      </p>
      {!eligible ? (
        <p>
          {copy(
            supported
              ? "native-commands.idleOnly"
              : "native-commands.unsupported",
          )}
        </p>
      ) : null}
      <Problem
        error={
          status.error ||
          machine.error ||
          job.error ||
          (!observed && run.error) ||
          (!resolved && cancel.error) ||
          action.error
        }
      />
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (!eligible || blocked || !validCommand || parsedTimeout === null)
            return;
          setConfirmation({
            command,
            timeout: parsedTimeout,
            revision: session.revision,
            action: newRequestId(),
          });
        }}
      >
        <label htmlFor={`${formId}-command`}>
          {copy("native-commands.command")}
        </label>
        <textarea
          ref={commandInput}
          id={`${formId}-command`}
          value={command}
          onChange={(event) => setCommand(event.target.value)}
          disabled={blocked}
          rows={4}
        />
        <label htmlFor={`${formId}-timeout`}>
          {copy("native-commands.timeout")}
        </label>
        <input
          id={`${formId}-timeout`}
          type="number"
          min={0}
          max={3600}
          step={1}
          value={timeout}
          onChange={(event) => setTimeout(event.target.value)}
          disabled={blocked}
          aria-describedby={`${formId}-timeout-help`}
        />
        <small id={`${formId}-timeout-help`}>
          {copy("native-commands.timeoutHelp")}
        </small>
        {parsedTimeout === null ? (
          <p role="alert">{copy("native-commands.invalidTimeout")}</p>
        ) : null}
        <div className="actions">
          <button
            type="submit"
            disabled={
              !eligible || blocked || !validCommand || parsedTimeout === null
            }
          >
            {copy("native-commands.run")}
          </button>
        </div>
      </form>
      {actionId ? (
        <section className="native-command-operation">
          <h3>{copy("native-commands.originalCommand")}</h3>
          <p role="status">
            {stateLabel}
            {observed &&
            !terminal(observed) &&
            [NativeCommandState.Exited, NativeCommandState.Canceled].includes(
              state!,
            )
              ? ` · ${copy("native-commands.cleanupPending")}`
              : ""}
          </p>
          <pre>
            {text(object(observed?.command).command) || pending?.command}
          </pre>
          <div className="actions">
            <button
              type="button"
              disabled={action.isFetching}
              onClick={() => {
                void action.refetch();
              }}
            >
              {copy("native-commands.readStatus")}
            </button>
            <button
              type="button"
              disabled={!canCancel}
              onClick={() => {
                if (!row || !canCancel) return;
                void cancel.send({
                  mutation: {
                    id: row.id,
                    requestId: newRequestId(),
                    expectedRevision: row.revision,
                  },
                  sessionId: session.id,
                });
              }}
            >
              {copy("native-commands.cancelCommand")}
            </button>
          </div>
          {(!observed && run.uncertain) ||
          (!resolved && cancel.uncertain) ||
          state === NativeCommandState.Uncertain ? (
            <p role="status">{copy("native-commands.uncertain")}</p>
          ) : null}
          {observed && observation.exit_code !== undefined ? (
            <p>
              {copy("native-commands.exitCode", {
                code: observation.exit_code,
              })}
            </p>
          ) : null}
          {observation.output || observation.aggregated_output ? (
            <>
              <h4>{copy("native-commands.output")}</h4>
              <pre className="native-command-output">
                {text(observation.aggregated_output) ||
                  text(observation.output)}
              </pre>
            </>
          ) : null}
        </section>
      ) : null}
      {confirmation && active ? (
        <Modal
          title={copy("native-commands.confirmTitle")}
          close={() => setConfirmation(undefined)}
          initialFocus={confirmButton}
        >
          <p>{copy("native-commands.disclosure")}</p>
          <pre>{confirmation.command}</pre>
          <div className="actions">
            <button type="button" onClick={() => setConfirmation(undefined)}>
              {copy("native-commands.cancel")}
            </button>
            <button
              ref={confirmButton}
              type="button"
              disabled={
                !eligible ||
                blocked ||
                confirmation.revision !== session.revision
              }
              onClick={() => {
                if (
                  !eligible ||
                  blocked ||
                  confirmation.revision !== session.revision
                )
                  return;
                const original = { ...confirmation };
                save({ pending: original });
                setConfirmation(undefined);
                void run.send({
                  mutation: {
                    id: session.id,
                    requestId: original.action,
                    expectedRevision: original.revision,
                  },
                  command: original.command,
                  access: NativeShellAccess.FULL_ACCESS,
                  timeoutMs: original.timeout,
                });
              }}
            >
              {copy("native-commands.run")}
            </button>
          </div>
        </Modal>
      ) : null}
    </section>
  );
}
