// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useState } from "react";
import { createConnectQueryKey, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  DeviceType,
  FailureCause,
  FailureCode,
  SystemCapability,
  SystemQuery,
  TailscaleQuery,
  clientFailure,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { InlineRemediation, Modal, Problem } from "./ui";
import { PersistentConnectionView } from "./connections-page";
import { useRetainedMutation } from "./mutation";
import { SSHSetup } from "./ssh-setup";
import "./tailscale-connections.css";

enum Ownership {
  Own = "own",
  Shared = "shared",
  Tagged = "tagged",
  Unknown = "unknown",
}
enum ApprovalState {
  Pending = "pending",
  Approved = "approved",
  Denied = "denied",
  Expired = "expired",
  Completed = "completed",
  Uncertain = "uncertain",
}
interface Peer {
  id: string;
  name: string;
  host: string;
  ownership: Ownership;
  online: boolean;
  shared_to: boolean;
}
interface Inventory {
  state: string;
  peers: Peer[];
  observed_at: string;
}
interface Access {
  enabled: boolean;
  state: string;
  server_id: string;
  origin?: string;
}
interface Identity {
  server_id: string;
  origin: string;
  approval: boolean;
}
interface Approval {
  id: string;
  name: string;
  server_id: string;
  origin: string;
  role: string;
  code: string;
  state: ApprovalState;
  selected_server?: string;
  selected_origin?: string;
  profile_id?: string;
}
function decode<T>(raw?: Uint8Array): T | undefined {
  if (!raw?.length) return;
  if (raw.length > 1 << 20) throw new Error("Oversized Tailscale observation");
  return JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw)) as T;
}
// Exact safe text of the Go login failure (tailscale.LoginRequired). Only this
// code, message and guidance triple is diagnosed; a generic unavailable failure
// or a retained logged-out discovery observation is never promoted to it.
enum LoginRequiredError {
  Message = "Tailscale login is required.",
  Guidance = "Open Tailscale on this computer and sign in. Then refresh Tailscale devices and inspect the original access request.",
}
function isLoginRequired(error: unknown): boolean {
  if (!error) return false;
  const failure = clientFailure(error);
  return (
    failure.code === FailureCode.Unavailable &&
    failure.message === LoginRequiredError.Message &&
    failure.guidance === LoginRequiredError.Guidance
  );
}
// Presentation only: the original retained mutation owns the Inspect control.
function LoginRequiredProblem({ error }: { error: unknown }) {
  useLocale();
  const failure = clientFailure(error);
  return (
    <InlineRemediation
      summary={
        <>
          <strong>{copy("tailscale.loginRequired.heading")}</strong>
          <p>{copy("tailscale.loginRequired.body")}</p>
        </>
      }
      details={
        <>
          <strong>{failure.message}</strong>
          <p>{failure.guidance}</p>
          <code>{failure.code}</code>
          {failure.correlationId ? <code>{failure.correlationId}</code> : null}
        </>
      }
    />
  );
}
// Proven Check-only causes and their localized guidance. Any other failure,
// including an unknown or mismatched cause, keeps the generic Problem.
const checkCauseCopy = {
  [FailureCause.TailscaleConnectionRefused]: {
    heading: "tailscale.cause.connectionRefused.heading",
    body: "tailscale.cause.connectionRefused.body",
  },
  [FailureCause.TailscaleTimeout]: {
    heading: "tailscale.cause.timeout.heading",
    body: "tailscale.cause.timeout.body",
  },
  [FailureCause.TailscaleCertificateInvalid]: {
    heading: "tailscale.cause.certificateInvalid.heading",
    body: "tailscale.cause.certificateInvalid.body",
  },
  [FailureCause.TailscaleUnexpectedHttp]: {
    heading: "tailscale.cause.unexpectedHttp.heading",
    body: "tailscale.cause.unexpectedHttp.body",
  },
  [FailureCause.TailscaleInvalidIdentity]: {
    heading: "tailscale.cause.invalidIdentity.heading",
    body: "tailscale.cause.invalidIdentity.body",
  },
} as const;
type CheckCause = keyof typeof checkCauseCopy;
function checkCause(error: unknown): CheckCause | undefined {
  if (!error) return undefined;
  const cause = clientFailure(error).cause;
  return cause !== undefined && Object.hasOwn(checkCauseCopy, cause)
    ? (cause as CheckCause)
    : undefined;
}
// Presentation only: the original Check query keeps its identity and error. The
// summary is outside Technical details, which keep the original safe failure.
function CheckCauseProblem({
  error,
  cause,
  device,
}: {
  error: unknown;
  cause: CheckCause;
  device: string;
}) {
  useLocale();
  const failure = clientFailure(error);
  return (
    <InlineRemediation
      summary={
        <>
          <strong>{copy(checkCauseCopy[cause].heading, { device })}</strong>
          <p>{copy(checkCauseCopy[cause].body, { device })}</p>
        </>
      }
      details={
        <>
          <strong>{failure.message}</strong>
          <p>{failure.guidance}</p>
          <code>{failure.code}</code>
          {failure.correlationId ? <code>{failure.correlationId}</code> : null}
        </>
      }
    />
  );
}
const ownershipCopy = {
  [Ownership.Own]: "tailscale.own",
  [Ownership.Shared]: "tailscale.shared",
  [Ownership.Tagged]: "tailscale.tagged",
  [Ownership.Unknown]: "tailscale.unknown",
} as const;
function approvalLabel(state: ApprovalState) {
  const keys = {
    pending: "tailscale.pending",
    approved: "tailscale.approved",
    denied: "tailscale.denied",
    expired: "tailscale.expired",
    completed: "tailscale.completed",
    uncertain: "tailscale.uncertain",
  } as const;
  return copy(keys[state] ?? "tailscale.uncertain");
}

// This controller stays with the original Local connection. The Runner Devices
// presentation supplies the access and devices slots; leaving it disposes
// discovery while exact accepted requests and approvals remain owned.
export function TailscaleConnections({
  current,
  target,
  onPaired,
}: {
  current?: HTMLElement;
  target?: HTMLElement;
  onPaired: () => void;
}) {
  useLocale();
  const accessHeading = useId();
  const status = useQuery(SystemQuery.getStatus, {});
  const supported =
    status.data?.capabilities.includes(
      SystemCapability.TAILSCALE_CONNECTIONS_V1,
    ) === true;
  const accessRead = useQuery(
    TailscaleQuery.getTailscaleAccess,
    {},
    { enabled: supported, refetchInterval: current ? 2000 : false },
  );
  const inbox = useQuery(
    TailscaleQuery.listTailscaleApprovals,
    {},
    { enabled: supported, refetchInterval: supported ? 2000 : false },
  );
  const [confirmation, setConfirmation] = useState(false),
    [dismissed, setDismissed] = useState<string>();
  const [acceptedAccess, setAcceptedAccess] = useState<Access>();
  useEffect(
    () => setAcceptedAccess(decode<Access>(accessRead.data?.documentJson)),
    [accessRead.data],
  );
  // A failed latest read leaves TanStack Query holding the previous data next to
  // the new error. The latest result controls: the cached access state never
  // outranks the current read failure, so status and actions treat it as unknown.
  const access = accessRead.error
    ? undefined
    : (acceptedAccess ?? decode<Access>(accessRead.data?.documentJson));
  const accessMutation = useRetainedMutation(
    "tailscale:access",
    TailscaleQuery.setTailscaleAccess,
    (response) => {
      setAcceptedAccess(decode<Access>(response.documentJson));
      void accessRead.refetch();
    },
  );
  const decisions = useRetainedMutation(
    "tailscale:decision",
    TailscaleQuery.decideTailscaleApproval,
    () => {
      void inbox.refetch();
      setDismissed(undefined);
    },
  );
  const approvals = decode<Approval[]>(inbox.data?.documentJson) ?? [];
  const approval = approvals.find(
    (a) => a.state === ApprovalState.Pending && a.id !== dismissed,
  );
  return (
    <>
      <PersistentConnectionView target={current} hidden>
        {supported ? (
          <section className="tailscale-access" aria-labelledby={accessHeading}>
            <h2 id={accessHeading}>{copy("tailscale.access")}</h2>
            <p role="status">
              {copy("tailscale.access")}:{" "}
              {access?.state === "ready"
                ? copy("tailscale.on")
                : access?.state === "off"
                  ? copy("tailscale.off")
                  : copy("tailscale.setupRequired")}
            </p>
            {access?.origin ? (
              <p className="tailscale-identity">{access.origin}</p>
            ) : null}
            <button
              type="button"
              disabled={accessMutation.busy}
              onClick={() =>
                access?.enabled
                  ? void accessMutation.send({
                      requestId: newRequestId(),
                      enabled: false,
                    })
                  : setConfirmation(true)
              }
            >
              {access?.enabled
                ? copy("tailscale.disable")
                : copy("tailscale.enable")}
            </button>
            {!accessRead.error && isLoginRequired(accessMutation.error) ? (
              <LoginRequiredProblem error={accessMutation.error} />
            ) : (
              <Problem
                error={
                  accessRead.error ??
                  accessMutation.error ??
                  inbox.error ??
                  decisions.error
                }
              />
            )}
            {decisions.uncertain ? (
              <button type="button" onClick={() => void decisions.retry()}>
                {copy("tailscale.reconcile")}
              </button>
            ) : null}
            {accessMutation.uncertain ? (
              <button type="button" onClick={() => void accessMutation.retry()}>
                {copy("tailscale.reconcile")}
              </button>
            ) : null}
          </section>
        ) : null}
      </PersistentConnectionView>
      <PersistentConnectionView target={target} hidden>
        {target ? (
          <section className="tailscale-devices">
            <h2>{copy("tailscale.devices")}</h2>
            {supported ? (
              <DeviceInventory access={access} onPaired={onPaired} />
            ) : (
              <p>{copy("tailscale.unsupported")}</p>
            )}
          </section>
        ) : null}
      </PersistentConnectionView>
      {confirmation ? (
        <Modal
          title={copy("tailscale.enable")}
          close={() => setConfirmation(false)}
          trapFocus
          focusClose
          headingEntrance
        >
          <p>{copy("tailscale.visibility")}</p>
          <button
            type="button"
            disabled={accessMutation.busy}
            onClick={() => {
              setConfirmation(false);
              void accessMutation.send({
                requestId: newRequestId(),
                enabled: true,
              });
            }}
          >
            {copy("tailscale.enable")}
          </button>
        </Modal>
      ) : null}
      {approval ? (
        <Modal
          title={copy("tailscale.allowConnection")}
          close={() => setDismissed(approval.id)}
          trapFocus
          focusClose
        >
          <p>{approval.name}</p>
          <p>
            {approval.role === "worker"
              ? copy("tailscale.workerIntent")
              : copy("tailscale.clientIntent")}
          </p>
          <p className="tailscale-identity">{approval.origin}</p>
          {approval.selected_server ? (
            <p className="tailscale-identity">
              {copy("tailscale.selectedServer")}: {approval.selected_server}
              <br />
              {approval.selected_origin}
            </p>
          ) : null}
          <p>{copy("tailscale.matchingCode")}</p>
          <strong className="tailscale-code">{approval.code}</strong>
          <div className="tailscale-actions">
            <button
              type="button"
              disabled={decisions.busy}
              onClick={() =>
                void decisions.send({
                  requestId: approval.id,
                  confirmationCode: approval.code,
                  allow: false,
                })
              }
            >
              {copy("tailscale.deny")}
            </button>
            <button
              type="button"
              className="primary"
              disabled={decisions.busy}
              onClick={() =>
                void decisions.send({
                  requestId: approval.id,
                  confirmationCode: approval.code,
                  allow: true,
                })
              }
            >
              {copy("tailscale.allow")}
            </button>
          </div>
          <Problem error={decisions.error} />
          {decisions.uncertain ? (
            <button type="button" onClick={() => void decisions.retry()}>
              {copy("tailscale.reconcile")}
            </button>
          ) : null}
        </Modal>
      ) : null}
    </>
  );
}
function DeviceInventory({
  access,
  onPaired,
}: {
  access?: Access;
  onPaired: () => void;
}) {
  useLocale();
  const discovery = useQuery(TailscaleQuery.discoverTailscale, {});
  const [selected, setSelected] = useState<{ peer: Peer; role: DeviceType }>();
  const operations = useQuery(TailscaleQuery.listTailscaleConnections, {});
  const [accepted, setAccepted] = useState<Approval>();
  const start = useRetainedMutation(
    "tailscale:begin",
    TailscaleQuery.beginTailscaleConnection,
    (response) => {
      const value = decode<Approval>(response.documentJson);
      setAccepted(value);
      if (value?.state === ApprovalState.Completed && value.profile_id)
        onPaired();
      void operations.refetch();
      setSelected(undefined);
    },
  );
  const resume = useRetainedMutation(
    "tailscale:resume",
    TailscaleQuery.resumeTailscaleConnection,
    (response) => {
      const value = decode<Approval>(response.documentJson);
      setAccepted(value);
      if (value?.state === ApprovalState.Completed && value.profile_id)
        onPaired();
      void operations.refetch();
    },
  );
  const cancel = useRetainedMutation(
    "tailscale:cancel",
    TailscaleQuery.cancelTailscaleConnection,
    (response) => {
      const value = decode<Approval>(response.documentJson);
      setAccepted(value);
      if (value?.state === ApprovalState.Completed && value.profile_id)
        onPaired();
      void operations.refetch();
    },
  );
  // A retained input (uncertain or in flight) makes the hook ignore any new
  // input, so row actions stay disabled until the original request is retried.
  const resumeBlocked =
    resume.busy || resume.uncertain || Boolean(resume.input);
  const cancelBlocked =
    cancel.busy || cancel.uncertain || Boolean(cancel.input);
  const retainedHelpId = "tailscale-retained-request-help";
  // A failed refresh keeps the previous inventory in the query next to the new
  // error. The latest result controls status and peer actions, so the cached
  // inventory is ignored while the current query has an error.
  const inventory = discovery.error
    ? undefined
    : decode<Inventory>(discovery.data?.documentJson);
  const original = decode<Approval[]>(operations.data?.documentJson) ?? [];
  const retained = accepted
    ? [accepted, ...original.filter((a) => a.id !== accepted.id)]
    : original;
  return (
    <>
      <p>{copy("tailscale.help")}</p>
      <button
        type="button"
        disabled={discovery.isFetching}
        onClick={() => void discovery.refetch()}
      >
        {copy("tailscale.refresh")}
      </button>
      <p role="status">
        {discovery.isPending
          ? copy("tailscale.loading")
          : inventory?.state === "running"
            ? copy("tailscale.connected")
            : inventory?.state === "logged-out"
              ? copy("tailscale.loggedOut")
              : inventory?.state === "machine-approval"
                ? copy("tailscale.machineApproval")
                : inventory?.state === "stopped"
                  ? copy("tailscale.stopped")
                  : inventory?.state === "starting"
                    ? copy("tailscale.starting")
                    : copy("tailscale.unavailable")}
      </p>
      <Problem error={discovery.error} />
      {inventory?.state === "running" && inventory.peers.length === 0 ? (
        <p>{copy("tailscale.empty")}</p>
      ) : null}
      <ul className="tailscale-list">
        {inventory?.peers.map((peer) => (
          <PeerRow
            key={peer.id}
            peer={peer}
            workerEnabled={access?.state === "ready"}
            choose={(role) => setSelected({ peer, role })}
          />
        ))}
      </ul>
      <Problem
        error={start.error ?? resume.error ?? cancel.error ?? operations.error}
      />
      {start.uncertain ? (
        <button type="button" onClick={() => void start.retry()}>
          {copy("tailscale.reconcile")}
        </button>
      ) : null}
      {resume.uncertain ? (
        <button
          type="button"
          disabled={resume.busy}
          onClick={() => void resume.retry()}
        >
          {copy("tailscale.retryResume")}
        </button>
      ) : null}
      {cancel.uncertain ? (
        <button
          type="button"
          disabled={cancel.busy}
          onClick={() => void cancel.retry()}
        >
          {copy("tailscale.retryCancel")}
        </button>
      ) : null}
      {resume.uncertain || cancel.uncertain ? (
        <p id={retainedHelpId} role="status">
          {copy("tailscale.retainedRequestHelp")}
        </p>
      ) : null}
      {retained.length ? (
        <section>
          <h3>{copy("tailscale.operations")}</h3>
          <ul className="tailscale-list">
            {retained.map((a) => (
              <li key={a.id}>
                <strong>{a.name || a.origin}</strong>
                <p role="status">{approvalLabel(a.state)}</p>
                {a.code ? (
                  <>
                    <p>{copy("tailscale.matchingCode")}</p>
                    <strong className="tailscale-code">{a.code}</strong>
                  </>
                ) : null}
                <p className="tailscale-identity">{a.origin}</p>
                {a.state === ApprovalState.Pending ||
                a.state === ApprovalState.Approved ||
                a.state === ApprovalState.Uncertain ? (
                  <button
                    type="button"
                    disabled={resumeBlocked}
                    aria-describedby={
                      resume.uncertain ? retainedHelpId : undefined
                    }
                    onClick={() => void resume.send({ requestId: a.id })}
                  >
                    {copy("tailscale.reconcile")}
                  </button>
                ) : null}
                {a.state === ApprovalState.Pending ? (
                  <button
                    type="button"
                    disabled={cancelBlocked}
                    aria-describedby={
                      cancel.uncertain ? retainedHelpId : undefined
                    }
                    onClick={() => void cancel.send({ requestId: a.id })}
                  >
                    {copy("tailscale.cancel")}
                  </button>
                ) : null}
                {a.state === ApprovalState.Completed ? (
                  <p>{copy("tailscale.completedHelp")}</p>
                ) : null}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {selected ? (
        <Modal
          title={
            selected.role === DeviceType.WORKER
              ? copy("tailscale.registerWorker")
              : copy("tailscale.connect")
          }
          close={() => setSelected(undefined)}
          trapFocus
          focusClose
          headingEntrance
        >
          <p>{selected.peer.name}</p>
          <p>
            {selected.role === DeviceType.WORKER
              ? copy("tailscale.workerIntent")
              : copy("tailscale.clientIntent")}
          </p>
          {selected.role === DeviceType.WORKER ? (
            <p className="tailscale-identity">
              {copy("tailscale.selectedServer")}: {access?.server_id}
              <br />
              {access?.origin}
            </p>
          ) : null}
          <p>{copy("tailscale.approvalHelp")}</p>
          <button
            className="primary"
            type="button"
            disabled={start.busy}
            onClick={() =>
              void start.send({
                requestId: newRequestId(),
                peerId: selected.peer.id,
                name: selected.peer.name,
                role: selected.role,
              })
            }
          >
            {copy("tailscale.requestApproval")}
          </button>
          <Problem error={start.error} />
        </Modal>
      ) : null}
    </>
  );
}
function PeerRow({
  peer,
  choose,
  workerEnabled,
}: {
  peer: Peer;
  choose: (role: DeviceType) => void;
  workerEnabled: boolean;
}) {
  useLocale();
  // A check belongs to the peer's host and online state when it was requested.
  // Deriving `checked` from that key invalidates the check in the same render
  // that observes a discovery change, so no stale result can show for a frame.
  const peerKey = JSON.stringify([peer.host, peer.online]);
  const [checkedKey, setCheckedKey] = useState<string>();
  // A key that no longer matches the peer is cleared in the same render, not
  // kept: otherwise a peer that returns to the checked host and online state
  // (online to offline to online, or host A to B to A) would match it again and
  // re-enable the query without an explicit Check.
  if (checkedKey !== undefined && checkedKey !== peerKey)
    setCheckedKey(undefined);
  const checked = checkedKey === peerKey;
  const queryClient = useQueryClient();
  // Checking is an explicit single-peer read. Unmount cancels it; cached online
  // metadata never establishes DeliDev support or enables another peer's action.
  const check = useQuery(
    TailscaleQuery.checkTailscale,
    { peerId: peer.id },
    {
      enabled: checked,
      refetchOnWindowFocus: false,
      refetchOnReconnect: false,
      staleTime: Infinity,
      gcTime: 0,
    },
  );
  // Changed host or online state invalidates the check. The disabled query would
  // otherwise keep its cached identity or error under the same peer ID and serve
  // it again on the next Check, so the cached result is dropped with the check.
  useEffect(() => {
    void queryClient.resetQueries({
      queryKey: createConnectQueryKey({
        schema: TailscaleQuery.checkTailscale,
        input: { peerId: peer.id },
        cardinality: "finite",
      }),
    });
  }, [queryClient, peer.id, peerKey]);
  // Every displayed value reads the query only for the current check. A failed
  // manual re-check keeps the previous identity next to the new error, so the
  // latest result controls: cached identity is ignored on error.
  const fetching = checked && check.isFetching;
  const checkError = checked ? check.error : undefined;
  const identity =
    checked && !check.error
      ? decode<Identity>(check.data?.documentJson)
      : undefined;
  const cause = checkCause(checkError);
  return (
    <li>
      <div>
        <strong>{peer.name}</strong>
        <p>
          {copy(ownershipCopy[peer.ownership] ?? "tailscale.unknown")} ·{" "}
          {peer.online ? copy("tailscale.online") : copy("tailscale.offline")}
        </p>
        <p role="status">
          {copy("tailscale.delidev")}:{" "}
          {fetching
            ? copy("tailscale.checking")
            : identity?.approval
              ? copy("tailscale.accepting")
              : checked
                ? cause === FailureCause.TailscaleConnectionRefused
                  ? copy("tailscale.couldNotConnect")
                  : copy("tailscale.unavailable")
                : copy("tailscale.notChecked")}
        </p>
      </div>
      <div className="tailscale-actions">
        <button
          type="button"
          disabled={!peer.online || peer.shared_to || fetching}
          onClick={() =>
            checked ? void check.refetch() : setCheckedKey(peerKey)
          }
        >
          {copy("tailscale.check")}
        </button>
        {identity?.approval ? (
          <>
            <button
              type="button"
              disabled={!peer.online}
              onClick={() => choose(DeviceType.CLIENT)}
            >
              {copy("tailscale.connect")}
            </button>
            <button
              type="button"
              disabled={!workerEnabled || !peer.online}
              onClick={() => choose(DeviceType.WORKER)}
            >
              {copy("tailscale.registerWorker")}
            </button>
          </>
        ) : null}
        {peer.online && !peer.shared_to ? (
          <SSHSetup active initialHost={peer.host} />
        ) : null}
      </div>
      {cause ? (
        <div className="tailscale-check-cause">
          <CheckCauseProblem
            error={checkError}
            cause={cause}
            device={peer.name}
          />
        </div>
      ) : (
        <Problem error={checkError} />
      )}
    </li>
  );
}
