import { LocalizedText, copy, useLocale } from "./localization";
import { SettingsHeading, SettingsEmpty, SettingsLoading } from "./settings-presentation";
// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useReducer, useRef, useState, type ReactNode } from "react";
import { subscriptionCatalog, SubscriptionBrand } from "./subscription-catalog";

export enum QuotaObservationState { Observed = "observed", Unknown = "unknown", Stale = "stale", Failed = "failed", Unsupported = "unsupported" }
export enum SubscriptionConnectionState { Connected = "connected", Disconnected = "disconnected", CleanupPending = "cleanup-pending" }
export enum SubscriptionReadState { Ready = "ready", Loading = "loading", Failed = "failed", PermissionDenied = "permission-denied", AuthenticationExpired = "authentication-expired", Unsupported = "unsupported" }
export enum SubscriptionOperationState { Ready = "ready", Busy = "busy", Failed = "failed", Uncertain = "uncertain", CleanupPending = "cleanup-pending" }

export interface SubscriptionQuotaWindow {
  id: string;
  /** Friendly labels are explicit presentation fixtures until native metadata exists. */
  label?: string;
  remaining?: number;
  observedAt?: string;
  resetAt?: string;
  state: QuotaObservationState;
}

export interface SubscriptionOperation {
  state: SubscriptionOperationState;
  message?: string;
  /** Supplied only by an owning operation; retries never create another identity. */
  retry?: () => void;
}

export interface SubscriptionAccountRow {
  id: string;
  alias: string;
  providerName: string;
  /** An explicit verified native identity is required; editable names are insufficient. */
  brand?: SubscriptionBrand;
  maskedIdentity?: string;
  connection: SubscriptionConnectionState;
  health: string;
  windows: readonly SubscriptionQuotaWindow[];
  enabled: boolean;
  providerState: string;
  confirmedExhausted: boolean;
  refreshOperation?: SubscriptionOperation;
  disconnectOperation?: SubscriptionOperation;
  connect?: () => void;
  refresh?: () => void;
  disconnect?: () => void;
  details: () => void;
  edit: () => void;
  delete: () => void;
  metadataAvailable: boolean;
}

export interface SubscriptionSettingsViewProps {
  accounts: readonly SubscriptionAccountRow[];
  state: SubscriptionReadState;
  problem?: ReactNode;
  retryRead?: () => void;
  /** This one callback owns all pages/filters; presentation never enumerates them. */
  refreshAll?: () => void;
  refreshAllOperation?: SubscriptionOperation;
  lifecycleUnavailable?: string;
  selectService?: (brand: SubscriptionBrand) => void;
  serviceLoginAvailable?: (brand: SubscriptionBrand) => boolean;
  activeFilter?: string;
  clearFilter: () => void;
  advanced: ReactNode;
  pagination?: ReactNode;
  now?: number;
  active?: boolean;
}

const connectionLabels: Record<SubscriptionConnectionState, string> = {
  get [SubscriptionConnectionState.Connected]() { return copy("subscription-settings.connected_229655"); },
  get [SubscriptionConnectionState.Disconnected]() { return copy("subscription-settings.disconnected_04dfac"); },
  get [SubscriptionConnectionState.CleanupPending]() { return copy("subscription-settings.credentialCleanupPending_50459d"); },
};
const quotaLabels: Record<QuotaObservationState, string> = {
  get [QuotaObservationState.Observed]() { return copy("subscription-settings.observed_64fa8a"); },
  get [QuotaObservationState.Unknown]() { return copy("subscription-settings.unknown_b764cd"); },
  get [QuotaObservationState.Stale]() { return copy("subscription-settings.stale_40c9e5"); },
  get [QuotaObservationState.Failed]() { return copy("subscription-settings.observationFailed_d2fffd"); },
  get [QuotaObservationState.Unsupported]() { return copy("subscription-settings.unsupported_543246"); },
};

export function quotaPresentation(window: SubscriptionQuotaWindow, now: number) {
  const observedAt = Date.parse(window.observedAt ?? "");
  const resetAt = Date.parse(window.resetAt ?? "");
  const validRemaining = typeof window.remaining === "number" && Number.isFinite(window.remaining) && window.remaining >= 0 && window.remaining <= 1;
  let state = window.state;
  if (state === QuotaObservationState.Observed) {
    if (!validRemaining || !Number.isFinite(observedAt)) state = QuotaObservationState.Unknown;
    else if (observedAt > now || now - observedAt > 5 * 60 * 1000 || (Number.isFinite(resetAt) && resetAt <= now)) state = QuotaObservationState.Stale;
  }
  return { state, percent: validRemaining && Number.isFinite(observedAt) && observedAt <= now && state !== QuotaObservationState.Unknown && state !== QuotaObservationState.Unsupported ? Math.round(window.remaining! * 100) : undefined };
}

function ProviderMark({ brand }: { brand?: SubscriptionBrand }) {
  useLocale();
  const descriptor = subscriptionCatalog.find((provider) => provider.brand === brand);
  return <span className={`subscription-mark${brand ? ` subscription-mark-${brand}` : ""}`} aria-hidden="true">
    {descriptor ? <img src={descriptor.mark} alt="" width="24" height="24" /> : <svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" strokeWidth="1.7"><circle cx="12" cy="8" r="3.5" /><path d="M5 21v-2a7 7 0 0 1 14 0v2" /></svg>}
  </span>;
}

function QuotaWindow({ window, now }: { window: SubscriptionQuotaWindow; now: number }) {
  useLocale();
  const label = window.label || window.id || "Unidentified window";
  const presentation = quotaPresentation(window, now);
  return <div className="subscription-quota" data-state={presentation.state}>
    <div className="subscription-quota-heading"><strong>{label}</strong><span>{presentation.percent === undefined ? copy("subscription-settings.remainingUnknown_e49e1a") : copy("subscription-settings.remaining_fe6b6b", { v0: presentation.percent })}</span></div>
    {presentation.percent !== undefined ? <progress max="100" value={presentation.percent} aria-label={copy("subscription-settings.remaining_f33475", { v0: label })} /> : null}
    <small>{quotaLabels[presentation.state]}{window.observedAt ? <><LocalizedText id="subscription-settings.observed_c0c869" components={{ s0: <time dateTime={window.observedAt}>{window.observedAt}</time> }} /></> : copy("subscription-settings.noObservationTime_02689f")}</small>
    {window.resetAt ? <small><LocalizedText id="subscription-settings.reset_22f04d" components={{ s0: <time dateTime={window.resetAt}>{window.resetAt}</time>, s1: <>{Date.parse(window.resetAt) <= now ? copy("subscription-settings.elapsedRecoveryUnconfirmed_4f7d23") : ""}</> }} /></small> : null}
  </div>;
}

function operationBlocked(operation?: SubscriptionOperation) {
  return operation !== undefined && [SubscriptionOperationState.Busy, SubscriptionOperationState.Uncertain, SubscriptionOperationState.CleanupPending].includes(operation.state);
}

function OperationNotice({ label, operation, retryBlocked = false }: { label: string; operation?: SubscriptionOperation; retryBlocked?: boolean }) {
  useLocale();
  if (!operation || operation.state === SubscriptionOperationState.Ready) return null;
  return <div className="subscription-operation" role={operation.state === SubscriptionOperationState.Failed ? "alert" : "status"}>
    <p>{label}: {operation.state === SubscriptionOperationState.Busy ? copy("subscription-settings.inProgress_c1f88e") : operation.state === SubscriptionOperationState.Uncertain ? copy("subscription-settings.resultNotConfirmed_a5d341") : operation.state === SubscriptionOperationState.CleanupPending ? copy("subscription-settings.credentialCleanupPending_50459d") : copy("subscription-settings.failed_031a8f")}{operation.message ? copy("subscription-settings.message_2fa20b", { v0: operation.message }) : ""}</p>
    {operation.state !== SubscriptionOperationState.Busy && operation.retry ? <button type="button" disabled={retryBlocked} onClick={operation.retry}><LocalizedText id="subscription-settings.retryOriginal_74138b" components={{ s0: <>{label.toLowerCase()}</> }} /></button> : null}
  </div>;
}

function SubscriptionRow({ account, now, unavailable }: { account: SubscriptionAccountRow; now: number; unavailable: string }) {
  useLocale();
  const [menu, setMenu] = useState(false);
  const [details, setDetails] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const menuId = useId(), detailsId = useId();
  const menuButton = useRef<HTMLButtonElement>(null);
  const disconnectButton = useRef<HTMLButtonElement>(null);
  const blocked = operationBlocked(account.refreshOperation) || operationBlocked(account.disconnectOperation);
  const retryBlocked = account.refreshOperation?.state === SubscriptionOperationState.Busy || account.disconnectOperation?.state === SubscriptionOperationState.Busy;
  const canRefresh = Boolean(account.refresh) && account.connection === SubscriptionConnectionState.Connected && !blocked;
  const canDisconnect = Boolean(account.disconnect) && account.connection === SubscriptionConnectionState.Connected && !blocked;
  const closeMenu = () => { setMenu(false); menuButton.current?.focus(); };
  const closeConfirm = () => { setConfirm(false); disconnectButton.current?.focus(); };
  return <article className="subscription-row" aria-label={account.alias}>
    <div className="subscription-row-main">
      <div className="subscription-identity"><ProviderMark brand={account.brand} /><div><h3>{account.alias}</h3><p>{account.providerName}{account.maskedIdentity ? copy("subscription-settings.message_2fa20b", { v0: account.maskedIdentity }) : ""}</p><span className="subscription-connection" data-state={account.connection}>{connectionLabels[account.connection]}</span>{["expired", "revoked", "failed"].includes(account.health) ? <span className="subscription-health"> · {account.health === "expired" ? copy("subscription-settings.authenticationExpired_032edd") : account.health === "revoked" ? copy("subscription-settings.authenticationRevoked_e049fb") : copy("subscription-settings.accountFailed_d5a3c3")}</span> : null}</div></div>
      <div className="subscription-quota-grid">{account.windows.length ? account.windows.slice(0, 2).map((window, index) => <QuotaWindow key={`${window.id}:${index}`} window={window} now={now} />) : <p className="subscription-no-quota">{copy("subscription-settings.noQuotaObservation_d9e3af")}</p>}</div>
      <div className="subscription-row-actions">
        <button type="button" disabled={!canRefresh} title={!account.refresh ? unavailable : undefined} aria-label={copy("subscription-settings.refresh_525a40", { v0: account.alias })} onClick={account.refresh}>{copy("subscription-settings.refresh_0e9161")}</button>
        {account.connect ? <button type="button" disabled={blocked} aria-label={copy("subscription-settings.manageLoginFor_e2d832", { v0: account.alias })} onClick={account.connect}>{account.connection === SubscriptionConnectionState.Disconnected ? copy("subscription-settings.logIn_c18984") : copy("subscription-settings.manageLogin_4b4e31")}</button> : null}
        <button ref={disconnectButton} type="button" disabled={!canDisconnect} title={!account.disconnect ? unavailable : undefined} aria-label={copy("subscription-settings.disconnect_fe6cee", { v0: account.alias })} onClick={() => setConfirm(true)}>{copy("subscription-settings.disconnect_acfc5b")}</button>
        <div className="subscription-more" onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setMenu(false); }}>
          <button ref={menuButton} type="button" aria-label={copy("subscription-settings.moreActionsFor_5057a7", { v0: account.alias })} aria-expanded={menu} aria-controls={menuId} onClick={() => setMenu(!menu)} onKeyDown={(event) => { if (event.key === "Escape" && menu) { event.preventDefault(); event.stopPropagation(); closeMenu(); } }}><span aria-hidden="true">⋯</span></button>
          {menu ? <div id={menuId} className="subscription-more-panel" role="group" aria-label={copy("subscription-settings.actionsFor_b59837", { v0: account.alias })} onKeyDown={(event) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeMenu(); } }}>
            <button type="button" aria-controls={detailsId} onClick={() => { setDetails(true); closeMenu(); }}>{copy("subscription-settings.accountDetails_17be95")}</button>
            <button type="button" disabled={!account.metadataAvailable} onClick={() => { closeMenu(); account.edit(); }}>{copy("subscription-settings.editPreferences_00b4cc")}</button>
            <button type="button" disabled={!account.metadataAvailable} onClick={() => { closeMenu(); account.delete(); }}>{copy("subscription-settings.deleteAccount_a2e20a")}</button>
          </div> : null}
        </div>
      </div>
    </div>
    {account.windows.length > 2 ? <button className="subscription-extra-windows" type="button" aria-expanded={details} aria-controls={detailsId} onClick={() => setDetails(!details)}><LocalizedText id="subscription-settings.allQuotaWindows_53404d" components={{ s0: <>{details ? copy("subscription-settings.hide_ac20a5") : copy("subscription-settings.show_0df6f1")}</>, s1: <>{account.windows.length}</> }} /></button> : null}
    <OperationNotice label={copy("subscription-settings.refresh_0e9161")} operation={account.refreshOperation} retryBlocked={retryBlocked} />
    <OperationNotice label={copy("subscription-settings.disconnection_1913a2")} operation={account.disconnectOperation} retryBlocked={retryBlocked} />
    {confirm ? <div className="subscription-confirmation" role="group" aria-label={copy("subscription-settings.confirmDisconnectionOf_476dd1", { v0: account.alias })} onKeyDown={(event) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeConfirm(); } }}>
      <p><LocalizedText id="subscription-settings.disconnectItsCredentialsWillBeRemoved_65894e" components={{ s0: <>{account.alias}</> }} /></p>
      <div className="actions"><button type="button" disabled={!canDisconnect} onClick={() => { setConfirm(false); account.disconnect?.(); disconnectButton.current?.focus(); }}>{copy("subscription-settings.confirmDisconnection_d61f53")}</button><button type="button" onClick={closeConfirm}>{copy("subscription-settings.keepAccountConnected_00ae06")}</button></div>
    </div> : null}
    {details ? <div id={detailsId} className="subscription-details"><h4>{copy("subscription-settings.accountDetails_17be95")}</h4><dl><div><dt>{copy("subscription-settings.health_558984")}</dt><dd>{account.health || "Unknown"}</dd></div><div><dt>{copy("subscription-settings.account_7e1b0d")}</dt><dd>{account.enabled ? copy("subscription-settings.enabled_92c1cd") : copy("subscription-settings.disabled_75081b")}</dd></div><div><dt>{copy("subscription-settings.serviceStatus_cce5ed")}</dt><dd>{account.providerState}</dd></div><div><dt>{copy("subscription-settings.exhaustion_c52628")}</dt><dd>{account.confirmedExhausted ? copy("subscription-settings.confirmedExhausted_763851") : copy("subscription-settings.notConfirmedExhausted_a80dbe")}</dd></div></dl>
      {account.windows.length > 2 ? <div className="subscription-quota-grid">{account.windows.slice(2).map((window, index) => <QuotaWindow key={`${window.id}:${index + 2}`} window={window} now={now} />)}</div> : null}
      <div className="actions"><button type="button" disabled={!account.metadataAvailable} onClick={account.details}>{copy("subscription-settings.manageMetadata_ddc14e")}</button><button type="button" onClick={() => { setDetails(false); menuButton.current?.focus(); }}>{copy("subscription-settings.closeAccountDetails_c62a46")}</button></div>
    </div> : null}
  </article>;
}

const readLabels: Partial<Record<SubscriptionReadState, string>> = {
  get [SubscriptionReadState.Loading]() { return copy("subscription-settings.loadingSubscriptions_d98d84"); },
  get [SubscriptionReadState.Failed]() { return copy("subscription-settings.unableToReadSubscriptionsOrServer_e2b6e5"); },
  get [SubscriptionReadState.PermissionDenied]() { return copy("subscription-settings.youDoNotHavePermissionTo_9c5aac"); },
  get [SubscriptionReadState.AuthenticationExpired]() { return copy("subscription-settings.authenticationExpiredReconnectToTheSelected_daa4ee"); },
  get [SubscriptionReadState.Unsupported]() { return copy("subscription-settings.accountListsRequireAServerThat_e8e714"); },
};

/** Presentation only; the owning controller negotiates every native action. */
export function SubscriptionSettingsView({ accounts, state, problem, retryRead, refreshAll, refreshAllOperation, lifecycleUnavailable = "Subscription login is not available yet. This server does not support subscription connection, quota refresh or disconnection.", selectService, serviceLoginAvailable = () => true, activeFilter, clearFilter, advanced, pagination, now, active = true }: SubscriptionSettingsViewProps) {
  useLocale();
  const noticeId = useId();
  const [, expireObservation] = useReducer((revision: number) => revision + 1, 0);
  const presentationNow = now ?? Date.now();
  useEffect(() => {
    if (!active || now !== undefined) return;
    // Expire presentation at the next known boundary while this surface is
    // active. This makes no requests and stops when Settings is hidden/disposed.
    const boundaries = accounts.flatMap((account) => account.windows.flatMap((window) => [
      Date.parse(window.resetAt ?? ""),
      ...(window.state === QuotaObservationState.Observed ? [Date.parse(window.observedAt ?? "") + 5 * 60 * 1000 + 1] : []),
    ]));
    const next = Math.min(...boundaries.filter((boundary) => Number.isFinite(boundary) && boundary > presentationNow));
    if (!Number.isFinite(next)) return;
    const timer = setTimeout(expireObservation, Math.min(next - presentationNow, 2 ** 31 - 1));
    return () => clearTimeout(timer);
  }, [accounts, active, now, presentationNow]);
  return <section className="subscription-settings" aria-label={copy("subscription-settings.aiSubscriptionAccountSettings_9f67d8")}>
    {activeFilter ? <div className="subscription-active-filter" role="status"><span><LocalizedText id="subscription-settings.providerFilter_f3f61a" components={{ s0: <>{activeFilter}</> }} /></span><button type="button" onClick={clearFilter}>{copy("subscription-settings.clearProviderFilter_e0b8c0")}</button></div> : null}
    <section aria-label={copy("subscription-settings.yourSubscriptions_b636e8")}><header className="subscription-section-heading"><h2>{copy("subscription-settings.yourSubscriptions_b636e8")}</h2><button type="button" disabled={!refreshAll || operationBlocked(refreshAllOperation)} title={!refreshAll ? lifecycleUnavailable : undefined} aria-describedby={!refreshAll ? noticeId : undefined} onClick={refreshAll}>{copy("subscription-settings.refreshAll_3c128b")}</button></header>
      {state !== SubscriptionReadState.Ready ? <div role={state === SubscriptionReadState.Loading ? undefined : state === SubscriptionReadState.Failed || state === SubscriptionReadState.PermissionDenied || state === SubscriptionReadState.AuthenticationExpired ? "alert" : "status"}>{state === SubscriptionReadState.Loading ? <SettingsLoading label={copy("subscription-settings.loadingSubscriptions_d98d84")} /> : <p>{readLabels[state]}</p>}{accounts.length && state !== SubscriptionReadState.Loading ? <p>{copy("subscription-settings.showingTheLastSuccessfullyLoadedSubscriptions_3cc29c")}</p> : null}{problem}{retryRead && state !== SubscriptionReadState.Loading && state !== SubscriptionReadState.Unsupported ? <button type="button" onClick={retryRead}>{copy("subscription-settings.retrySubscriptionRead_3772f6")}</button> : null}</div> : null}
      {accounts.length ? <div className="subscription-list">{accounts.map((account) => <SubscriptionRow key={account.id} account={account} now={presentationNow} unavailable={lifecycleUnavailable} />)}</div> : state === SubscriptionReadState.Ready ? <SettingsEmpty title={copy("subscription-settings.noSubscriptionsYet_9c2ace")}><p>{copy("subscription-settings.savedSubscriptionsWillAppearHereIncluding_383bff")}</p></SettingsEmpty> : null}
      <OperationNotice label={copy("subscription-settings.refreshAll_3c128b")} operation={refreshAllOperation} />
      {pagination}
    </section>
    <section aria-label={copy("subscription-settings.connectASubscription_46b98e")}><header className="subscription-section-heading"><h2>{copy("subscription-settings.connectASubscription_46b98e")}</h2></header><p id={noticeId} className="subscription-unavailable">{lifecycleUnavailable}</p>
      <div className="subscription-provider-cards">{subscriptionCatalog.map((provider) => <article className="subscription-provider-card" key={provider.brand}><ProviderMark brand={provider.brand} /><h3>{provider.name}</h3><p>{provider.purpose}</p><button type="button" disabled={!selectService || !serviceLoginAvailable(provider.brand)} aria-label={copy("subscription-settings.message_78735b", { v0: provider.name, v1: selectService && serviceLoginAvailable(provider.brand) ? copy("subscription-settings.addAccount_ee7ee5") : copy("subscription-settings.comingSoon_4f7d64") })} onClick={() => selectService?.(provider.brand)}>{selectService && serviceLoginAvailable(provider.brand) ? copy("subscription-settings.addAccount_ee7ee5") : copy("subscription-settings.comingSoon_4f7d64")}</button></article>)}</div>
    </section>
    <details className="subscription-advanced"><summary>{copy("subscription-settings.advancedSettings_7b0bd2")}</summary><div>{advanced}</div></details>
  </section>;
}
