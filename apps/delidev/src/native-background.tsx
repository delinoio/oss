// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  EntityKind,
  NativeBackgroundOperation,
  SessionQuery,
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

const bytes = (value: string) => new TextEncoder().encode(value).byteLength;
const uint64 = (value: unknown) =>
  typeof value === "string" &&
  /^[1-9][0-9]{0,19}$/.test(value) &&
  BigInt(value) <= 18446744073709551615n;
const bounded = (value: unknown, limit: number, required = false) =>
  typeof value === "string" &&
  bytes(value) <= limit &&
  !value.includes("\0") &&
  (!required || value.length > 0);
const nativeProcess = (value: unknown) =>
  typeof value === "string" &&
  /^-?(0|[1-9][0-9]*)$/.test(value) &&
  String(Number(value)) === value &&
  Number(value) >= -2147483648 &&
  Number(value) <= 2147483647;
export interface BackgroundTerminal {
  item_id: string;
  process_id: string;
  command: string;
  cwd: string;
  output: string;
  output_truncated: boolean;
  exit_code?: number;
  present: boolean;
}
export interface BackgroundInventory {
  version: 1;
  job_id: string;
  execution_id: string;
  native_thread_id: string;
  sequence: string;
  terminals: BackgroundTerminal[];
  owner_lost: boolean;
  native_empty: boolean;
  actions: number;
  pending_action_id?: string;
}
export function nativeBackgroundInventory(
  row: Resource | undefined,
  session: string,
): BackgroundInventory | undefined {
  if (
    !row ||
    row.id !== session ||
    !isEntityId(session) ||
    row.kind !== EntityKind.SESSION ||
    row.schemaVersion !== 1 ||
    row.revision <= 0n ||
    row.documentJson.byteLength > 4 << 20
  )
    return;
  const value = object(document(row).native_background);
  if (
    value.version !== 1 ||
    !isEntityId(text(value.job_id)) ||
    !isEntityId(text(value.execution_id)) ||
    value.job_id === value.execution_id ||
    !isEntityId(text(value.native_thread_id)) ||
    !uint64(value.sequence) ||
    !Array.isArray(value.terminals) ||
    value.terminals.length > 16 ||
    typeof value.owner_lost !== "boolean" ||
    typeof value.native_empty !== "boolean" ||
    !Number.isInteger(value.actions) ||
    Number(value.actions) < 0 ||
    Number(value.actions) > 128 ||
    (value.pending_action_id !== undefined &&
      !isEntityId(text(value.pending_action_id))) ||
    bytes(JSON.stringify(value)) > 512 << 10
  )
    return;
  const items = new Set<string>(),
    processes = new Set<string>();
  for (const raw of value.terminals) {
    const terminal = object(raw);
    if (
      !bounded(terminal.item_id, 1024, true) ||
      !nativeProcess(terminal.process_id) ||
      !bounded(terminal.command, 8192, true) ||
      !bounded(terminal.cwd, 4096, true) ||
      !bounded(terminal.output, 16384) ||
      typeof terminal.output_truncated !== "boolean" ||
      typeof terminal.present !== "boolean" ||
      (terminal.exit_code !== undefined &&
        (!Number.isInteger(terminal.exit_code) ||
          Number(terminal.exit_code) < -2147483648 ||
          Number(terminal.exit_code) > 2147483647)) ||
      items.has(text(terminal.item_id)) ||
      processes.has(text(terminal.process_id))
    )
      return;
    items.add(text(terminal.item_id));
    processes.add(text(terminal.process_id));
  }
  if (
    value.native_empty !==
    !value.terminals.some((raw) => object(raw).present === true)
  )
    return;
  return value as unknown as BackgroundInventory;
}
export function nativeBackgroundAction(
  row: Resource | undefined,
  session: string,
  action: string,
): Document | undefined {
  if (
    !row ||
    row.id !== action ||
    row.sessionId !== session ||
    row.kind !== EntityKind.NATIVE_ACTION ||
    row.schemaVersion !== 1 ||
    row.revision <= 0n ||
    !isEntityId(action) ||
    row.documentJson.byteLength > 128 << 10
  )
    return;
  const value = document(row),
    observation = object(value.observation),
    claim = object(value.claim);
  if (
    value.version !== 1 ||
    value.action_type !== "native-background" ||
    value.session_id !== session ||
    value.action_id !== action ||
    !isEntityId(text(value.job_id)) ||
    !isEntityId(text(value.execution_id)) ||
    !isEntityId(text(value.native_thread_id)) ||
    !uint64(value.inventory_sequence) ||
    !["list", "terminate", "clean"].includes(text(value.operation)) ||
    ![
      "queued",
      "claimed",
      "observed",
      "uncertain",
      "rejected",
      "canceled",
    ].includes(text(value.state))
  )
    return;
  const ids = [
    value.action_id,
    value.session_id,
    value.job_id,
    value.execution_id,
  ];
  if (
    new Set(ids).size !== ids.length ||
    !Array.isArray(value.targets) ||
    value.targets.length > 16
  )
    return;
  const processes = new Set<string>();
  for (const raw of value.targets) {
    const target = object(raw);
    if (
      !bounded(target.item_id, 1024, true) ||
      !nativeProcess(target.process_id) ||
      !bounded(target.command, 8192, true) ||
      !bounded(target.cwd, 4096, true) ||
      target.output !== "" ||
      target.output_truncated !== false ||
      target.present !== true ||
      target.exit_code !== undefined ||
      processes.has(text(target.process_id))
    )
      return;
    processes.add(text(target.process_id));
  }
  if (
    value.operation === "terminate"
      ? value.targets.length !== 1 ||
        object(value.targets[0]).item_id !== value.item_id ||
        object(value.targets[0]).process_id !== value.process_id
      : value.item_id !== undefined ||
        value.process_id !== undefined ||
        (value.operation === "list"
          ? value.targets.length !== 0
          : value.targets.length === 0)
  )
    return;
  if (value.state === "claimed" && value.observation !== undefined) return;
  if (
    value.claim !== undefined &&
    ![
      claim.request_id,
      claim.machine_id,
      claim.instance_id,
      claim.device_id,
    ].every((id) => isEntityId(text(id)))
  )
    return;
  if (value.state === "queued" || value.state === "canceled") {
    if (value.claim !== undefined || value.observation !== undefined) return;
  } else if (value.claim === undefined) return;
  if (["observed", "uncertain", "rejected"].includes(text(value.state))) {
    if (
      observation.action_id !== action ||
      typeof observation.acknowledged !== "boolean" ||
      typeof observation.native_empty !== "boolean" ||
      !["not-sent", "uncertain", "acknowledged"].includes(
        text(observation.delivery),
      ) ||
      observation.acknowledged !== (observation.delivery === "acknowledged") ||
      (observation.terminated !== undefined &&
        typeof observation.terminated !== "boolean")
    )
      return;
    if (
      (value.state === "rejected" &&
        (observation.delivery !== "not-sent" ||
          !text(observation.problem_code))) ||
      (value.state === "uncertain" && !text(observation.problem_code)) ||
      (observation.terminated !== undefined &&
        (value.operation !== "terminate" || !observation.acknowledged))
    )
      return;
    if (
      value.state === "observed" &&
      (observation.acknowledged !== true ||
        observation.problem_code !== undefined)
    )
      return;
  }
  return value;
}
export function nativeBackgroundEligible(
  row: Resource | undefined,
  inventory: BackgroundInventory | undefined,
  supported: boolean,
): boolean {
  if (
    !row ||
    !inventory ||
    !supported ||
    inventory.owner_lost ||
    inventory.pending_action_id ||
    inventory.actions >= 128
  )
    return false;
  const value = document(row),
    execution = object(value.execution);
  return (
    value.archive === "active" &&
    value.active_execution_id === inventory.execution_id &&
    execution.execution_id === inventory.execution_id &&
    execution.job_id === inventory.job_id &&
    execution.native_thread_id === inventory.native_thread_id &&
    execution.cleanup_verified === false &&
    !value.pending_steer_id &&
    !value.compaction_job_id &&
    !object(value.fork).sidechat_parent_snapshot
  );
}
const stateKeys = {
  queued: "native-commands.backgroundState.queued",
  claimed: "native-commands.backgroundState.claimed",
  observed: "native-commands.backgroundState.observed",
  uncertain: "native-commands.backgroundState.uncertain",
  rejected: "native-commands.backgroundState.rejected",
  canceled: "native-commands.backgroundState.canceled",
} as const;
interface BackgroundIntent {
  action: string;
  operation: NativeBackgroundOperation;
  execution: string;
  sequence: bigint;
  revision: bigint;
  item?: string;
  process?: string;
}
interface BackgroundPresentation {
  action?: string;
  pending?: BackgroundIntent;
}
export function NativeBackground({
  session,
  active,
  supported,
}: {
  session: Resource;
  active: boolean;
  supported: boolean;
}) {
  useLocale();
  const [confirmation, setConfirmation] = useState<BackgroundIntent>();
  const [inspected, setInspected] = useState<string>();
  const confirmButton = useRef<HTMLButtonElement>(null);
  const presentation = useRetainedMutationPresentation<BackgroundPresentation>(
    `native-background:${session.id}`,
  );
  const retained = nativeBackgroundInventory(session, session.id);
  const actionId =
    presentation.value?.pending?.action ??
    presentation.value?.action ??
    retained?.pending_action_id;
  const read = useQuery(
    SessionQuery.getNativeBackground,
    { sessionId: session.id, actionId },
    { enabled: active, refetchInterval: active ? 2000 : false },
  );
  const original = read.error ? undefined : read.data?.session;
  const inventory = nativeBackgroundInventory(original, session.id);
  const receipt = nativeBackgroundAction(
    read.data?.action,
    session.id,
    actionId ?? "",
  );
  const send = useRetainedMutation(
    `native-background-control:${session.id}`,
    SessionQuery.requestNativeBackground,
    (_result, request) => {
      presentation.save({ action: request.mutation!.requestId }, 16384);
      void read.refetch();
    },
    (result, request) => {
      const action = nativeBackgroundAction(
        result.action,
        request.mutation!.id,
        request.mutation!.requestId,
      );
      return Boolean(
        action &&
        result.requestId === request.mutation!.requestId &&
        action.execution_id === request.executionId &&
        action.inventory_sequence === String(request.inventorySequence) &&
        action.operation ===
          (
            {
              [NativeBackgroundOperation.LIST]: "list",
              [NativeBackgroundOperation.TERMINATE]: "terminate",
              [NativeBackgroundOperation.CLEAN]: "clean",
            } as Record<number, string>
          )[request.operation] &&
        (action.item_id ?? "") === request.itemId &&
        (action.process_id ?? "") === request.processId,
      );
    },
    false,
    () => presentation.save({ action: presentation.read()?.action }, 16384),
  );
  useEffect(() => {
    if (!active) {
      setConfirmation(undefined);
      setInspected(undefined);
    }
  }, [active]);
  useEffect(() => {
    if (presentation.value?.pending && receipt)
      presentation.save({ action: actionId }, 16384);
  }, [receipt, actionId, presentation.value?.pending]);
  const uncertain =
    receipt?.state === "uncertain" || (send.uncertain && !receipt);
  const eligible =
    active &&
    nativeBackgroundEligible(original, inventory, supported) &&
    !send.busy &&
    !uncertain &&
    !presentation.value?.pending;
  const intent = (
    operation: NativeBackgroundOperation,
    terminal?: BackgroundTerminal,
  ): BackgroundIntent | undefined =>
    original && inventory && eligible
      ? {
          action: newRequestId(),
          operation,
          execution: inventory.execution_id,
          sequence: BigInt(inventory.sequence),
          revision: original.revision,
          item: terminal?.item_id,
          process: terminal?.process_id,
        }
      : undefined;
  const submit = (value: BackgroundIntent) => {
    if (
      !eligible ||
      !original ||
      !inventory ||
      value.revision !== original.revision ||
      value.execution !== inventory.execution_id ||
      value.sequence !== BigInt(inventory.sequence)
    )
      return;
    presentation.save({ pending: value }, 16384);
    setConfirmation(undefined);
    void send.send({
      mutation: {
        id: session.id,
        requestId: value.action,
        expectedRevision: value.revision,
      },
      executionId: value.execution,
      inventorySequence: value.sequence,
      operation: value.operation,
      itemId: value.item ?? "",
      processId: value.process ?? "",
    });
  };
  return (
    <section className="native-command-operation native-background">
      <h3>{copy("native-commands.backgroundTitle")}</h3>
      <p>{copy("native-commands.backgroundScope")}</p>
      <Problem error={read.error || send.error} />
      <div className="actions">
        <button
          type="button"
          disabled={!eligible}
          onClick={() => {
            const value = intent(NativeBackgroundOperation.LIST);
            if (value) submit(value);
          }}
        >
          {copy("native-commands.backgroundRefresh")}
        </button>
        <button
          type="button"
          disabled={!eligible || inventory?.native_empty !== false}
          onClick={() =>
            setConfirmation(intent(NativeBackgroundOperation.CLEAN))
          }
        >
          {copy("native-commands.backgroundStopAll")}
        </button>
        <button
          type="button"
          disabled={read.isFetching}
          onClick={() => {
            void read.refetch();
          }}
        >
          {copy("native-commands.readStatus")}
        </button>
      </div>
      {!inventory ? (
        <p role="status">{copy("native-commands.backgroundUnavailable")}</p>
      ) : inventory.owner_lost ? (
        <p role="status">{copy("native-commands.backgroundOwnerLost")}</p>
      ) : null}
      {uncertain ? (
        <p role="status">{copy("native-commands.backgroundUncertain")}</p>
      ) : null}
      {inventory?.pending_action_id ? (
        <p role="status">{copy("native-commands.backgroundPending")}</p>
      ) : null}
      {receipt ? (
        <p role="status">
          {copy(stateKeys[text(receipt.state) as keyof typeof stateKeys])}
          {object(receipt.observation).acknowledged
            ? ` · ${copy("native-commands.backgroundAcknowledged")}`
            : ""}
        </p>
      ) : null}
      {inventory?.native_empty ? (
        <p>{copy("native-commands.backgroundEmpty")}</p>
      ) : null}
      {inventory?.terminals.map((terminal) => (
        <article
          key={`${inventory.execution_id}:${terminal.item_id}`}
          className="native-background-row"
        >
          <pre>{terminal.command}</pre>
          <small>{terminal.cwd}</small>
          <p>
            {copy(
              terminal.present
                ? "native-commands.backgroundRunning"
                : "native-commands.backgroundAbsent",
            )}
            {terminal.exit_code === undefined
              ? ""
              : ` · ${copy("native-commands.exitCode", { code: terminal.exit_code })}`}
          </p>
          <div className="actions">
            <button
              type="button"
              onClick={() =>
                setInspected(
                  inspected === terminal.item_id ? undefined : terminal.item_id,
                )
              }
            >
              {copy("native-commands.backgroundInspect")}
            </button>
            <button
              type="button"
              disabled={!eligible || !terminal.present}
              onClick={() => {
                const value = intent(
                  NativeBackgroundOperation.TERMINATE,
                  terminal,
                );
                if (value) submit(value);
              }}
            >
              {copy("native-commands.backgroundStop")}
            </button>
          </div>
          {inspected === terminal.item_id ? (
            <>
              <pre className="native-command-output">
                {terminal.output || copy("native-commands.backgroundNoOutput")}
              </pre>
              {terminal.output_truncated ? (
                <p>{copy("native-commands.backgroundTruncated")}</p>
              ) : null}
            </>
          ) : null}
        </article>
      ))}
      {confirmation && active ? (
        <Modal
          title={copy("native-commands.backgroundConfirm")}
          close={() => setConfirmation(undefined)}
          initialFocus={confirmButton}
        >
          <p>{copy("native-commands.backgroundConfirmScope")}</p>
          <ul>
            {inventory?.terminals
              .filter((terminal) => terminal.present)
              .map((terminal) => (
                <li key={terminal.item_id}>{terminal.command}</li>
              ))}
          </ul>
          <div className="actions">
            <button type="button" onClick={() => setConfirmation(undefined)}>
              {copy("native-commands.cancel")}
            </button>
            <button
              ref={confirmButton}
              type="button"
              disabled={
                !eligible ||
                confirmation.revision !== original?.revision ||
                confirmation.sequence !== BigInt(inventory?.sequence ?? "0")
              }
              onClick={() => submit(confirmation)}
            >
              {copy("native-commands.backgroundStopAll")}
            </button>
          </div>
        </Modal>
      ) : null}
    </section>
  );
}
