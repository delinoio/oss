// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  DeviceType,
  SystemCapability,
  SystemQuery,
  TailscaleQuery,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { Modal, Problem } from "./ui";
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

// This controller stays with the original Local connection. Page departure
// disposes discovery while exact accepted requests and approvals remain owned.
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
  const access =
    acceptedAccess ?? decode<Access>(accessRead.data?.documentJson);
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
          <section
            className="tailscale-access"
            aria-label={copy("tailscale.access")}
          >
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
            <Problem
              error={
                accessRead.error ??
                accessMutation.error ??
                inbox.error ??
                decisions.error
              }
            />
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
  const inventory = decode<Inventory>(discovery.data?.documentJson);
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
  const [checked, setChecked] = useState(false);
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
  const identity = checked
    ? decode<Identity>(check.data?.documentJson)
    : undefined;
  useEffect(() => setChecked(false), [peer.host, peer.online]);
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
          {check.isFetching
            ? copy("tailscale.checking")
            : identity?.approval
              ? copy("tailscale.accepting")
              : checked
                ? copy("tailscale.unavailable")
                : copy("tailscale.notChecked")}
        </p>
      </div>
      <div className="tailscale-actions">
        <button
          type="button"
          disabled={!peer.online || peer.shared_to || check.isFetching}
          onClick={() => (checked ? void check.refetch() : setChecked(true))}
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
      <Problem error={check.error} />
    </li>
  );
}
