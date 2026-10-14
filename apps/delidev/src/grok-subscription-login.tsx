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
import { subscriptionNameValid } from "./subscription-onboarding";
import {
  serviceAccount,
  subscriptionAliasDocument,
} from "./subscription-resource";
import "./subscription-onboarding.css";

// Closed presentation stages. They grant no login or mutation authority.
export enum GrokLoginStage {
  Preparing = "preparing",
  Waiting = "waiting",
  Naming = "naming",
  Canceled = "canceled",
  Expired = "expired",
  Unsupported = "unsupported",
  Recovery = "recovery",
  Failed = "failed",
}
enum CopyState {
  Idle = "idle",
  Copied = "copied",
  Failed = "failed",
}
// What the transient clipboard action copies. Both values are display-only.
enum CopyTarget {
  Link = "link",
  Code = "code",
}
// The explicit sign-in method chosen by the user. It never changes mid-login.
export enum GrokLoginMethod {
  Browser = "browser",
  DeviceCode = "device-code",
}
// The trusted native receiver rejects with its kebab-case failure name.
enum NativeFailureReason {
  Incompatible = "incompatible",
}
// The only notice a later successful status read may clear. Every other problem
// comes from an explicit user action or a one-shot native step and stays until
// the user acts again, so polling can never hide an actionable failure.
const statusProblem: MessageKey = "grok-subscription.problem.statusUnconfirmed";
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
  [GrokLoginStage.Waiting]: "grok-subscription.device.help.waiting",
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

interface View {
  method: GrokLoginMethod;
  stage: GrokLoginStage;
  name: string;
  suggested: boolean;
  busy: boolean;
  problem?: MessageKey;
  diagnostic?: GrokDiagnostic;
  browserReady: boolean;
  // True only while the native receiver confirmed that it opened the original
  // address; the waiting guidance never claims a browser opened otherwise.
  browserOpened: boolean;
  urlReady: boolean;
  // Transient device code display. Never persisted, logged or cached.
  userCode?: string;
  copyState: CopyState;
  copyTarget: CopyTarget;
  canRetry: boolean;
  canCancel: boolean;
  // The server accepted a cancellation, so the verification page is no longer
  // worth opening. The terminal canceled state arrives through polling.
  canceling: boolean;
}
interface Pending {
  opening: string;
  generation: string;
  account?: Resource;
  operation: string;
  url: string;
  method: GrokLoginMethod;
  userCode: string;
  bound: boolean;
  callbackDispatched: boolean;
  disposed: boolean;
  // A device verification open was requested, so the native host may hold an
  // admitted dispatch for this opening that only a native Dispose can retire.
  deviceOpened: boolean;
  canceling: boolean;
  polling: boolean;
  busy: boolean;
  terminal: boolean;
  copyTimer?: ReturnType<typeof setTimeout>;
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
  // A browser login owns a native receiver for its whole opening. A device
  // login owns native state only once the user asked to open the verification
  // page: that admitted dispatch may still be pending, and only disposing the
  // opening retires it before a browser starts.
  const nativeOwned = (p: Pending) =>
    p.method === GrokLoginMethod.Browser || p.deviceOpened;
  const disposeNative = (p: Pending) => {
    if (native && nativeOwned(p))
      void native(p.opening, OAuthNativeAction.Dispose, "", "", "").catch(
        () => undefined,
      );
  };
  // Disposal clears display, timer and callback authority only. It never
  // cancels an operation the server already accepted.
  const dispose = (p: Pending) => {
    p.disposed = true;
    p.retry = undefined;
    p.url = "";
    p.userCode = "";
    if (p.copyTimer) clearTimeout(p.copyTimer);
    p.copyTimer = undefined;
    disposeNative(p);
  };
  const finish = (p: Pending) => {
    p.terminal = true;
    p.retry = undefined;
    p.url = "";
    p.userCode = "";
    disposeNative(p);
  };
  const leave = () => {
    const p = pending.current;
    if (p) dispose(p);
    pending.current = undefined;
    setView(undefined);
    changed();
  };
  useEffect(() => {
    const close = () => {
      if (pending.current) dispose(pending.current);
      pending.current = undefined;
    };
    opening?.controller.signal.addEventListener("abort", close, { once: true });
    return () => {
      opening?.controller.signal.removeEventListener("abort", close);
      close();
    };
  }, [opening, native]);
  useEffect(() => {
    if (!active && pending.current) {
      dispose(pending.current);
      pending.current = undefined;
      setView(undefined);
    }
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
      throw new Error("Account ownership changed");
    if (live(p)) p.account = result.resource;
    return result.resource;
  };
  // Records the outcome of a status read. A healthy read recovers only the
  // polling notice; a failing read never replaces a more specific problem.
  const settleStatus = (p: Pending, healthy: boolean) => {
    if (!live(p)) return;
    setView((current) => {
      if (!current) return current;
      if (healthy)
        return current.problem === statusProblem
          ? { ...current, problem: undefined }
          : current;
      return current.problem && current.problem !== statusProblem
        ? current
        : { ...current, problem: statusProblem };
    });
  };
  const run = async (
    p: Pending,
    action: () => Promise<void>,
    problem: MessageKey,
    replay = false,
  ) => {
    if (!live(p) || p.busy) return;
    p.busy = true;
    // Only a replayable mutation owns the retry closure. An auxiliary action
    // (opening or reopening a browser) never consumes or hides it.
    if (replay) p.retry = undefined;
    update(p, {
      busy: true,
      problem: undefined,
      ...(replay ? { canRetry: false } : {}),
    });
    try {
      await action();
    } catch (error) {
      // A settled login makes an auxiliary open moot, so its late failure is
      // not worth a notice on the terminal screen.
      if (live(p) && (replay || !p.terminal)) {
        const uncertain = [
          FailureCode.Unavailable,
          FailureCode.ServerUnavailable,
          FailureCode.Canceled,
          FailureCode.Internal,
        ].includes(clientFailure(error).code);
        // An uncertain outcome keeps the exact original request for explicit
        // replay; a definite refusal before any operation ends the attempt.
        if (replay && uncertain) p.retry = action;
        update(p, {
          problem,
          ...(replay ? { canRetry: Boolean(p.retry) } : {}),
          ...(replay && !uncertain && !p.operation
            ? { stage: GrokLoginStage.Failed }
            : {}),
        });
      }
    } finally {
      p.busy = false;
      update(p, { busy: false });
    }
  };

  const begin = (method: GrokLoginMethod = GrokLoginMethod.Browser) => {
    if (!active || pending.current || opening?.disposed) return;
    const p: Pending = {
      opening: newRequestId(),
      generation: "",
      operation: "",
      url: "",
      method,
      userCode: "",
      bound: false,
      callbackDispatched: false,
      disposed: false,
      deviceOpened: false,
      canceling: false,
      polling: false,
      busy: false,
      terminal: false,
    };
    pending.current = p;
    setView({
      method,
      stage: native ? GrokLoginStage.Preparing : GrokLoginStage.Unsupported,
      name: "Grok",
      suggested: false,
      busy: false,
      browserReady: false,
      browserOpened: false,
      urlReady: false,
      copyState: CopyState.Idle,
      copyTarget: CopyTarget.Link,
      canRetry: false,
      canCancel: false,
      canceling: false,
    });
    if (!native) {
      update(p, { problem: "grok-subscription.problem.desktopRequired" });
      return;
    }
    const create = {
      mutation: { requestId: newRequestId() },
      kind: EntityKind.ACCOUNT,
      schemaVersion: 2,
      documentJson: encode({
        alias: "Grok",
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
          )
        )
          throw new Error("Invalid created account");
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
        throw new Error("Invalid login ownership");
      p.account = result.account;
      p.operation = result.operationId;
      update(p, { canCancel: true });
    };
    void run(p, start, "grok-subscription.problem.requestUnconfirmed", true);
  };

  // Sensitive progress stays in this opening, outside the shared query cache.
  // Polling only observes an explicitly admitted operation; it cannot start one.
  useEffect(() => {
    if (!active || !view) return;
    const poll = async () => {
      const p = pending.current;
      if (!p || !live(p) || !p.operation || p.polling || p.terminal) return;
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
          finish(p);
          update(p, {
            stage,
            name: subscriptionNameValid(progress.suggestedName)
              ? progress.suggestedName
              : "Grok",
            suggested: subscriptionNameValid(progress.suggestedName),
            problem: undefined,
            diagnostic: undefined,
            browserReady: false,
            browserOpened: false,
            urlReady: false,
            userCode: undefined,
            canCancel: false,
            canRetry: false,
            canceling: false,
          });
          changed();
          return;
        }
        const terminal = ![
          GrokLoginStage.Preparing,
          GrokLoginStage.Waiting,
        ].includes(stage);
        if (progress.canceled) p.canceling = true;
        update(p, {
          stage,
          diagnostic: progress.grokDiagnostic,
          // A poll never re-offers Cancel after the server acknowledged it.
          canCancel: !terminal && !p.canceling,
          ...(progress.canceled ? { canceling: true } : {}),
          ...(terminal
            ? {
                problem: undefined,
                browserReady: false,
                browserOpened: false,
                urlReady: false,
                userCode: undefined,
                canRetry: false,
                canceling: false,
              }
            : {}),
        });
        // A readable status is healthy whatever it says, so it recovers the
        // polling notice before any early return below. Problems from explicit
        // user actions and one-shot native steps are never cleared here.
        settleStatus(p, true);
        if (terminal) {
          finish(p);
          return;
        }
        // A canceled or cancellation-acknowledged login never opens a browser,
        // pins a device code or takes a callback, even while the server still
        // reports waiting.
        if (stage !== GrokLoginStage.Waiting || p.canceling) return;
        if (p.method === GrokLoginMethod.DeviceCode) {
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
          update(p, { urlReady: true, userCode: p.userCode });
          return;
        }
        if (
          progress.userCode ||
          !grokLoginURL(progress.url) ||
          (p.url && p.url !== progress.url)
        )
          throw new Error("Invalid original browser login");
        p.url = progress.url;
        update(p, { urlReady: true });
        if (!p.bound) {
          // Set before dispatch: an uncertain native reply must not open
          // another browser or callback receiver on the next poll.
          p.bound = true;
          update(p, { stage: GrokLoginStage.Preparing });
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
            update(p, {
              stage: GrokLoginStage.Waiting,
              browserReady: true,
              browserOpened: true,
              problem: undefined,
            });
          } catch (error) {
            // A closed incompatible refusal leaves no native binding, so
            // Open browser again would only be refused again: keep it hidden
            // and leave the copyable link as the only way forward. Any other
            // failure may succeed on an explicit reopen.
            const incompatible = error === NativeFailureReason.Incompatible;
            update(p, {
              stage: GrokLoginStage.Waiting,
              browserReady: !incompatible,
              browserOpened: false,
              problem: incompatible
                ? "grok-subscription.problem.remoteUnsupported"
                : "grok-subscription.problem.browserFailed",
            });
          }
        }
        if (p.generation && !p.callbackDispatched) {
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
            update(p, {
              problem: "grok-subscription.problem.callbackUncertain",
            });
            return;
          }
          if (!live(p)) {
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
              update(p, {
                problem: "grok-subscription.problem.forwardUncertain",
              });
            } finally {
              query.fill(0);
              result.code.fill(0);
            }
          }
        }
      } catch {
        settleStatus(p, false);
      } finally {
        p.polling = false;
      }
    };
    void poll();
    const timer = setInterval(() => void poll(), pollIntervalMilliseconds);
    return () => clearInterval(timer);
  }, [active, Boolean(view), clients, native]);

  // An auxiliary action opens a browser page for the original login. It is not
  // available once the login settled, while a replayable mutation is waiting
  // for its explicit retry, or after a cancellation was accepted.
  const auxiliary = (p: Pending | undefined): p is Pending =>
    Boolean(p && live(p) && !p.terminal && !p.canceling && !p.retry && !p.busy);
  const reopen = () => {
    const p = pending.current;
    if (!auxiliary(p) || !p.url || !native) return;
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
          update(p, {
            browserReady: false,
            browserOpened: false,
            problem: "grok-subscription.problem.remoteUnsupported",
          });
          return;
        }
        if (!live(p)) return;
        if (
          !isEntityId(result.generation) ||
          (p.generation && result.generation !== p.generation)
        )
          throw new Error("Original browser binding changed");
        p.generation = result.generation;
        update(p, { browserOpened: true, problem: undefined });
      },
      "grok-subscription.problem.browserFailed",
    );
  };
  const copyValue = (target: CopyTarget) => {
    const p = pending.current;
    const value = target === CopyTarget.Code ? p?.userCode : p?.url;
    if (!p || !live(p) || !value) return;
    const clipboard = globalThis.navigator?.clipboard;
    const settle = (copyState: CopyState) => {
      if (!live(p)) return;
      update(p, { copyState, copyTarget: target });
      if (p.copyTimer) clearTimeout(p.copyTimer);
      p.copyTimer = setTimeout(
        () => update(p, { copyState: CopyState.Idle }),
        copiedNoticeMilliseconds,
      );
    };
    if (!clipboard) {
      settle(CopyState.Failed);
      return;
    }
    void clipboard.writeText(value).then(
      () => settle(CopyState.Copied),
      () => settle(CopyState.Failed),
    );
  };
  // Opens the validated device verification page through the closed native
  // device-authorization action. Like every OAuth/subscription authorization it
  // uses the system default browser and never the General/Local browser choice
  // that applies to ordinary links. It never opens a second browser on its own.
  const openVerification = () => {
    const p = pending.current;
    if (
      !auxiliary(p) ||
      p.method !== GrokLoginMethod.DeviceCode ||
      !p.url ||
      !native
    )
      return;
    const url = p.url;
    // Marked before the native call so every exit path disposes the opening
    // and retires a dispatch that is admitted but not yet finished.
    p.deviceOpened = true;
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
      },
      "grok-subscription.problem.verificationOpenFailed",
    );
  };
  const cancel = () => {
    const p = pending.current;
    // A held retry closure is the one authoritative way to settle an uncertain
    // cancellation; a second request would not be the original request.
    if (!p || !live(p) || !p.operation || p.terminal || p.canceling || p.retry)
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
          throw new Error("Original cancellation ownership changed");
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
          throw new Error("Invalid cancellation");
        if (live(p)) {
          p.account = result.account;
          // The server acknowledged this exact cancellation. Polling keeps
          // observing until it reports the terminal state, but a second
          // click must not send another cancel mutation.
          p.canceling = true;
          update(p, {
            problem: "grok-subscription.problem.canceling",
            canCancel: false,
            canceling: true,
          });
        }
      },
      "grok-subscription.problem.cancelUnconfirmed",
      true,
    );
  };
  const save = () => {
    const p = pending.current;
    if (
      !p ||
      !live(p) ||
      view?.stage !== GrokLoginStage.Naming ||
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
          throw new Error("Invalid saved name");
        leave();
      },
      "grok-subscription.problem.nameUnconfirmed",
      true,
    );
  };
  const retry = () => {
    const p = pending.current;
    const original = p?.retry;
    if (p && original)
      void run(
        p,
        original,
        "grok-subscription.problem.requestUnconfirmed",
        true,
      );
  };

  const p = pending.current;
  const body = view ? (
    <GrokSubscriptionOnboarding
      view={view}
      active={active}
      changeName={(name) => setView((v) => v && { ...v, name })}
      saveName={save}
      reopen={reopen}
      copyLink={() => copyValue(CopyTarget.Link)}
      copyCode={() => copyValue(CopyTarget.Code)}
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
const diagnosticVersion = (value: string) =>
  value.length <= 64 && /^[a-zA-Z0-9.+_-]+$/.test(value);

function GrokSubscriptionOnboarding(props: OnboardingProps) {
  useLocale();
  const formId = useId(),
    visible = useSettingsTaskVisible(),
    back = useCloseSettingsTask(props.leave);
  const { view: v, active } = props;
  const naming = v.stage === GrokLoginStage.Naming,
    waiting = v.stage === GrokLoginStage.Waiting,
    inFlight = waiting || v.stage === GrokLoginStage.Preparing,
    busy = !active || v.busy;
  const nameInput = useRef<HTMLInputElement>(null),
    focused = useRef(false);
  useEffect(() => {
    if (visible && active && naming && !busy && !focused.current) {
      focused.current = true;
      nameInput.current?.focus({ preventScroll: true });
    }
  }, [visible, active, naming, busy]);
  const d = v.diagnostic,
    diagnostic =
      d &&
      !inFlight &&
      !naming &&
      (!d.detectedVersion || diagnosticVersion(d.detectedVersion)) &&
      Object.values(FailureCode).includes(d.code as FailureCode)
        ? d
        : undefined;
  const device = v.method === GrokLoginMethod.DeviceCode;
  // Waiting guidance follows what really happened: the browser opened, only the
  // copyable link remains, or nothing is offered yet. Device sign-in has its own
  // guidance because the user opens the verification page deliberately.
  const help = device
    ? deviceHelpKeys[v.stage]
    : waiting && !v.browserOpened
      ? v.urlReady
        ? "grok-subscription.help.waitingLink"
        : undefined
      : helpKeys[v.stage];
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
        {copy(statusKeys[v.stage])}
      </p>
      {v.copyState !== CopyState.Idle ? (
        <p role="status">
          {copy(
            v.copyTarget === CopyTarget.Code
              ? v.copyState === CopyState.Copied
                ? "grok-subscription.device.codeCopied"
                : "grok-subscription.device.codeCopyFailed"
              : v.copyState === CopyState.Copied
                ? "grok-subscription.linkCopied"
                : "grok-subscription.copyFailed",
          )}
        </p>
      ) : null}
      {naming ? (
        <form
          id={formId}
          onSubmit={(event) => {
            event.preventDefault();
            if (!busy && subscriptionNameValid(v.name)) props.saveName();
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
            disabled={busy}
            onChange={(event) => props.changeName(event.target.value)}
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
              disabled={busy || !subscriptionNameValid(v.name)}
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
          {waiting && device && v.userCode ? (
            <div className="grok-device-code">
              <p>{copy("grok-subscription.device.codeLabel")}</p>
              <output
                aria-label={copy("grok-subscription.device.codeAria")}
                aria-live="off"
                aria-readonly="true"
                role="textbox"
                // Keyboard users reach the code without a pointer; focus and
                // Ctrl/Cmd+A select exactly the code so the manual copy
                // fallback works after a clipboard failure.
                tabIndex={0}
                className="grok-device-user-code"
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
                {v.userCode}
              </output>
            </div>
          ) : null}
          {diagnostic ? (
            <dl className="subscription-onboarding-diagnostic">
              <div>
                <dt>{copy("grok-subscription.diagnostic.version")}</dt>
                <dd>
                  {diagnostic.detectedVersion ||
                    copy("grok-subscription.diagnostic.notDetected")}
                </dd>
              </div>
              <div>
                <dt>{copy("grok-subscription.diagnostic.code")}</dt>
                <dd>{diagnostic.code}</dd>
              </div>
            </dl>
          ) : null}
          <SettingsTaskActions className="">
            {waiting && device && v.urlReady && !v.canceling ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Open}
                type="button"
                className="primary"
                disabled={busy || v.canRetry}
                onClick={props.openVerification}
              >
                {copy("grok-subscription.device.openVerification")}
              </SettingsActionButton>
            ) : null}
            {waiting && device && v.urlReady ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Copy}
                type="button"
                disabled={!active}
                onClick={props.copyCode}
              >
                {copy("grok-subscription.device.copyCode")}
              </SettingsActionButton>
            ) : null}
            {waiting && !device && v.browserReady && !v.canceling ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Open}
                type="button"
                className="primary"
                disabled={busy || v.canRetry}
                onClick={props.reopen}
              >
                {copy("grok-subscription.openBrowser")}
              </SettingsActionButton>
            ) : null}
            {waiting && !device && v.urlReady ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Copy}
                type="button"
                disabled={!active}
                onClick={props.copyLink}
              >
                {copy("grok-subscription.copyLink")}
              </SettingsActionButton>
            ) : null}
            {v.canCancel && inFlight && !v.canceling ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Cancel}
                type="button"
                // An uncertain cancel is replayed only through Retry original
                // request, never as a second, different cancel mutation.
                disabled={busy || v.canRetry}
                onClick={props.cancel}
              >
                {copy("grok-subscription.cancel")}
              </SettingsActionButton>
            ) : null}
          </SettingsTaskActions>
        </>
      )}
      {v.problem ? <p role="alert">{copy(v.problem)}</p> : null}
      {v.canRetry ? (
        <SettingsActionButton
          icon={SettingsActionIcon.Retry}
          type="button"
          disabled={busy}
          onClick={props.retry}
        >
          {copy("grok-subscription.retryOriginal")}
        </SettingsActionButton>
      ) : null}
      {v.stage !== GrokLoginStage.Unsupported ? (
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
