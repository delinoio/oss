import { useLocale, ownedMessage, resolveMessage, type OwnedMessage, copy } from "./localization";
import { useEffect, useMemo, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { ConfigurationService, ResourceService, SubscriptionService, EntityKind, SubscriptionAction, SubscriptionLoginState, SubscriptionServiceId, FailureCode, clientFailure, isEntityId, newRequestId, subscriptionServiceNames, type Resource, type CodexDiagnostic, type GrokDiagnostic } from "@delinoio/delidev-api-client";
import { OAuthNativeAction, useOAuthNativeControl } from "./account-oauth";
import { useSettingsOpening } from "./settings-lifetime";
import { document, encode, object, text } from "./documents";
import { serviceAccount, subscriptionAliasDocument } from "./subscription-resource";
import { SubscriptionOnboarding, SubscriptionOnboardingStage as Stage, subscriptionNameValid } from "./subscription-onboarding";

export enum SubscriptionLoginMethod { Browser = "browser", DeviceCode = "device-code" }
interface View { service: SubscriptionServiceId; stage: Stage; name: string; suggested: boolean; busy: boolean; problem?: string | OwnedMessage; diagnostic?: CodexDiagnostic; grokDiagnostic?: GrokDiagnostic; browserReady: boolean; method: SubscriptionLoginMethod; userCode?: string; copied?: boolean }
interface Pending {
  service: SubscriptionServiceId; opening: string; generation: string; account?: Resource; operation: string; url: string;
  bound: boolean; callbackDispatched: boolean; disposed: boolean; polling: boolean; busy: boolean; named: boolean; terminal: boolean;
  retry?: () => Promise<void>;
  method: SubscriptionLoginMethod; userCode: string; nativeVerified: boolean;
}
const grokDeviceURI = "https://accounts.x.ai/oauth2/device";
const grokScopes = "openid profile email offline_access grok-cli:access api:access conversations:read conversations:write workspaces:read workspaces:write";
export function subscriptionBrowserURL(value: string, service: SubscriptionServiceId) {
  try {
    const url = new URL(value), q = url.searchParams;
    if (Array.from(q.keys()).some(key => q.getAll(key).length !== 1)) return false;
    const callback = q.get("redirect_uri");
    if (service === SubscriptionServiceId.Grok) {
      if (!callback) return false;
      const redirect = new URL(callback), port = Number(redirect.port);
      return value.length <= 8192 && url.href === value && url.origin === "https://auth.x.ai" && url.pathname === "/oauth2/authorize" && !url.username && !url.password && !url.hash &&
        Number.isInteger(port) && port > 0 && port <= 65535 && callback === `http://127.0.0.1:${port}/callback` &&
        Array.from(q.keys()).length === 9 && q.get("client_id") === "b1a00492-073a-47ea-816f-4c329264a828" && q.get("response_type") === "code" && q.get("code_challenge_method") === "S256" && q.get("scope") === grokScopes && q.get("referrer") === "grok-build" &&
        ["state", "nonce", "code_challenge"].every(key => /^[a-zA-Z0-9_-]{43}$/.test(q.get(key) ?? ""));
    }
    if (service !== SubscriptionServiceId.ChatGPT) return false;
    return value.length <= 8192 && url.protocol === "https:" && url.host === "auth.openai.com" && !url.username && !url.password && !url.hash && url.pathname === "/oauth/authorize" && (callback === "http://localhost:1457/auth/callback" || callback === "http://127.0.0.1:1457/auth/callback") && /^[a-zA-Z0-9_-]{16,512}$/.test(q.get("state") ?? "");
  } catch { return false; }
}
const stages: Partial<Record<SubscriptionLoginState, Stage>> = {
  [SubscriptionLoginState.PREPARING]: Stage.Preparing, [SubscriptionLoginState.WAITING]: Stage.Waiting,
  [SubscriptionLoginState.SUCCEEDED]: Stage.Naming, [SubscriptionLoginState.CANCELED]: Stage.Canceled,
  [SubscriptionLoginState.EXPIRED]: Stage.Expired, [SubscriptionLoginState.UNSUPPORTED]: Stage.Unsupported,
  [SubscriptionLoginState.RECOVERY_REQUIRED]: Stage.Recovery, [SubscriptionLoginState.FAILED]: Stage.Failed,
};

export function useSubscriptionLogin(active: boolean, changed: () => void) {
  useLocale();
  const transport = useTransport(), opening = useSettingsOpening(), native = useOAuthNativeControl();
  const clients = useMemo(() => ({ configuration: createClient(ConfigurationService, transport), resource: createClient(ResourceService, transport), subscription: createClient(SubscriptionService, transport) }), [transport]);
  const [view, setView] = useState<View>();
  const pending = useRef<Pending | undefined>(undefined);
  const live = (p: Pending) => pending.current === p && !p.disposed && !opening?.disposed;
  const update = (p: Pending, change: Partial<View>) => { if (live(p)) setView((current) => current && { ...current, ...change }); };
  const dispose = (p: Pending) => {
    p.disposed = true; p.retry = undefined; p.url = ""; p.userCode = "";
    if (native) void native(p.opening, OAuthNativeAction.Dispose, "", "", "").catch(() => undefined);
  };
  const leave = () => { const p = pending.current; if (p) dispose(p); pending.current = undefined; setView(undefined); changed(); };
  useEffect(() => () => { if (pending.current) dispose(pending.current); }, [opening, native]);
  useEffect(() => { if (!active && pending.current) { dispose(pending.current); pending.current = undefined; setView(undefined); } }, [active]);

  const account = async (p: Pending) => {
    const result = await clients.resource.getResource({ kind: EntityKind.ACCOUNT, id: p.account!.id });
    if (!serviceAccount(result.resource, p.account!.id, p.service, p.account!.revision)) throw new Error("Account ownership changed");
    if (live(p)) p.account = result.resource;
    return result.resource;
  };
  const run = async (p: Pending, action: () => Promise<void>, problem: string | OwnedMessage) => {
    if (!live(p) || p.busy) return;
    p.busy = true; p.retry = undefined; update(p, { busy: true, problem: undefined });
    try { await action(); }
    catch (error) {
      if (live(p)) {
        const failure = clientFailure(error).code;
        // Internal can follow durable admission when reading the account fails.
        // Retain the exact action for explicit replay after uncertain responses;
        // typed revision conflicts require a fresh read and another explicit Save.
        if ([FailureCode.Unavailable, FailureCode.ServerUnavailable, FailureCode.Canceled, FailureCode.Internal].includes(failure)) p.retry = action;
        update(p, { problem });
      }
    } finally { p.busy = false; update(p, { busy: false }); }
  };
  const begin = (service: SubscriptionServiceId, initial?: Resource, method = SubscriptionLoginMethod.Browser) => {
    if (!active || pending.current || opening?.disposed) return;
    const supported = service === SubscriptionServiceId.ChatGPT && method === SubscriptionLoginMethod.Browser || service === SubscriptionServiceId.Grok;
    const p: Pending = { service, method, userCode: "", nativeVerified: false, opening: newRequestId(), generation: "", account: initial, operation: "", url: "", bound: false, callbackDispatched: false, disposed: false, polling: false, busy: false, named: false, terminal: false };
    pending.current = p;
    setView({ service, method, stage: supported ? Stage.Preparing : Stage.Unsupported, name: subscriptionServiceNames[service], suggested: false, busy: false, browserReady: false });
    if (!supported) return;
    if (!native) { update(p, { stage: Stage.Unsupported, problem: ownedMessage("subscription-login.extra.22765583eb23") }); return; }
    const create = { mutation: { requestId: newRequestId() }, kind: EntityKind.ACCOUNT, schemaVersion: 2, documentJson: encode({ alias: subscriptionServiceNames[service], subscription_service: service, type: "subscription", enabled: true, exclude_automatic: false, recovery_notifications: false, health: "disconnected", quota: [], confirmed_exhausted: false }) };
    let login: Parameters<typeof clients.subscription.requestSubscription>[0] | undefined;
    const start = async () => {
      if (service === SubscriptionServiceId.Grok && !p.nativeVerified) {
        // Inspect the trusted host before admitting a server login. Older
        // desktop hosts keep ChatGPT support without acquiring Grok callbacks.
        const supported = (await native(p.opening, OAuthNativeAction.SubscriptionProfiles, "", "", "")).subscription_services;
        if (!live(p)) return;
        if (!supported?.includes(SubscriptionServiceId.Grok) || supported.length > 2 || new Set(supported).size !== supported.length || supported.some(value => value !== SubscriptionServiceId.ChatGPT && value !== SubscriptionServiceId.Grok)) {
          update(p, { stage: Stage.Unsupported, problem: ownedMessage("subscription-login.nativeGrokUnavailable") }); return;
        }
        p.nativeVerified = true;
      }
      if (!p.account) {
        const result = await clients.configuration.saveConfiguration(create);
        if (!live(p)) return;
        if (result.requestId !== create.mutation.requestId || !serviceAccount(result.resource, undefined, service) || document(result.resource).alias !== subscriptionServiceNames[service]) throw new Error("Invalid created account");
        p.account = result.resource; changed();
      }
      if (!live(p)) return;
      login ??= { mutation: { id: p.account.id, expectedRevision: p.account.revision, requestId: newRequestId() }, action: SubscriptionAction.LOGIN, deviceCode: method === SubscriptionLoginMethod.DeviceCode };
      const result = await clients.subscription.requestSubscription(login);
      if (!live(p)) return;
      if (result.operationId !== login.mutation?.requestId || !serviceAccount(result.account, p.account.id, service, p.account.revision)) throw new Error("Invalid login ownership");
      p.account = result.account; p.operation = result.operationId;
    };
    void run(p, start, ownedMessage("subscription-login.extra.07d42279a62f"));
  };

  // Sensitive progress stays in this opening, outside the shared query cache.
  // Polling only observes an explicitly admitted operation; it cannot start it.
  useEffect(() => {
    if (!active || !view) return;
    const poll = async () => {
      const p = pending.current;
      if (!p || !live(p) || !p.operation || p.polling || p.busy || p.named || p.terminal) return;
      p.polling = true;
      try {
        const progress = await clients.subscription.getSubscriptionProgress({ accountId: p.account!.id, operationId: p.operation });
        if (!live(p)) return;
        const stage = stages[progress.state];
        if (!stage) throw new Error("Unsupported login state");
        if (stage === Stage.Naming) {
          if (!isEntityId(progress.generation)) throw new Error("Invalid login generation");
          const current = await account(p), data = document(current), state = object(data.subscription);
          if (!live(p)) return;
          if (text(object(state.server_operation).id) !== p.operation || state.generation !== progress.generation || !isEntityId(text(object(data.connection).id)) || state.recovery_required === true) throw new Error("Login success could not be verified");
          p.named = true;
          p.url = ""; p.userCode = ""; p.retry = undefined;
          void native!(p.opening, OAuthNativeAction.Dispose, "", "", "").catch(() => undefined);
          const suggested = subscriptionNameValid(progress.suggestedName) ? progress.suggestedName : subscriptionServiceNames[p.service];
          update(p, { stage, name: suggested, suggested: Boolean(progress.suggestedName), userCode: undefined, copied: false, browserReady: false }); changed(); return;
        }
        const terminal = ![Stage.Preparing, Stage.Waiting].includes(stage);
        if (p.service === SubscriptionServiceId.Grok && progress.diagnostic || p.service !== SubscriptionServiceId.Grok && progress.grokDiagnostic) throw new Error("Foreign login diagnostic");
        update(p, { stage, diagnostic: progress.diagnostic, grokDiagnostic: progress.grokDiagnostic, ...(terminal ? { problem: undefined, userCode: undefined, copied: false, browserReady: false } : {}) });
        if (terminal) {
          p.terminal = true; p.url = ""; p.userCode = ""; p.retry = undefined;
          void native!(p.opening, OAuthNativeAction.Dispose, "", "", "").catch(() => undefined);
        }
        if (progress.canceled) {
          p.url = ""; p.userCode = "";
          update(p, { browserReady: false, userCode: undefined, copied: false, problem: ownedMessage("subscription-accounts.cancellationRequestedWaitingForOriginalCleanup_4f54b9") }); return;
        }
        if (stage !== Stage.Waiting) return;
        if (p.method === SubscriptionLoginMethod.DeviceCode) {
          if (p.service !== SubscriptionServiceId.Grok || progress.url !== grokDeviceURI || !/^[A-Z0-9-]{4,64}$/.test(progress.userCode) || p.url && p.url !== progress.url || p.userCode && p.userCode !== progress.userCode) throw new Error("Invalid original device login");
          p.url = progress.url; p.userCode = progress.userCode;
          update(p, { userCode: p.userCode, browserReady: true }); return;
        }
        if (progress.userCode || !subscriptionBrowserURL(progress.url, p.service) || p.url && p.url !== progress.url) throw new Error("Invalid original browser login");
        p.url = progress.url;
        if (!p.bound) {
          // Set before dispatch: an uncertain native reply must not reopen or
          // create another callback receiver on the next poll.
          p.bound = true;
          update(p, { stage: Stage.Preparing });
          try {
            const result = await native!(p.opening, OAuthNativeAction.SubscriptionOpen, "", p.operation, p.url);
            if (!live(p)) return;
            if (!isEntityId(result.generation)) throw new Error("Invalid native generation");
            p.generation = result.generation; update(p, { stage: Stage.Waiting, browserReady: true, problem: undefined });
          } catch { update(p, { stage: Stage.Waiting, browserReady: true, problem: ownedMessage("subscription-login.extra.c7ce5cc2ddce") }); }
        }
        if (p.generation && !p.callbackDispatched) {
          let result;
          try { result = await native!(p.opening, OAuthNativeAction.Take, p.generation, p.operation, ""); }
          catch {
            p.callbackDispatched = true;
            update(p, { problem: ownedMessage("subscription-login.extra.53784001245e") });
            return;
          }
          if (!live(p)) { result.code?.fill(0); return; }
          if (result.generation !== p.generation) { result.code?.fill(0); throw new Error("Native callback ownership changed"); }
          if (result.code) {
            p.callbackDispatched = true;
            if (!result.code.length || result.code.length > 16384 || !result.code.every(value => Number.isInteger(value) && value >= 0 && value <= 255)) {
              result.code.fill(0); throw new Error("Invalid native callback bytes");
            }
            const query = new Uint8Array(result.code);
            try {
              await clients.subscription.forwardSubscriptionCallback({ accountId: p.account!.id, operationId: p.operation, callbackQuery: query });
            } catch { update(p, { problem: ownedMessage("subscription-login.extra.c8dad9da5412") }); }
            finally { query.fill(0); result.code.fill(0); }
          }
        }
      } catch { update(p, { problem: ownedMessage("subscription-login.extra.0e515dfbf962") }); }
      finally { p.polling = false; }
    };
    void poll(); const timer = setInterval(() => void poll(), 1000); return () => clearInterval(timer);
  }, [active, Boolean(view), clients, native]);

  const reopen = () => {
    const p = pending.current; if (!p || !live(p) || !p.url || !native) return;
    void run(p, async () => {
      if (!p.generation) {
        const action = p.method === SubscriptionLoginMethod.DeviceCode && !p.bound ? OAuthNativeAction.SubscriptionDeviceOpen : OAuthNativeAction.SubscriptionReopen;
        p.bound = true;
        const original = await native(p.opening, action, "", p.operation, p.url);
        if (!live(p)) return;
        if (!isEntityId(original.generation)) throw new Error("Invalid native binding");
        p.generation = original.generation; return;
      }
      const result = await native(p.opening, OAuthNativeAction.Reopen, p.generation, p.operation, "");
      if (result.generation !== p.generation) throw new Error("Invalid native browser binding");
    }, ownedMessage("subscription-login.extra.34521ed193ad"));
  };
  const copyCode = () => {
    const p = pending.current;
    if (!p || !live(p) || p.method !== SubscriptionLoginMethod.DeviceCode || !p.userCode) return;
    if (!navigator.clipboard) { update(p, { problem: ownedMessage("subscription-login.copyCodeFailed") }); return; }
    void navigator.clipboard.writeText(p.userCode).then(() => update(p, { copied: true })).catch(() => update(p, { problem: ownedMessage("subscription-login.copyCodeFailed") }));
  };
  const cancel = () => {
    const p = pending.current; if (!p || !live(p) || !p.operation) return;
    let request: Parameters<typeof clients.subscription.cancelSubscription>[0] | undefined;
    void run(p, async () => {
      const current = request ? p.account! : await account(p); if (!live(p)) return;
      if (!request) {
        const state = object(document(current).subscription);
        if (text(object(state.pending).id) !== p.operation || text(object(state.server_operation).id) !== p.operation) throw new Error("Original cancellation ownership changed");
      }
      request ??= { mutation: { id: current.id, expectedRevision: current.revision, requestId: newRequestId() } };
      const result = await clients.subscription.cancelSubscription(request);
      if (!serviceAccount(result.account, current.id, p.service, current.revision)) throw new Error("Invalid cancellation");
      if (live(p)) p.account = result.account;
    }, ownedMessage("subscription-login.extra.13e06932e780"));
  };
  const save = () => {
    const p = pending.current; if (!p || !live(p) || !p.named || !view || !subscriptionNameValid(view.name)) return;
    const alias = view.name;
    let request: Parameters<typeof clients.configuration.saveConfiguration>[0] | undefined;
    void run(p, async () => {
      const current = request ? p.account! : await account(p); if (!live(p)) return;
      request ??= { mutation: { id: current.id, expectedRevision: current.revision, requestId: newRequestId() }, kind: EntityKind.ACCOUNT, schemaVersion: 2, documentJson: subscriptionAliasDocument(current, alias) };
      const result = await clients.configuration.saveConfiguration(request);
      if (!live(p)) return;
      if (result.requestId !== request.mutation?.requestId || !serviceAccount(result.resource, current.id, p.service, current.revision) || document(result.resource).alias !== alias) throw new Error("Invalid saved name");
      leave();
    }, ownedMessage("subscription-login.extra.21ebd4c8705a"));
  };
  const p = pending.current;
  const body = view ? <><SubscriptionOnboarding serviceName={subscriptionServiceNames[view.service]} stage={view.stage} active={active} name={view.name} suggested={view.suggested} busy={view.busy} problem={resolveMessage(view.problem)} diagnostic={view.diagnostic} grokDiagnostic={view.grokDiagnostic} deviceCode={view.userCode} copied={view.copied} copyCode={copyCode} canReopen={view.stage === Stage.Waiting && view.browserReady} canCancel={Boolean(p?.operation) && [Stage.Preparing, Stage.Waiting].includes(view.stage)} changeName={(name) => setView((v) => v && { ...v, name })} saveName={save} reopen={reopen} cancel={cancel} leave={leave} />{p?.retry ? <button type="button" disabled={view.busy} onClick={() => { const original = p.retry; if (original) void run(p, original, view.problem ?? ownedMessage("subscription-login.extra.557b693dbfb7")); }}>{copy("subscription-login.retryOriginalRequest_008780")}</button> : null}</> : null;
  return { begin, body, workflow: Boolean(view), retained: Boolean(p?.busy || p?.retry || p?.operation || p?.account), available: Boolean(native), leave };
}
