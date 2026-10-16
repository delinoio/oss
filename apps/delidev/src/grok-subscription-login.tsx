// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import {
  ConfigurationService,
  EntityKind,
  FailureCode,
  ResourceService,
  SubscriptionAction,
  SubscriptionLoginState,
  SubscriptionService,
  SubscriptionServiceId,
  clientFailure,
  isEntityId,
  newRequestId,
  type GrokDiagnostic,
  type Resource,
} from "@delinoio/delidev-api-client";
import "./wizard-presentation.css";
import { OAuthNativeAction, useOAuthNativeControl } from "./account-oauth";
import { document, encode, object, text } from "./documents";
import { copy, useLocale, type MessageKey } from "./localization";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { useSettingsOpening } from "./settings-lifetime";
import { SettingsTaskActions } from "./settings-task";
import {
  useCloseSettingsTask,
  useSettingsTaskVisible,
} from "./settings-task-context";
import { GrokDiagnosticNotice } from "./grok-diagnostic";
import { subscriptionNameValid } from "./subscription-onboarding";
import {
  ProgressDisposition,
  progressDisposition,
} from "./subscription-progress";
import {
  serviceAccount,
  subscriptionAliasDocument,
} from "./subscription-resource";
import {
  GrokCopyEvent,
  GrokCopyPhase,
  GrokCopyTarget,
  GrokHelp,
  GrokLoginEvent,
  GrokLifetimeTrigger,
  GrokLoginMethod,
  GrokLoginStage,
  GrokNameField,
  GrokNativeBinding,
  GrokRetry,
  grokCopyAfterPhase,
  grokCopyOffered,
  grokCopyTransition,
  grokLifetimeCells,
  grokLoginPresentation,
  grokPollEvent,
  grokRetryOutcomes,
  initialGrokCopyState,
  initialGrokLoginState,
  transition,
  type GrokCopyState,
  type GrokLoginState,
} from "./grok-login-machine";
import "./subscription-onboarding.css";

export { GrokLoginMethod, GrokLoginStage };
// A response whose receipt this controller rejects locally: the account, alias,
// operation, revision or ownership it carries is not the one that was
// requested. The server already answered, so replaying the same request would
// only reproduce the same rejected receipt. The failure is definite: it ends
// the attempt with the mutation's definite-failure notice and retains no
// request. Any other local or transport failure keeps its uncertain meaning.
class GrokReceiptRejected extends Error {}
// The trusted native receiver rejects with its kebab-case failure name.
enum NativeFailureReason {
  Incompatible = "incompatible",
}
const copyNotices: Record<
  GrokCopyTarget,
  { copied: MessageKey; failed: MessageKey }
> = {
  [GrokCopyTarget.Link]: {
    copied: "grok-subscription.linkCopied",
    failed: "grok-subscription.copyFailed",
  },
  [GrokCopyTarget.Code]: {
    copied: "grok-subscription.device.codeCopied",
    failed: "grok-subscription.device.codeCopyFailed",
  },
  [GrokCopyTarget.Address]: {
    copied: "grok-subscription.device.addressCopied",
    failed: "grok-subscription.device.addressCopyFailed",
  },
};
// The account is created with this alias and keeps it until the naming step.
const grokDefaultAlias = "Grok";
const copiedNoticeMilliseconds = 4000;
const pollIntervalMilliseconds = 1000;

const stages: Partial<Record<SubscriptionLoginState, GrokLoginStage>> = {
  [SubscriptionLoginState.PREPARING]: GrokLoginStage.Preparing,
  [SubscriptionLoginState.WAITING]: GrokLoginStage.Waiting,
  [SubscriptionLoginState.SUCCEEDED]: GrokLoginStage.Naming,
  [SubscriptionLoginState.CANCELED]: GrokLoginStage.Canceled,
  [SubscriptionLoginState.EXPIRED]: GrokLoginStage.Expired,
  [SubscriptionLoginState.UNSUPPORTED]: GrokLoginStage.Unsupported,
  [SubscriptionLoginState.RECOVERY_REQUIRED]: GrokLoginStage.Recovery,
  [SubscriptionLoginState.FAILED]: GrokLoginStage.Failed,
};
const statusKeys: Record<GrokLoginStage, MessageKey> = {
  [GrokLoginStage.Preparing]: "grok-subscription.status.preparing",
  [GrokLoginStage.Waiting]: "grok-subscription.status.waiting",
  [GrokLoginStage.Naming]: "grok-subscription.status.success",
  [GrokLoginStage.Canceled]: "grok-subscription.status.canceled",
  [GrokLoginStage.Expired]: "grok-subscription.status.expired",
  [GrokLoginStage.Unsupported]: "grok-subscription.status.unsupported",
  [GrokLoginStage.Recovery]: "grok-subscription.status.recovery",
  [GrokLoginStage.Failed]: "grok-subscription.status.failed",
};
const deviceHelpKeys: Partial<Record<GrokLoginStage, MessageKey>> = {
  [GrokLoginStage.Canceled]: "grok-subscription.device.help.canceled",
  [GrokLoginStage.Expired]: "grok-subscription.device.help.expired",
  [GrokLoginStage.Unsupported]: "grok-subscription.device.help.unsupported",
  [GrokLoginStage.Recovery]: "grok-subscription.help.recovery",
  [GrokLoginStage.Failed]: "grok-subscription.device.help.failed",
};
const helpKeys: Partial<Record<GrokLoginStage, MessageKey>> = {
  [GrokLoginStage.Waiting]: "grok-subscription.help.waiting",
  [GrokLoginStage.Canceled]: "grok-subscription.help.canceled",
  [GrokLoginStage.Expired]: "grok-subscription.help.expired",
  [GrokLoginStage.Unsupported]: "grok-subscription.help.unsupported",
  [GrokLoginStage.Recovery]: "grok-subscription.help.recovery",
  [GrokLoginStage.Failed]: "grok-subscription.help.failed",
};

const grokClientId = "b1a00492-073a-47ea-816f-4c329264a828";
const grokQueryKeys = [
  "response_type",
  "client_id",
  "redirect_uri",
  "scope",
  "code_challenge",
  "code_challenge_method",
  "state",
  "nonce",
  "referrer",
];
const deviceCodePattern = /^[A-Z0-9-]{4,32}$/;
const opaque = (value: string) => /^[A-Za-z0-9._~-]{1,512}$/.test(value);

// Mirrors the trusted native receiver's fixed xAI profile. The server owns PKCE
// and the loopback listener; the desktop only validates and opens the address.
export function grokLoginURL(raw: string): boolean {
  try {
    // URL.href keeps an empty "#" and the native parser rejects it, so the
    // delimiter itself is refused here.
    if (raw.length > 4096 || !/^[!-~]+$/.test(raw) || raw.includes("#"))
      return false;
    const url = new URL(raw),
      query = url.searchParams;
    if (
      url.href !== raw ||
      url.protocol !== "https:" ||
      url.host !== "auth.x.ai" ||
      url.username ||
      url.password ||
      url.hash ||
      url.pathname !== "/oauth2/authorize"
    )
      return false;
    if (
      [...query.keys()].some(
        (key) => !grokQueryKeys.includes(key) || query.getAll(key).length !== 1,
      )
    )
      return false;
    const port = /^http:\/\/127\.0\.0\.1:([1-9][0-9]{0,4})\/callback$/.exec(
      query.get("redirect_uri") ?? "",
    )?.[1];
    const scope = query.get("scope");
    return (
      port !== undefined &&
      Number(port) >= 1024 &&
      Number(port) <= 65535 &&
      query.get("response_type") === "code" &&
      query.get("client_id") === grokClientId &&
      query.get("code_challenge_method") === "S256" &&
      /^[A-Za-z0-9_-]{43}$/.test(query.get("code_challenge") ?? "") &&
      opaque(query.get("state") ?? "") &&
      opaque(query.get("nonce") ?? "") &&
      (!query.has("referrer") || query.get("referrer") === "grok-build") &&
      (scope === null ||
        (scope.length > 0 &&
          scope.length <= 1024 &&
          scope.split(" ").every((part) => /^[A-Za-z0-9:_.-]+$/.test(part))))
    );
  } catch {
    return false;
  }
}

// Validates the transient device verification address and user code pair. The
// address must be the fixed xAI device page; an embedded user_code must equal
// the displayed code so the page and the screen can never disagree. A query
// delimiter requires exactly that one parameter: URL.href keeps an empty "?" or
// "#" while the native parser rejects both, so neither may pass here.
export function grokDeviceVerification(raw: string, code: string): boolean {
  try {
    if (raw.length > 512 || !/^[!-~]+$/.test(raw) || raw.includes("#"))
      return false;
    if (!deviceCodePattern.test(code)) return false;
    const url = new URL(raw),
      query = url.searchParams;
    if (
      url.href !== raw ||
      url.protocol !== "https:" ||
      url.host !== "accounts.x.ai" ||
      url.username ||
      url.password ||
      url.hash ||
      url.pathname !== "/oauth2/device"
    )
      return false;
    if (!raw.includes("?")) return true;
    const keys = [...query.keys()];
    return (
      keys.length === 1 &&
      keys[0] === "user_code" &&
      query.get("user_code") === code
    );
  } catch {
    return false;
  }
}

// The machine owns everything that decides what the screen offers; this view
// only adds presentation values that never gate an action.
interface View {
  machine: GrokLoginState;
  name: string;
  suggested: boolean;
  busy: boolean;
  diagnostic?: GrokDiagnostic;
  // Transient device display values, never persisted, logged or cached. The
  // machine's presentation decides whether either is shown: the code while the
  // device login waits, the validated address only after the opener failed.
  userCode?: string;
  address?: string;
  // The copy attempt state of the machine's copy table; the notice is shown
  // exactly while it is Copied or Failed.
  copy: GrokCopyState;
}
interface Pending {
  opening: string;
  generation: string;
  account?: Resource;
  operation: string;
  url: string;
  userCode: string;
  bound: boolean;
  callbackDispatched: boolean;
  disposed: boolean;
  polling: boolean;
  busy: boolean;
  // The single transition-table state; see grok-login-machine.ts.
  machine: GrokLoginState;
  // The copy attempt table state and the timer of its visible notice.
  copy: GrokCopyState;
  copyTimer?: ReturnType<typeof setTimeout>;
  // The exact original request of the retained mutation. It is held exactly
  // while machine.retry is not None and replays only through Retry original
  // request.
  retry?: () => Promise<void>;
}

export function useGrokSubscriptionLogin(active: boolean, changed: () => void) {
  useLocale();
  const transport = useTransport(),
    opening = useSettingsOpening(),
    native = useOAuthNativeControl();
  const clients = useMemo(
    () => ({
      configuration: createClient(ConfigurationService, transport),
      resource: createClient(ResourceService, transport),
      subscription: createClient(SubscriptionService, transport),
    }),
    [transport],
  );
  const [view, setView] = useState<View>();
  const pending = useRef<Pending | undefined>(undefined);
  const live = (p: Pending) =>
    pending.current === p && !p.disposed && !opening?.disposed;
  const update = (p: Pending, change: Partial<View>) => {
    if (live(p)) setView((current) => current && { ...current, ...change });
  };
  const disposeNative = (p: Pending) => {
    if (native)
      void native(p.opening, OAuthNativeAction.Dispose, "", "", "").catch(
        () => undefined,
      );
  };
  // Installs the next copy attempt state. The notice timer belongs to the
  // state it was armed for, so every change replaces it.
  const setCopy = (p: Pending, next: GrokCopyState): Partial<View> => {
    if (next === p.copy) return {};
    p.copy = next;
    if (p.copyTimer) clearTimeout(p.copyTimer);
    p.copyTimer = undefined;
    if (
      next.phase === GrokCopyPhase.Copied ||
      next.phase === GrokCopyPhase.Failed
    ) {
      const attempt = next.attempt;
      p.copyTimer = setTimeout(
        () => dispatchCopy(p, GrokCopyEvent.NoticeExpired, attempt),
        copiedNoticeMilliseconds,
      );
    }
    return { copy: next };
  };
  const dispatchCopy = (p: Pending, event: GrokCopyEvent, attempt: number) => {
    if (live(p))
      update(p, setCopy(p, grokCopyTransition(p.copy, event, { attempt })));
  };
  // Applies one event to the transition table. Every state change, retained
  // closure, original-link release and native disposal follows from the table.
  const apply = (p: Pending, event: GrokLoginEvent) => {
    const previous = p.machine;
    const next = transition(previous, event);
    if (next === previous) return;
    p.machine = next;
    if (next.retry === GrokRetry.None) p.retry = undefined;
    const after = grokLoginPresentation(next);
    if (after.settled || after.canceling) {
      p.url = "";
      p.userCode = "";
    }
    if (
      grokLoginPresentation(previous).native === GrokNativeBinding.Owed &&
      after.native === GrokNativeBinding.Disposed
    )
      disposeNative(p);
    // The latest copy attempt ends exactly when its value stops being offered,
    // whatever else the phase still offers.
    update(p, {
      machine: next,
      ...setCopy(p, grokCopyAfterPhase(p.copy, after)),
      ...(after.copyCode ? {} : { userCode: undefined, address: undefined }),
    });
  };
  const dispatch = (p: Pending, event: GrokLoginEvent) => {
    if (live(p)) apply(p, event);
  };
  // Disposal clears display, timer and callback authority only. It never
  // cancels an operation the server already accepted.
  const dispose = (p: Pending) => {
    p.disposed = true;
    p.url = "";
    p.userCode = "";
    if (p.copyTimer) clearTimeout(p.copyTimer);
    p.copyTimer = undefined;
    apply(p, GrokLoginEvent.Dispose);
    p.retry = undefined;
  };
  // Ends the controller's lifetime for one trigger. Each trigger disposes the
  // pending login and clears the view, so no screen outlives its authority; the
  // lifetime table decides whether the parent is told.
  const release = (trigger: GrokLifetimeTrigger) => {
    const p = pending.current;
    if (p) dispose(p);
    pending.current = undefined;
    setView(undefined);
    if (grokLifetimeCells[trigger].notifyParent) changed();
  };
  const leave = () => release(GrokLifetimeTrigger.Leave);
  useEffect(() => {
    const close = () => release(GrokLifetimeTrigger.SettingsClosed);
    opening?.controller.signal.addEventListener("abort", close, { once: true });
    return () => {
      opening?.controller.signal.removeEventListener("abort", close);
      release(GrokLifetimeTrigger.AuthorityChanged);
    };
  }, [opening, native]);
  useEffect(() => {
    if (!active && pending.current) release(GrokLifetimeTrigger.Inactive);
  }, [active]);

  const fresh = async (p: Pending) => {
    const result = await clients.resource.getResource({
      kind: EntityKind.ACCOUNT,
      id: p.account!.id,
    });
    if (
      !serviceAccount(
        result.resource,
        p.account!.id,
        SubscriptionServiceId.Grok,
        p.account!.revision,
      )
    )
      throw new GrokReceiptRejected("Account ownership changed");
    if (live(p)) p.account = result.resource;
    return result.resource;
  };
  // Runs one action. A replayable mutation (kind) retains its exact original
  // request after an uncertain outcome and settles through its own events, so
  // the retained request keeps its mutation-specific problem across every
  // replay. An auxiliary action (Open browser again, Open verification page)
  // names its own start and failure events and never owns or consumes a retry.
  const run = async (
    p: Pending,
    action: () => Promise<void>,
    mode:
      | Exclude<GrokRetry, GrokRetry.None>
      | { started: GrokLoginEvent; failed: GrokLoginEvent },
  ) => {
    if (!live(p) || p.busy) return;
    const kind = typeof mode === "string" ? mode : undefined;
    p.busy = true;
    dispatch(
      p,
      typeof mode === "string" ? GrokLoginEvent.ReplayStarted : mode.started,
    );
    update(p, { busy: true });
    try {
      await action();
    } catch (error) {
      if (live(p)) {
        if (kind) {
          const uncertain =
            !(error instanceof GrokReceiptRejected) &&
            [
              FailureCode.Unavailable,
              FailureCode.ServerUnavailable,
              FailureCode.Canceled,
              FailureCode.Internal,
            ].includes(clientFailure(error).code);
          const outcomes = grokRetryOutcomes[kind];
          // An uncertain outcome keeps the exact original request for explicit
          // replay; a definite refusal, including a receipt this controller
          // rejects locally, ends the attempt without a retry.
          dispatch(p, uncertain ? outcomes.uncertain : outcomes.refused);
          p.retry = p.machine.retry === kind ? action : undefined;
        } else if (typeof mode !== "string") dispatch(p, mode.failed);
      }
    } finally {
      p.busy = false;
      update(p, { busy: false });
    }
  };

  const begin = (method: GrokLoginMethod = GrokLoginMethod.DeviceCode) => {
    if (!active || pending.current || opening?.disposed) return;
    const p: Pending = {
      opening: newRequestId(),
      generation: "",
      operation: "",
      url: "",
      userCode: "",
      bound: false,
      callbackDispatched: false,
      disposed: false,
      polling: false,
      busy: false,
      copy: initialGrokCopyState,
      machine: initialGrokLoginState(Boolean(native), method),
    };
    pending.current = p;
    setView({
      machine: p.machine,
      name: grokDefaultAlias,
      suggested: false,
      busy: false,
      copy: initialGrokCopyState,
    });
    if (!native) return;
    const create = {
      mutation: { requestId: newRequestId() },
      kind: EntityKind.ACCOUNT,
      schemaVersion: 2,
      documentJson: encode({
        alias: grokDefaultAlias,
        subscription_service: SubscriptionServiceId.Grok,
        type: "subscription",
        enabled: true,
        exclude_automatic: false,
        recovery_notifications: false,
        health: "disconnected",
        quota: [],
        confirmed_exhausted: false,
      }),
    };
    let login:
      | Parameters<typeof clients.subscription.requestSubscription>[0]
      | undefined;
    // One account document and one login request: the request IDs are retained,
    // so every later replay is the same original request.
    const start = async () => {
      if (!p.account) {
        const result = await clients.configuration.saveConfiguration(create);
        if (!live(p)) return;
        if (
          result.requestId !== create.mutation.requestId ||
          !serviceAccount(
            result.resource,
            undefined,
            SubscriptionServiceId.Grok,
          ) ||
          document(result.resource).alias !== grokDefaultAlias
        )
          throw new GrokReceiptRejected("Invalid created account");
        p.account = result.resource;
        changed();
      }
      if (!live(p)) return;
      login ??= {
        mutation: {
          id: p.account.id,
          expectedRevision: p.account.revision,
          requestId: newRequestId(),
        },
        action: SubscriptionAction.LOGIN,
        deviceCode: method === GrokLoginMethod.DeviceCode,
      };
      const result = await clients.subscription.requestSubscription(login);
      if (!live(p)) return;
      if (
        result.operationId !== login.mutation?.requestId ||
        !serviceAccount(
          result.account,
          p.account.id,
          SubscriptionServiceId.Grok,
          p.account.revision,
        )
      )
        throw new GrokReceiptRejected("Invalid login ownership");
      p.account = result.account;
      p.operation = result.operationId;
      dispatch(p, GrokLoginEvent.StartAccepted);
    };
    void run(p, start, GrokRetry.Start);
  };

  // Sensitive progress stays in this opening, outside the shared query cache.
  // Polling only observes an explicitly admitted operation; it cannot start one.
  useEffect(() => {
    if (!active || !view) return;
    const poll = async () => {
      const p = pending.current;
      if (
        !p ||
        !live(p) ||
        !p.operation ||
        p.polling ||
        grokLoginPresentation(p.machine).settled
      )
        return;
      p.polling = true;
      try {
        const progress = await clients.subscription.getSubscriptionProgress({
          accountId: p.account!.id,
          operationId: p.operation,
        });
        if (!live(p)) return;
        const stage = stages[progress.state];
        if (!stage) throw new Error("Unsupported login state");
        if (stage === GrokLoginStage.Naming) {
          if (!isEntityId(progress.generation))
            throw new Error("Invalid login generation");
          const current = await fresh(p),
            data = document(current),
            state = object(data.subscription);
          if (!live(p)) return;
          if (
            text(object(state.server_operation).id) !== p.operation ||
            state.generation !== progress.generation ||
            !isEntityId(text(object(data.connection).id)) ||
            state.recovery_required === true
          )
            throw new Error("Login success could not be verified");
          dispatch(p, GrokLoginEvent.PollSucceeded);
          update(p, {
            name: subscriptionNameValid(progress.suggestedName)
              ? progress.suggestedName
              : grokDefaultAlias,
            suggested: subscriptionNameValid(progress.suggestedName),
            diagnostic: undefined,
          });
          changed();
          return;
        }
        // A canceled observation is handled exactly like the local cancellation
        // acknowledgement: the machine disposes the native binding and offers
        // no further login action. A refused Take the server keeps queued is
        // provisional: polling and the original sign-in stay alive to adopt
        // whatever comes next.
        dispatch(
          p,
          grokPollEvent(
            stage,
            progress.canceled,
            progressDisposition(progress.state, progress.grokDiagnostic) ===
              ProgressDisposition.Provisional,
          )!,
        );
        update(p, { diagnostic: progress.grokDiagnostic });
        const observed = grokLoginPresentation(p.machine);
        // A settled, canceled or cancellation-acknowledged login never opens a
        // browser or takes a callback, even while the server still reports
        // waiting.
        if (
          observed.settled ||
          observed.canceling ||
          stage !== GrokLoginStage.Waiting
        )
          return;
        if (p.machine.method === GrokLoginMethod.DeviceCode) {
          // Slow-down and pending polls are server-side and carry no new
          // presentation. The first valid pair is pinned for the whole login.
          if (
            !grokDeviceVerification(progress.url, progress.userCode) ||
            (p.url && p.url !== progress.url) ||
            (p.userCode && p.userCode !== progress.userCode)
          )
            throw new Error("Invalid original device authorization");
          p.url = progress.url;
          p.userCode = progress.userCode;
          dispatch(p, GrokLoginEvent.CodeReady);
          update(p, { userCode: p.userCode, address: p.url });
          return;
        }
        if (
          progress.userCode ||
          !grokLoginURL(progress.url) ||
          (p.url && p.url !== progress.url)
        )
          throw new Error("Invalid original browser login");
        p.url = progress.url;
        if (!p.bound) {
          // Set before dispatch: an uncertain native reply must not open
          // another browser or callback receiver on the next poll.
          p.bound = true;
          dispatch(p, GrokLoginEvent.LinkReady);
          try {
            const result = await native!(
              p.opening,
              OAuthNativeAction.GrokSubscriptionOpen,
              "",
              p.operation,
              p.url,
            );
            if (!live(p)) return;
            if (!isEntityId(result.generation))
              throw new Error("Invalid native generation");
            p.generation = result.generation;
            // A cancellation acknowledged or observed while the open was
            // pending already disposed the native binding: the table ignores
            // this late result.
            dispatch(p, GrokLoginEvent.OpenSucceeded);
          } catch (error) {
            // A closed incompatible refusal leaves no native binding, so
            // Open browser again would only be refused again: keep it hidden
            // and leave the copyable link as the only way forward. Any other
            // failure may succeed on an explicit reopen.
            dispatch(
              p,
              error === NativeFailureReason.Incompatible
                ? GrokLoginEvent.OpenIncompatible
                : GrokLoginEvent.OpenFailed,
            );
          }
        }
        const callbackWindow = grokLoginPresentation(p.machine);
        if (
          p.generation &&
          !p.callbackDispatched &&
          !callbackWindow.canceling &&
          !callbackWindow.settled
        ) {
          let result;
          try {
            result = await native!(
              p.opening,
              OAuthNativeAction.Take,
              p.generation,
              p.operation,
              "",
            );
          } catch {
            p.callbackDispatched = true;
            dispatch(p, GrokLoginEvent.CallbackTakeFailed);
            return;
          }
          // A callback taken after the login stopped waiting (a cancellation
          // was acknowledged or observed) is zeroed and never forwarded.
          const taken = grokLoginPresentation(p.machine);
          if (!live(p) || taken.canceling || taken.settled) {
            result.code?.fill(0);
            return;
          }
          if (result.generation !== p.generation) {
            result.code?.fill(0);
            throw new Error("Native callback ownership changed");
          }
          if (result.code) {
            p.callbackDispatched = true;
            if (
              !result.code.length ||
              result.code.length > 16384 ||
              !result.code.every(
                (value) =>
                  Number.isInteger(value) && value >= 0 && value <= 255,
              )
            ) {
              result.code.fill(0);
              throw new Error("Invalid native callback bytes");
            }
            const query = new Uint8Array(result.code);
            try {
              await clients.subscription.forwardSubscriptionCallback({
                accountId: p.account!.id,
                operationId: p.operation,
                callbackQuery: query,
              });
            } catch {
              dispatch(p, GrokLoginEvent.CallbackForwardFailed);
            } finally {
              query.fill(0);
              result.code.fill(0);
            }
          }
        }
      } catch {
        dispatch(p, GrokLoginEvent.PollError);
      } finally {
        p.polling = false;
      }
    };
    void poll();
    const timer = setInterval(() => void poll(), pollIntervalMilliseconds);
    return () => clearInterval(timer);
  }, [active, Boolean(view), clients, native]);

  // Open browser again is an auxiliary action: it is unavailable while a
  // replayable mutation waits for its explicit retry and never consumes it.
  const reopen = () => {
    const p = pending.current;
    if (
      !p ||
      !live(p) ||
      !p.url ||
      !native ||
      p.retry ||
      !grokLoginPresentation(p.machine).openBrowser
    )
      return;
    void run(
      p,
      async () => {
        let result;
        try {
          result = await native(
            p.opening,
            OAuthNativeAction.GrokSubscriptionReopen,
            p.generation,
            p.operation,
            p.url,
          );
        } catch (error) {
          if (error !== NativeFailureReason.Incompatible) throw error;
          // The same closed refusal as the first open: stop offering reopen.
          dispatch(p, GrokLoginEvent.ReopenIncompatible);
          return;
        }
        if (!live(p)) return;
        if (
          !isEntityId(result.generation) ||
          (p.generation && result.generation !== p.generation)
        )
          throw new Error("Original browser binding changed");
        p.generation = result.generation;
        dispatch(p, GrokLoginEvent.ReopenSucceeded);
      },
      {
        started: GrokLoginEvent.AuxiliaryStarted,
        failed: GrokLoginEvent.ReopenFailed,
      },
    );
  };
  const copyValue = (target: GrokCopyTarget) => {
    const p = pending.current;
    if (!p || !live(p)) return;
    const value = target === GrokCopyTarget.Code ? p.userCode : p.url;
    // Only a value the machine currently offers is copied.
    if (!value || !grokCopyOffered(grokLoginPresentation(p.machine), target))
      return;
    const clipboard = globalThis.navigator?.clipboard;
    // The clipboard settles later: only the latest attempt, still pending, may
    // show a notice (see the copy attempt table of the machine).
    const started = grokCopyTransition(p.copy, GrokCopyEvent.Started, {
      target,
    });
    update(p, setCopy(p, started));
    const settle = (event: GrokCopyEvent) =>
      dispatchCopy(p, event, started.attempt);
    if (!clipboard) {
      settle(GrokCopyEvent.Rejected);
      return;
    }
    void clipboard.writeText(value).then(
      () => settle(GrokCopyEvent.Succeeded),
      () => settle(GrokCopyEvent.Rejected),
    );
  };
  // Opens the validated device verification page through the closed native
  // device-authorization action. Like every OAuth/subscription authorization it
  // uses the system default browser and never the General/Local browser choice
  // that applies to ordinary links. It never opens a second browser on its own.
  // The machine records that an open was requested before the native call, so
  // every exit disposes the opening and retires a dispatch that is admitted but
  // not yet finished.
  const openVerification = () => {
    const p = pending.current;
    if (
      !p ||
      !live(p) ||
      !native ||
      !p.url ||
      p.retry ||
      p.machine.method !== GrokLoginMethod.DeviceCode ||
      !grokLoginPresentation(p.machine).openVerification
    )
      return;
    const url = p.url;
    void run(
      p,
      async () => {
        await native(
          p.opening,
          OAuthNativeAction.GrokDeviceAuthorizationOpen,
          "",
          p.operation,
          url,
        );
        dispatch(p, GrokLoginEvent.VerificationOpenSucceeded);
      },
      {
        started: GrokLoginEvent.VerificationOpenStarted,
        failed: GrokLoginEvent.VerificationOpenFailed,
      },
    );
  };
  const cancel = () => {
    const p = pending.current;
    // A held retry closure is the one authoritative way to settle an uncertain
    // cancellation; a second request would not be the original request.
    if (
      !p ||
      !live(p) ||
      !p.operation ||
      p.retry ||
      !grokLoginPresentation(p.machine).cancel
    )
      return;
    let request:
      Parameters<typeof clients.subscription.cancelSubscription>[0] | undefined;
    void run(
      p,
      async () => {
        const current = request ? p.account! : await fresh(p);
        if (!live(p)) return;
        if (
          !request &&
          text(
            object(object(document(current).subscription).server_operation).id,
          ) !== p.operation
        )
          throw new GrokReceiptRejected(
            "Original cancellation ownership changed",
          );
        request ??= {
          mutation: {
            id: current.id,
            expectedRevision: current.revision,
            requestId: newRequestId(),
          },
        };
        const result = await clients.subscription.cancelSubscription(request);
        if (
          !serviceAccount(
            result.account,
            current.id,
            SubscriptionServiceId.Grok,
            current.revision,
          )
        )
          throw new GrokReceiptRejected("Invalid cancellation");
        if (live(p)) {
          p.account = result.account;
          // The server acknowledged this exact cancellation. The table retires
          // the native receiver and every login action; polling keeps
          // observing until the terminal state, and no second cancel mutation
          // can be sent.
          dispatch(p, GrokLoginEvent.CancelAccepted);
        }
      },
      GrokRetry.Cancel,
    );
  };
  const save = () => {
    const p = pending.current;
    if (
      !p ||
      !live(p) ||
      p.retry ||
      !view ||
      grokLoginPresentation(p.machine).name !== GrokNameField.Editable ||
      !subscriptionNameValid(view.name)
    )
      return;
    const alias = view.name;
    let request:
      Parameters<typeof clients.configuration.saveConfiguration>[0] | undefined;
    void run(
      p,
      async () => {
        const current = request ? p.account! : await fresh(p);
        if (!live(p)) return;
        request ??= {
          mutation: {
            id: current.id,
            expectedRevision: current.revision,
            requestId: newRequestId(),
          },
          kind: EntityKind.ACCOUNT,
          schemaVersion: 2,
          documentJson: subscriptionAliasDocument(current, alias),
        };
        const result = await clients.configuration.saveConfiguration(request);
        if (!live(p)) return;
        if (
          result.requestId !== request.mutation?.requestId ||
          !serviceAccount(
            result.resource,
            current.id,
            SubscriptionServiceId.Grok,
            current.revision,
          ) ||
          document(result.resource).alias !== alias
        )
          throw new GrokReceiptRejected("Invalid saved name");
        dispatch(p, GrokLoginEvent.NameSaved);
        leave();
      },
      GrokRetry.Name,
    );
  };
  const retry = () => {
    const p = pending.current;
    const original = p?.retry;
    const kind = p?.machine.retry;
    if (p && original && kind && kind !== GrokRetry.None)
      void run(p, original, kind);
  };

  const p = pending.current;
  const body = view ? (
    <GrokSubscriptionOnboarding
      view={view}
      active={active}
      changeName={(name) => setView((v) => v && { ...v, name })}
      saveName={save}
      reopen={reopen}
      copyLink={() => copyValue(GrokCopyTarget.Link)}
      copyCode={() => copyValue(GrokCopyTarget.Code)}
      copyAddress={() => copyValue(GrokCopyTarget.Address)}
      openVerification={openVerification}
      cancel={cancel}
      retry={retry}
      leave={leave}
    />
  ) : null;
  return {
    begin,
    body,
    workflow: Boolean(view),
    retained: Boolean(p?.busy || p?.retry || p?.operation || p?.account),
    available: Boolean(native),
    leave,
  };
}

interface OnboardingProps {
  view: View;
  active: boolean;
  changeName: (name: string) => void;
  saveName: () => void;
  reopen: () => void;
  copyLink: () => void;
  copyCode: () => void;
  copyAddress: () => void;
  openVerification: () => void;
  cancel: () => void;
  retry: () => void;
  leave: () => void;
}
// Selects only the given element's text, for a keyboard-reachable manual copy.
const selectContents = (element: HTMLElement) => {
  const selection = element.ownerDocument.defaultView?.getSelection();
  if (!selection) return;
  const range = element.ownerDocument.createRange();
  range.selectNodeContents(element);
  selection.removeAllRanges();
  selection.addRange(range);
};
// A keyboard-reachable, read-only value. Focus and Ctrl/Cmd+A select exactly
// this value so the manual copy fallback works after a clipboard failure and
// never needs a pointer.
function SelectableValue(props: {
  label: string;
  className: string;
  children: string;
}) {
  return (
    <output
      aria-label={props.label}
      aria-live="off"
      aria-readonly="true"
      role="textbox"
      tabIndex={0}
      className={props.className}
      onFocus={(event) => selectContents(event.currentTarget)}
      onKeyDown={(event) => {
        if (
          (event.ctrlKey || event.metaKey) &&
          event.key.toLowerCase() === "a"
        ) {
          event.preventDefault();
          selectContents(event.currentTarget);
        }
      }}
    >
      {props.children}
    </output>
  );
}

function GrokSubscriptionOnboarding(props: OnboardingProps) {
  useLocale();
  const formId = useId(),
    visible = useSettingsTaskVisible(),
    back = useCloseSettingsTask(props.leave);
  const { view: v, active } = props;
  // Every action below follows the transition table's presentation of the
  // current phase; nothing here decides on its own what to offer.
  const ui = grokLoginPresentation(v.machine),
    stage = ui.stage ?? GrokLoginStage.Failed;
  const naming = stage === GrokLoginStage.Naming,
    waiting = stage === GrokLoginStage.Waiting,
    inFlight = waiting || stage === GrokLoginStage.Preparing,
    busy = !active || v.busy;
  // An uncertain naming save keeps its exact request: the name stays frozen and
  // Retry original request is the only mutation path until it settles.
  const locked = busy || ui.name === GrokNameField.Locked;
  const nameInput = useRef<HTMLInputElement>(null),
    focused = useRef(false);
  useEffect(() => {
    if (visible && active && naming && !locked && !focused.current) {
      focused.current = true;
      nameInput.current?.focus({ preventScroll: true });
    }
  }, [visible, active, naming, locked]);
  // Waiting guidance follows what really happened: the browser opened, only the
  // copyable link remains, or nothing is offered yet.
  const device = v.machine.method === GrokLoginMethod.DeviceCode;
  const helpByKind: Record<GrokHelp, MessageKey | undefined> = {
    [GrokHelp.None]: undefined,
    [GrokHelp.Waiting]: "grok-subscription.help.waiting",
    [GrokHelp.WaitingLink]: "grok-subscription.help.waitingLink",
    [GrokHelp.DeviceWaiting]: "grok-subscription.device.help.waiting",
    [GrokHelp.Stage]: (device ? deviceHelpKeys : helpKeys)[stage],
  };
  const help = helpByKind[ui.help];
  return (
    <section
      className="subscription-account-create subscription-onboarding desktop-wizard"
      aria-label={copy("grok-subscription.title")}
    >
      <SettingsActionButton
        icon={SettingsActionIcon.Back}
        type="button"
        className="subscription-onboarding-back"
        disabled={!active}
        onClick={back}
      >
        {copy("grok-subscription.back")}
      </SettingsActionButton>
      <h2>{copy("grok-subscription.title")}</h2>
      <ol
        className="subscription-onboarding-steps wizard-progress"
        aria-label={copy("grok-subscription.steps")}
      >
        <li data-completed={naming} aria-current={!naming ? "step" : undefined}>
          <span className="wizard-step-number" aria-hidden="true">
            1
          </span>
          {naming ? <span aria-hidden="true">✓ </span> : null}
          {copy("grok-subscription.signIn")}
        </li>
        <li aria-current={naming ? "step" : undefined}>
          <span className="wizard-step-number" aria-hidden="true">
            2
          </span>
          {copy("grok-subscription.accountName")}
        </li>
      </ol>
      <p
        className={`subscription-onboarding-status${naming ? " subscription-onboarding-success" : ""}`}
        role="status"
      >
        {inFlight ? (
          <span
            className="subscription-onboarding-spinner"
            aria-hidden="true"
          />
        ) : null}
        {naming ? <span aria-hidden="true">✓</span> : null}
        {copy(statusKeys[stage])}
      </p>
      {v.copy.target &&
      (v.copy.phase === GrokCopyPhase.Copied ||
        v.copy.phase === GrokCopyPhase.Failed) ? (
        <p role="status">
          {copy(
            copyNotices[v.copy.target][
              v.copy.phase === GrokCopyPhase.Copied ? "copied" : "failed"
            ],
          )}
        </p>
      ) : null}
      {naming ? (
        <form
          id={formId}
          onSubmit={(event) => {
            event.preventDefault();
            if (!locked && subscriptionNameValid(v.name)) props.saveName();
          }}
        >
          <label htmlFor="grok-subscription-name">
            {copy("grok-subscription.accountName")}
          </label>
          <input
            id="grok-subscription-name"
            ref={nameInput}
            autoComplete="off"
            required
            value={v.name}
            disabled={locked}
            onChange={(event) => {
              if (!locked) props.changeName(event.target.value);
            }}
            aria-describedby="grok-subscription-name-help"
          />
          <p id="grok-subscription-name-help">
            {copy(
              v.suggested
                ? "grok-subscription.nameSuggested"
                : "grok-subscription.nameChoose",
            )}
          </p>
          <SettingsTaskActions form={formId} className="">
            <SettingsActionButton
              icon={SettingsActionIcon.Save}
              className="primary"
              disabled={locked || !subscriptionNameValid(v.name)}
            >
              {copy("grok-subscription.saveName")}
            </SettingsActionButton>
            <SettingsActionButton
              icon={SettingsActionIcon.Cancel}
              type="button"
              disabled={!active}
              onClick={back}
            >
              {copy("grok-subscription.later")}
            </SettingsActionButton>
          </SettingsTaskActions>
        </form>
      ) : (
        <>
          {help ? <p>{copy(help)}</p> : null}
          {ui.copyCode && v.userCode ? (
            <div className="grok-device-code">
              <p>{copy("grok-subscription.device.codeLabel")}</p>
              <SelectableValue
                label={copy("grok-subscription.device.codeAria")}
                className="grok-device-user-code"
              >
                {v.userCode}
              </SelectableValue>
            </div>
          ) : null}
          {ui.copyAddress && v.address ? (
            <div className="grok-device-code">
              <p>{copy("grok-subscription.device.addressLabel")}</p>
              <SelectableValue
                label={copy("grok-subscription.device.addressAria")}
                className="grok-device-verification-address"
              >
                {v.address}
              </SelectableValue>
            </div>
          ) : null}
          {v.diagnostic ? (
            <GrokDiagnosticNotice diagnostic={v.diagnostic} />
          ) : null}
          <SettingsTaskActions className="">
            {ui.openVerification ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Open}
                type="button"
                className="primary"
                disabled={busy || ui.retry}
                onClick={props.openVerification}
              >
                {copy("grok-subscription.device.openVerification")}
              </SettingsActionButton>
            ) : null}
            {ui.copyCode ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Copy}
                type="button"
                disabled={!active}
                onClick={props.copyCode}
              >
                {copy("grok-subscription.device.copyCode")}
              </SettingsActionButton>
            ) : null}
            {ui.copyAddress ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Copy}
                type="button"
                disabled={!active}
                onClick={props.copyAddress}
              >
                {copy("grok-subscription.device.copyAddress")}
              </SettingsActionButton>
            ) : null}
            {ui.openBrowser ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Open}
                type="button"
                className="primary"
                disabled={busy || ui.retry}
                onClick={props.reopen}
              >
                {copy("grok-subscription.openBrowser")}
              </SettingsActionButton>
            ) : null}
            {ui.copyLink ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Copy}
                type="button"
                disabled={!active}
                onClick={props.copyLink}
              >
                {copy("grok-subscription.copyLink")}
              </SettingsActionButton>
            ) : null}
            {ui.cancel ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Cancel}
                type="button"
                // An uncertain cancel is replayed only through Retry original
                // request, never as a second, different cancel mutation.
                disabled={busy || ui.retry}
                onClick={props.cancel}
              >
                {copy("grok-subscription.cancel")}
              </SettingsActionButton>
            ) : null}
          </SettingsTaskActions>
        </>
      )}
      {v.machine.problem ? <p role="alert">{copy(v.machine.problem)}</p> : null}
      {ui.retry ? (
        <SettingsActionButton
          icon={SettingsActionIcon.Retry}
          type="button"
          disabled={busy}
          onClick={props.retry}
        >
          {copy("grok-subscription.retryOriginal")}
        </SettingsActionButton>
      ) : null}
      {stage !== GrokLoginStage.Unsupported ? (
        <p className="subscription-onboarding-footer">
          {copy(
            naming
              ? "grok-subscription.footer.naming"
              : "grok-subscription.footer.pending",
          )}
        </p>
      ) : null}
    </section>
  );
}
