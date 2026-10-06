// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale, ownedMessage, useProductMessage, LocalizedText } from "./localization";
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { invoke } from "@tauri-apps/api/core";
import { BrowserQuery, BrowserCapability, BrowserProfileState, newRequestId, type Resource, type BrowserProfile } from "@delinoio/delidev-api-client";

import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import { useBrowserHost } from "./host-capabilities";

enum BrowserAction { Navigate = "navigate", Back = "back", Forward = "forward", Reload = "reload", NewTab = "new-tab", SelectTab = "select-tab", CloseTab = "close-tab", Resize = "resize", Hide = "hide" }
interface BrowserState { tabs: { tabs: { id: string; url: string }[]; selected: string }; removal_pending: boolean }
const idPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export function browserProfile(profile: BrowserProfile | undefined, accountId: string): string {
 if (!profile || profile.revision === 0n || ![profile.id,profile.serverId,profile.deviceId,profile.accountId].every((id)=>idPattern.test(id)) || profile.accountId!==accountId || profile.state!==BrowserProfileState.ACTIVE || profile.deletionRequestId) throw new Error("Browser profile ownership is unavailable.");
 return profile.id;
}

function browserState(value: BrowserState): BrowserState {
  if (!value || typeof value.removal_pending !== "boolean" || !Array.isArray(value.tabs?.tabs) || value.tabs.tabs.length > 16 || typeof value.tabs.selected !== "string") throw new Error("Local browser state is unavailable.");
  const ids = new Set<string>();
  for (const tab of value.tabs.tabs) {
    if (!idPattern.test(tab.id) || ids.has(tab.id) || typeof tab.url !== "string" || tab.url.length > 8192) throw new Error("Local browser state is unavailable.");
    const url = new URL(tab.url);
    if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) throw new Error("Local browser state is unavailable.");
    ids.add(tab.id);
  }
  if (ids.size ? !ids.has(value.tabs.selected) : value.tabs.selected !== "") throw new Error("Local browser state is unavailable.");
  return value;
}
export function SessionBrowser({ session, accountId, close }: { session: Resource; accountId: string; close: () => void }) {
  useLocale();
  const available = useBrowserHost();
  if (!available) return <section aria-label={copy("session-browser.sessionBrowser_47d746")}><p>{copy("session-browser.cefBrowserProfilesRequireTheSupported_c9b9e8")}</p><button onClick={close}>{copy("session-browser.closeBrowser_dd3303")}</button></section>;
  return <NativeSessionBrowser session={session} accountId={accountId} close={close} />;
}
function NativeSessionBrowser({ session, accountId, close }: { session: Resource; accountId: string; close: () => void }) {
  useLocale();
  const [address, setAddress] = useState("");
  const [profileId, setProfileId] = useState<string>();
  const [state, setState] = useState<BrowserState>();
  const [failure, setFailure] = useProductMessage();
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const viewport = useRef<HTMLDivElement>(null);
  const addressInput = useRef<HTMLInputElement>(null);
  const alive = useRef(true);
  const presentation = useRef("");
  const capabilities = useQuery(BrowserQuery.getBrowserCapabilities, {}, {retry:false});
  const supported = capabilities.data?.capabilities.includes(BrowserCapability.PROTECTED_DEVICE_PROFILE_V1) === true;
  const registration = useRetainedMutation(`browser-register:${session.id}:${accountId}`, BrowserQuery.registerBrowserProfile, (response) => {
    try { setProfileId(browserProfile(response.profile, accountId)); setFailure(undefined); } catch { setFailure(ownedMessage("session-browser.extra.5092c54369dd")); }
  });
  useEffect(() => { alive.current = true; addressInput.current?.focus(); return () => { alive.current = false; }; }, []);
  useEffect(() => {
    if (!profileId || !viewport.current) return;
    let disposed = false, opened = false, opening = false, resizing = false;
    let lastBounds = "", queued = false, failed = false, ownedViewId = "";
    let hiding = false, hideRequired = false, hideRetry: number | undefined;
    const node = viewport.current;
    const visible = () => node.isConnected && !node.closest("[hidden], [inert]") && !Array.from(documentGlobal().querySelectorAll('dialog[open]:not([role="region"]), [role="dialog"]:not(dialog)')).some((dialog) => {
      // The responsive sidebar retains a closed dialog, and its wide layout is
      // an open nonmodal region. Only a visible dialog should cover the child.
      const style = getComputedStyle(dialog), rect = dialog.getBoundingClientRect();
      return !dialog.closest("[hidden], [inert]") && style.display !== "none" && style.visibility !== "hidden" && rect.width > 0 && rect.height > 0;
    }) && node.getBoundingClientRect().width >= 1 && node.getBoundingClientRect().height >= 1;
    const bounds = () => {
      const r = node.getBoundingClientRect();
      let left = Math.max(0, r.left), top = Math.max(0, r.top), right = Math.min(window.innerWidth, r.right), bottom = Math.min(window.innerHeight, r.bottom);
      for (let parent = node.parentElement; parent; parent = parent.parentElement) {
        const style = getComputedStyle(parent), rect = parent.getBoundingClientRect();
        if (/auto|scroll|hidden|clip/.test(style.overflowX)) { left = Math.max(left, rect.left); right = Math.min(right, rect.right); }
        if (/auto|scroll|hidden|clip/.test(style.overflowY)) { top = Math.max(top, rect.top); bottom = Math.min(bottom, rect.bottom); }
      }
      return { x: left, y: top, width: Math.max(0, right - left), height: Math.max(0, bottom - top) };
    };
    const hide = async () => {
      if (!opened || hiding) return;
      hideRequired = true; hiding = true;
      const viewId = ownedViewId;
      let closed = false;
      try {
        await invoke("control_browser", { profileId, viewId, action: BrowserAction.Hide });
        opened = false; ownedViewId = ""; lastBounds = ""; hideRequired = false; closed = true;
        if (presentation.current === viewId) presentation.current = "";
        if (hideRetry !== undefined) { window.clearTimeout(hideRetry); hideRetry = undefined; }
      } catch {
        if (!disposed) setFailure(ownedMessage("session-browser.extra.a520c066c88f"));
        // Keep exact cleanup ownership after unmount. A failed Hide has not
        // released the raw child; retry only this closure, never an open/action.
        if (hideRetry === undefined) hideRetry = window.setTimeout(() => { hideRetry = undefined; void hide(); }, 250);
      } finally {
        hiding = false;
        if (closed && !disposed) { queued = false; void update(); }
      }
    };
    const update = async () => {
      if (disposed || failed) return;
      if (opening || resizing || hiding) { queued = true; return; }
      if (opened && hideRequired) { await hide(); return; }
      const area = bounds(), geometry = JSON.stringify(area);
      if (!visible() || area.width < 1 || area.height < 1) { await hide(); return; }
      if (!opened) {
        opening = true;
        const viewId = newRequestId(); ownedViewId = viewId; presentation.current = viewId;
        try {
          const result = browserState(await invoke<BrowserState>("open_browser", { profileId, viewId, url: address, bounds: area }));
          opened = true; lastBounds = geometry;
          if (disposed || presentation.current !== viewId) { await hide(); return; }
          if (!visible()) { await hide(); return; }
          setState(result);
        } catch { failed = true; if (!disposed) setFailure(ownedMessage("session-browser.extra.08759b664829")); }
        finally { opening = false; if (queued) { queued = false; void update(); } }
      } else {
        if (geometry === lastBounds) return;
        resizing = true;
        const viewId = presentation.current;
        try { const result = browserState(await invoke<BrowserState>("control_browser", { profileId, viewId, action: BrowserAction.Resize, bounds: area })); if (!disposed && opened && presentation.current === viewId) { lastBounds = geometry; setState(result); } } catch { lastBounds = ""; if (!disposed) setFailure(ownedMessage("session-browser.extra.1b8cff3d45fb")); }
        finally { resizing = false; if (queued) { queued = false; void update(); } }
      }
    };
    void update();
    const move = () => void update(); window.addEventListener("scroll", move, true); window.addEventListener("resize", move);
    const observer = new ResizeObserver(() => void update()); observer.observe(node);
    const visibility = new MutationObserver(() => void update()); visibility.observe(documentGlobal().body, { subtree: true, attributes: true, attributeFilter: ["hidden", "inert", "open", "class"], childList: true });
    const timer = window.setInterval(() => {
      if (!opened || disposed) return;
      void invoke<BrowserState>("browser_state", { profileId, viewId: presentation.current }).then((value) => { if (!disposed) setState(browserState(value)); }).catch(() => { if (!disposed) setFailure(ownedMessage("session-browser.extra.c6d1ec80c1c4")); });
    }, 1000);
    return () => { disposed = true; window.removeEventListener("scroll", move, true); window.removeEventListener("resize", move); observer.disconnect(); visibility.disconnect(); window.clearInterval(timer); void hide(); };
    // Address changes navigate the existing native tab only at the explicit Go action.
    // Reopening after a modal restores native local tabs without another registration.
  }, [profileId, retry]);
  const control = async (action: BrowserAction, tabId?: string) => {
    if (!profileId || busy || state?.removal_pending) return;
    setBusy(true); setFailure(undefined);
    try { const result = browserState(await invoke<BrowserState>("control_browser", { profileId, viewId: presentation.current, action, url: address, tabId })); if (alive.current) setState(result); }
    catch { if (alive.current) setFailure(ownedMessage("session-browser.extra.51fee7071e2b")); }
    finally { if (alive.current) setBusy(false); }
  };
  const blocked = busy || registration.busy || registration.uncertain || state?.removal_pending;
  return <section className="session-browser" aria-label={copy("session-browser.sessionBrowser_47d746")}>
    <header><h3>{copy("session-browser.browser_d31de1")}</h3><button onClick={close}>{copy("session-browser.closeBrowser_dd3303")}</button></header>
    <p>{copy("session-browser.sharedWithThisAccountSSessions_361a18")}</p>
    {!profileId ? <><p>{copy("session-browser.enterAWebAddressThenOpen_60a48b")}</p><button disabled={blocked || !supported || !idPattern.test(accountId) || !validAddress(address)} onClick={() => void registration.send({ session: { id: session.id, expectedRevision: session.revision, requestId: newRequestId() }, accountId })}>{copy("session-browser.openAccountBrowser_6ee5d1")}</button>{registration.uncertain ? <button disabled={registration.busy} onClick={registration.retry}>{copy("session-browser.retryTheSameRegistration_c86ef9")}</button> : null}<Problem error={registration.error} /></> : null}
    {!supported ? <p>{copy("session-browser.protectedBrowserProfilesAreUnavailableUntil_033b80")}</p> : null}
    <Problem error={capabilities.error} />
    {failure ? <p role="alert">{failure}</p> : null}
    {state?.removal_pending ? <p role="alert">{copy("session-browser.thisAccountWasDeletedItsProfile_4cf6fc")}</p> : null}
    <form onSubmit={(event) => { event.preventDefault(); void control(BrowserAction.Navigate); }} className="browser-address">
      <label>{copy("session-browser.address_56ef8f")}<input ref={addressInput} value={address} maxLength={8192} onChange={(event) => setAddress(event.target.value)} placeholder="https://example.com/" /></label><button disabled={!profileId || blocked}>{copy("session-browser.go_6cc851")}</button>
    </form>
    {profileId ? <><div className="actions"><button disabled={blocked} onClick={() => void control(BrowserAction.Back)}>{copy("session-browser.back_76900f")}</button><button disabled={blocked} onClick={() => void control(BrowserAction.Forward)}>{copy("session-browser.forward_f1c65e")}</button><button disabled={blocked} onClick={() => void control(BrowserAction.Reload)}>{copy("session-browser.reload_bdc090")}</button><button disabled={blocked || (state?.tabs.tabs.length ?? 0) >= 16} onClick={() => void control(BrowserAction.NewTab)}>{copy("session-browser.newTab_1e08fd")}</button><button disabled={blocked} onClick={() => { setFailure(undefined); setRetry((n) => n + 1); }}>{copy("session-browser.retryNativeView_0d84d0")}</button></div><ul aria-label={copy("session-browser.browserTabs_3e94f1")} className="browser-tabs">{state?.tabs.tabs.map((tab, index) => <li key={tab.id}><button disabled={blocked} aria-current={state.tabs.selected === tab.id ? "page" : undefined} onClick={() => void control(BrowserAction.SelectTab, tab.id)}><LocalizedText id="session-browser.tab_eb3a73" components={{ s0: <>{index + 1}</>, s1: <>{tab.url}</> }} /></button><button disabled={blocked} aria-label={copy("session-browser.closeTab_bc9560", { v0: index + 1 })} onClick={() => void control(BrowserAction.CloseTab, tab.id)}>×</button></li>)}</ul><p className="browser-current-address">{state?.tabs.tabs.find((t) => t.id === state.tabs.selected)?.url}</p></> : null}
    <div ref={viewport} className="browser-viewport" aria-label={copy("session-browser.untrustedBrowserContent_9c54bc")} />
  </section>;
}
// Keep DOM access distinct from the versioned resource-document decoder.
const documentGlobal = () => globalThis.document;

function validAddress(raw: string): boolean { try { const url = new URL(raw); return ["http:", "https:"].includes(url.protocol) && !url.username && !url.password && raw.length <= 8192; } catch { return false; } }
