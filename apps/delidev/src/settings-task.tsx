// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { Children, Fragment, cloneElement, createContext, isValidElement, useCallback, useContext, useId, useLayoutEffect, useMemo, useRef, useState, type ButtonHTMLAttributes, type ReactNode, type KeyboardEvent } from "react";
import { createPortal } from "react-dom";
import { SettingsDialogFocus, SettingsDialogSize, SettingsTaskContext, useSettingsTaskDismiss, type SettingsTaskPresentation } from "./settings-task-context";
import { useQueryClient } from "@tanstack/react-query";
import { SettingsLifetime, useSettingsOpening } from "./settings-lifetime";
import { MutationIntents } from "./mutation";
import "./settings-task.css";
import { DialogSurface } from "./ui";
import { copy, useLocale } from "./localization";

export { SettingsDialogFocus, SettingsDialogSize } from "./settings-task-context";
interface Host { outlet: HTMLDivElement | null; statusTarget: HTMLDivElement | null; setStatusTarget: (node: HTMLDivElement | null) => void; modal: boolean; register: (id: string, mounted: boolean) => void }
const HostContext = createContext<Host | undefined>(undefined);
const ScopeContext = createContext(false);

// Controllers created above their dialog must enter this boundary first. One
// original task owns its waits and retries; nested steps share that same scope.
export function SettingsTaskScope({ children }: { children: ReactNode }) {
  return <SettingsLifetime>{() => <MutationIntents><ScopeContext.Provider value>{children}</ScopeContext.Provider></MutationIntents>}</SettingsLifetime>;
}

export function SettingsTasks({ children }: { children: ReactNode }) {
  const [outlet, setOutlet] = useState<HTMLDivElement | null>(null);
  const [statusTarget, setStatusTarget] = useState<HTMLDivElement | null>(null);
  const tasks = useRef(new Set<string>());
  const [modal, setModal] = useState(false);
  const register = useCallback((id: string, mounted: boolean) => {
    if (mounted) tasks.current.add(id); else tasks.current.delete(id);
    setModal(tasks.current.size > 0);
  }, []);
  const host = useMemo(() => ({ outlet, statusTarget, setStatusTarget, modal, register }), [outlet, statusTarget, modal, register]);
  return <HostContext.Provider value={host}>{children}<div className="settings-task-outlet" ref={setOutlet} /></HostContext.Provider>;
}

export function SettingsTaskStatusOutlet({ className = "" }: { className?: string }) {
  const host = useContext(HostContext);
  return <div className={`settings-task-status ${className}`} ref={host?.setStatusTarget} />;
}

export function SettingsTaskBackground({ children }: { children: ReactNode }) {
  const host = useContext(HostContext);
  // Dialogs are portaled outside this fieldset. Only a mounted task disables
  // the category; dismissal releases it before the close callback returns.
  return <fieldset className="settings-task-background" disabled={host?.modal} inert={host?.modal || undefined} aria-hidden={host?.modal || undefined} onClickCapture={event => {
    if (event.target instanceof Element) event.target.closest<HTMLButtonElement>("button")?.focus({ preventScroll: true });
  }}>{children}</fieldset>;
}

function available(node: HTMLElement | null) {
  if (!node?.isConnected || node.closest("[hidden], [inert]") || node.matches(":disabled")) return false;
  const dialog = node.closest("dialog");
  if (dialog && !dialog.open) return false;
  const style = getComputedStyle(node);
  return style.display !== "none" && style.visibility !== "hidden";
}
function anotherModal(dialog: HTMLDialogElement, ancestors: readonly HTMLDialogElement[] = []) {
  return [...document.querySelectorAll<HTMLDialogElement>("dialog[open]")].some(other => other !== dialog && !ancestors.includes(other) && other.getAttribute("role") !== "region");
}

function containTab(event: KeyboardEvent<HTMLDialogElement>) {
  if (event.key !== "Tab" || event.defaultPrevented) return;
  const dialog = event.currentTarget;
  const controls = [...dialog.querySelectorAll<HTMLElement>("button, input, textarea, select, summary, a[href], [tabindex]")].filter(node => node.tabIndex >= 0 && available(node) && node.getClientRects().length > 0);
  const first = controls[0], last = controls.at(-1), active = document.activeElement;
  if (!first) { event.preventDefault(); dialog.querySelector<HTMLElement>("h2")?.focus(); return; }
  if (event.shiftKey && (active === first || !controls.includes(active as HTMLElement))) { event.preventDefault(); last?.focus(); }
  else if (!event.shiftKey && (active === last || !controls.includes(active as HTMLElement))) { event.preventDefault(); first.focus(); }
}

// Only New Project registration owns this independent mutation child. Ordinary
// nested workflows continue to use internal steps and their original owner.
export function ProjectRepositoryRegistrationDialog(props: DialogProps) {
  const ancestors = useRef([...document.querySelectorAll<HTMLDialogElement>("dialog[open]:not([role=region])")]);
  const parent = ancestors.current.at(-1);
  useLayoutEffect(() => {
    if (!parent) return;
    const retained = parent.hasAttribute("inert"); parent.setAttribute("inert", "");
    return () => { if (!retained) parent.removeAttribute("inert"); };
  }, [parent]);
  return <SettingsTaskContext.Provider value={undefined}><SettingsTaskScope><SettingsTaskWindow {...props} ancestorDialogs={ancestors.current} /></SettingsTaskScope></SettingsTaskContext.Provider>;
}

interface DialogProps extends SettingsTaskPresentation { close: () => void; children: ReactNode; retained?: boolean; onDismiss?: () => void; activation?: number; fallbackFocus?: () => HTMLElement | null }
export function SettingsTaskDialog(props: DialogProps) {
  const parent = useContext(SettingsTaskContext);
  const scoped = useContext(ScopeContext);
  return parent ? <SettingsTaskStep {...props} /> : scoped ? <SettingsTaskWindow {...props} /> : <SettingsTaskScope><SettingsTaskWindow {...props} /></SettingsTaskScope>;
}
function SettingsTaskStep({ title, subtitle, size, focus, children, onDismiss }: DialogProps) {
  const task = useContext(SettingsTaskContext)!, id = useId();
  const opener = useRef(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  useSettingsTaskDismiss(() => onDismiss?.());
  const present = task.present;
  useLayoutEffect(() => {
    present(id, { title, subtitle, size, focus });
    return () => { present(id); requestAnimationFrame(() => { if (available(opener.current)) opener.current?.focus({ preventScroll: true }); }); };
  }, [id, present, title, subtitle, size, focus]);
  const context = useMemo(() => ({ ...task, stepId: id }), [task, id]);
  return task.stepTarget ? createPortal(<SettingsTaskContext.Provider value={context}><div data-settings-task-step hidden={task.activeStep !== id}>{children}</div></SettingsTaskContext.Provider>, task.stepTarget) : null;
}
function SettingsTaskWindow({ title, subtitle, size = SettingsDialogSize.Form, focus = SettingsDialogFocus.Input, close, children, onDismiss: dismissed, fallbackFocus, ancestorDialogs = [] }: DialogProps & { ancestorDialogs?: readonly HTMLDialogElement[] }) {
  useLocale();
  const opening = useSettingsOpening()!, client = useQueryClient();
  const id = useId(), dialog = useRef<HTMLDialogElement>(null), heading = useRef<HTMLHeadingElement>(null);
  const opener = useRef<HTMLElement | null>(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  const returnFocus = useRef(fallbackFocus);
  returnFocus.current = fallbackFocus;
  const openerRetired = useRef(false);
  const closeRequested = useRef(false);
  const categoryContent = useRef(document.querySelector<HTMLElement>(".settings-content"));
  const [actions, setActions] = useState<HTMLDivElement | null>(null);
  const [presentation, setPresentation] = useState<SettingsTaskPresentation>();
  const [activeStep, setActiveStep] = useState<string>();
  const [stepTarget, setStepTarget] = useState<HTMLDivElement | null>(null);
  const committed = useRef(false);
  const dismissals = useRef(new Map<string, () => void>()), presentations = useRef(new Map<string, SettingsTaskPresentation>());
  const host = useContext(HostContext), register = host?.register;
  const onDismiss = useCallback((key: string, action?: () => void) => { if (action) dismissals.current.set(key, action); else dismissals.current.delete(key); }, []);
  const present = useCallback((key: string, value?: SettingsTaskPresentation) => {
    if (value) presentations.current.set(key, value); else presentations.current.delete(key);
    setPresentation([...presentations.current.values()].at(-1));
    setActiveStep([...presentations.current.keys()].at(-1));
  }, []);
  const current = presentation ?? { title, subtitle, size, focus };
  const dismissWithClose = useCallback((idleClose?: () => void, _force = false) => {
    if (closeRequested.current) return;
    closeRequested.current = true;
    // Fence late continuations before callbacks can mount another task. Disposal
    // aborts client waits, never an explicit server/Worker cancellation operation.
    opening.dispose(client);
    dismissed?.();
    for (const action of dismissals.current.values()) action();
    (idleClose ?? close)();
  }, [opening, client, dismissed, close]);
  const dismiss = useCallback(() => dismissWithClose(), [dismissWithClose]);
  const retireOpener = useCallback((removed: (node: HTMLElement) => boolean) => {
    if (opener.current && removed(opener.current)) openerRetired.current = true;
  }, []);
  const context = useMemo(() => ({ visible: true, dismiss, dismissWithClose, actions, stepTarget, activeStep, onDismiss, present, retireOpener }), [dismiss, dismissWithClose, actions, stepTarget, activeStep, onDismiss, present, retireOpener]);
  useLayoutEffect(() => { register?.(id, true); return () => register?.(id, false); }, [id, register]);
  useLayoutEffect(() => {
    const node = dialog.current;
    if (!node) return;
    // React Strict Mode replays this layout effect before the first microtask.
    // A later unmount without dismissWithClose is a programmatic completion
    // (for example, a successful save closing its parent task), so it must use
    // the same focus restoration path as an explicit close.
    let live = true;
    queueMicrotask(() => { if (live) committed.current = true; });
    node.showModal();
    const initialFocus = document.activeElement;
    const frame = requestAnimationFrame(() => {
      if (!node.open || anotherModal(node, ancestorDialogs)) return;
      // A nested chooser can open and restore its trigger before this frame.
      // Preserve newer focus inside this original task instead of replaying its
      // initial focus policy over the user's accepted destination.
      const focused = document.activeElement;
      if (focused !== initialFocus && focused?.isConnected && node.contains(focused)) return;
      const target = current.focus === SettingsDialogFocus.Heading ? heading.current
        : current.focus === SettingsDialogFocus.Close ? node.querySelector<HTMLElement>(".settings-task-close")
        : current.focus === SettingsDialogFocus.Cancel ? node.querySelector<HTMLElement>("[data-settings-task-cancel]:not(:disabled)") ?? node.querySelector<HTMLElement>(".settings-task-close")
        : node.querySelector<HTMLElement>("input:not([type=hidden]):not(:disabled), textarea:not(:disabled), select:not(:disabled)");
      (target ?? heading.current)?.focus({ preventScroll: true });
    });
    return () => {
      live = false;
      cancelAnimationFrame(frame);
      node.close();
      // Strict Mode's simulated cleanup must not unlock or steal focus.
      if (!closeRequested.current && !committed.current) return;
      // A category departure or replacement dialog cannot restore a stale opener.
      if (anotherModal(node, ancestorDialogs)) return;
      const restoreFocus = () => {
        if (anotherModal(node, ancestorDialogs)) return null;
        const focused = document.activeElement;
        if (focused !== document.body && focused !== document.documentElement && focused !== opener.current && !node.contains(focused)) return null;
        const openerDialog = opener.current?.closest("dialog");
        // Document roots cannot receive focus as a task opener.
        const openerTarget = !openerRetired.current && opener.current !== document.body && opener.current !== document.documentElement && opener.current?.isConnected && !opener.current.hasAttribute("disabled") && !opener.current.matches("[hidden], [aria-hidden=true]") && !opener.current.closest("[hidden]") && (!openerDialog || openerDialog.open) && (!returnFocus.current || available(opener.current)) ? opener.current : null;
        const fallback = returnFocus.current?.() ?? (categoryContent.current?.isConnected ? categoryContent.current.querySelector<HTMLElement>(".settings-toolbar button:not(:disabled), .settings-heading button:not(:disabled)") ?? categoryContent.current.querySelector<HTMLElement>("h1") : null);
        // The opener or category fallback can still be inert until this parent
        // teardown commits. Release their task backgrounds before checking
        // visibility, while preserving unrelated hidden/disabled boundaries.
        for (const candidate of [openerTarget, fallback]) {
          const background = candidate?.closest("fieldset.settings-task-background");
          background?.removeAttribute("disabled");
          background?.removeAttribute("inert");
          background?.removeAttribute("aria-hidden");
        }
        const target = available(openerTarget) ? openerTarget : available(fallback) ? fallback : null;
        // Native close can restore an opener that this commit has just disabled.
        // Do not leave focus on an unavailable control while its read refreshes.
        if (focused === opener.current && opener.current?.matches(":disabled")) opener.current.blur();
        if (target?.matches("h1")) target.tabIndex = -1;
        target?.focus({ preventScroll: true });
        if (target && document.activeElement !== target) requestAnimationFrame(() => { if (target.isConnected) target.focus({ preventScroll: true }); });
        return target;
      };
      // Programmatic completion runs before React commits updated list controls.
      // Inspect their final availability after that commit, preserving explicit
      // user dismissal's immediate return and the native dialog restoration.
      if (closeRequested.current) restoreFocus(); else queueMicrotask(restoreFocus);
      requestAnimationFrame(restoreFocus);
    };
  // Step changes do not create another modal opening or overwrite its opener.
  }, [host?.outlet]);
  useLayoutEffect(() => {
    const node = dialog.current;
    if (!presentation || !node?.open || anotherModal(node, ancestorDialogs)) return;
    const target = presentation.focus === SettingsDialogFocus.Close ? node.querySelector<HTMLElement>(".settings-task-close") : presentation.focus === SettingsDialogFocus.Cancel ? node.querySelector<HTMLElement>(".settings-task-footer [data-settings-task-cancel]:not(:disabled)") ?? node.querySelector<HTMLElement>(".settings-task-close") : presentation.focus === SettingsDialogFocus.Input ? node.querySelector<HTMLElement>(".settings-task-body [data-settings-task-step]:not([hidden]) input:not(:disabled)") : heading.current;
    (target ?? heading.current)?.focus({ preventScroll: true });
  }, [presentation]);
  const content = <SettingsTaskContext.Provider value={context}>
    {(!host || host.outlet) ? createPortal(<>

    <DialogSurface ref={dialog} onKeyDown={event => { containTab(event); if (ancestorDialogs.length) event.stopPropagation(); }} className="settings-task-dialog" data-size={current.size} aria-modal="true" aria-labelledby={`${id}-title`} onCancel={event => { event.preventDefault(); event.stopPropagation(); dismiss(); }}>
      <header className="settings-task-header"><div><h2 ref={heading} tabIndex={-1} id={`${id}-title`}>{current.title}</h2><p>{current.subtitle ?? copy("settings.savedOnTheSelectedServer_93dbee")}</p></div><button type="button" className="settings-task-close" aria-label={`${copy("settings-task.close")} ${current.title}`} onClick={dismiss}>×</button></header>
      <div className="settings-task-body settings-content-column"><div hidden={Boolean(activeStep)}>{children}</div><div ref={setStepTarget} /></div>
      <div ref={setActions} className="settings-task-footer" />
    </DialogSurface></>, host?.outlet ?? document.body) : null}
  </SettingsTaskContext.Provider>;
  return content;
}

function associateForm(children: ReactNode, form?: string, task?: React.ContextType<typeof SettingsTaskContext>): ReactNode {
  const nodes = Children.toArray(children).flatMap(child => isValidElement(child) && child.type === Fragment ? Children.toArray((child.props as { children: ReactNode }).children) : [child]);
  nodes.sort((a, b) => Number(isValidElement(a) && String((a.props as { className?: string }).className ?? "").split(" ").includes("primary")) - Number(isValidElement(b) && String((b.props as { className?: string }).className ?? "").split(" ").includes("primary")));
  return Children.map(nodes, child => {
    if (!isValidElement(child)) return child;
    if (child.type === Fragment) return cloneElement(child as React.ReactElement<{ children: ReactNode }>, { children: associateForm((child.props as { children: ReactNode }).children, form, task) });
    // Only explicitly audited dismissal duplicates opt in; marked business or
    // nested cancellation controls keep their existing adaptation.
    if (child.type === SettingsTaskDismissButton && task && !task.stepId) return null;
    if (child.type !== "button" && child.type !== SettingsActionButton && child.type !== SettingsTaskDismissButton) return child;
    const button = child as React.ReactElement<ButtonHTMLAttributes<HTMLButtonElement> & { "data-settings-task-cancel"?: boolean }>;
    // Local cancellation remains available while a request is pending. Nested
    // steps keep their own Back/Keep callback and the parent's lifetime.
    const cancel = button.props["data-settings-task-cancel"] && task && (!task.stepId || button.props.disabled)
      ? (event: React.MouseEvent<HTMLButtonElement>) => {
        if (task.stepId || !button.props.onClick || button.props.onClick === task.dismiss) task.dismiss();
        else task.dismissWithClose(() => button.props.onClick?.(event));
      } : undefined;
    return cloneElement(button, { ...(cancel ? { disabled: false, onClick: cancel } : {}), form: button.props.form ?? form, type: button.props.type ?? (form ? "submit" : "button") });
  });
}
// Use only when the complete action equals header dismissal in a top-level task.
// Page cancellation and nested return remain available with their original guards.
export function SettingsTaskDismissButton(props: ButtonHTMLAttributes<HTMLButtonElement> & { "data-settings-task-cancel"?: boolean }) {
  const task = useContext(SettingsTaskContext);
  return task && !task.stepId ? null : <SettingsActionButton icon={SettingsActionIcon.Cancel} {...props} />;
}

export function SettingsTaskActions({ children, className = "", form }: { children: ReactNode; className?: string; form?: string }) {
  const task = useContext(SettingsTaskContext);
  if (task?.activeStep !== task?.stepId) return null;
  const associated = associateForm(children, form, task);
  if (!Children.toArray(associated).length) return null;
  const content = <div className={`actions ${className}`}>{associated}</div>;
  return task?.actions ? createPortal(content, task.actions) : content;
}
