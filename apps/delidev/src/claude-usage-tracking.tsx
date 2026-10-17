// SPDX-License-Identifier: Apache-2.0
import "./claude-usage-tracking.css";
import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import {
  ClaudeUsageLoginState,
  FailureCode,
  SubscriptionObservationAction,
  SubscriptionQuery,
  SubscriptionService,
  SubscriptionServiceId,
  isEntityId,
  newRequestId,
  type GetClaudeUsageLoginProgressResponse,
  type NativeSubscriptionDiagnostic,
  type Resource,
  type SystemCapability,
} from "@delinoio/delidev-api-client";
import { OAuthNativeAction, useOAuthNativeControl } from "./account-oauth";
import { claudeLoginURL } from "./claude-subscription-login";
import {
  ClaudeUsageCapability,
  ClaudeUsageConnection,
  ClaudeUsageFailure,
  ClaudeUsageOperationState,
  ClaudeUsageRefreshPhase,
  ClaudeUsageView,
  claudeUsageCapability,
  claudeUsageFailureMessages,
  claudeUsageLoginMessages,
  claudeUsageLoginOperationState,
  claudeUsageQuotaWindows,
  claudeUsageSection,
  claudeUsageView,
  type ClaudeUsageProjection,
} from "./claude-usage-state";
import { document, object, text } from "./documents";
import { validInstallationVersion } from "./harness-version";
import { LocalizedText, copy, useLocale } from "./localization";
import { useRetainedMutation } from "./mutation";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import {
  SettingsDialogFocus,
  SettingsDialogSize,
  SettingsTaskActions,
  SettingsTaskDialog,
  SettingsTaskDismissButton,
} from "./settings-task";
import { useSettingsTaskVisible } from "./settings-task-context";
import { QuotaWindow } from "./subscription-settings";
import { serviceAccount } from "./subscription-resource";
import { Timestamp, TimestampMode } from "./timestamp-display";
import { Problem } from "./ui";

export { claudeUsageRowIdentity } from "./claude-usage-state";

const manualCallback = "https://platform.claude.com/oauth/code/callback";
const maxApprovalCode = 16384;
const progressPollMilliseconds = 1000;
const confirmationPollMilliseconds = 2000;
const clockMilliseconds = 15000;

/**
 * The usage login always runs the manual approval-code callback on the server.
 * Only the ordinary auth-login authorization URL with that callback may reach
 * the trusted native browser binding; a client localhost callback never does.
 */
export function claudeUsageAuthorizationURL(raw: string): boolean {
  if (!claudeLoginURL(raw)) return false;
  try {
    return new URL(raw).searchParams.get("redirect_uri") === manualCallback;
  } catch {
    return false;
  }
}

function useClock(enabled: boolean) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!enabled) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), clockMilliseconds);
    return () => clearInterval(timer);
  }, [enabled]);
  return now;
}

/**
 * Polls the actor-bound login progress without the shared query cache. The
 * reply carries the authorization address (OAuth transaction data) and the
 * reviewed identity, which are transient presentation data: they live only in
 * this component and vanish with it instead of surviving in the connection's
 * QueryClient for its garbage-collection lifetime. Reads never relaunch work.
 */
function useClaudeUsageProgress(request: {
  enabled: boolean;
  accountId: string;
  operationId: string;
  usageGeneration: string;
}) {
  const transport = useTransport();
  const client = useMemo(
    () => createClient(SubscriptionService, transport),
    [transport],
  );
  const [result, setResult] = useState<{
    data?: GetClaudeUsageLoginProgressResponse;
    error?: unknown;
  }>({});
  const { enabled, accountId, operationId, usageGeneration } = request;
  useEffect(() => {
    if (!enabled) {
      setResult({});
      return;
    }
    let canceled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      let next = progressPollMilliseconds;
      try {
        const data = await client.getClaudeUsageLoginProgress({
          accountId,
          operationId,
          usageGeneration,
        });
        if (canceled) return;
        setResult({ data });
        if (progressTerminal(data.state)) return;
        if (data.state === ClaudeUsageLoginState.AWAITING_CONFIRMATION)
          next = confirmationPollMilliseconds;
      } catch (error) {
        if (canceled) return;
        // A failed read keeps the last reply and is retried like any other poll.
        setResult((previous) => ({ data: previous.data, error }));
      }
      timer = setTimeout(() => void poll(), next);
    };
    void poll();
    return () => {
      canceled = true;
      if (timer) clearTimeout(timer);
    };
  }, [client, enabled, accountId, operationId, usageGeneration]);
  return result;
}

export const progressTerminal = (value?: ClaudeUsageLoginState) =>
  value !== undefined &&
  ![
    ClaudeUsageLoginState.PREPARING,
    ClaudeUsageLoginState.WAITING,
    ClaudeUsageLoginState.AWAITING_CONFIRMATION,
  ].includes(value);

interface Binding {
  opening: string;
  operation: string;
  generation: string;
}
export interface ClaudeUsageStatusRead {
  data?: { capabilities: readonly SystemCapability[] };
  error: unknown;
  isFetching: boolean;
  isSuccess: boolean;
}
export interface ClaudeUsageTrackingOptions {
  /** The verified current account; undefined while the account is loading. */
  account?: Resource;
  active: boolean;
  /** Only a server-token Claude account can hold a usage connection. */
  managed: boolean;
  status: ClaudeUsageStatusRead;
  /** Another account lifecycle operation owns admission. */
  externalBusy: boolean;
  /** Manage Refresh is running; it blocks actions but not its own closure. */
  refreshing: boolean;
  accept: (resource: Resource) => void;
  reread: () => void;
}
/** The retained-mutation surface the presentation needs. */
export interface RetainedControl {
  busy: boolean;
  uncertain: boolean;
  error: unknown;
  retry: () => unknown;
}
interface Review {
  email: string;
  organization: string;
  plan: string;
  commitment: string;
}
export interface ClaudeUsageModel {
  account: Resource;
  view: ClaudeUsageView;
  capability: ClaudeUsageCapability;
  usage?: ClaudeUsageProjection;
  review?: Review;
  reviewChanged: boolean;
  diagnostic?: NativeSubscriptionDiagnostic;
  now: number;
  active: boolean;
  connected: boolean;
  /** No operation owns admission and the capability read has settled. */
  ready: boolean;
  nativeAvailable: boolean;
  browserFailed: boolean;
  /** The server reported a waiting login whose address was not recognized. */
  urlInvalid: boolean;
  urlReady: boolean;
  codeRequested: boolean;
  codeSubmitted: boolean;
  progressProblem: boolean;
  progressError: unknown;
  pending: boolean;
  start: RetainedControl;
  code: RetainedControl;
  confirm: RetainedControl;
  cancel: RetainedControl;
  disconnect: RetainedControl;
  observe: RetainedControl;
  begin: () => void;
  submitCode: (value: string) => boolean;
  link: () => void;
  cancelLogin: () => void;
  /** Disconnects only the usage generation the confirmation reviewed. */
  disconnectUsage: (reviewedGeneration: string) => void;
  openBrowser: () => void;
}

/**
 * Owns the Usage tracking lifecycle of one Manage Claude dialog. Every mutation
 * is a retained original request, progress reads never relaunch work, and
 * closing the dialog only unmounts this presenter: the server keeps the login
 * until the user explicitly cancels it.
 */
export function useClaudeUsageTracking(options: ClaudeUsageTrackingOptions): {
  section: ReactNode;
  /** Defined only while a linked connection can take part in Manage Refresh. */
  refreshQuota?: () => Promise<void>;
  busy: boolean;
} {
  const {
    account,
    active,
    managed,
    status,
    externalBusy,
    refreshing,
    accept,
    reread,
  } = options;
  useLocale();
  const native = useOAuthNativeControl();
  const enabled = managed && Boolean(account);
  const now = useClock(active && enabled);
  const data = document(account),
    state = object(data.subscription),
    connectionId = text(object(data.connection).id),
    connected = isEntityId(connectionId),
    accountId = account?.id ?? "",
    section = enabled
      ? claudeUsageSection(state)
      : ({ kind: "absent" } as const);
  const capability = claudeUsageCapability(status);
  const settled = status.isSuccess && !status.error && !status.isFetching;
  const usage = section.kind === "present" ? section.usage : undefined;
  const operation = usage?.operation;
  const lifecycle =
    usage?.connection === ClaudeUsageConnection.Connecting ||
    usage?.connection === ClaudeUsageConnection.Confirming;
  const progress = useClaudeUsageProgress({
    enabled:
      active &&
      enabled &&
      capability === ClaudeUsageCapability.Available &&
      lifecycle &&
      Boolean(operation),
    accountId,
    operationId: operation?.id ?? "",
    usageGeneration: usage?.generation ?? "",
  });
  const reply =
    operation &&
    usage &&
    progress.data?.operationId === operation.id &&
    progress.data.usageGeneration === usage.generation &&
    progress.data.state !== ClaudeUsageLoginState.UNSPECIFIED
      ? progress.data
      : undefined;
  const reported = reply?.state;
  const view = claudeUsageView({
    capability,
    section,
    progress: reported,
    now,
  });
  const authorizationURL =
    reply?.state === ClaudeUsageLoginState.WAITING &&
    claudeUsageAuthorizationURL(reply.url)
      ? reply.url
      : "";
  const review: Review | undefined =
    reply?.state === ClaudeUsageLoginState.AWAITING_CONFIRMATION &&
    reply.identity?.email &&
    reply.identity.identityCommitment
      ? {
          email: reply.identity.email,
          organization: reply.identity.organization,
          plan: reply.identity.plan,
          commitment: reply.identity.identityCommitment,
        }
      : undefined;
  // The document identity is the one the server staged; a different reviewed
  // commitment means the review is stale and must be repeated.
  const reviewChanged = Boolean(
    review &&
    usage?.identity?.commitment &&
    review.commitment !== usage.identity.commitment,
  );

  const [startedHere, setStartedHere] = useState("");
  const [submitted, setSubmitted] = useState("");
  const accepted = (result: { account?: Resource }) => {
    if (result.account) accept(result.account);
    reread();
  };
  const verified = (
    result: { account?: Resource },
    request: { mutation?: { expectedRevision: bigint } },
  ) =>
    serviceAccount(
      result.account,
      accountId,
      SubscriptionServiceId.Claude,
      request.mutation?.expectedRevision ?? 1n,
    );
  const start = useRetainedMutation(
    `claude-usage:start:${accountId}`,
    SubscriptionQuery.startClaudeUsageLogin,
    (result) => {
      setStartedHere(result.operationId);
      accepted(result);
    },
    (result, request) =>
      isEntityId(result.operationId) &&
      isEntityId(result.usageGeneration) &&
      verified(result, request),
  );
  const code = useRetainedMutation(
    `claude-usage:code:${accountId}`,
    SubscriptionQuery.submitClaudeUsageApprovalCode,
    (_result, request) => {
      setSubmitted(request.operationId);
      reread();
    },
    (result) => result.accepted,
  );
  const confirm = useRetainedMutation(
    `claude-usage:confirm:${accountId}`,
    SubscriptionQuery.confirmClaudeUsageLink,
    accepted,
    verified,
  );
  const cancel = useRetainedMutation(
    `claude-usage:cancel:${accountId}`,
    SubscriptionQuery.cancelClaudeUsageLogin,
    accepted,
    verified,
  );
  const disconnect = useRetainedMutation(
    `claude-usage:disconnect:${accountId}`,
    SubscriptionQuery.disconnectClaudeUsage,
    accepted,
    verified,
  );
  const observe = useRetainedMutation(
    `claude-usage:observe:${accountId}`,
    SubscriptionQuery.requestSubscriptionObservation,
    (result) => accepted(result),
    (result, request) =>
      result.operationId === request.mutation?.requestId &&
      verified(result, request),
  );
  const mutations = [start, code, confirm, cancel, disconnect, observe];
  const pending = mutations.some((value) => value.busy || value.uncertain);
  // A terminal progress result can arrive before the account read delivers the
  // revised document. Every mutation captures the document revision, so actions
  // stay disabled until the document operation agrees with that result and a
  // fresh account read is requested meanwhile.
  const awaitingDocument =
    lifecycle &&
    reply !== undefined &&
    progressTerminal(reply.state) &&
    operation !== undefined &&
    claudeUsageLoginOperationState(reply.state) !== operation.state;
  const awaitedState = awaitingDocument ? reply?.state : undefined;
  useEffect(() => {
    if (awaitedState === undefined) return;
    reread();
    const timer = setInterval(reread, confirmationPollMilliseconds);
    return () => clearInterval(timer);
  }, [awaitedState, operation?.id]);
  const idle =
    active &&
    enabled &&
    capability === ClaudeUsageCapability.Available &&
    settled &&
    !externalBusy &&
    !awaitingDocument &&
    !pending;
  const ready = idle && !refreshing;

  // The trusted native binding opens the original authorization URL. It never
  // runs a callback listener and is disposed with the waiting login.
  const binding = useRef<Binding | undefined>(undefined);
  const opened = useRef("");
  const [browserFailed, setBrowserFailed] = useState(false);
  const latest = useRef({ url: "", operation: "" });
  latest.current = { url: authorizationURL, operation: operation?.id ?? "" };
  const disposeBinding = () => {
    const current = binding.current;
    binding.current = undefined;
    if (current && native)
      void native(current.opening, OAuthNativeAction.Dispose, "", "", "").catch(
        () => undefined,
      );
  };
  const openBrowser = () => {
    const { url, operation: id } = latest.current;
    if (!native || !url || !id) return;
    if (binding.current && binding.current.operation !== id) disposeBinding();
    const current = (binding.current ??= {
      opening: newRequestId(),
      operation: id,
      generation: "",
    });
    void (async () => {
      try {
        const result = await native(
          current.opening,
          current.generation
            ? OAuthNativeAction.ClaudeSubscriptionReopen
            : OAuthNativeAction.ClaudeSubscriptionOpen,
          current.generation,
          id,
          url,
        );
        if (binding.current !== current) return;
        if (
          !isEntityId(result.generation) ||
          (current.generation && result.generation !== current.generation)
        )
          throw new Error("Browser ownership changed");
        current.generation = result.generation;
        setBrowserFailed(false);
      } catch {
        // A failed binding is discarded so the next explicit open starts with
        // a fresh opening identity instead of reusing a half-open one.
        if (binding.current === current) {
          disposeBinding();
          setBrowserFailed(true);
        }
      }
    })();
  };
  const waiting = view === ClaudeUsageView.Waiting;
  useEffect(() => {
    if (!waiting) {
      disposeBinding();
      opened.current = "";
      setBrowserFailed(false);
      return;
    }
    // A hidden Manage task owns no browser: the binding is disposed and the
    // login never opens one automatically until the explicit open action.
    if (!active) {
      disposeBinding();
      if (operation) opened.current = operation.id;
      return;
    }
    // Only the client that started this login opens the browser by itself; a
    // reopened dialog offers the explicit Open browser again action.
    if (
      authorizationURL &&
      native &&
      operation &&
      startedHere === operation.id &&
      opened.current !== operation.id
    ) {
      opened.current = operation.id;
      openBrowser();
    }
  }, [waiting, active, authorizationURL, operation?.id, native, startedHere]);
  useEffect(
    () => () => {
      disposeBinding();
    },
    [native],
  );

  const mutation = () => ({
    id: accountId,
    expectedRevision: account?.revision ?? 0n,
    requestId: newRequestId(),
  });
  const model: ClaudeUsageModel | undefined = account && {
    account,
    view,
    capability,
    usage,
    review,
    reviewChanged,
    diagnostic: reply?.diagnostic,
    now,
    active,
    connected,
    ready,
    nativeAvailable: Boolean(native),
    browserFailed,
    urlInvalid:
      reply?.state === ClaudeUsageLoginState.WAITING && !authorizationURL,
    urlReady: Boolean(authorizationURL),
    codeRequested: reply?.codeRequested === true,
    codeSubmitted:
      operation?.codeSubmitted === true || submitted === operation?.id,
    progressProblem: Boolean(progress.error),
    progressError: progress.error,
    pending,
    start,
    code,
    confirm,
    cancel,
    disconnect,
    observe,
    begin: () => {
      if (
        !ready ||
        !native ||
        !connected ||
        ![
          ClaudeUsageView.NotConnected,
          ClaudeUsageView.LoginFailed,
          ClaudeUsageView.ReconnectRequired,
        ].includes(view)
      )
        return;
      void start.send({ mutation: mutation() });
    },
    submitCode: (value) => {
      const bytes = new TextEncoder().encode(value);
      try {
        if (
          !ready ||
          !operation ||
          !usage ||
          !waiting ||
          code.uncertain ||
          bytes.length === 0 ||
          bytes.length > maxApprovalCode ||
          !bytes.every((byte) => byte >= 33 && byte <= 126)
        )
          return false;
        void code.send({
          mutation: mutation(),
          operationId: operation.id,
          usageGeneration: usage.generation,
          code: bytes,
        });
        return true;
      } finally {
        // The retained request holds its own wire copy; clear this buffer.
        bytes.fill(0);
      }
    },
    link: () => {
      if (
        !ready ||
        !operation ||
        !usage ||
        !review ||
        reviewChanged ||
        view !== ClaudeUsageView.Confirmation
      )
        return;
      void confirm.send({
        mutation: mutation(),
        operationId: operation.id,
        usageGeneration: usage.generation,
        identityCommitment: review.commitment,
      });
    },
    cancelLogin: () => {
      if (
        !ready ||
        !operation ||
        !usage ||
        ![
          ClaudeUsageView.Preparing,
          ClaudeUsageView.Waiting,
          ClaudeUsageView.Confirmation,
          ClaudeUsageView.Recovery,
        ].includes(view)
      )
        return;
      void cancel.send({
        mutation: mutation(),
        operationId: operation.id,
        usageGeneration: usage.generation,
      });
    },
    disconnectUsage: (reviewedGeneration) => {
      if (
        !ready ||
        !usage ||
        usage.generation !== reviewedGeneration ||
        ![
          ClaudeUsageView.Linked,
          ClaudeUsageView.Stale,
          ClaudeUsageView.Backoff,
          ClaudeUsageView.RefreshFailed,
          ClaudeUsageView.ReconnectRequired,
        ].includes(view)
      )
        return;
      void disconnect.send({
        mutation: mutation(),
        usageGeneration: usage.generation,
      });
    },
    openBrowser,
  };

  // Manage Refresh stays the only refresh action. While reads are paused it
  // rereads the account without asking the server to fetch usage again.
  const backoffActive =
    usage?.backoffUntil !== undefined && Date.parse(usage.backoffUntil) > now;
  const refreshable =
    idle &&
    connected &&
    usage?.connection === ClaudeUsageConnection.Linked &&
    usage.published &&
    usage.refresh === ClaudeUsageRefreshPhase.Idle;
  const generation = text(state.generation);
  const refreshQuota =
    refreshable && account
      ? async () => {
          if (backoffActive || !isEntityId(generation)) return;
          await observe.send({
            mutation: {
              id: account.id,
              expectedRevision: account.revision,
              requestId: newRequestId(),
            },
            machineId: "",
            action: SubscriptionObservationAction.QUOTA,
            connectionId,
            generationId: generation,
          });
        }
      : undefined;
  return {
    section: model && enabled ? <ClaudeUsageSection model={model} /> : null,
    refreshQuota,
    busy: pending,
  };
}

function diagnosticValid(d?: NativeSubscriptionDiagnostic) {
  return Boolean(
    d &&
    (!d.detectedVersion || validInstallationVersion(d.detectedVersion)) &&
    d.phase >= 1 &&
    d.phase <= 9 &&
    Object.values(FailureCode).includes(d.code as FailureCode) &&
    isEntityId(d.correlationId),
  );
}

function ClaudeUsageSection({ model }: { model: ClaudeUsageModel }) {
  useLocale();
  const id = useId(),
    heading = useRef<HTMLHeadingElement>(null),
    code = useRef<HTMLInputElement>(null),
    visible = useSettingsTaskVisible();
  // The confirmation is bound to the usage generation it was opened for.
  const [confirmation, setConfirmation] = useState<string>(),
    [invalidCode, setInvalidCode] = useState(false);
  const { view, usage } = model;
  const busy = !model.ready;
  const showCode =
    view === ClaudeUsageView.Waiting &&
    model.codeRequested &&
    !model.codeSubmitted;
  // The single approval-code input takes focus when it first appears.
  useEffect(() => {
    if (showCode) code.current?.focus({ preventScroll: true });
  }, [showCode]);
  // Hiding the dialog or leaving the waiting step discards the typed code.
  useEffect(() => {
    if ((!visible || !showCode) && code.current) code.current.value = "";
  }, [visible, showCode]);
  // A control that unmounts with its state change must not strand keyboard
  // focus on the document body: return it to this section's heading.
  const previous = useRef(view);
  useEffect(() => {
    if (previous.current === view) return;
    previous.current = view;
    const focused = globalThis.document.activeElement;
    if (!focused || focused === globalThis.document.body)
      heading.current?.focus({ preventScroll: true });
  }, [view]);
  useEffect(() => {
    if (!model.active || !visible) setConfirmation(undefined);
  }, [model.active, visible]);
  // Another client may disconnect and reconnect usage while the confirmation is
  // open: a different generation closes it and needs a fresh confirmation.
  useEffect(() => {
    setConfirmation((reviewed) =>
      reviewed !== undefined && reviewed !== usage?.generation
        ? undefined
        : reviewed,
    );
  }, [usage?.generation]);

  const linkedFamily = [
    ClaudeUsageView.Linked,
    ClaudeUsageView.Stale,
    ClaudeUsageView.Backoff,
    ClaudeUsageView.RefreshFailed,
  ].includes(view);
  const retry = (operation: RetainedControl) =>
    operation.uncertain ? (
      <SettingsActionButton
        icon={SettingsActionIcon.Retry}
        type="button"
        disabled={operation.busy || !model.active}
        onClick={operation.retry}
      >
        {copy("claude-usage.retryOriginal")}
      </SettingsActionButton>
    ) : null;
  const connectAction = (
    label:
      | "claude-usage.connect"
      | "claude-usage.reconnect"
      | "claude-usage.tryAgain",
  ) => (
    <SettingsActionButton
      icon={SettingsActionIcon.Connect}
      type="button"
      disabled={busy || !model.connected || !model.nativeAvailable}
      onClick={model.begin}
    >
      {copy(label)}
    </SettingsActionButton>
  );
  const disconnectAction = (
    <SettingsActionButton
      icon={SettingsActionIcon.Delete}
      type="button"
      disabled={busy}
      onClick={() => {
        if (!busy && usage) setConfirmation(usage.generation);
      }}
    >
      {copy("claude-usage.disconnect")}
    </SettingsActionButton>
  );
  const identity = (review: {
    email: string;
    organization: string;
    plan: string;
  }) => (
    <dl className="claude-usage-identity">
      <div>
        <dt>{copy("claude-usage.email")}</dt>
        <dd>{review.email}</dd>
      </div>
      {review.organization ? (
        <div>
          <dt>{copy("claude-usage.organization")}</dt>
          <dd>{review.organization}</dd>
        </div>
      ) : null}
      {review.plan ? (
        <div>
          <dt>{copy("claude-usage.plan")}</dt>
          <dd>{review.plan}</dd>
        </div>
      ) : null}
    </dl>
  );
  const evidence = usage ? claudeUsageQuotaWindows(usage) : [];
  const backoffActive =
    usage?.backoffUntil !== undefined &&
    Date.parse(usage.backoffUntil) > model.now;

  let body: ReactNode = null;
  switch (view) {
    case ClaudeUsageView.Loading:
      body = <p role="status">{copy("claude-usage.loading")}</p>;
      break;
    case ClaudeUsageView.Unavailable:
      body = <p role="alert">{copy("claude-usage.unavailable")}</p>;
      break;
    case ClaudeUsageView.Unreadable:
      body = <p role="alert">{copy("claude-usage.unreadable")}</p>;
      break;
    case ClaudeUsageView.UpdateRequired:
      body = (
        <>
          <p role="status">{copy("claude-usage.updateRequired")}</p>
          <p className="claude-usage-note">
            {copy("claude-usage.executionUnchanged")}
          </p>
        </>
      );
      break;
    case ClaudeUsageView.NotConnected:
      body = (
        <>
          <p>{copy("claude-usage.description")}</p>
          <p className="claude-usage-note">
            {copy("claude-usage.executionUnchanged")}
          </p>
          {!model.connected ? (
            <p className="claude-usage-note">
              {copy("claude-usage.needsConnectedAccount")}
            </p>
          ) : null}
          {!model.nativeAvailable ? (
            <p className="claude-usage-note">
              {copy("claude-usage.browserUnavailable")}
            </p>
          ) : null}
          <div className="actions claude-usage-actions">
            {connectAction("claude-usage.connect")}
            {retry(model.start)}
          </div>
        </>
      );
      break;
    case ClaudeUsageView.Preparing:
      body = (
        <>
          <p role="status">{copy("claude-usage.preparing")}</p>
          <div className="actions claude-usage-actions">
            <SettingsActionButton
              icon={SettingsActionIcon.Cancel}
              type="button"
              disabled={busy}
              onClick={model.cancelLogin}
            >
              {copy("claude-usage.cancel")}
            </SettingsActionButton>
            {retry(model.cancel)}
          </div>
        </>
      );
      break;
    case ClaudeUsageView.Waiting:
      body = (
        <>
          <p role="status">{copy("claude-usage.waiting")}</p>
          <p className="claude-usage-note">
            {copy("claude-usage.waitingBody")}
          </p>
          {model.urlInvalid ? (
            <p role="alert">{copy("claude-usage.urlInvalid")}</p>
          ) : null}
          {model.browserFailed ? (
            <p role="alert">{copy("claude-usage.browserFailed")}</p>
          ) : null}
          <div className="actions claude-usage-actions">
            <SettingsActionButton
              icon={SettingsActionIcon.Open}
              type="button"
              disabled={busy || !model.nativeAvailable || !model.urlReady}
              onClick={model.openBrowser}
            >
              {copy("claude-usage.openBrowser")}
            </SettingsActionButton>
          </div>
          {model.codeRequested || model.codeSubmitted ? (
            <form
              className="claude-usage-code"
              onSubmit={(event) => {
                event.preventDefault();
                const value = code.current?.value ?? "";
                if (!value || busy || model.codeSubmitted) return;
                if (model.submitCode(value)) {
                  setInvalidCode(false);
                  if (code.current) code.current.value = "";
                } else setInvalidCode(true);
              }}
            >
              <label htmlFor={`${id}-code`}>
                {copy("claude-usage.approvalCode")}
              </label>
              <input
                id={`${id}-code`}
                ref={code}
                type="password"
                autoComplete="off"
                spellCheck={false}
                maxLength={maxApprovalCode}
                placeholder={copy("claude-usage.codePlaceholder")}
                disabled={busy || model.codeSubmitted}
              />
              <p className="claude-usage-note">
                {copy(
                  model.codeSubmitted
                    ? "claude-usage.codeSubmitted"
                    : "claude-usage.codeHelp",
                )}
              </p>
              {invalidCode ? (
                <p role="alert">{copy("claude-usage.codeInvalid")}</p>
              ) : null}
              <div className="actions claude-usage-actions">
                <SettingsActionButton
                  icon={SettingsActionIcon.Confirm}
                  className="primary"
                  disabled={busy || model.codeSubmitted}
                >
                  {copy("claude-usage.submitCode")}
                </SettingsActionButton>
                {retry(model.code)}
              </div>
            </form>
          ) : null}
          <div className="actions claude-usage-actions">
            <SettingsActionButton
              icon={SettingsActionIcon.Cancel}
              type="button"
              disabled={busy}
              onClick={model.cancelLogin}
            >
              {copy("claude-usage.cancel")}
            </SettingsActionButton>
            {retry(model.cancel)}
          </div>
        </>
      );
      break;
    case ClaudeUsageView.Confirmation: {
      const review = model.review;
      body = (
        <>
          <h4>{copy("claude-usage.confirmTitle")}</h4>
          {review
            ? identity(review)
            : usage?.identity
              ? identity(usage.identity)
              : null}
          {!review ? (
            <p role="status">{copy("claude-usage.reviewLoading")}</p>
          ) : null}
          <p>{copy("claude-usage.confirmBody")}</p>
          {model.reviewChanged ? (
            <p role="alert">{copy("claude-usage.reviewChanged")}</p>
          ) : null}
          <p className="claude-usage-note">{copy("claude-usage.backHelp")}</p>
          <div className="actions claude-usage-actions">
            <SettingsActionButton
              icon={SettingsActionIcon.Back}
              type="button"
              disabled={busy}
              onClick={model.cancelLogin}
            >
              {copy("claude-usage.back")}
            </SettingsActionButton>
            <SettingsActionButton
              icon={SettingsActionIcon.Confirm}
              className="primary"
              type="button"
              disabled={busy || !review || model.reviewChanged}
              onClick={model.link}
            >
              {copy("claude-usage.link")}
            </SettingsActionButton>
            {retry(model.confirm)}
            {retry(model.cancel)}
          </div>
        </>
      );
      break;
    }
    case ClaudeUsageView.LoginFailed:
    case ClaudeUsageView.Recovery: {
      const state = usage?.operation?.state;
      const reported =
        state && state !== ClaudeUsageOperationState.Succeeded
          ? claudeUsageLoginMessages[state]
          : undefined;
      body = (
        <>
          <p role="alert">{copy(reported ?? "claude-usage.login.failed")}</p>
          {diagnosticValid(model.diagnostic) ? (
            <dl className="claude-usage-diagnostic">
              <div>
                <dt>{copy("claude-subscription.version")}</dt>
                <dd>
                  {model.diagnostic!.detectedVersion ||
                    copy("claude-subscription.notDetected")}
                </dd>
              </div>
              <div>
                <dt>{copy("claude-subscription.failureCode")}</dt>
                <dd>{model.diagnostic!.code}</dd>
              </div>
            </dl>
          ) : null}
          <div className="actions claude-usage-actions">
            {view === ClaudeUsageView.Recovery ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Cancel}
                type="button"
                disabled={busy}
                onClick={model.cancelLogin}
              >
                {copy("claude-usage.cancel")}
              </SettingsActionButton>
            ) : (
              connectAction("claude-usage.tryAgain")
            )}
            {retry(model.start)}
            {retry(model.cancel)}
          </div>
        </>
      );
      break;
    }
    case ClaudeUsageView.ReconnectRequired:
      body = (
        <>
          <p role="alert">{copy("claude-usage.reconnectRequired")}</p>
          {usage?.refresh === ClaudeUsageRefreshPhase.RotationUncertain ? (
            <p role="alert">{copy("claude-usage.rotationUncertain")}</p>
          ) : null}
          {usage?.failure ? (
            <p>{copy(claudeUsageFailureMessages[usage.failure])}</p>
          ) : null}
          {evidence.length ? (
            <div className="claude-usage-quotas">
              {evidence.map((window) => (
                <QuotaWindow
                  key={window.id}
                  window={window}
                  now={model.now}
                  active={model.active}
                />
              ))}
            </div>
          ) : null}
          <div className="actions claude-usage-actions">
            {connectAction("claude-usage.reconnect")}
            {disconnectAction}
            {retry(model.start)}
            {retry(model.disconnect)}
          </div>
        </>
      );
      break;
    default:
      body =
        linkedFamily && usage ? (
          <>
            <p
              className="claude-usage-status"
              role={view === ClaudeUsageView.RefreshFailed ? "alert" : "status"}
              data-tone={
                view === ClaudeUsageView.Linked
                  ? "success"
                  : view === ClaudeUsageView.RefreshFailed
                    ? "danger"
                    : "warning"
              }
            >
              {copy(
                view === ClaudeUsageView.Backoff
                  ? "claude-usage.status.paused"
                  : view === ClaudeUsageView.Stale
                    ? "claude-usage.status.stale"
                    : view === ClaudeUsageView.RefreshFailed
                      ? "claude-usage.status.failed"
                      : "claude-usage.status.connected",
              )}
            </p>
            {usage.identity ? (
              <div className="claude-usage-account">
                <div>
                  <strong>{copy("claude-usage.account")}</strong>
                  {identity(usage.identity)}
                  <p className="claude-usage-note">
                    {copy("claude-usage.linkedManually")}
                  </p>
                </div>
                {disconnectAction}
              </div>
            ) : null}
            {evidence.length ? (
              <div className="claude-usage-quotas">
                {evidence.map((window) => (
                  <QuotaWindow
                    key={window.id}
                    window={window}
                    now={model.now}
                    active={model.active}
                  />
                ))}
              </div>
            ) : (
              <p className="claude-usage-note">
                {copy("claude-usage.noObservation")}
              </p>
            )}
            {usage.lastSuccessAt ? (
              <p className="claude-usage-note">
                <LocalizedText
                  id="claude-usage.updated"
                  components={{
                    s0: (
                      <Timestamp
                        value={usage.lastSuccessAt}
                        mode={TimestampMode.Relative}
                        active={model.active}
                      />
                    ),
                  }}
                />
              </p>
            ) : null}
            {view === ClaudeUsageView.Stale ? (
              <p>{copy("claude-usage.staleNote")}</p>
            ) : null}
            {backoffActive && usage.backoffUntil ? (
              <p>
                <LocalizedText
                  id="claude-usage.backoff"
                  components={{
                    s0: (
                      <Timestamp
                        value={usage.backoffUntil}
                        active={model.active}
                      />
                    ),
                  }}
                />
              </p>
            ) : null}
            {usage.failure !== ClaudeUsageFailure.None ? (
              <p
                role={
                  view === ClaudeUsageView.RefreshFailed ? "alert" : undefined
                }
              >
                {copy(claudeUsageFailureMessages[usage.failure])}
              </p>
            ) : null}
            {usage.refresh === ClaudeUsageRefreshPhase.Renewing ? (
              <p role="status">{copy("claude-usage.renewing")}</p>
            ) : null}
            {usage.refresh === ClaudeUsageRefreshPhase.RotationUncertain ? (
              <p role="alert">{copy("claude-usage.rotationUncertain")}</p>
            ) : null}
            <div className="actions claude-usage-actions">
              {retry(model.disconnect)}
              {retry(model.observe)}
            </div>
          </>
        ) : null;
  }

  return (
    <section
      className="claude-usage"
      data-state={view}
      aria-labelledby={`${id}-title`}
    >
      <h3 id={`${id}-title`} ref={heading} tabIndex={-1}>
        {copy("claude-usage.title")}
      </h3>
      {body}
      {model.progressProblem && model.progressError ? (
        <p role="alert">{copy("claude-usage.statusUnconfirmed")}</p>
      ) : null}
      <Problem
        error={
          model.start.error ||
          model.code.error ||
          model.confirm.error ||
          model.cancel.error ||
          model.disconnect.error ||
          model.observe.error
        }
        summary={<p>{copy("claude-usage.requestFailed")}</p>}
      />
      {confirmation !== undefined && confirmation === usage?.generation ? (
        <SettingsTaskDialog
          title={copy("claude-usage.disconnectTitle")}
          size={SettingsDialogSize.Confirmation}
          focus={SettingsDialogFocus.Cancel}
          close={() => setConfirmation(undefined)}
        >
          <section aria-label={copy("claude-usage.disconnectConfirmation")}>
            <p>{copy("claude-usage.disconnectBody")}</p>
            <SettingsTaskActions className="">
              <SettingsActionButton
                icon={SettingsActionIcon.Confirm}
                type="button"
                disabled={busy}
                onClick={() => {
                  if (busy) return;
                  model.disconnectUsage(confirmation);
                  setConfirmation(undefined);
                }}
              >
                {copy("claude-usage.confirmDisconnect")}
              </SettingsActionButton>
              <SettingsTaskDismissButton
                data-settings-task-cancel
                type="button"
                onClick={() => setConfirmation(undefined)}
              >
                {copy("claude-usage.keepTracking")}
              </SettingsTaskDismissButton>
            </SettingsTaskActions>
          </section>
        </SettingsTaskDialog>
      ) : null}
    </section>
  );
}
