// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ConnectError, createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { AccountService, AccountOAuthFlow as WireOAuthFlow, AccountOAuthState, ErrorDetailSchema, EntityKind, FailureCode, clientFailure, newRequestId, type AccountOAuthAttempt, type CompleteAccountOAuthResponse, type CancelAccountOAuthResponse, type GetAccountOAuthStatusResponse, type Mutation, type Resource } from "@delinoio/delidev-api-client";
import type { AccountProviderSummary } from "./account-settings";
import { useSettingsOpening } from "./settings-lifetime";
import { document } from "./documents";

export enum OAuthNativeAction { Begin = "begin", BeginHuggingFace = "begin-hugging-face", Profiles = "profiles", SubscriptionOpen = "subscription-open", SubscriptionReopen = "subscription-reopen", BindOpen = "bind-open", Reopen = "reopen", Take = "take", Dispose = "dispose" }
export enum AccountOAuthProfile { OpenRouter = "openrouter", HuggingFace = "hugging-face" }
export interface OAuthNativeResult { generation: string; callback_url?: string; code?: number[]; state?: number[]; profiles?: AccountOAuthProfile[]; denied?: boolean }
export type OAuthNativeControl = (opening: string, action: OAuthNativeAction, generation: string, attempt: string, authorization: string) => Promise<OAuthNativeResult>;
const NativeContext = createContext<OAuthNativeControl | undefined>(undefined);
export const useOAuthNativeControl = () => useContext(NativeContext);
export function OAuthNativeProvider({ control, children }: { control: OAuthNativeControl; children: ReactNode }) { return <NativeContext.Provider value={control}>{children}</NativeContext.Provider>; }
enum Stage { Starting, Awaiting, Exchanging, Saving, Canceling, Recovering, Connected, Canceled, Expired, Interrupted, Recovery }
interface View { provider: AccountProviderSummary; stage: Stage; attempt?: AccountOAuthAttempt; account?: Resource; problem?: string; openFailed?: boolean }
interface Pending {
  provider: AccountProviderSummary; nativeOpening: string; generation: string; callback: string; startId: string;
  attempt?: AccountOAuthAttempt; completion?: Mutation; cancel?: Mutation; problem?: string; openFailed?: boolean; bound: boolean; serverStartDispatched: boolean; polling: boolean; busy: boolean; disposed: boolean;
}
const validId = (id: string) => /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(id);
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
      if (active && !opening?.disposed) setProfiles((result.profiles ?? []).filter(profile => profile === AccountOAuthProfile.OpenRouter || profile === AccountOAuthProfile.HuggingFace));
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
    value.callback = "";
  };
  useEffect(() => {
    alive.current = true;
    const close = () => { if (pending.current) disposeNative(pending.current); pending.current = undefined; };
    opening?.controller.signal.addEventListener("abort", close, { once: true });
    return () => { alive.current = false; opening?.controller.signal.removeEventListener("abort", close); close(); };
  }, [opening, native]);
  const failure = (value: Pending, message: string) => { value.problem = message; if (current(value)) setView(previous => ({ provider: value.provider, stage: previous?.stage ?? Stage.Recovery, attempt: value.attempt, account: previous?.account, problem: message, openFailed: previous?.openFailed })); };
  const accept = (value: Pending, result: CompleteAccountOAuthResponse | CancelAccountOAuthResponse | GetAccountOAuthStatusResponse) => {
    const attempt = checkedAttempt(result.attempt, value), account = checkedAccount(result.account, value);
    value.attempt = attempt;
    if (attempt.state === AccountOAuthState.ACCOUNT_OAUTH_STATE_CONNECTED) { value.problem = undefined; value.openFailed = false; }
    if (current(value)) setView({ provider: value.provider, stage: stage(attempt.state), attempt, account,
      problem: attempt.problem?.code === "permission_denied" ? `${value.provider.displayName} authorization was denied. Cancel before starting another connection.` : attempt.problem ? attempt.state === AccountOAuthState.ACCOUNT_OAUTH_STATE_INTERRUPTED && !value.completion ? "Authorization is no longer available for this provider. Cancel the original attempt and refresh the provider, or use an API key instead." : recoveryMessage(value.provider) : value.problem, openFailed: value.openFailed });
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
    } catch { failure(value, "Completion was not confirmed. Inspect the original attempt. An uncertain exchange is never repeated."); }
    finally { code.fill(0); state.fill(0); value.busy = false; }
  };
  const startOriginal = async (value: Pending) => {
    if (!native || !current(value) || value.busy) return;
    value.busy = true;
    try {
      if (!value.generation) {
        const result = await native(value.nativeOpening, profileOf(value.provider) === AccountOAuthProfile.HuggingFace ? OAuthNativeAction.BeginHuggingFace : OAuthNativeAction.Begin, "", "", "");
        if (!current(value)) { disposeNative(value); return; }
        if (!validId(result.generation) || !result.callback_url) throw new Error("callback");
        value.generation = result.generation; value.callback = result.callback_url;
      }
      value.serverStartDispatched = true;
      const result = await service.startAccountOAuth({ provider: { id: value.provider.providerId, expectedRevision: value.provider.provider.revision, requestId: value.startId }, callbackUrl: value.callback });
      if (!current(value)) return;
      if (result.requestId !== value.startId) throw new Error("start receipt");
      value.attempt = checkedAttempt(result.attempt, value);
      if (profileOf(value.provider) !== AccountOAuthProfile.OpenRouter && result.flow !== WireOAuthFlow.ACCOUNT_OAUTH_FLOW_PKCE) throw new Error("OAuth flow");
      setView({ provider: value.provider, stage: stage(value.attempt.state), attempt: value.attempt });
      if (value.attempt.state === AccountOAuthState.ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION && result.authorizationUrl && !value.bound) {
        try { await native(value.nativeOpening, OAuthNativeAction.BindOpen, value.generation, value.attempt.id, result.authorizationUrl); value.bound = true; value.openFailed = false; value.problem = undefined; }
        catch { value.openFailed = true; failure(value, "The browser could not be opened. Open it again deliberately or cancel this connection."); }
      }
    } catch (error) {
      const rejected = ConnectError.from(error).findDetails(ErrorDetailSchema).some(detail => detail.cause === "oauth_start_not_admitted");
      if (!value.attempt && rejected) {
        value.serverStartDispatched = false;
        // Keep the opening until explicit Cancel/Back performs native disposal.
        // The cause proves admission rolled back; transport errors cannot do so.
        failure(value, "Authorization was not started. Cancel or return to providers to refresh this provider, or use an API key instead.");
      } else failure(value, "Authorization could not be confirmed. Retry only the original start or inspect the original attempt before starting another connection.");
    }
    finally { value.busy = false; }
  };
  const start = (provider: AccountProviderSummary) => {
    if (!native || pending.current || opening?.disposed || !supports(provider) || !provider.enabled) return;
    const value: Pending = { provider, nativeOpening: newRequestId(), generation: "", callback: "", startId: newRequestId(), bound: false, serverStartDispatched: false, polling: false, busy: false, disposed: false };
    pending.current = value; setView({ provider, stage: Stage.Starting }); void startOriginal(value);
  };
  const observe = async (value: Pending) => {
    if (!current(value) || !value.attempt) return;
    try { const result = await service.getAccountOAuthStatus({ attemptId: value.attempt.id }); if (current(value)) accept(value, result); }
    catch { failure(value, "The original connection status is unavailable. Inspect again; this does not repeat authorization or exchange."); }
  };
  useEffect(() => {
    if (!view) return;
    const poll = async () => {
      const value = pending.current;
      if (!value || !current(value) || value.polling || !value.attempt || !native) return;
      value.polling = true;
      try {
        if (value.attempt.state === AccountOAuthState.ACCOUNT_OAUTH_STATE_AWAITING_AUTHORIZATION && value.bound && !value.completion && !value.busy) {
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
      } catch { failure(value, "The original browser callback could not be verified. Inspect or cancel this attempt; no new exchange was sent."); }
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
      failure(value, "Cancellation or credential cleanup was not confirmed. Keep this original attempt and retry its cancellation before another connection.");
    }
    finally { value.busy = false; }
  };
  const reopen = async () => {
    const value = pending.current; if (!native || !value?.attempt || !current(value) || value.busy || value.completion) return;
    if (!value.bound) { await startOriginal(value); return; }
    try { await native(value.nativeOpening, OAuthNativeAction.Reopen, value.generation, value.attempt.id, ""); value.problem = undefined; value.openFailed = false; if (current(value)) setView(previous => previous ? { ...previous, openFailed: false, problem: undefined } : previous); }
    catch { failure(value, "The browser could not be opened for this original live attempt. Cancel before starting another."); }
  };
  const recover = async () => {
    const value = pending.current; if (!value?.completion || !current(value) || value.busy) return;
    value.busy = true;
    setView(previous => previous ? { ...previous, stage: Stage.Recovering, problem: undefined } : previous);
    try { const result = await service.completeAccountOAuth({ mutation: value.completion, authorizationCode: new Uint8Array() }); if (result.requestId !== value.completion.requestId) throw new Error("recovery receipt"); if (current(value)) accept(value, result); }
    catch { failure(value, recoveryMessage(value.provider)); }
    finally { value.busy = false; }
  };
  return { view, available: Boolean(native), supports, start, abandon, reopen, recover, retryStart: () => { const value = pending.current; if (value && !value.attempt) void startOriginal(value); }, observe: () => { const value = pending.current; if (value) void observe(value); }, completionClaimed: Boolean(pending.current?.completion), canLeave: Boolean(pending.current && (!pending.current.serverStartDispatched || pending.current.attempt)) };
}
export const useOpenRouterOAuth = useAccountOAuth;
export type AccountOAuthFlow = ReturnType<typeof useAccountOAuth>;
export type OpenRouterOAuthFlow = AccountOAuthFlow;
function profileOf(provider: AccountProviderSummary): AccountOAuthProfile | undefined {
  const preset = provider.presetId ?? document(provider.provider).preset_id;
  return preset === AccountOAuthProfile.OpenRouter ? AccountOAuthProfile.OpenRouter : preset === AccountOAuthProfile.HuggingFace ? AccountOAuthProfile.HuggingFace : undefined;
}
function providerServiceName(provider: AccountProviderSummary): string {
  return profileOf(provider) === AccountOAuthProfile.HuggingFace ? "Hugging Face" : provider.displayName;
}
function recoveryMessage(provider: AccountProviderSummary): string {
  return profileOf(provider) === AccountOAuthProfile.OpenRouter ? "The original result requires recovery. Inspect the OpenRouter keys dashboard; local cancellation cannot revoke a provider key. Recover only the original saved local result." : `The original ${provider.displayName} result requires recovery. Local cancellation cannot revoke provider access. Recover only the original saved local result, or cancel and reconnect.`;
}

export function AccountOAuth({ flow, back, manual, edit, manage, done }: { flow: OpenRouterOAuthFlow; back: () => void; manual: () => void; edit: (account: Resource) => void; manage: (account: Resource) => void; done: () => void }) {
  const heading = useRef<HTMLHeadingElement>(null), view = flow.view;
  useEffect(() => { heading.current?.focus(); }, [view?.provider.providerId]);
  if (!view) return null;
  const busy = view.stage === Stage.Starting || view.stage === Stage.Exchanging || view.stage === Stage.Saving || view.stage === Stage.Canceling || view.stage === Stage.Recovering;
  const connected = view.stage === Stage.Connected && view.account;
  const waiting = view.stage === Stage.Awaiting;
  const progress = view.stage === Stage.Starting ? "Preparing authorization…" : view.stage === Stage.Exchanging ? "Exchanging authorization…" : view.stage === Stage.Saving ? "Saving your connection…" : view.stage === Stage.Canceling ? "Confirming cancellation and cleanup…" : view.stage === Stage.Recovering ? "Recovering the original saved result…" : connected ? `${view.provider.displayName} connected` : waiting ? "Waiting for authorization…" : view.stage === Stage.Expired ? "Authorization expired" : view.stage === Stage.Interrupted ? "Authorization was interrupted" : view.stage === Stage.Canceled ? "Connection canceled" : "Connection requires recovery";
  const leave = (fallback: boolean, callback: () => void) => void flow.abandon(fallback, () => callback());
  return <section className="api-keys-view account-oauth-card" aria-labelledby="account-oauth-title">
    <h2 id="account-oauth-title" tabIndex={-1} ref={heading}>Connect {view.provider.displayName}</h2>
    <p className="account-oauth-subheading">Complete sign-in in your browser</p>
    <p>Approve access on {providerServiceName(view.provider)}. DeliDev will finish connecting automatically.</p>
    <div className="account-oauth-progress" role="status" aria-live="polite"><span className="account-oauth-spinner" aria-hidden="true" />{progress}</div>
    {view.problem ? <p role="alert">{view.problem}</p> : null}
    {connected ? <div className="actions"><button onClick={() => leave(false, () => edit(connected))}>Edit account</button><button onClick={() => leave(false, () => manage(connected))}>Manage account</button><button onClick={() => leave(false, done)}>Done</button></div> : <>
      <div className="actions"><button disabled={!waiting || flow.completionClaimed} onClick={() => void flow.reopen()}>Open browser again</button><button disabled={busy && !view.problem || !flow.canLeave} onClick={() => leave(false, back)}>Cancel</button><button disabled={busy && !view.problem || !flow.canLeave} onClick={() => leave(false, back)}>Back to providers</button></div>
      <button className="account-oauth-fallback" disabled={busy && !view.problem || !flow.canLeave} onClick={() => leave(true, manual)}>Use an API key instead</button>
      {view.problem ? <div className="actions"><button onClick={flow.observe} disabled={!view.attempt}>Inspect original attempt</button>{!view.attempt ? <button onClick={flow.retryStart}>Retry original start</button> : null}{flow.completionClaimed ? <button onClick={() => void flow.recover()}>Recover saved result</button> : null}</div> : null}
    </>}
    <footer><p>Your credential will be stored securely on the selected server.</p><p>You can validate your account after connecting.</p></footer>
  </section>;
}

export const OpenRouterOAuth = AccountOAuth;
