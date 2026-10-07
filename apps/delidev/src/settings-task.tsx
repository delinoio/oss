// SPDX-License-Identifier: Apache-2.0
import { Children, Fragment, cloneElement, createContext, isValidElement, useCallback, useContext, useId, useLayoutEffect, useMemo, useRef, useState, type ButtonHTMLAttributes, type ReactNode, type KeyboardEvent } from "react";
import { createPortal } from "react-dom";
import { SettingsDialogFocus, SettingsDialogSize, SettingsTaskStatus, SettingsTaskContext, useRetainSettingsTask, useSettingsTaskDismiss, type SettingsTaskPresentation } from "./settings-task-context";
import "./settings-task.css";
import { DialogSurface } from "./ui";
import { copy, useLocale } from "./localization";

export { SettingsDialogFocus, SettingsDialogSize } from "./settings-task-context";
interface Host { outlet: HTMLDivElement | null; statusTarget: HTMLDivElement | null; setStatusTarget: (node: HTMLDivElement | null) => void; locked: boolean; modal: boolean; register: (id: string, mounted: boolean, visible?: boolean) => void }
const HostContext = createContext<Host | undefined>(undefined);

export function SettingsTasks({ children }: { children: ReactNode }) {
  const [outlet, setOutlet] = useState<HTMLDivElement | null>(null);
  const [statusTarget, setStatusTarget] = useState<HTMLDivElement | null>(null);
  const tasks = useRef(new Map<string, boolean>());
  const [locked, setLocked] = useState(false);
  const [modal, setModal] = useState(false);
  const register = useCallback((id: string, mounted: boolean, visible = true) => {
    if (mounted) tasks.current.set(id, visible); else tasks.current.delete(id);
    setLocked(tasks.current.size > 0);
    setModal([...tasks.current.values()].some(Boolean));
  }, []);
  const host = useMemo(() => ({ outlet, statusTarget, setStatusTarget, locked, modal, register }), [outlet, statusTarget, locked, modal, register]);
  return <HostContext.Provider value={host}>{children}<div className="settings-task-outlet" ref={setOutlet} /></HostContext.Provider>;
}

export function SettingsTaskStatusOutlet({ className = "" }: { className?: string }) {
  const host = useContext(HostContext);
  return <div className={`settings-task-status ${className}`} ref={host?.setStatusTarget} />;
}

export function SettingsTaskBackground({ children }: { children: ReactNode }) {
  const host = useContext(HostContext);
  // Dialogs are portaled outside this fieldset. A hidden unresolved task still
  // disables replacement writes, while its explicit outcome opener stays usable.
  return <><SettingsTaskStatusOutlet /><fieldset className="settings-task-background" disabled={host?.locked} inert={host?.modal || undefined} aria-hidden={host?.modal || undefined} onClickCapture={event => {
    if (event.target instanceof Element) event.target.closest<HTMLButtonElement>("button")?.focus({ preventScroll: true });
  }}>{children}</fieldset></>;
}

function available(node: HTMLElement | null) {
  if (!node?.isConnected || node.closest("[hidden], [inert]") || node.matches(":disabled")) return false;
  const dialog = node.closest("dialog");
  if (dialog && !dialog.open) return false;
  const style = getComputedStyle(node);
  return style.display !== "none" && style.visibility !== "hidden";
}
function anotherModal(dialog: HTMLDialogElement) {
  return [...document.querySelectorAll<HTMLDialogElement>("dialog[open]")].some(other => other !== dialog && other.getAttribute("role") !== "region");
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

interface DialogProps extends SettingsTaskPresentation { close: () => void; children: ReactNode; retained?: boolean; onDismiss?: () => void; activation?: number; fallbackFocus?: () => HTMLElement | null }
export function SettingsTaskDialog(props: DialogProps) {
  const parent = useContext(SettingsTaskContext);
  return parent ? <SettingsTaskStep {...props} /> : <SettingsTaskWindow {...props} />;
}
function SettingsTaskStep({ title, size, focus, children, retained = false, onDismiss }: DialogProps) {
  const task = useContext(SettingsTaskContext)!, id = useId();
  const opener = useRef(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  useRetainSettingsTask(retained);
  useSettingsTaskDismiss(() => onDismiss?.());
  const present = task.present;
  useLayoutEffect(() => {
    present(id, { title, size, focus });
    return () => { present(id); requestAnimationFrame(() => { if (available(opener.current)) opener.current?.focus({ preventScroll: true }); }); };
  }, [id, present, title, size, focus]);
  const context = useMemo(() => ({ ...task, stepId: id }), [task, id]);
  return task.stepTarget ? createPortal(<SettingsTaskContext.Provider value={context}><div data-settings-task-step hidden={task.activeStep !== id}>{children}</div></SettingsTaskContext.Provider>, task.stepTarget) : null;
}
function SettingsTaskWindow({ title, size = SettingsDialogSize.Form, focus = SettingsDialogFocus.Input, close, children, retained = false, onDismiss: dismissed, activation, fallbackFocus }: DialogProps) {
  useLocale();
  const id = useId(), dialog = useRef<HTMLDialogElement>(null), heading = useRef<HTMLHeadingElement>(null);
  const opener = useRef<HTMLElement | null>(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  const lastActivation = useRef(activation);
  const returnFocus = useRef(fallbackFocus);
  returnFocus.current = fallbackFocus;
  const closeRequested = useRef(false);
  const retainedDismissal = useRef(false);
  const categoryContent = useRef(document.querySelector<HTMLElement>(".settings-content"));
  const [visible, setVisible] = useState(true), [actions, setActions] = useState<HTMLDivElement | null>(null);
  const [presentation, setPresentation] = useState<SettingsTaskPresentation>();
  const [activeStep, setActiveStep] = useState<string>();
  const [stepTarget, setStepTarget] = useState<HTMLDivElement | null>(null);
  const [signalStatus, setSignalStatus] = useState(SettingsTaskStatus.AwaitingConfirmation);
  const committed = useRef(false);
  const signals = useRef(new Map<string, SettingsTaskStatus>()), dismissals = useRef(new Map<string, () => void>()), presentations = useRef(new Map<string, SettingsTaskPresentation>());
  const host = useContext(HostContext), register = host?.register;
  const retain = useCallback((key: string, retained: boolean, status = SettingsTaskStatus.AwaitingConfirmation) => {
    if (retained) signals.current.set(key, status); else signals.current.delete(key);
    const states = [...signals.current.values()];
    setSignalStatus(states.includes(SettingsTaskStatus.Uncertain) ? SettingsTaskStatus.Uncertain : states.includes(SettingsTaskStatus.Pending) ? SettingsTaskStatus.Pending : SettingsTaskStatus.AwaitingConfirmation);
  }, []);
  const onDismiss = useCallback((key: string, action?: () => void) => { if (action) dismissals.current.set(key, action); else dismissals.current.delete(key); }, []);
  const present = useCallback((key: string, value?: SettingsTaskPresentation) => {
    if (value) presentations.current.set(key, value); else presentations.current.delete(key);
    setPresentation([...presentations.current.values()].at(-1));
    setActiveStep([...presentations.current.keys()].at(-1));
  }, []);
  const current = presentation ?? { title, size, focus };
  const dismissWithClose = useCallback((idleClose?: () => void) => {
    const keep = retained || signals.current.size > 0;
    dismissed?.();
    for (const action of dismissals.current.values()) action();
    if (keep) { retainedDismissal.current = true; setVisible(false); } else { closeRequested.current = true; (idleClose ?? close)(); }
  }, [retained, dismissed, close]);
  const dismiss = useCallback(() => dismissWithClose(), [dismissWithClose]);
  const context = useMemo(() => ({ visible, dismiss, dismissWithClose, actions, stepTarget, activeStep, retain, onDismiss, present }), [visible, dismiss, dismissWithClose, actions, stepTarget, activeStep, retain, onDismiss, present]);
  useLayoutEffect(() => {
    if (lastActivation.current === activation) return;
    lastActivation.current = activation;
    if (visible) return;
    // External creation entries reopen the original retained controller rather
    // than replacing its immutable pending or uncertain request.
    opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setVisible(true);
  }, [activation, visible]);
  useLayoutEffect(() => { register?.(id, true, visible); return () => register?.(id, false); }, [id, register, visible]);
  useLayoutEffect(() => {
    const node = dialog.current;
    if (!visible || !node) return;
    retainedDismissal.current = false;
    // React Strict Mode replays this layout effect before the first microtask.
    // A later unmount without dismissWithClose is a programmatic completion
    // (for example, a successful save closing its parent task), so it must use
    // the same focus restoration path as an explicit close.
    let live = true;
    queueMicrotask(() => { if (live) committed.current = true; });
    node.showModal();
    const frame = requestAnimationFrame(() => {
      if (!node.open || anotherModal(node)) return;
      const target = current.focus === SettingsDialogFocus.Heading ? heading.current
        : current.focus === SettingsDialogFocus.Cancel ? node.querySelector<HTMLElement>("[data-settings-task-cancel]") ?? node.querySelector<HTMLElement>("button:not(.primary):not(.settings-task-close)")
        : node.querySelector<HTMLElement>("input:not([type=hidden]):not(:disabled), textarea:not(:disabled), select:not(:disabled)");
      (target ?? heading.current)?.focus({ preventScroll: true });
    });
    return () => {
      live = false;
      cancelAnimationFrame(frame);
      node.close();
      // StrictMode and retained-task visibility changes also clean up this
      // effect. They must not unlock the background or steal focus.
      if (!closeRequested.current && !committed.current) return;
      // Settings keeps its retained background locked. External creation can
      // return to its visible Home opener after an explicit pending dismissal.
      if ((retained || signals.current.size > 0) && !(returnFocus.current && retainedDismissal.current)) return;
      // A category departure or replacement dialog cannot restore a stale opener.
      if (anotherModal(node)) return;
      const restoreFocus = () => {
        if (anotherModal(node)) return;
        const focused = document.activeElement;
        if (focused !== document.body && focused !== document.documentElement && focused !== opener.current && !node.contains(focused)) return;
        const openerDialog = opener.current?.closest("dialog");
        const openerTarget = opener.current?.isConnected && !opener.current.hasAttribute("disabled") && !opener.current.matches("[hidden], [aria-hidden=true]") && !opener.current.closest("[hidden]") && (!openerDialog || openerDialog.open) && (!returnFocus.current || available(opener.current)) ? opener.current : null;
        const fallback = returnFocus.current?.() ?? (categoryContent.current?.isConnected ? categoryContent.current.querySelector<HTMLElement>(".settings-toolbar button:not(:disabled), .settings-heading button:not(:disabled)") ?? categoryContent.current.querySelector<HTMLElement>("h1") : null);
        const target = openerTarget ?? (available(fallback) ? fallback : null);
        if (target?.matches("h1")) target.tabIndex = -1;
        // The opener can remain inside the task background until the parent
        // state update commits. Temporarily clear that synchronous focus
        // boundary so close restores focus in the same event turn.
        const background = target?.closest("fieldset.settings-task-background");
        background?.removeAttribute("disabled");
        background?.removeAttribute("inert");
        background?.removeAttribute("aria-hidden");
        target?.focus({ preventScroll: true });
        if (target && document.activeElement !== target) requestAnimationFrame(() => { if (target.isConnected) target.focus({ preventScroll: true }); });
        return target;
      };
      restoreFocus();
      requestAnimationFrame(restoreFocus);
    };
  // Step changes do not create another modal opening or overwrite its opener.
  }, [visible, host?.outlet]);
  useLayoutEffect(() => {
    const node = dialog.current;
    if (!visible || !presentation || !node?.open || anotherModal(node)) return;
    const target = presentation.focus === SettingsDialogFocus.Cancel ? node.querySelector<HTMLElement>(".settings-task-footer [data-settings-task-cancel]") ?? node.querySelector<HTMLElement>(".settings-task-footer button:not(.primary)") : presentation.focus === SettingsDialogFocus.Input ? node.querySelector<HTMLElement>(".settings-task-body [data-settings-task-step]:not([hidden]) input:not(:disabled)") : heading.current;
    (target ?? heading.current)?.focus({ preventScroll: true });
  }, [visible, presentation]);
  const content = <SettingsTaskContext.Provider value={context}>
    {!visible && (!host || host.statusTarget) ? createPortal(<div className="settings-task-retained" role="status"><span>{title}: {signalStatus === SettingsTaskStatus.Pending ? copy("settings-task.originalOperationInProgress") : signalStatus === SettingsTaskStatus.Uncertain ? copy("settings-task.originalResultUnconfirmed") : copy("settings-task.originalOperationAwaitsConfirmation")}</span><button type="button" onClick={event => { opener.current = event.currentTarget; setVisible(true); }}>{copy("settings-task.viewOriginalOperation")}</button></div>, host?.statusTarget ?? document.body) : null}
    {(!host || host.outlet) ? createPortal(<>

    <DialogSurface ref={dialog} onKeyDown={containTab} className="settings-task-dialog" data-size={current.size} aria-modal="true" aria-labelledby={`${id}-title`} onCancel={event => { event.preventDefault(); event.stopPropagation(); dismiss(); }}>
      <header className="settings-task-header"><div><h2 ref={heading} tabIndex={-1} id={`${id}-title`}>{current.title}</h2><p>{copy("settings.savedOnTheSelectedServer_93dbee")}</p></div><button type="button" className="settings-task-close" aria-label={`${copy("settings-task.close")} ${current.title}`} onClick={dismiss}>×</button></header>
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
    if (child.type !== "button") return child;
    const button = child as React.ReactElement<ButtonHTMLAttributes<HTMLButtonElement> & { "data-settings-task-cancel"?: boolean }>;
    // Idle cancellation preserves the controller's own cleanup/focus callback.
    // An admitted operation instead hides without invoking that departure path.
    const cancel = button.props["data-settings-task-cancel"] && task && (!task.stepId || button.props.disabled)
      ? (event: React.MouseEvent<HTMLButtonElement>) => {
        if (task.stepId || !button.props.onClick || button.props.onClick === task.dismiss) task.dismiss();
        else task.dismissWithClose(() => button.props.onClick?.(event));
      } : undefined;
    return cloneElement(button, { ...(cancel ? { disabled: false, onClick: cancel } : {}), form: button.props.form ?? form, type: button.props.type ?? (form ? "submit" : "button") });
  });
}
export function SettingsTaskActions({ children, className = "", form }: { children: ReactNode; className?: string; form?: string }) {
  const task = useContext(SettingsTaskContext);
  if (task?.activeStep !== task?.stepId) return null;
  const content = <div className={`actions ${className}`}>{associateForm(children, form, task)}</div>;
  return task?.actions ? createPortal(content, task.actions) : content;
}
