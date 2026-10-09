// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary, DisclosureDensity } from "./disclosure";
import { useEffect, useRef, useState } from "react";
import { Channel, invoke } from "@tauri-apps/api/core";
import { copy, displayLocale, i18n, useLocale, type MessageKey } from "./localization";
import { formatTimestampLabel, TimestampMode } from "./timestamp-format";
import { TrayQuotaState, type TrayQuota } from "./tray-types";
import { TrayPanelAction, parseTrayPanel, type TrayPanelSnapshot, type TrayPanelTarget } from "./tray-status-model";
import "./tray-status.css";

export interface TrayPanelBridge {
  read(): Promise<unknown>;
  subscribe(changed: () => void): Promise<() => void>;
  activate(action: TrayPanelAction, target?: TrayPanelTarget): Promise<void>;
  dismiss(): Promise<void>;
}
export function trayPanelBridge(instance: string): TrayPanelBridge {
  // Order subscription replacement across Strict Mode disposal without allowing
  // an earlier native watch acknowledgment to replace its successor channel.
  let watches: Promise<unknown> = Promise.resolve();
  return { read: () => invoke("read_tray_status", { instance }),
    subscribe: async changed => {
      const channel = new Channel<string>();
      channel.onmessage = original => { if (original === instance) changed(); };
      const watch = watches.catch(() => {}).then(() => invoke("watch_tray_status", { instance, channel }));
      watches = watch;
      await watch;
      return () => { channel.onmessage = () => {}; };
    },
    activate: (action, target) => invoke("activate_tray_status", { instance, action, target: target ?? null }),
    dismiss: () => invoke("dismiss_tray_status", { instance }) };
}
const states: Record<TrayQuotaState, MessageKey> = { observed: "tray-status.observed", unknown: "tray-status.unknown", stale: "tray-status.stale", failed: "tray-status.failed", unsupported: "tray-status.unsupported" };
function count(value: string | null | undefined) { return value == null ? copy("tray-status.unavailable") : new Intl.NumberFormat(displayLocale()).format(BigInt(value)); }
function Quota({ quota, index, stale, snapshot, now }: { quota: TrayQuota; index: number; stale: boolean; snapshot: TrayPanelSnapshot; now: number }) {
  const expired = quota.reset_at !== null && Date.parse(quota.reset_at) <= now;
  const state = quota.state === TrayQuotaState.Observed && (stale || expired) ? TrayQuotaState.Stale : quota.state;
  const remaining = [TrayQuotaState.Observed, TrayQuotaState.Stale].includes(state) ? quota.remaining_basis_points : null;
  const percentage = remaining === null ? null : new Intl.NumberFormat(displayLocale(), { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(remaining / 100);
  const stamp = (at: string, mode = TimestampMode.Ordinary) => formatTimestampLabel(at, { preference: snapshot.date_format, now, mode });
  return <section className="tray-quota" aria-label={quota.id ?? copy("tray-status.quota", { number: index + 1 })}>
    <div className="tray-quota-id">{quota.id ?? copy("tray-status.quota", { number: index + 1 })}</div>
    <strong className="tray-quota-remaining">{percentage === null ? copy("tray-status.remainingUnknown") : copy("tray-status.remaining", { amount: percentage })}</strong>
    {remaining !== null ? <progress aria-hidden="true" max={10000} value={remaining} /> : null}
    <p className="tray-quota-state">{copy(states[state])}</p>
    <p>{quota.reset_at ? expired ? copy("tray-status.resetExpired", { at: stamp(quota.reset_at, TimestampMode.Absolute) }) : stamp(quota.reset_at, TimestampMode.QuotaCountdown) : copy("tray-status.resetUnknown")}</p>
    <p>{quota.observed_at ? copy("tray-status.observedAt", { at: stamp(quota.observed_at) }) : copy("tray-status.observationUnknown")}</p>
  </section>;
}
export function TrayStatus({ instance, bridge }: { instance: string; bridge: TrayPanelBridge }) {
  useLocale();
  const [snapshot, setSnapshot] = useState<TrayPanelSnapshot>();
  const [selected, setSelected] = useState<string>();
  const [problem, setProblem] = useState(false);
  const [busy, setBusy] = useState(false);
  const activationRunning = useRef(false);
  const [now, setNow] = useState(Date.now);
  const nativeReceived = useRef(performance.now());
  const selection = useRef<HTMLSelectElement>(null);
  const reload = useRef<() => void>(() => {});
  const lastFocus = useRef(false);
  useEffect(() => {
    let disposed = false, running = false, requested = false, subscriptionReady = false, subscribing = false;
    let remove: (() => void) | undefined;
    const read = async () => {
      if (disposed) return;
      if (running) { requested = true; return; }
      running = true;
      do {
        requested = false;
        try {
          const next = parseTrayPanel(await bridge.read(), instance);
          if (disposed) break;
          // Preference projection is read-only: no device controllers or writes.
          void i18n.changeLanguage(next.language);
          document.documentElement.dataset.theme = next.theme;
          nativeReceived.current = performance.now();
          setSnapshot(next);
          setSelected(original => next.windows.some(row => row.target.instance === original) ? original : next.recent && next.windows.some(row => row.target.instance === next.recent) ? next.recent : next.windows[0]?.target.instance);
          setProblem(!subscriptionReady);
          if (!lastFocus.current) { queueMicrotask(() => { if (!disposed) selection.current?.focus(); }); lastFocus.current = true; }
        } catch { if (!disposed) setProblem(true); }
      } while (!disposed && requested);
      running = false;
    };
    const connect = () => {
      if (disposed || subscribing) return;
      subscribing = true;
      void bridge.subscribe(() => { void read(); }).then(unlisten => {
        if (disposed) unlisten(); else { remove = unlisten; subscriptionReady = true; void read(); }
      }).catch(() => { if (!disposed) { setProblem(true); void read(); } }).finally(() => { subscribing = false; });
    };
    reload.current = () => { if (!subscriptionReady) connect(); else void read(); };
    connect();
    const tick = setInterval(() => setNow(Date.now()), 1000);
    const focus = () => { lastFocus.current = false; void read(); };
    window.addEventListener("focus", focus);
    return () => { disposed = true; remove?.(); clearInterval(tick); window.removeEventListener("focus", focus); reload.current = () => {}; };
  }, [instance, bridge]);
  const current = snapshot?.windows.find(row => row.target.instance === selected);
  const stale = Boolean(problem || current?.stale || current && current.observed_age_ms + performance.now() - nativeReceived.current >= 45000);
  const summary = current?.summary;
  const overview = summary?.overview;
  const action = async (value: TrayPanelAction) => {
    if (activationRunning.current) return;
    activationRunning.current = true;
    setBusy(true);
    try { await bridge.activate(value, current?.target); }
    catch { setProblem(true); }
    finally { activationRunning.current = false; setBusy(false); }
  };
  const dismiss = () => { void bridge.dismiss().catch(() => setProblem(true)); };
  return <main className="tray-status" aria-label="DeliDev" onKeyDown={event => { if (event.key === "Escape" && !event.nativeEvent.isComposing) { event.preventDefault(); dismiss(); } }}>
    <header><h1>DeliDev</h1><button type="button" aria-label={copy("tray-status.close")} onClick={dismiss}>×</button></header>
    <div className="tray-status-scroll">
      <label className="tray-window-selector">{copy("tray-status.window")}<select ref={selection} value={selected ?? ""} aria-disabled={!snapshot?.windows.length} onChange={event => setSelected(event.target.value)}>{snapshot?.windows.length ? snapshot.windows.map(row => <option key={row.target.instance} value={row.target.instance}>{row.name}</option>) : <option value="">{copy("tray-status.noWindows")}</option>}</select></label>
      <p className="tray-connection" role="status">{copy(!overview ? "tray-status.unavailable" : stale ? "tray-status.connectionStale" : "tray-status.connected")}</p>
      {overview ? <p>{copy("tray-status.updatedAt", { at: formatTimestampLabel(overview.observed_at, { preference: snapshot?.date_format, now }) })}</p> : null}
      {problem ? <div role="alert"><p>{copy("tray-status.problem")}</p><button onClick={() => reload.current()}>{copy("tray-status.reload")}</button></div> : null}
      <h2>{copy("tray-status.accounts")}</h2>
      {!summary?.accounts ? <p>{copy("tray-status.accountsUnavailable")}</p> : !summary.accounts.entries.length ? <p>{copy("tray-status.accountsEmpty")}</p> : snapshot ? summary.accounts.entries.map((account, index) => <article className="tray-account" key={index}>
        <h3>{account.subscription_service ? `${account.subscription_service === "chatgpt" ? "ChatGPT" : account.subscription_service === "claude" ? "Claude" : "Grok"} · ` : ""}{account.alias_hidden ? copy("tray-status.aliasHidden") : account.alias}</h3>
        {account.windows.length ? account.windows.map((quota, i) => <Quota key={i} quota={quota} index={i} stale={stale} snapshot={snapshot} now={now} />) : <p>{copy("tray-status.quotasMissing")}</p>}
        {account.more ? <p>{copy("tray-status.moreQuotas")}</p> : null}
      </article>) : null}
      {summary?.accounts?.more ? <p>{copy("tray-status.moreAccounts")}</p> : null}
      <button disabled={!current || busy || problem} onClick={() => void action(TrayPanelAction.Settings)}>{copy("tray-status.manage")}</button>
      {snapshot?.more ? <p>{copy("tray-status.moreWindows")}</p> : null}
      <section className="tray-metrics" aria-label={copy("tray-status.metrics")}>
        <button disabled={!current || busy || problem} onClick={() => void action(TrayPanelAction.Sessions)}><span>{copy("tray-status.sessions")}</span><strong>{count(overview?.active_sessions)}</strong></button>
        <button disabled={!current || busy || problem} onClick={() => void action(TrayPanelAction.Inbox)}><span>{copy("tray-status.responses")}</span><strong>{count(overview?.pending_interactions)}</strong></button>
        <button disabled={!current || busy || problem} onClick={() => void action(TrayPanelAction.Settings)}><span>{copy("tray-status.runners")}</span><strong>{overview ? copy("tray-status.connectedRunners", { connected: count(overview.connected_workers), registered: count(overview.registered_workers) }) : copy("tray-status.unavailable")}</strong></button>
        <button disabled={!current || busy || problem} onClick={() => void action(TrayPanelAction.Usage)}><span>{copy("tray-status.tokens")}</span><strong>{count(summary?.usage?.known_tokens)}</strong></button>
      </section>
      <p>{copy(stale ? "tray-status.stale" : "tray-status.incomplete")}</p>
      <Disclosure density={DisclosureDensity.Settings}><DisclosureSummary>{copy("tray-status.usageDetails")}</DisclosureSummary><p>{copy("tray-status.incomplete")}</p>{summary?.usage?.estimates.map(estimate => <p key={estimate.currency}>{copy("tray-status.estimate", { currency: estimate.currency, amount: estimate.known_amount ?? copy("tray-status.unavailable") })}</p>)}</Disclosure>
    </div>
    <footer><button className="primary" disabled={busy || Boolean(current && problem)} onClick={() => void action(current ? TrayPanelAction.Show : TrayPanelAction.Recovery)}>{copy("tray-status.open")}</button><button disabled={busy} onClick={() => void action(TrayPanelAction.Quit)}>{copy("tray-status.quit")}</button></footer>
  </main>;
}
