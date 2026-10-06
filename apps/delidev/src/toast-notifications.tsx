// SPDX-License-Identifier: Apache-2.0
import { copy, resolveMessage, useLocale } from "./localization";
import { createContext, useContext, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { ToastKind, ToastPause, ToastStore, type NotificationController, type ToastNotification } from "./toast-store";
import "./toast-notifications.css";
export { ToastKind } from "./toast-store";
export type { NotificationController, ToastOptions } from "./toast-store";

// Toasts are optional presentation for isolated consumers. A missing viewport
// must never turn an acknowledged business operation into a failed mutation.
const absent: NotificationController = { notify: ({ id }) => id ?? "", dismiss: () => {} };
const Context = createContext<NotificationController>(absent);
export function useNotifications(): NotificationController { return useContext(Context); }

export function NotificationProvider({ children }: { children: ReactNode }) {
  useLocale();
  const [store] = useState(() => new ToastStore());
  useLayoutEffect(() => { store.activate(); return () => store.dispose(); }, [store]);
  return <Context.Provider value={store}>{children}<NotificationViewport store={store} /></Context.Provider>;
}

function modalVisible() {
  // The wide sidebar uses an open nonmodal region. Only actual modal
  // presentations, including its compact drawer, cover the toast viewport.
  return [...document.querySelectorAll<HTMLElement>('dialog[open]:not([role="region"]), [aria-modal="true"]')].some(node => {
    const style = getComputedStyle(node);
    return !node.closest("[hidden], [inert]") && style.display !== "none" && style.visibility !== "hidden";
  });
}
function NotificationViewport({ store }: { store: ToastStore }) {
  useLocale();
  const notifications = useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
  const active = notifications.length > 0;
  const [covered, setCovered] = useState(modalVisible);
  const previousFocus = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    const rememberFocus = () => {
      const node = document.activeElement;
      if (node instanceof HTMLElement && node !== document.body && !node.closest(".toast-viewport, [hidden], [inert]")) previousFocus.current = node;
    };
    rememberFocus();
    document.addEventListener("focusin", rememberFocus);
    return () => document.removeEventListener("focusin", rememberFocus);
  }, []);
  useLayoutEffect(() => {
    // The idle app needs no modal/style scans. Start observation only while a
    // toast exists, and recheck before paint if the first toast arrives covered.
    if (!active) { store.suspend(document.hidden); setCovered(false); return; }
    const update = () => {
      const modal = modalVisible();
      store.suspend(modal || document.hidden);
      setCovered(modal);
    };
    update();
    const observer = new MutationObserver(update);
    observer.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["open", "hidden", "inert", "class", "style", "role", "aria-modal"] });
    document.addEventListener("visibilitychange", update);
    return () => { observer.disconnect(); document.removeEventListener("visibilitychange", update); store.suspend(true); };
  }, [store, active]);
  const restoreFocus = () => {
    const available = (node: HTMLElement | null): node is HTMLElement => {
      if (!node?.isConnected || node.closest("[hidden], [inert]") || node.matches(":disabled")) return false;
      for (let parent: HTMLElement | null = node; parent; parent = parent.parentElement) {
        const style = getComputedStyle(parent);
        if (style.display === "none" || style.visibility === "hidden" || style.visibility === "collapse") return false;
      }
      return true;
    };
    const fallback = document.getElementById("main");
    const target = available(previousFocus.current) ? previousFocus.current : available(fallback) ? fallback : null;
    target?.focus({ preventScroll: true });
    if (target && document.activeElement !== target && available(fallback)) fallback.focus({ preventScroll: true });
  };
  return createPortal(<div className="toast-viewport" role="region" aria-label={copy("toast-notifications.notifications_788011")} hidden={!active || covered}>
    {notifications.map(notice => <Toast key={notice.id} notice={notice} store={store} restoreFocus={restoreFocus} />)}
  </div>, document.body);
}

const labels: Record<ToastKind, string> = { get [ToastKind.Success]() { return copy("toast-notifications.success_c88a0b"); }, get [ToastKind.Info]() { return copy("toast-notifications.information_1cb0ba"); }, get [ToastKind.Warning]() { return copy("toast-notifications.warning_e981dd"); }, get [ToastKind.Error]() { return copy("toast-notifications.error_54a0e8"); } };
function ToastIcon({ kind }: { kind: ToastKind }) {
  useLocale();
  return <svg className="toast-icon" aria-hidden="true" viewBox="0 0 24 24">{kind === ToastKind.Success ? <><circle cx="12" cy="12" r="10" /><path d="m7 12 3 3 7-7" /></> : kind === ToastKind.Warning ? <><path d="m12 3 10 18H2L12 3Z" /><path d="M12 9v5m0 3h.01" /></> : <><circle cx="12" cy="12" r="10" /><path d={kind === ToastKind.Error ? "m8 8 8 8m0-8-8 8" : "M12 11v6m0-10h.01"} /></>}</svg>;
}
function Toast({ notice, store, restoreFocus }: { notice: ToastNotification; store: ToastStore; restoreFocus: () => void }) {
  useLocale();
  const message = resolveMessage(notice.message)!;
  const ref = useRef<HTMLElement>(null);
  const close = () => {
    const ownsFocus = ref.current?.contains(document.activeElement);
    store.dismiss(notice.id);
    if (ownsFocus) restoreFocus();
  };
  return <article ref={ref} className="toast-notification" data-kind={notice.kind}
    onMouseEnter={() => store.pause(notice.id, ToastPause.Hover, true)} onMouseLeave={() => store.pause(notice.id, ToastPause.Hover, false)}
    onFocus={() => store.pause(notice.id, ToastPause.Focus, true)} onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) store.pause(notice.id, ToastPause.Focus, false); }}
    onKeyDown={event => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); } }}>
    <div className="toast-content" role={notice.kind === ToastKind.Error ? "alert" : "status"} aria-atomic="true"><ToastIcon kind={notice.kind} /><p><span className="toast-kind-label">{labels[notice.kind]}: </span>{message}</p></div>
    <button className="toast-close" type="button" aria-label={copy("toast-notifications.dismissNotification_1dd388", { v0: message })} onClick={close}><svg aria-hidden="true" viewBox="0 0 24 24"><path d="m6 6 12 12m0-12L6 18" /></svg></button>
  </article>;
}
