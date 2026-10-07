import { ownedMessage, resolveMessage, type OwnedMessage, copy, useLocale } from "./localization";
// SPDX-License-Identifier: Apache-2.0
import { SettingsTaskActions } from "./settings-task";
import { useSettingsTaskVisible, useCloseSettingsTask, useInSettingsTask } from "./settings-task-context";
import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ConnectError, createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { AccountService, AccountOAuthFlow as WireOAuthFlow, AccountOAuthState, ErrorDetailSchema, EntityKind, FailureCode, clientFailure, newRequestId, type ErrorDetail, type AccountOAuthAttempt, type CompleteAccountOAuthResponse, type CancelAccountOAuthResponse, type GetAccountOAuthStatusResponse, type Mutation, type Resource } from "@delinoio/delidev-api-client";
import type { AccountProviderSummary } from "./account-settings";
import { useSettingsOpening } from "./settings-lifetime";
import { document } from "./documents";

export enum OAuthNativeAction { Begin = "begin", BeginHuggingFace = "begin-hugging-face", BeginGoogleGemini = "begin-google-gemini", BeginBaseten = "begin-baseten", Profiles = "profiles", SubscriptionOpen = "subscription-open", SubscriptionReopen = "subscription-reopen", BindOpen = "bind-open", Reopen = "reopen", Take = "take", Dispose = "dispose" }
export enum AccountOAuthProfile { OpenRouter = "openrouter", HuggingFace = "hugging-face", GoogleGemini = "google-gemini", Baseten = "baseten" }
export interface OAuthNativeResult { generation: string; callback_url?: string; code?: number[]; state?: number[]; profiles?: AccountOAuthProfile[]; denied?: boolean }
export type OAuthNativeControl = (opening: string, action: OAuthNativeAction, generation: string, attempt: string, authorization: string) => Promise<OAuthNativeResult>;
const NativeContext = createContext<OAuthNativeControl | undefined>(undefined);
export const useOAuthNativeControl = () => useContext(NativeContext);
export function OAuthNativeProvider({ control, children }: { control: OAuthNativeControl; children: ReactNode }) {
  useLocale(); return <NativeContext.Provider value={control}>{children}</NativeContext.Provider>; }
enum Stage { Configure, Starting, Awaiting, Exchanging, Saving, Canceling, Recovering, Connected, Canceled, Expired, Interrupted, Recovery }
interface View { provider: AccountProviderSummary; stage: Stage; userCode?: string; attempt?: AccountOAuthAttempt; account?: Resource; problem?: string | OwnedMessage; openFailed?: boolean }
interface Pending {
  provider: AccountProviderSummary; quotaProject?: string; userCode?: string; nativeOpening: string; generation: string; callback: string; startId: string;
  attempt?: AccountOAuthAttempt; completion?: Mutation; cancel?: Mutation; problem?: string | OwnedMessage; openFailed?: boolean; bound: boolean; serverStartDispatched: boolean; polling: boolean; busy: boolean; disposed: boolean;
}
const validId = (id: string) => /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(id);
function serverCodeRecovery(problem: ErrorDetail | undefined): OwnedMessage | undefined {
  if (problem?.code === "recovery_required" && ["credential_executable_changed", "credential_executable_invalid", "oauth_start_not_admitted"].includes(problem.cause)) {
    return ownedMessage("account-oauth.serverCodeRequiresRestart");
  }
}
function serverCodeError(error: unknown): OwnedMessage | undefined {
  for (const problem of ConnectError.from(error).findDetails(ErrorDetailSchema)) {
    const message = serverCodeRecovery(problem);
    if (message) return message;
  }
}
function stage(state: AccountOAuthState): Stage {
  switch (state) {
    case AccountOAuthState.ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION: return Stage.Awaiting;
    case AccountOAuthState.ACCOUNT_OAUTH_STATE_EXCHANGING: return Stage.Exchanging;
    case AccountOAuthState.ACCOUNT_OAUTH_STATE_SAVING: return Stage.Saving;
    case AccountOAuthState.ACCOUNT_OAUTH_STATE_CONNECTED: return Stage.Connected;
    case AccountOAuthState.ACCOUNT_OAUTH_STATE_CANCELED: return Stage.Canceled;
    case AccountOAuthState.ACCOUNT_OAUTH_STATE_EXPIRED: return Stage.Expired;
    case AccountOAuthState.ACCOUNT_OAUTH_STATE_INTERRUPTED: return Stage.Interrupted;
    case AccountOAuthState.ACCOUNT_OAUTH_STATE_FAILED:
    case AccountOAuthState.ACCOUNT_OAUTH_STATE_RECOVERY_REQUIRED: return Stage.Recovery;
    default: throw new Error("Unsupported OAuth state");
  }
}
function checkedAttempt(value: AccountOAuthAttempt | undefined, pending: Pending): AccountOAuthAttempt {
  if (!value || !validId(value.id) || value.revision < 1n || value.providerId !== pending.provider.providerId || !Number.isFinite(Date.parse(value.expiresAt)) || pending.attempt && (pending.attempt.id !== value.id || pending.attempt.revision > value.revision)) throw new Error("Unsupported OAuth ownership");
  stage(value.state); return value;
}
function checkedAccount(value: Resource | undefined, pending: Pending): Resource | undefined {
  if (!value) return undefined;
  const body = document(value);
  if (value.kind !== EntityKind.ACCOUNT || value.schemaVersion !== 1 || !validId(value.id) || value.revision < 1n || body.type !== "api" || body.provider_id !== pending.provider.providerId) throw new Error("Unsupported OAuth account");
  return value;
}

export function useAccountOAuth() {
  const native = useContext(NativeContext), opening = useSettingsOpening(), transport = useTransport();
  const service = useMemo(() => createClient(AccountService, transport), [transport]);
  const pending = useRef<Pending | undefined>(undefined), alive = useRef(true);
  const [view, setView] = useState<View>();
  const [profiles, setProfiles] = useState<readonly AccountOAuthProfile[]>([]);
  useEffect(() => {
    if (!native || opening?.disposed) return;
    let active = true;
    // This is an inventory read. It creates no listener and opens no browser.
    void native(newRequestId(), OAuthNativeAction.Profiles, "", "", "").then(result => {
      if (active && !opening?.disposed) setProfiles((result.profiles ?? []).filter(profile => profile === AccountOAuthProfile.OpenRouter || profile === AccountOAuthProfile.HuggingFace || profile === AccountOAuthProfile.GoogleGemini || profile === AccountOAuthProfile.Baseten));
    }).catch(() => { if (active) setProfiles([]); });
    return () => { active = false; };
  }, [native, opening]);
  const supports = (provider: AccountProviderSummary) => Boolean(native && provider.oauthAvailable && (profileOf(provider) === AccountOAuthProfile.OpenRouter || profiles.includes(profileOf(provider)!)));
  const current = (value: Pending) => alive.current && pending.current === value && !value.disposed && !opening?.disposed;
  const disposeNative = (value: Pending) => {
    value.disposed = true;
    // This uses the known local opening even when Begin's response was lost.
    // Native keeps its tombstone, so a queued late Begin cannot recreate it.
    if (native) void native(value.nativeOpening, OAuthNativeAction.Dispose, value.generation, value.attempt?.id ?? "", "").catch(() => undefined);
    value.callback = ""; value.userCode = undefined;
  };
  useEffect(() => {
    alive.current = true;
    const close = () => { if (pending.current) disposeNative(pending.current); pending.current = undefined; };
    opening?.controller.signal.addEventListener("abort", close, { once: true });
    return () => { alive.current = false; opening?.controller.signal.removeEventListener("abort", close); close(); };
  }, [opening, native]);
  const failure = (value: Pending, message: string | OwnedMessage) => { value.problem = message; if (current(value)) setView(previous => ({ provider: value.provider, stage: previous?.stage ?? Stage.Recovery, attempt: value.attempt, account: previous?.account, userCode: value.userCode, problem: message, openFailed: previous?.openFailed })); };
  const accept = (value: Pending, result: CompleteAccountOAuthResponse | CancelAccountOAuthResponse | GetAccountOAuthStatusResponse, deviceReceipt = false) => {
    const attempt = checkedAttempt(result.attempt, value), account = checkedAccount(result.account, value);
    if (deviceReceipt && profileOf(value.provider) === AccountOAuthProfile.Baseten && result.requestId && validId(result.requestId)) {
      // Device attempts remain at revision 1 until their sole Go completion.
      // Status returns that non-secret receipt after the original claim exists.
      if (value.completion && value.completion.requestId !== result.requestId) throw new Error("Device receipt ownership");
      value.completion ??= { $typeName: "delidev.v1.Mutation", id: attempt.id, expectedRevision: 1n, requestId: result.requestId };
    }
    value.attempt = attempt;
    if (attempt.state !== AccountOAuthState.ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION) value.userCode = undefined;
    if (attempt.state === AccountOAuthState.ACCOUNT_OAUTH_STATE_CONNECTED) { value.problem = undefined; value.openFailed = false; }
    if (current(value)) setView({ provider: value.provider, stage: stage(attempt.state), attempt, account, userCode: value.userCode,
      problem: serverCodeRecovery(attempt.problem) ?? (attempt.problem?.code === "permission_denied" ? ownedMessage("account-oauth.extra.50181bfbea4a") : attempt.problem ? attempt.state === AccountOAuthState.ACCOUNT_OAUTH_STATE_INTERRUPTED && !value.completion ? ownedMessage("account-oauth.extra.02b9057706ae") : ownedMessage("account-oauth.extra.50181bfbea4a") : value.problem), openFailed: value.openFailed });
  };
  const finish = async (value: Pending, code: Uint8Array, state: Uint8Array) => {
    if (!current(value) || !value.attempt || value.completion) { code.fill(0); state.fill(0); return; }
    value.completion = { $typeName: "delidev.v1.Mutation", id: value.attempt.id, expectedRevision: value.attempt.revision, requestId: newRequestId() };
    value.busy = true; setView({ provider: value.provider, stage: Stage.Exchanging, attempt: value.attempt });
    try {
      // Direct write-only RPC: code bytes never enter a mutation/query cache.
      const result = await service.completeAccountOAuth({ mutation: value.completion, authorizationCode: code, authorizationState: state });
      if (result.requestId !== value.completion.requestId) throw new Error("completion receipt");
      if (current(value)) accept(value, result);
    } catch (error) { failure(value, serverCodeError(error) ?? ownedMessage("account-oauth.extra.c91835a315d9")); }
    finally { code.fill(0); state.fill(0); value.busy = false; }
  };
  const startOriginal = async (value: Pending) => {
    if (!native || !current(value) || value.busy) return;
    value.busy = true;
    try {
      if (!value.generation) {
        const result = await native(value.nativeOpening, profileOf(value.provider) === AccountOAuthProfile.HuggingFace ? OAuthNativeAction.BeginHuggingFace : profileOf(value.provider) === AccountOAuthProfile.GoogleGemini ? OAuthNativeAction.BeginGoogleGemini : profileOf(value.provider) === AccountOAuthProfile.Baseten ? OAuthNativeAction.BeginBaseten : OAuthNativeAction.Begin, "", "", "");
        if (!current(value)) { disposeNative(value); return; }
        if (!validId(result.generation) || (profileOf(value.provider) === AccountOAuthProfile.Baseten ? Boolean(result.callback_url) : !result.callback_url)) throw new Error("callback");
        value.generation = result.generation; value.callback = result.callback_url ?? "";
      }
      value.serverStartDispatched = true;
      const result = await service.startAccountOAuth({ provider: { id: value.provider.providerId, expectedRevision: value.provider.provider.revision, requestId: value.startId }, callbackUrl: value.callback, google: value.quotaProject ? { quotaProjectId: value.quotaProject } : undefined });
      if (!current(value)) return;
      if (result.requestId !== value.startId) throw new Error("start receipt");
      value.attempt = checkedAttempt(result.attempt, value);
      const device = profileOf(value.provider) === AccountOAuthProfile.Baseten;
      if (profileOf(value.provider) !== AccountOAuthProfile.OpenRouter && result.flow !== (device ? WireOAuthFlow.ACCOUNT_OAUTH_FLOW_DEVICE : WireOAuthFlow.ACCOUNT_OAUTH_FLOW_PKCE)) throw new Error("OAuth flow");
      if (device && result.userCode && (!/^[!-~]{1,64}$/.test(result.userCode))) throw new Error("Device user code");
      if (!device && result.userCode) throw new Error("Unexpected Device code");
 value.userCode = device && value.attempt.state === AccountOAuthState.ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION ? result.userCode : undefined;
      setView({ provider: value.provider, stage: stage(value.attempt.state), attempt: value.attempt, userCode:value.userCode });
      if (value.attempt.state === AccountOAuthState.ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION && result.authorizationUrl && !value.bound) {
        try { await native(value.nativeOpening, OAuthNativeAction.BindOpen, value.generation, value.attempt.id, result.authorizationUrl); value.bound = true; value.openFailed = false; value.problem = undefined; }
        catch { value.openFailed = true; failure(value, ownedMessage("account-oauth.extra.f0d9ec9fd7e7")); }
      }
    } catch (error) {
      const rejected = ConnectError.from(error).findDetails(ErrorDetailSchema).some(detail => detail.cause === "oauth_start_not_admitted");
      if (!value.attempt && rejected) {
        value.serverStartDispatched = false;
        // Keep the opening until explicit Cancel/Back performs native disposal.
        // The cause proves admission rolled back; transport errors cannot do so.
        failure(value, serverCodeError(error) ?? ownedMessage("account-oauth.extra.3843239cf0da"));
      } else failure(value, serverCodeError(error) ?? ownedMessage("account-oauth.extra.66c9a691ff0d"));
    }
    finally { value.busy = false; }
  };
  const start = (provider: AccountProviderSummary) => {
    if (!native || pending.current || opening?.disposed || !supports(provider) || !provider.enabled) return;
    const value: Pending = { provider, nativeOpening: newRequestId(), generation: "", callback: "", startId: newRequestId(), bound: false, serverStartDispatched: false, polling: false, busy: false, disposed: false };
    pending.current = value;
    if (profileOf(provider) === AccountOAuthProfile.GoogleGemini) setView({ provider, stage: Stage.Configure });
    else { setView({ provider, stage: Stage.Starting }); void startOriginal(value); }
  };
  const observe = async (value: Pending) => {
    if (!current(value) || !value.attempt) return;
    try { const result = await service.getAccountOAuthStatus({ attemptId: value.attempt.id }); if (current(value)) accept(value, result, true); }
    catch { failure(value, ownedMessage("account-oauth.extra.f6dcc33443bb")); }
  };
  useEffect(() => {
    if (!view) return;
    const poll = async () => {
      const value = pending.current;
      if (!value || !current(value) || value.polling || !value.attempt || !native) return;
      value.polling = true;
      try {
        if (profileOf(value.provider) !== AccountOAuthProfile.Baseten && value.attempt.state === AccountOAuthState.ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION && value.bound && !value.completion && !value.busy) {
          const result = await native(value.nativeOpening, OAuthNativeAction.Take, value.generation, value.attempt.id, "");
          if (result.code || result.denied) {
            let code: Uint8Array | undefined, state: Uint8Array | undefined;
            try {
              if (result.generation !== value.generation) throw new Error("callback generation");
              if (result.denied && result.code?.length) throw new Error("ambiguous callback");
              const bytes = result.code ?? [];
              if (!Array.isArray(bytes) || (!result.denied && bytes.length < 1) || bytes.length > 8192 || bytes.some(byte => !Number.isInteger(byte) || byte < 0 || byte > 255)) throw new Error("code");
              code = Uint8Array.from(bytes);
              const stateBytes = result.state ?? [];
              if (!Array.isArray(stateBytes) || stateBytes.some(byte => !Number.isInteger(byte) || byte < 0 || byte > 255) || (profileOf(value.provider) !== AccountOAuthProfile.OpenRouter && (stateBytes.length !== 43 || stateBytes.some(byte => !/[A-Za-z0-9_-]/.test(String.fromCharCode(byte)))))) throw new Error("state");
              state = Uint8Array.from(stateBytes);
              const text = new TextDecoder("utf-8", { fatal: true }).decode(code);
              if (/[\u0000-\u001f\u007f-\u009f]/u.test(text)) throw new Error("code");
              if (current(value)) await finish(value, code, state);
            } finally { result.code?.fill(0); result.state?.fill(0); code?.fill(0); state?.fill(0); }
          }
        }
        if (current(value) && !value.busy) await observe(value);
      } catch { failure(value, ownedMessage("account-oauth.extra.1890f536687d")); }
      finally { value.polling = false; }
    };
    const timer = setInterval(() => void poll(), 500);
    return () => clearInterval(timer);
  }, [Boolean(view), native, service, opening]);
  const abandon = async (manual: boolean, done: (manual: boolean) => void) => {
    const value = pending.current;
    if (!value || !current(value) || value.busy || value.serverStartDispatched && !value.attempt) return;
    value.busy = true;
    setView(previous => previous ? { ...previous, stage: Stage.Canceling, problem: undefined } : previous);
    try {
      if (!value.attempt) {
        if (!native) return;
        await native(value.nativeOpening, OAuthNativeAction.Dispose, value.generation, "", "");
        if (!current(value)) return;
        disposeNative(value); pending.current = undefined; setView(undefined); done(manual); return;
      }
      if (value.attempt.state !== AccountOAuthState.ACCOUNT_OAUTH_STATE_CONNECTED) {
        value.cancel ??= { $typeName: "delidev.v1.Mutation", id: value.attempt.id, expectedRevision: value.attempt.revision, requestId: newRequestId() };
        const result = await service.cancelAccountOAuth({ mutation: value.cancel });
        if (!current(value)) return;
        if (result.requestId !== value.cancel.requestId) throw new Error("cancel receipt");
        accept(value, result);
        if (result.attempt?.state !== AccountOAuthState.ACCOUNT_OAUTH_STATE_CANCELED) return;
      }
      disposeNative(value); pending.current = undefined; setView(undefined); done(manual);
    } catch (error) {
      // A typed revision conflict proves this cancellation was not admitted.
      // Unknown/cleanup outcomes retain their original exact request instead.
      if (clientFailure(error).code === FailureCode.Conflict) value.cancel = undefined;
      failure(value, ownedMessage("account-oauth.extra.9668547d218a"));
    }
    finally { value.busy = false; }
  };
  const reopen = async () => {
    const value = pending.current; if (!native || !value?.attempt || !current(value) || value.busy || value.completion) return;
    if (!value.bound) { await startOriginal(value); return; }
    try { await native(value.nativeOpening, OAuthNativeAction.Reopen, value.generation, value.attempt.id, ""); value.problem = undefined; value.openFailed = false; if (current(value)) setView(previous => previous ? { ...previous, openFailed: false, problem: undefined } : previous); }
    catch { failure(value, ownedMessage("account-oauth.extra.12e9df66c23f")); }
  };
  const recover = async () => {
    const value = pending.current; if (!value?.completion || !current(value) || value.busy) return;
    value.busy = true;
    setView(previous => previous ? { ...previous, stage: Stage.Recovering, problem: undefined } : previous);
    try { const result = await service.completeAccountOAuth({ mutation: value.completion, authorizationCode: new Uint8Array() }); if (result.requestId !== value.completion.requestId) throw new Error("recovery receipt"); if (current(value)) accept(value, result); }
    catch { failure(value, ownedMessage("account-oauth.extra.01b9ed071310")); }
    finally { value.busy = false; }
  };
  const continueInBrowser = (project: string) => {
    const value = pending.current;
    if (!value || !current(value) || value.serverStartDispatched || value.busy || value.quotaProject) return;
    if (!/^[a-z][a-z0-9-]{4,28}[a-z0-9]$/.test(project)) { failure(value, ownedMessage("account-oauth.invalidGoogleCloudProjectId_9e2b1c")); return; }
    value.quotaProject = project;
    setView({ provider: value.provider, stage: Stage.Starting }); void startOriginal(value);
  };
  return { view, continueInBrowser, available: Boolean(native), supports, start, abandon, reopen, recover, retryStart: () => { const value = pending.current; if (value && !value.attempt) void startOriginal(value); }, observe: () => { const value = pending.current; if (value) void observe(value); }, completionClaimed: Boolean(pending.current?.completion), canLeave: Boolean(pending.current && (!pending.current.serverStartDispatched || pending.current.attempt)) };
}
export const useOpenRouterOAuth = useAccountOAuth;
export type AccountOAuthFlow = ReturnType<typeof useAccountOAuth>;
export type OpenRouterOAuthFlow = AccountOAuthFlow;
function profileOf(provider: AccountProviderSummary): AccountOAuthProfile | undefined {
  const preset = provider.presetId ?? document(provider.provider).preset_id;
  return preset === AccountOAuthProfile.OpenRouter ? AccountOAuthProfile.OpenRouter : preset === AccountOAuthProfile.HuggingFace ? AccountOAuthProfile.HuggingFace : preset === "gemini" ? AccountOAuthProfile.GoogleGemini : preset === "baseten" ? AccountOAuthProfile.Baseten : undefined;
}
function providerServiceName(provider: AccountProviderSummary): string {
  return profileOf(provider) === AccountOAuthProfile.HuggingFace ? "Hugging Face" : provider.displayName;
}
function recoveryMessage(provider: AccountProviderSummary): string {
  return profileOf(provider) === AccountOAuthProfile.OpenRouter ? "The original result requires recovery. Inspect the OpenRouter keys dashboard; local cancellation cannot revoke a provider key. Recover only the original saved local result." : `The original ${provider.displayName} result requires recovery. Local cancellation cannot revoke provider access. Recover only the original saved local result, or cancel and reconnect.`;
}

export function AccountOAuth({ flow, back, manual, done }: { flow: OpenRouterOAuthFlow; back: () => void; manual: () => void; done: (account: Resource) => void }) {
  useLocale();
  const visible = useSettingsTaskVisible(), closeTask = useCloseSettingsTask(back), inTask = useInSettingsTask();
  const heading = useRef<HTMLHeadingElement>(null), view = flow.view;
  const [project, setProject] = useState("");
  useEffect(() => { if (visible) heading.current?.focus(); }, [view?.provider.providerId, visible]);
  const connected = view?.stage === Stage.Connected ? view.account : undefined;
  useEffect(() => {
    if (!visible || !connected) return;
    // Reuse Done's original-attempt cleanup. abandon clears pending ownership
    // synchronously for connected attempts, so effect replay cannot finish twice.
    void flow.abandon(false, () => done(connected));
  }, [visible, connected, flow, done]);
  if (!view || connected) return null;
  const busy = view.stage === Stage.Starting || view.stage === Stage.Exchanging || view.stage === Stage.Saving || view.stage === Stage.Canceling || view.stage === Stage.Recovering;
  const waiting = view.stage === Stage.Awaiting;
  const configuring = view.stage === Stage.Configure;
  const huggingFace = profileOf(view.provider) === AccountOAuthProfile.HuggingFace;
  const progress = view.stage === Stage.Starting ? copy("account-oauth.extra.d2fd2ff796d5") : view.stage === Stage.Exchanging ? copy("account-oauth.extra.e290f644cae5") : view.stage === Stage.Saving ? copy("account-oauth.extra.adfcae535266") : view.stage === Stage.Canceling ? copy("account-oauth.extra.1d7dcbdd28ae") : view.stage === Stage.Recovering ? copy("account-oauth.extra.b62b51814edd") : waiting ? copy("account-oauth.extra.808197b5a070") : view.stage === Stage.Expired ? copy("account-oauth.extra.92b4263f2141") : view.stage === Stage.Interrupted ? copy("account-oauth.extra.3b6a9f24087b") : view.stage === Stage.Canceled ? copy("account-oauth.extra.9198736066a6") : copy("account-oauth.extra.dcf547440e7c");
  const leave = (fallback: boolean, callback: () => void) => void flow.abandon(fallback, () => callback());
  return <section className="api-keys-view account-oauth-card" aria-labelledby="account-oauth-title">
    <h2 id="account-oauth-title" tabIndex={-1} ref={heading}>{configuring ? copy("account-oauth.chooseGoogleCloudProject_0f5a3e") : huggingFace ? copy("account-oauth.connectHuggingFaceInferenceProviders_7b4d2c") : copy("account-oauth.connectOpenrouter_6c38bc")}</h2>
    <p className="account-oauth-subheading">{configuring ? copy("account-oauth.chooseGoogleCloudProject_0f5a3e") : copy("account-oauth.completeSignInInYourBrowser_64e524")}</p>
    {configuring ? <p>{copy("account-oauth.useGoogleCloudProjectToPay_2b7c11")}</p> : <p>{huggingFace ? copy("account-oauth.approveAccessOnProviderDelidevWill_7f1c4a", { v0: providerServiceName(view.provider) }) : copy("account-oauth.approveAccessOnOpenrouterDelidevWill_8d81b0")}</p>}
    {configuring ? <label className="account-oauth-project">{copy("account-oauth.googleCloudProjectId_4e7d2a")}<input value={project} maxLength={30} autoComplete="off" spellCheck={false} onChange={event => setProject(event.target.value)} /></label> : null}
    {!configuring ? <div className="account-oauth-progress" role="status" aria-live="polite"><span className="account-oauth-spinner" aria-hidden="true" />{progress}</div> : null}
    {waiting && view.userCode ? <div className="account-oauth-device"><p>{copy("account-oauth.ifAskedEnterThisCode_6d3a1f")}</p><output aria-label={copy("account-oauth.temporaryAuthorizationCode_1d4e7a")} className="account-oauth-user-code">{view.userCode}</output></div> : null}
    {view.problem ? <p role="alert">{resolveMessage(view.problem)}</p> : null}
    <SettingsTaskActions>{configuring ? <button onClick={() => flow.continueInBrowser(project)}>{copy("account-oauth.continueInBrowser_7c2d9b")}</button> : <button disabled={!waiting || flow.completionClaimed} onClick={() => void flow.reopen()}>{copy("account-oauth.openBrowserAgain_63833e")}</button>}<button hidden={configuring} data-settings-task-cancel disabled={!inTask && (busy && !view.problem || !flow.canLeave)} onClick={inTask ? closeTask : () => leave(false, back)}>{copy("account-oauth.cancel_19766e")}</button><button disabled={busy && !view.problem || !flow.canLeave} onClick={() => leave(false, back)}>{copy("account-oauth.backToProviders_efe541")}</button></SettingsTaskActions>
    <button className="account-oauth-fallback" disabled={busy && !view.problem || !flow.canLeave} onClick={() => leave(true, manual)}>{copy("account-oauth.useAnApiKeyInstead_b728ab")}</button>
    {view.problem && !configuring ? <SettingsTaskActions><button onClick={flow.observe} disabled={!view.attempt}>{copy("account-oauth.inspectOriginalAttempt_887b78")}</button>{!view.attempt ? <button onClick={flow.retryStart}>{copy("account-oauth.retryOriginalStart_eefc3a")}</button> : null}{flow.completionClaimed ? <button onClick={() => void flow.recover()}>{copy("account-oauth.recoverSavedResult_3cb5e6")}</button> : null}</SettingsTaskActions> : null}
    <footer><p>{copy("account-oauth.yourCredentialWillBeStoredSecurely_26be74")}</p>{!configuring ? <p>{copy("account-oauth.youCanValidateYourAccountAfter_70f97e")}</p> : null}</footer>
  </section>;
}

export const OpenRouterOAuth = AccountOAuth;
