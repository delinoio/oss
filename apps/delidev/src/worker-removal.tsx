// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  DeviceQuery,
  EntityKind,
  FailureCode,
  ResourceQuery,
  SystemCapability,
  clientFailure,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { document } from "./documents";
import { copy, useLocale } from "./localization";
import {
  observeLocalWorkerMachine,
  type ReadLocalWorkerProof,
} from "./local-worker";
import { useRetainedMutation } from "./mutation";
import {
  SettingsActionButton,
  SettingsActionIcon,
  SettingsActionTone,
} from "./settings-action";
import { Modal, Problem } from "./ui";
import "./worker-removal.css";

/** Retained-mutation key prefix; one original request per saved machine. */
export const workerRemovalPrefix = "remove-worker:";

/** Server support for the dedicated RemoveWorker operation. */
export enum WorkerRemovalSupport {
  Pending = "pending",
  Unknown = "unknown",
  Unsupported = "unsupported",
  Supported = "supported",
}
export function workerRemovalSupport(
  status: { capabilities: SystemCapability[] } | undefined,
  failed: boolean,
): WorkerRemovalSupport {
  if (status)
    return status.capabilities.includes(SystemCapability.WORKER_REMOVAL_V1)
      ? WorkerRemovalSupport.Supported
      : WorkerRemovalSupport.Unsupported;
  return failed ? WorkerRemovalSupport.Unknown : WorkerRemovalSupport.Pending;
}

enum Verification {
  Idle = "idle",
  Checking = "checking",
  Protected = "protected",
  Unverified = "unverified",
}

/**
 * Confirmation for deleting one saved Worker. Fresh confirmation re-reads the
 * trusted native local-Worker identity and sends one revision-bound request;
 * an uncertain result only replays the retained original request.
 */
export function WorkerRemovalDialog({
  machine,
  name,
  readLocalWorker,
  active,
  close,
  removed,
}: {
  machine: Resource;
  name: string;
  readLocalWorker?: ReadLocalWorkerProof;
  active: boolean;
  close: () => void;
  removed: (machine: Resource) => void;
}) {
  useLocale();
  const cancel = useRef<HTMLButtonElement>(null);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const [target, setTarget] = useState(machine);
  const [verification, setVerification] = useState(Verification.Idle);
  const current = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.MACHINE, id: machine.id },
    { enabled: active, refetchInterval: active ? 5000 : false, retry: false },
  );
  const mutation = useRetainedMutation(
    `${workerRemovalPrefix}${machine.id}`,
    DeviceQuery.removeWorker,
    (result) => removed(result.machine ?? target),
    (result, request) =>
      result.requestId === request.mutation?.requestId &&
      result.machine?.id === request.mutation?.id &&
      document(result.machine).retired === true,
  );
  const latest = current.data?.resource;
  const alreadyRemoved = document(latest).retired === true;
  const stale = Boolean(latest && latest.revision !== target.revision);
  const failure = mutation.error ? clientFailure(mutation.error) : undefined;
  const conflict =
    failure?.code === FailureCode.Conflict && !mutation.uncertain;
  const checking = verification === Verification.Checking;
  const pending = mutation.busy || checking;
  const unavailable =
    !latest ||
    Boolean(current.error) ||
    target.schemaVersion !== 1 ||
    alreadyRemoved;
  const confirm = async () => {
    if (pending || mutation.uncertain || stale || unavailable || conflict)
      return;
    setVerification(Verification.Checking);
    let next = Verification.Idle;
    try {
      // The identity is observed afresh for every new confirmation, never cached.
      const local = await observeLocalWorkerMachine(readLocalWorker);
      if (local === target.id) next = Verification.Protected;
    } catch {
      next = Verification.Unverified;
    }
    if (!alive.current) return;
    setVerification(next);
    if (next !== Verification.Idle) return;
    void mutation.send({
      mutation: {
        id: target.id,
        expectedRevision: target.revision,
        requestId: newRequestId(),
      },
      schemaVersion: 1,
    });
  };
  const review = () => {
    if (!latest || pending) return;
    mutation.clearRejected();
    setVerification(Verification.Idle);
    setTarget(latest);
  };
  // Closing never cancels or resubmits an accepted request.
  const dismiss = () => {
    if (!mutation.busy) close();
  };
  return (
    <Modal
      title={copy("worker-removal.title")}
      close={dismiss}
      visible={active}
      initialFocus={cancel}
      trapFocus
      className="worker-removal-dialog"
    >
      <p className="worker-removal-target">{name}</p>
      <p>{copy("worker-removal.consequence")}</p>
      <p>{copy("worker-removal.retained")}</p>
      {target.schemaVersion !== 1 ? (
        <p role="alert">{copy("worker-removal.schemaUnsupported")}</p>
      ) : null}
      {alreadyRemoved ? (
        <p role="alert">{copy("worker-removal.alreadyRemoved")}</p>
      ) : null}
      {(stale || conflict) && !alreadyRemoved ? (
        <div role="alert" className="problem">
          <p>{copy("worker-removal.stale")}</p>
          <SettingsActionButton
            icon={SettingsActionIcon.Refresh}
            type="button"
            disabled={pending || !latest}
            onClick={review}
          >
            {copy("worker-removal.review")}
          </SettingsActionButton>
        </div>
      ) : null}
      {verification === Verification.Protected ? (
        <p role="alert">{copy("worker-removal.protected")}</p>
      ) : null}
      {verification === Verification.Unverified ? (
        <p role="alert">{copy("worker-removal.guardUnverified")}</p>
      ) : null}
      {current.error ? (
        <Problem
          error={current.error}
          actions={
            <SettingsActionButton
              icon={SettingsActionIcon.Retry}
              type="button"
              disabled={!active || pending || current.isFetching}
              onClick={() => void current.refetch()}
            >
              {copy("ui.retryCurrentRead")}
            </SettingsActionButton>
          }
        />
      ) : null}
      {mutation.error && !conflict ? (
        <Problem
          error={mutation.error}
          summary={
            mutation.uncertain ? (
              <p>{copy("worker-removal.uncertain")}</p>
            ) : undefined
          }
          actions={
            mutation.uncertain ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Retry}
                type="button"
                disabled={mutation.busy}
                onClick={mutation.retry}
              >
                {copy("worker-removal.retryOriginal")}
              </SettingsActionButton>
            ) : undefined
          }
        />
      ) : null}
      <div className="worker-removal-actions">
        <SettingsActionButton
          icon={SettingsActionIcon.Cancel}
          type="button"
          ref={cancel}
          disabled={mutation.busy}
          onClick={dismiss}
        >
          {copy("worker-removal.cancel")}
        </SettingsActionButton>
        <SettingsActionButton
          tone={SettingsActionTone.Danger}
          icon={SettingsActionIcon.Delete}
          type="button"
          className="worker-removal-confirm"
          disabled={
            pending || mutation.uncertain || stale || conflict || unavailable
          }
          aria-busy={pending}
          onClick={() => void confirm()}
        >
          {mutation.busy
            ? copy("worker-removal.deleting")
            : checking
              ? copy("worker-removal.verifying")
              : copy("worker-removal.confirm")}
        </SettingsActionButton>
      </div>
    </Modal>
  );
}
