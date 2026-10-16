// SPDX-License-Identifier: Apache-2.0
import "./wizard-presentation.css";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import {
  ConfigurationService,
  EntityKind,
  FailureCode,
  ResourceService,
  SubscriptionAction,
  SubscriptionLoginMethod,
  SubscriptionLoginState,
  SubscriptionService,
  SubscriptionServiceId,
  clientFailure,
  isEntityId,
  newRequestId,
  type NativeSubscriptionDiagnostic,
  type Resource,
} from "@delinoio/delidev-api-client";
import { OAuthNativeAction, useOAuthNativeControl } from "./account-oauth";
import { document, encode, object, text } from "./documents";
import { validInstallationVersion } from "./harness-version";
import { copy, useLocale, type MessageKey } from "./localization";
import { useSettingsOpening } from "./settings-lifetime";
import {
  useCloseSettingsTask,
  useSettingsTaskVisible,
} from "./settings-task-context";
import { SettingsTaskActions } from "./settings-task";
import {
  serviceAccount,
  subscriptionAliasDocument,
} from "./subscription-resource";
import { subscriptionNameValid } from "./subscription-onboarding";
import "./subscription-onboarding.css";
enum Step {
  Login,
  Name,
  Terminal,
}
interface View {
  step: Step;
  name: string;
  state: SubscriptionLoginState;
  method: SubscriptionLoginMethod;
  busy: boolean;
  codeClaimed: boolean;
  browserReady: boolean;
  problem?: MessageKey;
  diagnostic?: NativeSubscriptionDiagnostic;
}
interface Pending {
  opening: string;
  account?: Resource;
  operation: string;
  url: string;
  generation: string;
  disposed: boolean;
  bound: boolean;
  polling: boolean;
  busy: boolean;
  terminal: boolean;
  codeClaimed: boolean;
  start?: () => Promise<void>;
  retry?: () => Promise<void>;
}
function claudeAuthorizationURL(raw: string, setupToken: boolean): boolean {
  try {
    if (raw.length > 8192 || !/^[!-~]+$/.test(raw)) return false;
    const u = new URL(raw),
      q = u.searchParams,
      opaque = (s: string) => /^[A-Za-z0-9_-]{16,512}$/.test(s);
    if (
      u.href !== raw ||
      u.protocol !== "https:" ||
      u.host !== "claude.com" ||
      u.pathname !== "/cai/oauth/authorize" ||
      u.username ||
      u.password ||
      u.hash
    )
      return false;
    const allowed = [
      "code",
      "client_id",
      "response_type",
      "redirect_uri",
      "scope",
      "code_challenge",
      "code_challenge_method",
      "state",
      "orgUUID",
      "login_hint",
      "login_method",
    ];
    if (
      [...q.entries()].some(
        ([k, v]) =>
          !allowed.includes(k) ||
          q.getAll(k).length !== 1 ||
          v.length > 2048 ||
          /[\x00-\x1f\x7f]/.test(v),
      )
    )
      return false;
    if (
      q.get("client_id") !== "9d1c250a-e61b-44d9-88ed-5944d1962f5e" ||
      q.get("response_type") !== "code" ||
      q.get("code_challenge_method") !== "S256" ||
      q.get("code_challenge")?.length !== 43 ||
      !opaque(q.get("code_challenge") ?? "") ||
      !opaque(q.get("state") ?? "") ||
      ![null, "true"].includes(q.get("code"))
    )
      return false;
    const redirect = q.get("redirect_uri");
    // A server cannot receive a browser callback: setup-token sign-in is the
    // inference-only, code-pasting profile and never a localhost callback.
    if (
      setupToken &&
      (redirect !== "https://platform.claude.com/oauth/code/callback" ||
        q.get("code") !== "true")
    )
      return false;
    if (redirect !== "https://platform.claude.com/oauth/code/callback") {
      const c = new URL(redirect ?? "");
      if (
        c.href !== redirect ||
        c.protocol !== "http:" ||
        c.hostname !== "localhost" ||
        !c.port ||
        Number(c.port) < 1 ||
        c.username ||
        c.password ||
        c.pathname !== "/callback" ||
        c.search ||
        c.hash
      )
        return false;
    }
    const scopes = (q.get("scope") ?? "").split(/\s+/);
    if (setupToken)
      return scopes.length === 1 && scopes[0] === "user:inference";
    const permitted = [
      "org:create_api_key",
      "user:profile",
      "user:inference",
      "user:sessions:claude_code",
      "user:mcp_servers",
      "user:file_upload",
    ];
    return (
      scopes.includes("user:profile") &&
      scopes.includes("user:inference") &&
      new Set(scopes).size === scopes.length &&
      scopes.every((s) => permitted.includes(s))
    );
  } catch {
    return false;
  }
}
export function claudeLoginURL(raw: string): boolean {
  return claudeAuthorizationURL(raw, false);
}
/** The usage login accepts the original auth-login address only with the
 * manual approval-code callback: a remote server cannot receive a callback on
 * this device's localhost, and DeliDev never substitutes another callback. */
export function claudeUsageLoginURL(raw: string): boolean {
  if (!claudeAuthorizationURL(raw, false)) return false;
  try {
    return (
      new URL(raw).searchParams.get("redirect_uri") ===
      "https://platform.claude.com/oauth/code/callback"
    );
  } catch {
    return false;
  }
}
export function claudeSetupTokenURL(raw: string): boolean {
  return claudeAuthorizationURL(raw, true);
}
export function useClaudeSubscriptionLogin(
  active: boolean,
  changed: () => void,
) {
  useLocale();
  const transport = useTransport(),
    native = useOAuthNativeControl(),
    opening = useSettingsOpening();
  const clients = useMemo(
    () => ({
      configuration: createClient(ConfigurationService, transport),
      resource: createClient(ResourceService, transport),
      subscription: createClient(SubscriptionService, transport),
    }),
    [transport],
  );
  const pending = useRef<Pending | undefined>(undefined),
    [view, setView] = useState<View>(),
    [hidden, setHidden] = useState(false),
    approvalInput = useRef<HTMLInputElement>(null);

  const live = (p: Pending) =>
    pending.current === p && !p.disposed && !opening?.disposed;
  const update = (p: Pending, v: Partial<View>) => {
    if (live(p)) setView((current) => current && { ...current, ...v });
  };
  const dispose = (p: Pending) => {
    p.disposed = true;
    p.url = "";
    p.retry = undefined;
    p.start = undefined;
    if (native)
      void native(p.opening, OAuthNativeAction.Dispose, "", "", "").catch(
        () => undefined,
      );
  };
  const leave = () => {
    if (approvalInput.current) approvalInput.current.value = "";
    if (pending.current) dispose(pending.current);
    pending.current = undefined;
    setView(undefined);
    setHidden(false);
    changed();
  };
  const hide = () => {
    if (approvalInput.current) approvalInput.current.value = "";
    const p = pending.current;
    if (!p || (!p.account && !p.operation && !p.busy)) {
      leave();
      return;
    }
    setHidden(true);
  };
  const show = () => {
    if (pending.current && live(pending.current)) setHidden(false);
  };
  useEffect(
    () => () => {
      if (pending.current) dispose(pending.current);
    },
    [native, opening],
  );
  useEffect(() => {
    if (!active) {
      if (pending.current) dispose(pending.current);
      pending.current = undefined;
      setView(undefined);
      setHidden(false);
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
        SubscriptionServiceId.Claude,
        p.account!.revision,
      )
    )
      throw new Error("Account ownership changed");
    if (live(p)) p.account = result.resource;
    return result.resource;
  };
  const run = async (
    p: Pending,
    action: () => Promise<void>,
    failure: MessageKey,
    replay = false,
  ) => {
    if (!live(p) || p.busy) return;
    p.busy = true;
    p.retry = undefined;
    update(p, { busy: true, problem: undefined });
    try {
      await action();
    } catch (error) {
      if (live(p)) {
        const uncertain = [
          FailureCode.Unavailable,
          FailureCode.ServerUnavailable,
          FailureCode.Canceled,
          FailureCode.Internal,
        ].includes(clientFailure(error).code);
        if (replay && uncertain) p.retry = action;
        update(p, {
          problem: failure,
          ...(action === p.start && !p.operation
            ? {
                step: Step.Terminal,
                state: uncertain
                  ? SubscriptionLoginState.RECOVERY_REQUIRED
                  : SubscriptionLoginState.FAILED,
              }
            : {}),
        });
      }
    } finally {
      p.busy = false;
      update(p, { busy: false });
    }
  };
  // Claude sign-in is server-owned: one setup-token operation, no Runner Device.
  const begin = (initial?: Resource) => {
    if (!active || pending.current || opening?.disposed) return;
    const p: Pending = {
      opening: newRequestId(),
      account: initial,
      operation: "",
      url: "",
      generation: "",
      disposed: false,
      bound: false,
      polling: false,
      busy: false,
      terminal: false,
      codeClaimed: false,
    };
    pending.current = p;
    setView({
      step: Step.Login,
      name: "Claude",
      state: SubscriptionLoginState.PREPARING,
      method: SubscriptionLoginMethod.UNSPECIFIED,
      busy: false,
      codeClaimed: false,
      browserReady: false,
    });
    const create = {
      mutation: { requestId: newRequestId() },
      kind: EntityKind.ACCOUNT,
      schemaVersion: 2,
      documentJson: encode({
        alias: "Claude",
        subscription_service: SubscriptionServiceId.Claude,
        type: "subscription",
        enabled: true,
        exclude_automatic: false,
        recovery_notifications: false,
        health: "disconnected",
        quota: [],
        confirmed_exhausted: false,
      }),
    };
    let request:
      | Parameters<typeof clients.subscription.requestSubscription>[0]
      | undefined;
    p.start = async () => {
      if (!native) throw new Error("Native browser control unavailable");
      if (!p.account) {
        const result = await clients.configuration.saveConfiguration(create);
        if (!live(p)) return;
        if (
          result.requestId !== create.mutation.requestId ||
          !serviceAccount(
            result.resource,
            undefined,
            SubscriptionServiceId.Claude,
          )
        )
          throw new Error("Invalid account receipt");
        p.account = result.resource;
        changed();
      }
      if (!live(p)) return;
      request ??= {
        mutation: {
          id: p.account.id,
          expectedRevision: p.account.revision,
          requestId: newRequestId(),
        },
        action: SubscriptionAction.LOGIN,
        deviceCode: false,
      };
      const result = await clients.subscription.requestSubscription(request);
      if (!live(p)) return;
      if (
        result.operationId !== request.mutation?.requestId ||
        !serviceAccount(
          result.account,
          p.account.id,
          SubscriptionServiceId.Claude,
          p.account.revision,
        )
      )
        throw new Error("Invalid original operation");
      p.account = result.account;
      p.operation = result.operationId;
      changed();
    };
    // One deliberate Add account click starts exactly one request. Its retained
    // request ID makes every later click a replay.
    void run(p, p.start, "claude-subscription.requestUnconfirmed", true);
  };
  useEffect(() => {
    if (!active || !view) return;
    const poll = async () => {
      const p = pending.current;
      if (!p || !live(p) || !p.operation || p.polling || p.terminal) return;
      p.polling = true;
      try {
        const result = await clients.subscription.getSubscriptionProgress({
          accountId: p.account!.id,
          operationId: p.operation,
        });
        if (!live(p)) return;
        if (result.state === SubscriptionLoginState.SUCCEEDED) {
          if (!isEntityId(result.generation))
            throw new Error("Invalid native generation");
          const current = await fresh(p),
            d = document(current),
            s = object(d.subscription);
          if (!live(p)) return;
          if (
            s.claude_credential !== "server-token" ||
            text(object(s.server_operation).id) !== p.operation ||
            Boolean(s.owner_machine_id) ||
            Boolean(s.native_profile_id) ||
            s.generation !== result.generation ||
            s.recovery_required === true ||
            !isEntityId(text(object(d.connection).id))
          )
            throw new Error("Native success ownership changed");
          p.terminal = true;
          p.url = "";
          void native!(p.opening, OAuthNativeAction.Dispose, "", "", "").catch(
            () => undefined,
          );
          update(p, {
            step: Step.Name,
            state: result.state,
            name: "Claude",
            problem: undefined,
          });
          changed();
          return;
        }
        if (
          ![
            SubscriptionLoginState.PREPARING,
            SubscriptionLoginState.WAITING,
          ].includes(result.state)
        ) {
          if (
            ![
              SubscriptionLoginState.CANCELED,
              SubscriptionLoginState.EXPIRED,
              SubscriptionLoginState.UNSUPPORTED,
              SubscriptionLoginState.RECOVERY_REQUIRED,
              SubscriptionLoginState.FAILED,
            ].includes(result.state)
          )
            throw new Error("Invalid native state");
          p.terminal = true;
          p.url = "";
          void native!(p.opening, OAuthNativeAction.Dispose, "", "", "").catch(
            () => undefined,
          );
          update(p, {
            step: Step.Terminal,
            state: result.state,
            diagnostic: result.nativeDiagnostic,
            problem: undefined,
          });
          return;
        }
        update(p, {
          step: Step.Login,
          state: result.state,
          method: result.loginMethod,
        });
        if (result.state !== SubscriptionLoginState.WAITING || result.canceled)
          return;
        if (
          result.userCode ||
          !claudeSetupTokenURL(result.url) ||
          (p.url && p.url !== result.url) ||
          result.loginMethod !== SubscriptionLoginMethod.BROWSER_CODE
        )
          throw new Error("Invalid original native URL");
        p.url = result.url;
        if (!p.bound) {
          p.bound = true;
          update(p, { browserReady: true });
          try {
            const opened = await native!(
              p.opening,
              OAuthNativeAction.ClaudeSubscriptionOpen,
              "",
              p.operation,
              p.url,
            );
            if (live(p)) {
              if (!isEntityId(opened.generation))
                throw new Error("Invalid browser ownership");
              p.generation = opened.generation;
            }
          } catch {
            update(p, { problem: "claude-subscription.browserFailed" });
          }
        }
      } catch {
        update(p, { problem: "claude-subscription.statusUnconfirmed" });
      } finally {
        p.polling = false;
      }
    };
    void poll();
    const timer = setInterval(() => void poll(), 1000);
    return () => clearInterval(timer);
  }, [active, Boolean(view), clients, native]);
  const cancel = () => {
    const p = pending.current;
    if (!p?.operation || p.terminal) return;
    let request:
      Parameters<typeof clients.subscription.cancelSubscription>[0] | undefined;
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
        };
        const result = await clients.subscription.cancelSubscription(request);
        if (
          !serviceAccount(
            result.account,
            p.account!.id,
            SubscriptionServiceId.Claude,
            current.revision,
          )
        )
          throw new Error("Cancellation receipt");
        if (live(p)) {
          p.account = result.account;
          update(p, { problem: "claude-subscription.canceling" });
        }
      },
      "claude-subscription.cancelUnconfirmed",
      true,
    );
  };
  const reopen = () => {
    const p = pending.current;
    if (!p?.url || !native) return;
    void run(
      p,
      async () => {
        const result = await native(
          p.opening,
          OAuthNativeAction.ClaudeSubscriptionReopen,
          p.generation,
          p.operation,
          p.url,
        );
        if (
          !isEntityId(result.generation) ||
          (p.generation && result.generation !== p.generation)
        )
          throw new Error("Original browser binding changed");
        if (live(p)) p.generation = result.generation;
      },
      "claude-subscription.browserFailed",
    );
  };
  const submit = (value: string) => {
    const p = pending.current;
    if (
      !p ||
      p.codeClaimed ||
      p.busy ||
      !live(p) ||
      !value ||
      !view ||
      view.method !== SubscriptionLoginMethod.BROWSER_CODE
    )
      return;
    p.codeClaimed = true;
    update(p, { codeClaimed: true });
    const code = new TextEncoder().encode(value);
    void run(
      p,
      async () => {
        try {
          if (code.length > 16384 || !code.every((b) => b >= 33 && b <= 126))
            throw new Error("Invalid original input");
          const current = await fresh(p);
          if (!live(p)) return;
          const accepted =
            await clients.subscription.submitSubscriptionLoginCode({
              mutation: {
                requestId: newRequestId(),
                id: current.id,
                expectedRevision: current.revision,
              },
              operationId: p.operation,
              code,
            });
          if (!accepted.accepted) throw new Error("Code receipt unconfirmed");
        } finally {
          code.fill(0);
        }
      },
      "claude-subscription.codeUnconfirmed",
    );
  };
  const save = () => {
    const p = pending.current;
    if (!p || view?.step !== Step.Name || !subscriptionNameValid(view.name))
      return;
    const name = view.name;
    let request:
      Parameters<typeof clients.configuration.saveConfiguration>[0] | undefined;
    void run(
      p,
      async () => {
        const current = request ? p.account! : await fresh(p);
        if (!live(p)) return;
        request ??= {
          mutation: {
            requestId: newRequestId(),
            id: current.id,
            expectedRevision: current.revision,
          },
          kind: EntityKind.ACCOUNT,
          schemaVersion: 2,
          documentJson: subscriptionAliasDocument(current, name),
        };
        const result = await clients.configuration.saveConfiguration(request);
        if (
          !serviceAccount(
            result.resource,
            current.id,
            SubscriptionServiceId.Claude,
            current.revision,
          ) ||
          document(result.resource).alias !== name
        )
          throw new Error("Name receipt");
        if (live(p)) leave();
      },
      "claude-subscription.nameUnconfirmed",
      true,
    );
  };
  const p = pending.current;
  const body =
    view && !hidden ? (
      <>
        <ClaudeSubscriptionOnboarding
          view={view}
          setApprovalInput={(input) => {
            approvalInput.current = input;
          }}
          active={active}
          name={(name) => setView((v) => v && { ...v, name })}
          cancel={cancel}
          reopen={reopen}
          submit={submit}
          save={save}
          hide={hide}
          leave={leave}
          retry={
            p?.retry
              ? () => {
                  const retry = p.retry;
                  if (retry)
                    void run(
                      p,
                      retry,
                      "claude-subscription.requestUnconfirmed",
                      true,
                    );
                }
              : undefined
          }
        />
      </>
    ) : null;
  return {
    begin,
    body,
    workflow: Boolean(view),
    retained: Boolean(p && (p.busy || p.account || p.operation || p.retry)),
    hidden,
    available: Boolean(native),
    hide,
    show,
    leave,
  };
}
interface OnboardingProps {
  view: View;
  setApprovalInput: (input: HTMLInputElement | null) => void;
  active: boolean;
  name: (name: string) => void;
  cancel: () => void;
  reopen: () => void;
  submit: (code: string) => void;
  save: () => void;
  hide: () => void;
  leave: () => void;
  retry?: () => void;
}
function ClaudeSubscriptionOnboarding(p: OnboardingProps) {
  useLocale();
  const visible = useSettingsTaskVisible(),
    back = useCloseSettingsTask(p.hide),
    id = useId(),
    code = useRef<HTMLInputElement>(null),
    name = useRef<HTMLInputElement>(null);
  const v = p.view,
    busy = !p.active || v.busy;
  useEffect(() => {
    if (visible && v.step === Step.Name)
      name.current?.focus({ preventScroll: true });
    if (!visible && code.current) code.current.value = "";
  }, [visible, v.step]);
  const step = v.step === Step.Name ? 1 : 0;
  const serverStatus: Partial<Record<SubscriptionLoginState, MessageKey>> = {
    [SubscriptionLoginState.PREPARING]: "claude-subscription.server.preparing",
    [SubscriptionLoginState.WAITING]: "claude-subscription.server.waiting",
    [SubscriptionLoginState.CANCELED]: "claude-subscription.server.canceled",
    [SubscriptionLoginState.EXPIRED]: "claude-subscription.server.expired",
    [SubscriptionLoginState.UNSUPPORTED]:
      "claude-subscription.server.unsupported",
    [SubscriptionLoginState.RECOVERY_REQUIRED]:
      "claude-subscription.server.recovery",
    [SubscriptionLoginState.FAILED]: "claude-subscription.server.failed",
  };
  const status = serverStatus;
  const d = v.diagnostic,
    safeDiagnostic =
      d &&
      (!d.detectedVersion || validInstallationVersion(d.detectedVersion)) &&
      d.phase >= 1 &&
      d.phase <= 9 &&
      Object.values(FailureCode).includes(d.code as FailureCode) &&
      isEntityId(d.correlationId);
  return (
    <section
      className="subscription-account-create subscription-onboarding desktop-wizard"
      aria-label={copy("claude-subscription.title")}
    >
      <ol
        className="subscription-onboarding-steps wizard-progress"
        aria-label={copy("claude-subscription.steps")}
      >
        {(
          [
            "claude-subscription.login",
            "claude-subscription.accountName",
          ] as readonly MessageKey[]
        ).map((key, index) => (
          <li
            key={key}
            data-completed={index < step}
            aria-current={step === index ? "step" : undefined}
          >
            <span className="wizard-step-number" aria-hidden="true">
              {index + 1}
            </span>
            {copy(key)}
          </li>
        ))}
      </ol>
      <>
        <p>{copy("claude-subscription.server.subtitle")}</p>
        {v.step === Step.Name ? (
          <>
            <p
              role="status"
              className="subscription-onboarding-status subscription-onboarding-success"
            >
              <span aria-hidden="true">✓</span>
              {copy("claude-subscription.success")}
            </p>
            <form
              id={id}
              onSubmit={(e) => {
                e.preventDefault();
                p.save();
              }}
            >
              <label htmlFor={`${id}-name`}>
                {copy("claude-subscription.accountName")}
              </label>
              <input
                id={`${id}-name`}
                ref={name}
                autoComplete="off"
                value={v.name}
                disabled={busy}
                onChange={(e) => p.name(e.target.value)}
              />
              <p>{copy("claude-subscription.nameHelp")}</p>
              <SettingsTaskActions form={id}>
                <SettingsActionButton
                  icon={SettingsActionIcon.Save}
                  className="primary"
                  disabled={busy || !subscriptionNameValid(v.name)}
                >
                  {copy("claude-subscription.saveName")}
                </SettingsActionButton>
                <SettingsActionButton
                  icon={SettingsActionIcon.Cancel}
                  type="button"
                  onClick={p.leave}
                >
                  {copy("claude-subscription.later")}
                </SettingsActionButton>
              </SettingsTaskActions>
            </form>
          </>
        ) : (
          <>
            {v.step === Step.Login ? (
              <>
                <h3>{copy("claude-subscription.server.heading")}</h3>
                <p>{copy("claude-subscription.server.body")}</p>
              </>
            ) : null}
            <p role="status" className="subscription-onboarding-status">
              {v.step === Step.Login ? (
                <span
                  className="subscription-onboarding-spinner"
                  aria-hidden="true"
                />
              ) : null}
              {v.state === SubscriptionLoginState.UNSUPPORTED ? (
                <>
                  <strong>
                    {copy("claude-subscription.validationHeading")}
                  </strong>{" "}
                </>
              ) : null}
              {copy(status[v.state] ?? "claude-subscription.failed")}
            </p>
            {v.step === Step.Login && v.browserReady ? (
              <SettingsActionButton
                icon={SettingsActionIcon.Open}
                type="button"
                disabled={busy}
                onClick={p.reopen}
              >
                {copy("claude-subscription.reopen")}
              </SettingsActionButton>
            ) : null}
            {v.step === Step.Login &&
            v.method === SubscriptionLoginMethod.BROWSER_CODE ? (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  const value = code.current?.value ?? "";
                  if (code.current) code.current.value = "";
                  p.submit(value);
                }}
              >
                <label htmlFor={`${id}-code`}>
                  {copy("claude-subscription.approvalCode")}
                </label>
                <input
                  id={`${id}-code`}
                  ref={(input) => {
                    code.current = input;
                    p.setApprovalInput(input);
                  }}
                  type="password"
                  autoComplete="off"
                  spellCheck={false}
                  maxLength={16384}
                  placeholder={copy("claude-subscription.codePlaceholder")}
                  disabled={busy || v.codeClaimed}
                />
                <p>
                  {copy(
                    v.codeClaimed
                      ? "claude-subscription.codeClaimed"
                      : "claude-subscription.codeHelp",
                  )}
                </p>
                <SettingsActionButton
                  icon={SettingsActionIcon.Confirm}
                  className="primary"
                  disabled={busy || v.codeClaimed}
                >
                  {copy("claude-subscription.submitCode")}
                </SettingsActionButton>
              </form>
            ) : null}
            {safeDiagnostic ? (
              <dl className="subscription-onboarding-diagnostic">
                <div>
                  <dt>{copy("claude-subscription.version")}</dt>
                  <dd>
                    {d.detectedVersion ||
                      copy("claude-subscription.notDetected")}
                  </dd>
                </div>
                <div>
                  <dt>{copy("claude-subscription.phase")}</dt>
                  <dd>
                    {copy(`claude-subscription.phase${d.phase}` as MessageKey)}
                  </dd>
                </div>
                <div>
                  <dt>{copy("claude-subscription.failureCode")}</dt>
                  <dd>{d.code}</dd>
                </div>
              </dl>
            ) : null}
            <SettingsTaskActions>
              {v.step === Step.Login ? (
                <SettingsActionButton
                  icon={SettingsActionIcon.Cancel}
                  type="button"
                  disabled={busy}
                  onClick={p.cancel}
                >
                  {copy("claude-subscription.cancel")}
                </SettingsActionButton>
              ) : null}
              <SettingsActionButton
                icon={SettingsActionIcon.Back}
                type="button"
                onClick={back}
              >
                {copy("claude-subscription.back")}
              </SettingsActionButton>
            </SettingsTaskActions>
          </>
        )}
      </>
      {v.problem ? <p role="alert">{copy(v.problem)}</p> : null}
      {p.retry ? (
        <SettingsActionButton
          icon={SettingsActionIcon.Retry}
          type="button"
          disabled={busy}
          onClick={p.retry}
        >
          {copy("claude-subscription.retryOriginal")}
        </SettingsActionButton>
      ) : null}
    </section>
  );
}
