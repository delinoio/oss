// SPDX-License-Identifier: Apache-2.0
import "./claude-usage-tracking.css";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import {
  ClaudeUsageLoginState,
  EntityKind,
  FailureCode,
  ResourceService,
  SubscriptionObservationAction,
  SubscriptionService,
  SubscriptionServiceId,
  clientFailure,
  isEntityId,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { OAuthNativeAction, useOAuthNativeControl } from "./account-oauth";
import { claudeUsageLoginURL } from "./claude-subscription-login";
import { document, items, object, text } from "./documents";
import { copy, useLocale, type MessageKey } from "./localization";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import {
  SettingsDialogFocus,
  SettingsDialogSize,
  SettingsTaskActions,
  SettingsTaskDialog,
} from "./settings-task";
import { serviceAccount } from "./subscription-resource";
import { QuotaObservationState, QuotaWindow } from "./subscription-settings";
import { Timestamp } from "./timestamp-display";

/** Closed lifecycle of one usage login, mirroring the server vocabulary. */
export enum ClaudeUsageLoginPhase {
  Preparing = "preparing",
  Waiting = "waiting",
  Confirming = "confirming",
  Linked = "linked",
  Canceled = "canceled",
  Expired = "expired",
  Unsupported = "unsupported",
  Failed = "failed",
  Recovery = "recovery-required",
}

/** Closed presentation states of the Usage tracking section. */
export enum ClaudeUsageView {
  Loading = "loading",
  Unavailable = "unavailable",
  Unlinked = "unlinked",
  Progress = "progress",
  Confirmation = "confirmation",
  Ended = "ended",
  Linked = "linked",
}

/** Distinct health of a linked connection. */
export enum ClaudeUsageHealth {
  Waiting = "waiting",
  Reading = "reading",
  Fresh = "fresh",
  Stale = "stale",
  Failed = "failed",
  Backoff = "backoff",
  ReconnectRequired = "reconnect-required",
  Cleanup = "cleanup",
}

export interface ClaudeUsageIdentity {
  email: string;
  organization: string;
  plan: string;
}
export interface ClaudeUsageOperation {
  id: string;
  generation: string;
  phase: ClaudeUsageLoginPhase;
  failure: string;
  canceled: boolean;
  identity?: ClaudeUsageIdentity;
}
export interface ClaudeUsageProjection {
  generation: string;
  linked: boolean;
  identity?: ClaudeUsageIdentity;
  operation?: ClaudeUsageOperation;
  lastSuccessAt: string;
  backoffUntil: string;
  failure: string;
  reconnectRequired: boolean;
  cleanupPending: boolean;
  reading: boolean;
}

const identityOf = (value: unknown): ClaudeUsageIdentity | undefined => {
  const raw = object(value);
  return text(raw.email)
    ? {
        email: text(raw.email),
        organization: text(raw.organization),
        plan: text(raw.plan),
      }
    : undefined;
};

/** Reads the server-owned usage state from an account document. */
export function claudeUsageProjection(
  data: Record<string, unknown>,
): ClaudeUsageProjection {
  const usage = object(object(data.subscription).claude_usage);
  const operation = object(usage.operation);
  const phase = Object.values(ClaudeUsageLoginPhase).find(
    (value) => value === operation.state,
  );
  return {
    generation: text(usage.generation),
    linked:
      Boolean(text(usage.linked_at)) && Boolean(text(usage.credential_id)),
    identity: identityOf(usage.identity),
    operation:
      phase && text(operation.id)
        ? {
            id: text(operation.id),
            generation: text(operation.generation),
            phase,
            failure: text(operation.failure),
            canceled: operation.canceled === true,
            identity: identityOf(operation.identity),
          }
        : undefined,
    lastSuccessAt: text(usage.last_success_at),
    backoffUntil: text(usage.backoff_until),
    failure: text(usage.failure),
    reconnectRequired: usage.reconnect_required === true,
    cleanupPending: usage.cleanup_pending === true,
    reading: ["fetching", "refreshing"].includes(
      text(object(usage.read).phase),
    ),
  };
}

/** Health of a linked connection at a point in time. */
export function claudeUsageHealth(
  usage: ClaudeUsageProjection,
  now: number,
): ClaudeUsageHealth {
  if (usage.cleanupPending) return ClaudeUsageHealth.Cleanup;
  if (usage.reconnectRequired) return ClaudeUsageHealth.ReconnectRequired;
  if (Date.parse(usage.backoffUntil) > now) return ClaudeUsageHealth.Backoff;
  if (usage.reading) return ClaudeUsageHealth.Reading;
  if (usage.failure) return ClaudeUsageHealth.Failed;
  const observed = Date.parse(usage.lastSuccessAt);
  if (!Number.isFinite(observed)) return ClaudeUsageHealth.Waiting;
  return now - observed > 5 * 60 * 1000
    ? ClaudeUsageHealth.Stale
    : ClaudeUsageHealth.Fresh;
}

const failureMessages: Record<string, MessageKey> = {
  timeout: "claude-usage.failure.timeout",
  network: "claude-usage.failure.network",
  route_unsupported: "claude-usage.failure.routeUnsupported",
  redirect: "claude-usage.failure.redirect",
  oversized: "claude-usage.failure.oversized",
  malformed: "claude-usage.failure.malformed",
  empty: "claude-usage.failure.empty",
  unauthorized: "claude-usage.failure.unauthorized",
  forbidden: "claude-usage.failure.forbidden",
  rate_limited: "claude-usage.failure.rateLimited",
  server_error: "claude-usage.failure.serverError",
  credentials_unavailable: "claude-usage.failure.credentialsUnavailable",
  storage_unsupported: "claude-usage.failure.storageUnsupported",
  refresh_rejected: "claude-usage.failure.refreshRejected",
  refresh_unsupported: "claude-usage.failure.refreshUnsupported",
  refresh_uncertain: "claude-usage.failure.refreshUncertain",
  identity_changed: "claude-usage.failure.identityChanged",
  login_rejected: "claude-usage.failure.loginRejected",
  login_unsupported: "claude-usage.failure.loginUnsupported",
  login_manual_code_required: "claude-usage.failure.loginManualCode",
  profile_unsafe: "claude-usage.failure.profileUnsafe",
  cleanup_uncertain: "claude-usage.failure.cleanupUncertain",
  installation_unsupported: "claude-usage.failure.installation",
  interrupted: "claude-usage.failure.interrupted",
  authority_changed: "claude-usage.failure.authorityChanged",
};
const failureMessage = (code: string): MessageKey =>
  failureMessages[code] ?? "claude-usage.failure.generic";

const endedMessages: Partial<Record<ClaudeUsageLoginPhase, MessageKey>> = {
  [ClaudeUsageLoginPhase.Canceled]: "claude-usage.state.canceled",
  [ClaudeUsageLoginPhase.Expired]: "claude-usage.state.expired",
  [ClaudeUsageLoginPhase.Failed]: "claude-usage.state.failed",
  [ClaudeUsageLoginPhase.Unsupported]: "claude-usage.state.unsupported",
  [ClaudeUsageLoginPhase.Recovery]: "claude-usage.state.recovery",
};

/** Window labels for the Claude comparison group. */
export function claudeUsageWindowLabel(id: string): string | undefined {
  return id === "five_hour"
    ? copy("claude-usage.window.fiveHour")
    : id === "seven_day"
      ? copy("claude-usage.window.sevenDay")
      : undefined;
}

interface UsageRunOptions {
  /** False when the request owned secret bytes that cannot be replayed. */
  retryable?: boolean;
  /** Message shown instead of the retry guidance when it cannot be retried. */
  unreplayable?: MessageKey;
  /** Failure codes that must be replayed with the original request. */
  recoverable?: FailureCode[];
}

interface UsageMutation {
  kind: string;
  send: () => Promise<void>;
  options: UsageRunOptions;
}

/**
 * Owns the whole Usage tracking lifecycle of one connected server-token Claude
 * account. Every mutation keeps its request identity until it is acknowledged,
 * so an uncertain result is only ever retried with the original request.
 */
export function useClaudeUsageTracking({
  account,
  active,
  supported,
  settled,
  changed,
}: {
  account: Resource | undefined;
  active: boolean;
  /** CLAUDE_SERVER_USAGE_V1 is advertised by a settled, error-free status read. */
  supported: boolean;
  /** The status read has settled without error. */
  settled: boolean;
  changed: () => void;
}) {
  useLocale();
  const transport = useTransport(),
    native = useOAuthNativeControl();
  const clients = useMemo(
    () => ({
      resource: createClient(ResourceService, transport),
      subscription: createClient(SubscriptionService, transport),
    }),
    [transport],
  );
  const data = document(account);
  const usage = useMemo(() => claudeUsageProjection(data), [account]);
  const [now, setNow] = useState(() => Date.now());
  const [hidden, setHidden] = useState(true);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<MessageKey>();
  const [retry, setRetry] = useState<UsageMutation>();
  const [disconnecting, setDisconnecting] = useState(false);
  const [progress, setProgress] = useState<{
    operation: string;
    url: string;
    codeRequested: boolean;
    canceled: boolean;
    browserFailed: boolean;
  }>();
  const opening = useRef(newRequestId());
  const binding = useRef({ operation: "", url: "", generation: "" });
  const requests = useRef(new Map<string, unknown>());
  const inflight = useRef(false);
  const accountId = account?.id ?? "";
  const operation = usage.operation;
  const live =
    operation !== undefined &&
    [
      ClaudeUsageLoginPhase.Preparing,
      ClaudeUsageLoginPhase.Waiting,
      ClaudeUsageLoginPhase.Confirming,
    ].includes(operation.phase);

  useEffect(() => {
    if (!active) return;
    const timer = setInterval(() => setNow(Date.now()), 15000);
    return () => clearInterval(timer);
  }, [active]);

  const dispose = () => {
    binding.current = { operation: "", url: "", generation: "" };
    // The native host permanently records a disposed opening scope, so the
    // next login or reconnect must open under a fresh opening identity.
    const disposed = opening.current;
    opening.current = newRequestId();
    if (native)
      void native(disposed, OAuthNativeAction.Dispose, "", "", "").catch(
        () => undefined,
      );
  };
  // Leaving the lifetime releases the trusted browser binding and the address.
  useEffect(
    () => () => {
      if (binding.current.operation) dispose();
    },
    [],
  );
  useEffect(() => {
    if (!live && binding.current.operation) dispose();
    if (!live) setProgress(undefined);
  }, [live]);

  const fresh = async () => {
    const result = await clients.resource.getResource({
      kind: EntityKind.ACCOUNT,
      id: accountId,
    });
    if (
      !serviceAccount(
        result.resource,
        accountId,
        SubscriptionServiceId.Claude,
        account?.revision ?? 1n,
      )
    )
      throw new Error("Account ownership changed");
    return result.resource!;
  };

  /** Runs one mutation. The prepared request is reused until acknowledged. */
  const run = async (
    kind: string,
    send: () => Promise<void>,
    failure: MessageKey,
    options: UsageRunOptions = {},
  ) => {
    if (inflight.current) return;
    inflight.current = true;
    setBusy(true);
    setProblem(undefined);
    try {
      await send();
      setRetry(undefined);
      requests.current.delete(kind);
    } catch (error) {
      const code = clientFailure(error).code;
      // A recoverable code (for example a disconnect whose credential cleanup
      // must be replayed with the exact original request) is retried with the
      // retained request identity instead of being treated as final.
      const uncertain =
        [
          FailureCode.Unavailable,
          FailureCode.ServerUnavailable,
          FailureCode.Canceled,
          FailureCode.Internal,
        ].includes(code) || (options.recoverable ?? []).includes(code);
      setProblem(
        uncertain
          ? options.retryable === false
            ? (options.unreplayable ?? "claude-usage.requestUnconfirmed")
            : "claude-usage.requestUnconfirmed"
          : failure,
      );
      // A request whose secret bytes were already zeroed cannot be replayed;
      // the user re-enters the value instead of retrying with a dead buffer.
      if (uncertain && options.retryable !== false)
        setRetry({ kind, send, options });
      else requests.current.delete(kind);
    } finally {
      inflight.current = false;
      setBusy(false);
      changed();
    }
  };
  const prepared = <T,>(kind: string, build: () => T): T => {
    if (!requests.current.has(kind)) requests.current.set(kind, build());
    return requests.current.get(kind) as T;
  };

  const start = () => {
    if (!account || !supported || !settled || inflight.current) return;
    void run(
      "start",
      async () => {
        const current = requests.current.has("start") ? account : await fresh();
        const request = prepared("start", () => ({
          mutation: {
            id: current.id,
            expectedRevision: current.revision,
            requestId: newRequestId(),
          },
          observedUsageGeneration: usage.generation,
        }));
        const result = await clients.subscription.startClaudeUsage(request);
        if (
          result.operationId !== request.mutation.requestId ||
          !isEntityId(result.operationId)
        )
          throw new Error("Invalid original operation");
        setHidden(false);
      },
      "claude-usage.state.failed",
    );
  };
  const cancel = () => {
    if (!operation || inflight.current) return;
    const target = operation;
    void run(
      "cancel:" + target.id,
      async () => {
        const current = await fresh();
        const request = prepared("cancel:" + target.id, () => ({
          mutation: {
            id: current.id,
            expectedRevision: current.revision,
            requestId: newRequestId(),
          },
          operationId: target.id,
          usageGeneration: target.generation,
        }));
        await clients.subscription.cancelClaudeUsage(request);
        setHidden(true);
      },
      "claude-usage.state.failed",
    );
  };
  const link = () => {
    if (!operation || inflight.current) return;
    const target = operation;
    void run(
      "link:" + target.id,
      async () => {
        const current = await fresh();
        const request = prepared("link:" + target.id, () => ({
          mutation: {
            id: current.id,
            expectedRevision: current.revision,
            requestId: newRequestId(),
          },
          operationId: target.id,
          usageGeneration: target.generation,
        }));
        await clients.subscription.confirmClaudeUsageLink(request);
        setHidden(true);
      },
      "claude-usage.state.failed",
    );
  };
  const disconnect = () => {
    if (inflight.current) return;
    void run(
      "disconnect",
      async () => {
        const current = await fresh();
        const request = prepared("disconnect", () => ({
          mutation: {
            id: current.id,
            expectedRevision: current.revision,
            requestId: newRequestId(),
          },
          usageGeneration: usage.generation,
        }));
        await clients.subscription.disconnectClaudeUsage(request);
        setDisconnecting(false);
      },
      "claude-usage.state.failed",
      { recoverable: [FailureCode.RecoveryRequired] },
    );
  };
  const submitCode = (value: string) => {
    if (!operation || !value || inflight.current) return;
    const target = operation;
    const code = new TextEncoder().encode(value);
    void run(
      "code:" + target.id,
      async () => {
        try {
          if (code.length > 16384 || !code.every((b) => b >= 33 && b <= 126))
            throw new Error("Invalid original input");
          const current = await fresh();
          const accepted = await clients.subscription.submitClaudeUsageCode({
            mutation: {
              id: current.id,
              expectedRevision: current.revision,
              requestId: newRequestId(),
            },
            operationId: target.id,
            usageGeneration: target.generation,
            code,
          });
          if (!accepted.accepted) throw new Error("Code receipt unconfirmed");
        } finally {
          code.fill(0);
        }
      },
      "claude-usage.state.failed",
      { retryable: false, unreplayable: "claude-usage.codeUnconfirmed" },
    );
  };
  const reopen = () => {
    const bound = binding.current;
    if (!native || !bound.url || !operation) return;
    void native(
      opening.current,
      OAuthNativeAction.ClaudeSubscriptionReopen,
      bound.generation,
      bound.operation,
      bound.url,
    ).catch(() => setProgress((p) => (p ? { ...p, browserFailed: true } : p)));
  };

  // Progress is read only while the login is shown. It never relaunches work.
  useEffect(() => {
    if (!active || !live || !operation || !account || hidden) return;
    let stopped = false;
    const poll = async () => {
      try {
        const result = await clients.subscription.getClaudeUsageProgress({
          accountId: account.id,
          operationId: operation.id,
          usageGeneration: operation.generation,
        });
        if (stopped) return;
        const waiting =
          result.state === ClaudeUsageLoginState.WAITING && !result.canceled;
        if (waiting) {
          if (
            !claudeUsageLoginURL(result.url) ||
            (binding.current.url && binding.current.url !== result.url)
          )
            throw new Error("Invalid original native URL");
          let openFailed = false;
          if (!binding.current.url && native) {
            binding.current = {
              operation: operation.id,
              url: result.url,
              generation: "",
            };
            try {
              const opened = await native(
                opening.current,
                OAuthNativeAction.ClaudeSubscriptionOpen,
                "",
                operation.id,
                result.url,
              );
              if (!stopped && isEntityId(opened.generation))
                binding.current.generation = opened.generation;
            } catch {
              openFailed = true;
            }
          }
          if (stopped) return;
          setProgress((previous) => ({
            operation: operation.id,
            url: result.url,
            codeRequested: result.codeRequested,
            canceled: result.canceled,
            browserFailed: previous?.browserFailed === true || openFailed,
          }));
          return;
        }
        setProgress((previous) => ({
          operation: operation.id,
          url: "",
          codeRequested: false,
          canceled: result.canceled,
          browserFailed: previous?.browserFailed === true,
        }));
      } catch {
        if (!stopped) setProblem("claude-usage.requestUnconfirmed");
      }
    };
    void poll();
    const timer = setInterval(() => void poll(), 1000);
    return () => {
      stopped = true;
      clearInterval(timer);
    };
  }, [active, live, operation?.id, operation?.phase, hidden, clients, native]);

  // Every confirming operation shows the confirmation view first, including a
  // reconnect whose previous connection is intentionally still linked.
  const view = !account
    ? ClaudeUsageView.Loading
    : operation?.phase === ClaudeUsageLoginPhase.Confirming
      ? ClaudeUsageView.Confirmation
      : usage.linked && !live
        ? ClaudeUsageView.Linked
        : live
          ? ClaudeUsageView.Progress
          : !settled
            ? ClaudeUsageView.Loading
            : !supported
              ? ClaudeUsageView.Unavailable
              : operation &&
                  operation.phase !== ClaudeUsageLoginPhase.Linked &&
                  endedMessages[operation.phase]
                ? ClaudeUsageView.Ended
                : ClaudeUsageView.Unlinked;
  const health = claudeUsageHealth(usage, now);
  const backoff = health === ClaudeUsageHealth.Backoff;
  /** The server only reads usage for a connection that is neither pending reconnection nor cleanup. */
  const unreadable =
    health === ClaudeUsageHealth.ReconnectRequired ||
    health === ClaudeUsageHealth.Cleanup;
  /** The shared Manage Refresh footer reads usage through this action. */
  const refresh = async () => {
    if (!account || !usage.linked || !supported || backoff || unreadable)
      return;
    const current = await fresh();
    await clients.subscription.requestSubscriptionObservation({
      mutation: {
        id: current.id,
        expectedRevision: current.revision,
        requestId: newRequestId(),
      },
      machineId: "",
      action: SubscriptionObservationAction.QUOTA,
      connectionId: text(object(document(current).connection).id),
      generationId: usage.generation,
    });
  };
  const dialogOpen = live && !hidden && Boolean(account);
  const section = (
    <ClaudeUsageSection
      view={view}
      usage={usage}
      health={health}
      now={now}
      active={active}
      data={data}
      busy={busy || !active}
      problem={problem}
      retry={
        retry
          ? () =>
              void run(
                retry.kind,
                retry.send,
                "claude-usage.state.failed",
                retry.options,
              )
          : undefined
      }
      dialogHidden={live && hidden}
      start={start}
      show={() => setHidden(false)}
      disconnect={() => setDisconnecting(true)}
    />
  );
  const dialog = dialogOpen ? (
    <SettingsTaskDialog
      title={copy("claude-usage.dialogTitle")}
      size={SettingsDialogSize.Wide}
      focus={SettingsDialogFocus.Input}
      retained={busy}
      close={() => setHidden(true)}
    >
      <ClaudeUsageLogin
        view={view}
        operation={operation!}
        progress={progress?.operation === operation?.id ? progress : undefined}
        busy={busy || !active}
        problem={problem}
        cancel={cancel}
        link={link}
        reopen={reopen}
        submit={submitCode}
        retry={
          retry
            ? () =>
                void run(
                  retry.kind,
                  retry.send,
                  "claude-usage.state.failed",
                  retry.options,
                )
            : undefined
        }
      />
    </SettingsTaskDialog>
  ) : null;
  const confirmation = disconnecting ? (
    <SettingsTaskDialog
      title={copy("claude-usage.disconnectTitle")}
      size={SettingsDialogSize.Confirmation}
      focus={SettingsDialogFocus.Cancel}
      close={() => setDisconnecting(false)}
    >
      <section aria-label={copy("claude-usage.disconnectTitle")}>
        <p>{copy("claude-usage.disconnectBody")}</p>
        <SettingsTaskActions className="">
          <SettingsActionButton
            icon={SettingsActionIcon.Delete}
            type="button"
            disabled={busy}
            onClick={disconnect}
          >
            {copy("claude-usage.disconnectConfirm")}
          </SettingsActionButton>
          <SettingsActionButton
            icon={SettingsActionIcon.Cancel}
            type="button"
            data-settings-task-cancel
            disabled={busy}
            onClick={() => setDisconnecting(false)}
          >
            {copy("claude-usage.keep")}
          </SettingsActionButton>
        </SettingsTaskActions>
      </section>
    </SettingsTaskDialog>
  ) : null;
  return {
    section,
    dialogs: (
      <>
        {dialog}
        {confirmation}
      </>
    ),
    view,
    health,
    usage,
    linked: usage.linked,
    /** Whether the shared footer may send a usage refresh now. */
    refreshAvailable:
      usage.linked && supported && settled && !backoff && !unreadable,
    /** Whether an active provider backoff disables the footer refresh. */
    paused: usage.linked && backoff,
    refresh,
    busy: busy || disconnecting || dialogOpen,
    workflow: live || disconnecting,
  };
}

function ClaudeUsageSection({
  view,
  usage,
  health,
  now,
  active,
  data,
  busy,
  problem,
  retry,
  dialogHidden,
  start,
  show,
  disconnect,
}: {
  view: ClaudeUsageView;
  usage: ClaudeUsageProjection;
  health: ClaudeUsageHealth;
  now: number;
  active: boolean;
  data: Record<string, unknown>;
  busy: boolean;
  problem?: MessageKey;
  retry?: () => void;
  dialogHidden: boolean;
  start: () => void;
  show: () => void;
  disconnect: () => void;
}) {
  useLocale();
  const windows = items(data.quota)
    .map((entry) => object(entry))
    .filter((window) => window.comparison_group === "claude");
  const heading = useId();
  const healthMessage: Partial<Record<ClaudeUsageHealth, MessageKey>> = {
    [ClaudeUsageHealth.Waiting]: "claude-usage.status.waitingFirst",
    [ClaudeUsageHealth.Reading]: "claude-usage.status.reading",
    [ClaudeUsageHealth.Stale]: "claude-usage.status.stale",
    [ClaudeUsageHealth.Failed]: "claude-usage.status.failed",
    [ClaudeUsageHealth.ReconnectRequired]: "claude-usage.status.reconnect",
    [ClaudeUsageHealth.Cleanup]: "claude-usage.status.cleanup",
  };
  return (
    <section
      className="claude-usage"
      aria-labelledby={heading}
      data-view={view}
      data-health={view === ClaudeUsageView.Linked ? health : undefined}
    >
      <div className="claude-usage-heading">
        <h3 id={heading}>{copy("claude-usage.heading")}</h3>
        {view === ClaudeUsageView.Linked ? (
          <span className="claude-usage-status" data-health={health}>
            {copy("claude-usage.connected")}
          </span>
        ) : null}
      </div>
      {view === ClaudeUsageView.Loading ? (
        <p role="status">{copy("claude-usage.loading")}</p>
      ) : null}
      {view === ClaudeUsageView.Unavailable ? (
        <p role="status">{copy("claude-usage.unavailable")}</p>
      ) : null}
      {view === ClaudeUsageView.Unlinked || view === ClaudeUsageView.Ended ? (
        <>
          {view === ClaudeUsageView.Ended && usage.operation ? (
            <>
              <p role="status">{copy(endedMessages[usage.operation.phase]!)}</p>
              {usage.operation.failure ? (
                <p>{copy(failureMessage(usage.operation.failure))}</p>
              ) : null}
            </>
          ) : (
            <p>{copy("claude-usage.description")}</p>
          )}
          <p className="claude-usage-note">{copy("claude-usage.unchanged")}</p>
          <SettingsActionButton
            icon={SettingsActionIcon.Connect}
            type="button"
            disabled={busy}
            onClick={start}
          >
            {copy("claude-usage.connect")}
          </SettingsActionButton>
        </>
      ) : null}
      {view === ClaudeUsageView.Progress ||
      view === ClaudeUsageView.Confirmation ? (
        <>
          <p role="status">
            {copy(
              view === ClaudeUsageView.Confirmation
                ? "claude-usage.confirmBody"
                : "claude-usage.preparing",
            )}
          </p>
          {dialogHidden ? (
            <SettingsActionButton
              icon={SettingsActionIcon.Retry}
              type="button"
              onClick={show}
            >
              {copy("claude-usage.viewOperation")}
            </SettingsActionButton>
          ) : null}
        </>
      ) : null}
      {view === ClaudeUsageView.Linked && usage.identity ? (
        <>
          <dl className="claude-usage-identity">
            <div>
              <dt>{copy("claude-usage.account")}</dt>
              <dd>{usage.identity.email}</dd>
            </div>
          </dl>
          <p className="claude-usage-note">
            {copy("claude-usage.linkedManually")}
            {usage.identity.plan
              ? ` · ${copy("claude-usage.plan", { v0: usage.identity.plan })}`
              : ""}
          </p>
          <div className="claude-usage-actions">
            <SettingsActionButton
              icon={SettingsActionIcon.Delete}
              type="button"
              disabled={busy}
              onClick={disconnect}
            >
              {copy("claude-usage.disconnect")}
            </SettingsActionButton>
            {usage.operation &&
            usage.reconnectRequired &&
            health === ClaudeUsageHealth.ReconnectRequired
              ? null
              : null}
            {health === ClaudeUsageHealth.ReconnectRequired ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Connect}
                type="button"
                disabled={busy}
                onClick={start}
              >
                {copy("claude-usage.reconnect")}
              </SettingsActionButton>
            ) : null}
          </div>
          {windows.length ? (
            <div className="claude-usage-windows">
              {windows.map((window) => (
                <QuotaWindow
                  key={text(window.id)}
                  now={now}
                  active={active}
                  window={{
                    id: text(window.id),
                    label: claudeUsageWindowLabel(text(window.id)),
                    state:
                      Object.values(QuotaObservationState).find(
                        (state) => state === window.state,
                      ) ?? QuotaObservationState.Unknown,
                    remaining:
                      typeof window.remaining === "number"
                        ? window.remaining
                        : undefined,
                    observedAt: text(window.observed_at),
                    resetAt: text(window.reset_at),
                  }}
                />
              ))}
            </div>
          ) : null}
          {health === ClaudeUsageHealth.Backoff ? (
            <p
              role="status"
              className="claude-usage-health"
              data-health={health}
            >
              {copy("claude-usage.status.backoff", {
                v0: new Date(usage.backoffUntil).toLocaleTimeString(),
              })}
            </p>
          ) : healthMessage[health] ? (
            <p
              role="status"
              className="claude-usage-health"
              data-health={health}
            >
              {copy(healthMessage[health]!)}
            </p>
          ) : null}
          {usage.failure && health !== ClaudeUsageHealth.Cleanup ? (
            <p className="claude-usage-health" data-health="failure">
              {copy(failureMessage(usage.failure))}
            </p>
          ) : null}
          {usage.lastSuccessAt ? (
            <small>
              <Timestamp active={active} value={usage.lastSuccessAt} />
            </small>
          ) : null}
        </>
      ) : null}
      {problem ? <p role="alert">{copy(problem)}</p> : null}
      {retry ? (
        <SettingsActionButton
          icon={SettingsActionIcon.Retry}
          type="button"
          disabled={busy}
          onClick={retry}
        >
          {copy("claude-usage.retry")}
        </SettingsActionButton>
      ) : null}
    </section>
  );
}

function ClaudeUsageLogin({
  view,
  operation,
  progress,
  busy,
  problem,
  cancel,
  link,
  reopen,
  submit,
  retry,
}: {
  view: ClaudeUsageView;
  operation: ClaudeUsageOperation;
  progress?: {
    url: string;
    codeRequested: boolean;
    canceled: boolean;
    browserFailed: boolean;
  };
  busy: boolean;
  problem?: MessageKey;
  cancel: () => void;
  link: () => void;
  reopen: () => void;
  submit: (code: string) => void;
  retry?: () => void;
}) {
  useLocale();
  const id = useId();
  const code = useRef<HTMLInputElement>(null);
  const confirming = view === ClaudeUsageView.Confirmation;
  const requested = progress?.codeRequested === true;
  // The single approval-code input takes focus the moment it is shown.
  useEffect(() => {
    if (requested) code.current?.focus({ preventScroll: true });
    return () => {
      if (code.current) code.current.value = "";
    };
  }, [requested]);
  return (
    <section
      className="claude-usage-login"
      aria-label={copy("claude-usage.dialogTitle")}
    >
      {confirming && operation.identity ? (
        <>
          <h3>{copy("claude-usage.confirmHeading")}</h3>
          <dl className="claude-usage-identity">
            <div>
              <dt>{copy("claude-usage.account")}</dt>
              <dd>{operation.identity.email}</dd>
            </div>
          </dl>
          <p>{copy("claude-usage.plan", { v0: operation.identity.plan })}</p>
          {operation.identity.organization ? (
            <p>
              {copy("claude-usage.organization", {
                v0: operation.identity.organization,
              })}
            </p>
          ) : null}
          <p>{copy("claude-usage.confirmBody")}</p>
          <SettingsTaskActions className="">
            <SettingsActionButton
              icon={SettingsActionIcon.Back}
              type="button"
              disabled={busy}
              onClick={cancel}
            >
              {copy("claude-usage.back")}
            </SettingsActionButton>
            <SettingsActionButton
              icon={SettingsActionIcon.Confirm}
              type="button"
              className="primary"
              disabled={busy}
              onClick={link}
            >
              {copy("claude-usage.link")}
            </SettingsActionButton>
          </SettingsTaskActions>
        </>
      ) : (
        <>
          <p role="status">
            {copy(
              operation.canceled || progress?.canceled
                ? "claude-usage.canceling"
                : progress?.url
                  ? "claude-usage.waiting"
                  : "claude-usage.preparing",
            )}
          </p>
          {progress?.url && !operation.canceled ? (
            <SettingsActionButton
              icon={SettingsActionIcon.Open}
              type="button"
              disabled={busy}
              onClick={reopen}
            >
              {copy("claude-usage.openBrowser")}
            </SettingsActionButton>
          ) : null}
          {progress?.browserFailed ? (
            <p role="alert">{copy("claude-usage.browserFailed")}</p>
          ) : null}
          {requested && !operation.canceled ? (
            <form
              onSubmit={(event) => {
                event.preventDefault();
                const value = code.current?.value ?? "";
                if (code.current) code.current.value = "";
                submit(value);
              }}
            >
              <label htmlFor={`${id}-code`}>
                {copy("claude-usage.codeLabel")}
              </label>
              <input
                id={`${id}-code`}
                ref={code}
                type="password"
                autoComplete="off"
                spellCheck={false}
                maxLength={16384}
                disabled={busy}
              />
              <p>{copy("claude-usage.codeHint")}</p>
              <SettingsActionButton
                icon={SettingsActionIcon.Confirm}
                className="primary"
                disabled={busy}
              >
                {copy("claude-usage.submitCode")}
              </SettingsActionButton>
            </form>
          ) : progress?.url && !requested && !operation.canceled ? (
            <p role="status">{copy("claude-usage.codeSubmitted")}</p>
          ) : null}
          <SettingsTaskActions className="">
            <SettingsActionButton
              icon={SettingsActionIcon.Cancel}
              type="button"
              disabled={busy || operation.canceled}
              onClick={cancel}
            >
              {copy("claude-usage.cancel")}
            </SettingsActionButton>
          </SettingsTaskActions>
        </>
      )}
      {problem ? <p role="alert">{copy(problem)}</p> : null}
      {retry ? (
        <SettingsActionButton
          icon={SettingsActionIcon.Retry}
          type="button"
          disabled={busy}
          onClick={retry}
        >
          {copy("claude-usage.retry")}
        </SettingsActionButton>
      ) : null}
    </section>
  );
}
