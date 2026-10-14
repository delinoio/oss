// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useId, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  DeviceType,
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
// A peer the visit queue may check automatically: exactly the peers whose
// manual Check control is enabled.
const checkEligible = (peer: Peer) => peer.online && !peer.shared_to;
interface QueuedPeer {
  id: string;
  host: string;
}
const queuedAndEligible = (queued: QueuedPeer, peer: Peer) =>
  queued.id === peer.id && queued.host === peer.host && checkEligible(peer);
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

// The stored access intent (Access.enabled) and the observed Access.state are
// separate facts. Only the Ready state is ever presented as ready.
enum AccessStatus {
  Loading = "loading",
  Unavailable = "unavailable",
  Off = "off",
  Ready = "ready",
  ActionNeeded = "action-needed",
  SetupRequired = "setup-required",
}
function accessStatus(access: Access | undefined, pending: boolean) {
  if (!access) return pending ? AccessStatus.Loading : AccessStatus.Unavailable;
  if (access.state === "ready") return AccessStatus.Ready;
  if (access.state === "off") return AccessStatus.Off;
  return access.enabled
    ? AccessStatus.ActionNeeded
    : AccessStatus.SetupRequired;
}
const accessStatusCopy = {
  [AccessStatus.Loading]: "tailscale.accessLoading",
  [AccessStatus.Unavailable]: "tailscale.unavailable",
  [AccessStatus.Off]: "tailscale.off",
  [AccessStatus.Ready]: "tailscale.ready",
  [AccessStatus.ActionNeeded]: "tailscale.actionNeeded",
  [AccessStatus.SetupRequired]: "tailscale.setupRequired",
} as const;

// This controller stays with the original Local connection. The Tailscale
// category presentation supplies the access (This computer), devices and
// requests slots; leaving it disposes discovery while exact accepted requests
// and approvals remain owned.
export function TailscaleConnections({
  current,
  target,
  requests,
  onPaired,
}: {
  current?: HTMLElement;
  target?: HTMLElement;
  requests?: HTMLElement;
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
  const accessState = accessStatus(access, accessRead.isPending);
  const approvals = decode<Approval[]>(inbox.data?.documentJson) ?? [];
  const approval = approvals.find(
    (a) => a.state === ApprovalState.Pending && a.id !== dismissed,
  );
  return (
    <>
      <PersistentConnectionView target={current} hidden>
        {current ? (
          <section
            className="tailscale-section tailscale-access"
            aria-labelledby={accessHeading}
            data-settings-search-target="tailscale-computer"
          >
            <h2 id={accessHeading}>{copy("tailscale.thisComputer")}</h2>
            {supported ? (
              <>
                <p className="tailscale-help">
                  {copy("tailscale.computerHelp")}
                </p>
                <dl className="tailscale-facts">
                  {access ? (
                    <div>
                      <dt>{copy("tailscale.intent")}</dt>
                      <dd>
                        {access.enabled
                          ? copy("tailscale.intentEnabled")
                          : copy("tailscale.intentDisabled")}
                      </dd>
                    </div>
                  ) : null}
                  <div>
                    <dt>{copy("tailscale.status")}</dt>
                    <dd role="status" data-access-status={accessState}>
                      {copy(accessStatusCopy[accessState])}
                    </dd>
                  </div>
                </dl>
                {accessState === AccessStatus.ActionNeeded ? (
                  <p className="tailscale-guidance">
                    {copy("tailscale.setupGuidance")}
                  </p>
                ) : null}
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
                {access?.enabled ? (
                  <p className="tailscale-note">
                    {copy("tailscale.disableNote")}
                  </p>
                ) : null}
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
                  <button
                    type="button"
                    onClick={() => void accessMutation.retry()}
                  >
                    {copy("tailscale.reconcile")}
                  </button>
                ) : null}
              </>
            ) : status.isPending ? (
              <p role="status">{copy("tailscale.loading")}</p>
            ) : status.error ? (
              // A failed capability read is not evidence that the server lacks
              // Tailscale support.
              <>
                <p role="status">{copy("tailscale.unavailable")}</p>
                <Problem error={status.error} />
              </>
            ) : (
              <p>{copy("tailscale.unsupported")}</p>
            )}
          </section>
        ) : null}
      </PersistentConnectionView>
      {supported && (target || requests) ? (
        <DeviceInventory
          access={access}
          onPaired={onPaired}
          devices={target}
          requests={requests}
        />
      ) : null}
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
// Owns discovery, retained connection requests and the begin, resume and cancel
// mutations. It presents the devices and requests regions through two stable
// portal hosts so the same state feeds both sections of the category.
function DeviceInventory({
  access,
  onPaired,
  devices,
  requests,
}: {
  access?: Access;
  onPaired: () => void;
  devices?: HTMLElement;
  requests?: HTMLElement;
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
  // Automatic availability checks belong to this mount, which is exactly one
  // visit of the devices presentation: leaving the category unmounts it and
  // reopening starts fresh. The queue is snapshotted once from the first
  // successful running inventory, so rerenders, Strict Mode replay, focus,
  // reconnect and Refresh never restart or extend it.
  const [queue, setQueue] = useState<readonly QueuedPeer[]>();
  const [settled, setSettled] = useState<readonly string[]>([]);
  useEffect(() => {
    if (queue || !discovery.isSuccess || discovery.isFetching) return;
    if (inventory?.state !== "running") return;
    const next = inventory.peers
      .filter(checkEligible)
      .map(({ id, host }) => ({ id, host }));
    setQueue(next);
  }, [queue, discovery.isSuccess, discovery.isFetching, inventory]);
  const markSettled = useCallback(
    (id: string) =>
      setSettled((prev) => (prev.includes(id) ? prev : [...prev, id])),
    [],
  );
  // Peers whose automatic read is in flight, reported by their rows. An entry
  // that stops being eligible keeps its read running until it ends, so the
  // queue waits for it instead of starting the next peer's read alongside it.
  const [reading, setReading] = useState<readonly string[]>([]);
  const markReading = useCallback(
    (id: string, busy: boolean) =>
      setReading((prev) =>
        busy
          ? prev.includes(id)
            ? prev
            : [...prev, id]
          : prev.includes(id)
            ? prev.filter((other) => other !== id)
            : prev,
      ),
    [],
  );
  // Queue entries that went offline, vanished or changed host leave the visit
  // queue for good: a later Refresh that restores the peer never re-enters it,
  // so it is only checkable manually for the rest of the visit.
  const [dropped, setDropped] = useState<readonly string[]>([]);
  const currentPeers = inventory?.peers ?? [];
  const live = (q: QueuedPeer) =>
    !dropped.includes(q.id) &&
    currentPeers.some((p) => queuedAndEligible(q, p));
  useEffect(() => {
    if (!queue || !inventory) return;
    const invalid = queue
      .filter(
        (q) =>
          !dropped.includes(q.id) &&
          !inventory.peers.some((p) => queuedAndEligible(q, p)),
      )
      .map((q) => q.id);
    if (invalid.length) setDropped((prev) => [...prev, ...invalid]);
  }, [queue, inventory, dropped]);
  // Serial advance: only the first queued peer that is still live and has not
  // settled is checked, and only after every invalidated entry's read ended.
  const waiting = queue?.some(
    (q) => !live(q) && !settled.includes(q.id) && reading.includes(q.id),
  );
  const activeId = waiting
    ? undefined
    : queue?.find((q) => !settled.includes(q.id) && live(q))?.id;
  const original = decode<Approval[]>(operations.data?.documentJson) ?? [];
  const retained = accepted
    ? [accepted, ...original.filter((a) => a.id !== accepted.id)]
    : original;
  return (
    <>
      <PersistentConnectionView target={devices} hidden>
        {devices ? (
          <section
            className="tailscale-section tailscale-devices"
            aria-labelledby="tailscale-devices-heading"
            data-settings-search-target="tailscale-devices"
          >
            <h2 id="tailscale-devices-heading">
              {copy("tailscale.tailnetDevices")}
            </h2>
            <p className="tailscale-help">{copy("tailscale.help")}</p>
            <div className="tailscale-toolbar">
              <div>
                <span className="tailscale-label">
                  {copy("tailscale.client")}
                </span>
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
              </div>
              <button
                type="button"
                disabled={discovery.isFetching || operations.isFetching}
                onClick={() => {
                  // Refresh is the only recovery control, so it retries every
                  // read this category owns, including a failed request list.
                  void discovery.refetch();
                  void operations.refetch();
                }}
              >
                {copy("tailscale.refresh")}
              </button>
            </div>
            <Problem error={discovery.error} />
            {inventory?.state === "running" && inventory.peers.length === 0 ? (
              <p>{copy("tailscale.empty")}</p>
            ) : null}
            <ul className="tailscale-list">
              {inventory?.peers.map((peer) => {
                const queued = queue?.find(
                  (q) => !dropped.includes(q.id) && queuedAndEligible(q, peer),
                );
                const active = queued !== undefined && queued.id === activeId;
                return (
                  <PeerRow
                    key={peer.id}
                    peer={peer}
                    workerEnabled={access?.state === "ready"}
                    choose={(role) => setSelected({ peer, role })}
                    automatic={
                      active ||
                      (queued !== undefined && settled.includes(peer.id))
                    }
                    active={active}
                    onSettled={markSettled}
                    onReading={markReading}
                  />
                );
              })}
            </ul>
          </section>
        ) : null}
      </PersistentConnectionView>
      <PersistentConnectionView target={requests} hidden>
        {requests ? (
          <section
            className="tailscale-section tailscale-requests"
            aria-labelledby="tailscale-requests-heading"
            data-settings-search-target="tailscale-requests"
          >
            <h2 id="tailscale-requests-heading">
              {copy("tailscale.requests")}
            </h2>
            <p className="tailscale-help">{copy("tailscale.requestsHelp")}</p>
            <Problem
              error={
                start.error ?? resume.error ?? cancel.error ?? operations.error
              }
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
              <ul className="tailscale-list">
                {retained.map((a) => (
                  <li key={a.id}>
                    <div>
                      <strong>{a.name || a.origin}</strong>
                      <p role="status">{approvalLabel(a.state)}</p>
                      {a.code ? (
                        <>
                          <p>{copy("tailscale.matchingCode")}</p>
                          <strong className="tailscale-code">{a.code}</strong>
                        </>
                      ) : null}
                      <p className="tailscale-identity">{a.origin}</p>
                      {a.state === ApprovalState.Completed ? (
                        <p>{copy("tailscale.completedHelp")}</p>
                      ) : null}
                    </div>
                    <div className="tailscale-actions">
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
                    </div>
                  </li>
                ))}
              </ul>
            ) : operations.isPending ? (
              <p role="status">{copy("tailscale.requestsLoading")}</p>
            ) : operations.data ? (
              <p>{copy("tailscale.requestsEmpty")}</p>
            ) : (
              // The list was never read, so it cannot be reported as empty.
              <p role="status">{copy("tailscale.requestsUnavailable")}</p>
            )}
          </section>
        ) : null}
      </PersistentConnectionView>
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
  automatic,
  active,
  onSettled,
  onReading,
}: {
  peer: Peer;
  choose: (role: DeviceType) => void;
  workerEnabled: boolean;
  // The visit queue has started (or finished) the automatic check of this peer.
  automatic: boolean;
  // This peer is the one automatic check currently owning the queue.
  active: boolean;
  onSettled: (id: string) => void;
  // Reports whether this row has a check read in flight.
  onReading: (id: string, busy: boolean) => void;
}) {
  useLocale();
  const [checked, setChecked] = useState(false);
  const enabled = checked || automatic;
  // Checking is an explicit single-peer read. Unmount cancels it; cached online
  // metadata never establishes DeliDev support or enables another peer's action.
  const check = useQuery(
    TailscaleQuery.checkTailscale,
    { peerId: peer.id },
    {
      enabled,
      refetchOnWindowFocus: false,
      refetchOnReconnect: false,
      staleTime: Infinity,
      gcTime: 0,
    },
  );
  const identity = enabled
    ? decode<Identity>(check.data?.documentJson)
    : undefined;
  useEffect(() => setChecked(false), [peer.host, peer.online]);
  // The active automatic check is done once its read is no longer in flight and
  // has produced a result or an error (including a check that a manual click
  // already finished); the queue then advances to the next peer.
  const settledRead = !check.isFetching && check.status !== "pending";
  useEffect(() => {
    if (active && settledRead) onSettled(peer.id);
  }, [active, settledRead, onSettled, peer.id]);
  useEffect(() => {
    onReading(peer.id, check.isFetching);
  }, [check.isFetching, onReading, peer.id]);
  useEffect(() => () => onReading(peer.id, false), [onReading, peer.id]);
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
          {check.isFetching ? (
            <>
              {copy("tailscale.checking")}
              <span className="tailscale-progress" aria-hidden="true" />
            </>
          ) : identity?.approval ? (
            copy("tailscale.accepting")
          ) : enabled ? (
            copy("tailscale.unavailable")
          ) : (
            copy("tailscale.notChecked")
          )}
        </p>
      </div>
      <div className="tailscale-actions">
        <button
          type="button"
          disabled={!peer.online || peer.shared_to || check.isFetching}
          onClick={() => {
            setChecked(true);
            if (enabled) void check.refetch();
          }}
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
