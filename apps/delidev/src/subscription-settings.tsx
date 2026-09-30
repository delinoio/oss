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
  activeFilter?: string;
  clearFilter: () => void;
  advanced: ReactNode;
  pagination?: ReactNode;
  now?: number;
  active?: boolean;
}

const connectionLabels: Record<SubscriptionConnectionState, string> = {
  [SubscriptionConnectionState.Connected]: "Connected",
  [SubscriptionConnectionState.Disconnected]: "Disconnected",
  [SubscriptionConnectionState.CleanupPending]: "Credential cleanup pending",
};
const quotaLabels: Record<QuotaObservationState, string> = {
  [QuotaObservationState.Observed]: "Observed",
  [QuotaObservationState.Unknown]: "Unknown",
  [QuotaObservationState.Stale]: "Stale",
  [QuotaObservationState.Failed]: "Observation failed",
  [QuotaObservationState.Unsupported]: "Unsupported",
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
  const descriptor = subscriptionCatalog.find((provider) => provider.brand === brand);
  return <span className={`subscription-mark${brand ? ` subscription-mark-${brand}` : ""}`} aria-hidden="true">
    {descriptor ? <img src={descriptor.mark} alt="" width="24" height="24" /> : <svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" strokeWidth="1.7"><circle cx="12" cy="8" r="3.5" /><path d="M5 21v-2a7 7 0 0 1 14 0v2" /></svg>}
  </span>;
}

function QuotaWindow({ window, now }: { window: SubscriptionQuotaWindow; now: number }) {
  const label = window.label || window.id || "Unidentified window";
  const presentation = quotaPresentation(window, now);
  return <div className="subscription-quota" data-state={presentation.state}>
    <div className="subscription-quota-heading"><strong>{label}</strong><span>{presentation.percent === undefined ? "Remaining unknown" : `${presentation.percent}% remaining`}</span></div>
    {presentation.percent !== undefined ? <progress max="100" value={presentation.percent} aria-label={`${label} remaining`} /> : null}
    <small>{quotaLabels[presentation.state]}{window.observedAt ? <> · Observed <time dateTime={window.observedAt}>{window.observedAt}</time></> : " · No observation time"}</small>
    {window.resetAt ? <small>Reset <time dateTime={window.resetAt}>{window.resetAt}</time>{Date.parse(window.resetAt) <= now ? " · Elapsed; recovery unconfirmed" : ""}</small> : null}
  </div>;
}

function operationBlocked(operation?: SubscriptionOperation) {
  return operation !== undefined && [SubscriptionOperationState.Busy, SubscriptionOperationState.Uncertain, SubscriptionOperationState.CleanupPending].includes(operation.state);
}

function OperationNotice({ label, operation, retryBlocked = false }: { label: string; operation?: SubscriptionOperation; retryBlocked?: boolean }) {
  if (!operation || operation.state === SubscriptionOperationState.Ready) return null;
  return <div className="subscription-operation" role={operation.state === SubscriptionOperationState.Failed ? "alert" : "status"}>
    <p>{label}: {operation.state === SubscriptionOperationState.Busy ? "In progress" : operation.state === SubscriptionOperationState.Uncertain ? "Result not confirmed" : operation.state === SubscriptionOperationState.CleanupPending ? "Credential cleanup pending" : "Failed"}{operation.message ? ` · ${operation.message}` : ""}</p>
    {operation.state !== SubscriptionOperationState.Busy && operation.retry ? <button type="button" disabled={retryBlocked} onClick={operation.retry}>Retry original {label.toLowerCase()}</button> : null}
  </div>;
}

function SubscriptionRow({ account, now, unavailable }: { account: SubscriptionAccountRow; now: number; unavailable: string }) {
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
      <div className="subscription-identity"><ProviderMark brand={account.brand} /><div><h3>{account.alias}</h3><p>{account.providerName}{account.maskedIdentity ? ` · ${account.maskedIdentity}` : ""}</p><span className="subscription-connection" data-state={account.connection}>{connectionLabels[account.connection]}</span>{["expired", "revoked", "failed"].includes(account.health) ? <span className="subscription-health"> · {account.health === "expired" ? "Authentication expired" : account.health === "revoked" ? "Authentication revoked" : "Account failed"}</span> : null}</div></div>
      <div className="subscription-quota-grid">{account.windows.length ? account.windows.slice(0, 2).map((window, index) => <QuotaWindow key={`${window.id}:${index}`} window={window} now={now} />) : <p className="subscription-no-quota">No quota observation</p>}</div>
      <div className="subscription-row-actions">
        <button type="button" disabled={!canRefresh} title={!account.refresh ? unavailable : undefined} aria-label={`Refresh ${account.alias}`} onClick={account.refresh}>Refresh</button>
        <button ref={disconnectButton} type="button" disabled={!canDisconnect} title={!account.disconnect ? unavailable : undefined} aria-label={`Disconnect ${account.alias}`} onClick={() => setConfirm(true)}>Disconnect</button>
        <div className="subscription-more" onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setMenu(false); }}>
          <button ref={menuButton} type="button" aria-label={`More actions for ${account.alias}`} aria-expanded={menu} aria-controls={menuId} onClick={() => setMenu(!menu)} onKeyDown={(event) => { if (event.key === "Escape" && menu) { event.preventDefault(); event.stopPropagation(); closeMenu(); } }}><span aria-hidden="true">⋯</span></button>
          {menu ? <div id={menuId} className="subscription-more-panel" role="group" aria-label={`Actions for ${account.alias}`} onKeyDown={(event) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeMenu(); } }}>
            <button type="button" aria-controls={detailsId} onClick={() => { setDetails(true); closeMenu(); }}>Account details</button>
            <button type="button" disabled={!account.metadataAvailable} onClick={() => { closeMenu(); account.edit(); }}>Edit preferences</button>
            <button type="button" disabled={!account.metadataAvailable} onClick={() => { closeMenu(); account.delete(); }}>Delete account</button>
          </div> : null}
        </div>
      </div>
    </div>
    {account.windows.length > 2 ? <button className="subscription-extra-windows" type="button" aria-expanded={details} aria-controls={detailsId} onClick={() => setDetails(!details)}>{details ? "Hide" : "Show"} all {account.windows.length} quota windows</button> : null}
    <OperationNotice label="Refresh" operation={account.refreshOperation} retryBlocked={retryBlocked} />
    <OperationNotice label="Disconnection" operation={account.disconnectOperation} retryBlocked={retryBlocked} />
    {confirm ? <div className="subscription-confirmation" role="group" aria-label={`Confirm disconnection of ${account.alias}`} onKeyDown={(event) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeConfirm(); } }}>
      <p>Disconnect {account.alias}? Its credentials will be removed and active executions canceled. Account preferences and history are preserved. Other accounts stay connected.</p>
      <div className="actions"><button type="button" disabled={!canDisconnect} onClick={() => { setConfirm(false); account.disconnect?.(); disconnectButton.current?.focus(); }}>Confirm disconnection</button><button type="button" onClick={closeConfirm}>Keep account connected</button></div>
    </div> : null}
    {details ? <div id={detailsId} className="subscription-details"><h4>Account details</h4><dl><div><dt>Health</dt><dd>{account.health || "Unknown"}</dd></div><div><dt>Account</dt><dd>{account.enabled ? "Enabled" : "Disabled"}</dd></div><div><dt>Provider status</dt><dd>{account.providerState}</dd></div><div><dt>Exhaustion</dt><dd>{account.confirmedExhausted ? "Confirmed exhausted" : "Not confirmed exhausted"}</dd></div></dl>
      {account.windows.length > 2 ? <div className="subscription-quota-grid">{account.windows.slice(2).map((window, index) => <QuotaWindow key={`${window.id}:${index + 2}`} window={window} now={now} />)}</div> : null}
      <div className="actions"><button type="button" disabled={!account.metadataAvailable} onClick={account.details}>Manage metadata</button><button type="button" onClick={() => { setDetails(false); menuButton.current?.focus(); }}>Close account details</button></div>
    </div> : null}
  </article>;
}

const readLabels: Partial<Record<SubscriptionReadState, string>> = {
  [SubscriptionReadState.Loading]: "Loading subscriptions…",
  [SubscriptionReadState.Failed]: "Unable to read subscriptions or server capabilities. Try again.",
  [SubscriptionReadState.PermissionDenied]: "You do not have permission to read these subscriptions.",
  [SubscriptionReadState.AuthenticationExpired]: "Authentication expired. Reconnect to the selected server.",
  [SubscriptionReadState.Unsupported]: "Account lists require a server that supports account-type filtering. Update the selected server to manage subscriptions.",
};

/** Pure presentation seam: production supplies no native lifecycle callbacks today. */
export function SubscriptionSettingsView({ accounts, state, problem, retryRead, refreshAll, refreshAllOperation, lifecycleUnavailable = "Subscription login is not available yet. This server does not support subscription connection, quota refresh or disconnection.", activeFilter, clearFilter, advanced, pagination, now, active = true }: SubscriptionSettingsViewProps) {
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
  return <section className="subscription-settings" aria-label="AI subscription account settings">
    {activeFilter ? <div className="subscription-active-filter" role="status"><span>Provider filter: {activeFilter}</span><button type="button" onClick={clearFilter}>Clear provider filter</button></div> : null}
    <section aria-label="Your subscriptions"><header className="subscription-section-heading"><h2>Your subscriptions</h2><button type="button" disabled={!refreshAll || operationBlocked(refreshAllOperation)} title={!refreshAll ? lifecycleUnavailable : undefined} aria-describedby={!refreshAll ? noticeId : undefined} onClick={refreshAll}>Refresh all</button></header>
      {state !== SubscriptionReadState.Ready ? <div role={state === SubscriptionReadState.Failed || state === SubscriptionReadState.PermissionDenied || state === SubscriptionReadState.AuthenticationExpired ? "alert" : "status"}><p>{readLabels[state]}</p>{accounts.length && state !== SubscriptionReadState.Loading ? <p>Showing the last successfully loaded subscriptions.</p> : null}{problem}{retryRead && state !== SubscriptionReadState.Loading && state !== SubscriptionReadState.Unsupported ? <button type="button" onClick={retryRead}>Retry subscription read</button> : null}</div> : null}
      {accounts.length ? <div className="subscription-list">{accounts.map((account) => <SubscriptionRow key={account.id} account={account} now={presentationNow} unavailable={lifecycleUnavailable} />)}</div> : state === SubscriptionReadState.Ready ? <div className="subscription-empty"><h3>No subscriptions yet</h3><p>Saved subscriptions will appear here, including disconnected accounts.</p></div> : null}
      <OperationNotice label="Refresh all" operation={refreshAllOperation} />
      {pagination}
    </section>
    <section aria-label="Connect a subscription"><header className="subscription-section-heading"><h2>Connect a subscription</h2></header><p id={noticeId} className="subscription-unavailable">{lifecycleUnavailable}</p>
      <div className="subscription-provider-cards">{subscriptionCatalog.map((provider) => <article className="subscription-provider-card" key={provider.brand}><ProviderMark brand={provider.brand} /><h3>{provider.name}</h3><p>{provider.purpose}</p><button type="button" disabled aria-label={`${provider.name} · Coming soon`}>Coming soon</button></article>)}</div>
    </section>
    <details className="subscription-advanced"><summary>Advanced settings</summary><div>{advanced}</div></details>
  </section>;
}
