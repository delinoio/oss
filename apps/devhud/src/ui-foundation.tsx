import { useEffect, useId, useRef, useState, type ButtonHTMLAttributes, type HTMLAttributes, type ReactNode, type Ref, type RefObject } from "react";
import { ErrorIcon, InfoIcon, SuccessIcon, WarningIcon } from "./ui-icons";

export const ShellLayout = { Sidebar: "sidebar", Rail: "rail", Mobile: "mobile" } as const;
export type ShellLayout = (typeof ShellLayout)[keyof typeof ShellLayout];

export function resolveShellLayout(width: number): ShellLayout {
  if (width <= 700) return ShellLayout.Mobile;
  if (width <= 1023) return ShellLayout.Rail;
  return ShellLayout.Sidebar;
}

export function useShellLayout(): ShellLayout {
  const [layout, setLayout] = useState(() => resolveShellLayout(window.innerWidth));
  useEffect(() => {
    const resize = () => setLayout(resolveShellLayout(window.innerWidth));
    addEventListener("resize", resize);
    return () => removeEventListener("resize", resize);
  }, []);
  return layout;
}

export function AppShell({ layout, skipLabel, navigation, topBar, bottomBar, children, className, ...props }: HTMLAttributes<HTMLDivElement> & { readonly layout: ShellLayout; readonly skipLabel: string; readonly navigation?: ReactNode; readonly topBar?: ReactNode; readonly bottomBar?: ReactNode }) {
  const main = useRef<HTMLElement>(null);
  const skip = (event: React.MouseEvent<HTMLAnchorElement>) => {
    event.preventDefault();
    main.current?.focus();
  };
  return <div className={`app-shell${className ? ` ${className}` : ""}`} data-shell-layout={layout} {...props}>
    <a className="skip-link" href="#devhud-main-content" onClick={skip}>{skipLabel}</a>
    {navigation}
    {topBar}
    <main ref={main} id="devhud-main-content" className="content" tabIndex={-1} aria-live="polite">{children}</main>
    {bottomBar}
  </div>;
}

export function PageHeader({ eyebrow, title, summary, level = 2, actions }: { readonly eyebrow?: ReactNode; readonly title: ReactNode; readonly summary?: ReactNode; readonly level?: 1 | 2; readonly actions?: ReactNode }) {
  const Heading = level === 1 ? "h1" : "h2";
  return <header className="page-header">
    <div>{eyebrow && <p className="eyebrow">{eyebrow}</p>}<Heading>{title}</Heading>{summary && <p>{summary}</p>}</div>
    {actions && <div className="page-header-actions">{actions}</div>}
  </header>;
}

export function Card({ children, interactive = false, className, ...props }: HTMLAttributes<HTMLElement> & { readonly children: ReactNode; readonly interactive?: boolean }) {
  return <section className={`ui-card${interactive ? " ui-card-interactive" : ""}${className ? ` ${className}` : ""}`} {...props}>{children}</section>;
}

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";
export function Button({ variant = "secondary", icon, children, className, ref, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { readonly variant?: ButtonVariant; readonly icon?: ReactNode; readonly ref?: Ref<HTMLButtonElement> }) {
  return <button ref={ref} className={`ui-button ui-button-${variant}${className ? ` ${className}` : ""}`} {...props}>{icon}{children && <span>{children}</span>}</button>;
}

export function Field({ label, inputId, hint, error, children }: { readonly label: ReactNode; readonly inputId: string; readonly hint?: ReactNode; readonly error?: ReactNode; readonly children: ReactNode }) {
  const hintId = `${inputId}-hint`;
  const errorId = `${inputId}-error`;
  return <div className="ui-field">
    <label htmlFor={inputId}>{label}</label>
    {children}
    {hint && <p id={hintId} className="ui-field-hint">{hint}</p>}
    {error && <p id={errorId} className="ui-field-error" role="alert">{error}</p>}
  </div>;
}

export type StatusTone = "neutral" | "info" | "success" | "warning" | "danger";
const statusIcons = { neutral: InfoIcon, info: InfoIcon, success: SuccessIcon, warning: WarningIcon, danger: ErrorIcon } as const;
export function StatusBadge({ tone = "neutral", children }: { readonly tone?: StatusTone; readonly children: ReactNode }) {
  const StatusIcon = statusIcons[tone];
  return <span className={`status-badge status-badge-${tone}`}><StatusIcon /><span>{children}</span></span>;
}

export function StatePanel({ eyebrow, title, summary, role = "status", tone = "info", details, actions, progress = false }: { readonly eyebrow: ReactNode; readonly title: ReactNode; readonly summary: ReactNode; readonly role?: "status" | "alert"; readonly tone?: StatusTone; readonly details?: ReactNode; readonly actions?: ReactNode; readonly progress?: boolean }) {
  const titleId = useId();
  return <section className="state-panel" role={role} aria-labelledby={titleId}>
    <StatusBadge tone={tone}>{eyebrow}</StatusBadge>
    <h2 id={titleId} tabIndex={-1}>{title}</h2>
    <p>{summary}</p>
    {details}
    {actions && <div className="state-panel-actions">{actions}</div>}
    {progress && <span className="progress" aria-hidden="true" />}
  </section>;
}

export function DataRow({ icon, title, description, trailing, onClick, ariaCurrent }: { readonly icon?: ReactNode; readonly title: ReactNode; readonly description?: ReactNode; readonly trailing?: ReactNode; readonly onClick?: () => void; readonly ariaCurrent?: "page" }) {
  const content = <><span className="data-row-icon">{icon}</span><span className="data-row-content"><strong>{title}</strong>{description && <span>{description}</span>}</span>{trailing && <span className="data-row-trailing">{trailing}</span>}</>;
  if (onClick) return <button type="button" className="data-row" onClick={onClick} aria-current={ariaCurrent}>{content}</button>;
  return <div className="data-row">{content}</div>;
}

const focusableSelector = "button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex='-1'])";

function ModalSurface({ open, title, titleId, className, initialFocusRef, returnFocusRef, restoreFocus, onClose, children }: { readonly open: boolean; readonly title: ReactNode; readonly titleId: string; readonly className: string; readonly initialFocusRef?: RefObject<HTMLElement | null>; readonly returnFocusRef?: RefObject<HTMLElement | null>; readonly restoreFocus: boolean; readonly onClose: () => void; readonly children: ReactNode }) {
  const surface = useRef<HTMLElement>(null);
  const capturedOpener = useRef<HTMLElement | null>(null);
  const shouldRestore = useRef(restoreFocus);
  shouldRestore.current = restoreFocus;
  useEffect(() => {
    if (!open) return;
    capturedOpener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const animation = requestAnimationFrame(() => {
      const target = initialFocusRef?.current ?? surface.current?.querySelector<HTMLElement>(focusableSelector) ?? surface.current;
      target?.focus();
    });
    return () => {
      cancelAnimationFrame(animation);
      if (shouldRestore.current) requestAnimationFrame(() => (returnFocusRef?.current ?? capturedOpener.current)?.focus());
    };
  }, [open]);
  if (!open) return null;
  const keyDown = (event: React.KeyboardEvent<HTMLElement>) => {
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); onClose(); return; }
    if (event.key !== "Tab") return;
    const focusable = surface.current?.querySelectorAll<HTMLElement>(focusableSelector);
    if (!focusable?.length) { event.preventDefault(); surface.current?.focus(); return; }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };
  return <div className="ui-overlay" role="presentation"><section ref={surface} className={className} role="dialog" aria-modal="true" aria-labelledby={titleId} tabIndex={-1} onKeyDown={keyDown}><h2 id={titleId}>{title}</h2>{children}</section></div>;
}

export function Dialog({ open, title, initialFocusRef, returnFocusRef, restoreFocus = true, onClose, children }: { readonly open: boolean; readonly title: ReactNode; readonly initialFocusRef?: RefObject<HTMLElement | null>; readonly returnFocusRef?: RefObject<HTMLElement | null>; readonly restoreFocus?: boolean; readonly onClose: () => void; readonly children: ReactNode }) {
  const titleId = useId();
  return <ModalSurface open={open} title={title} titleId={titleId} className="ui-dialog" initialFocusRef={initialFocusRef} returnFocusRef={returnFocusRef} restoreFocus={restoreFocus} onClose={onClose}>{children}</ModalSurface>;
}

export function Sheet({ open, title, backLabel, initialFocusRef, returnFocusRef, restoreFocus = true, onClose, children }: { readonly open: boolean; readonly title: ReactNode; readonly backLabel: string; readonly initialFocusRef?: RefObject<HTMLElement | null>; readonly returnFocusRef?: RefObject<HTMLElement | null>; readonly restoreFocus?: boolean; readonly onClose: () => void; readonly children: ReactNode }) {
  const titleId = useId();
  return <ModalSurface open={open} title={title} titleId={titleId} className="ui-sheet" initialFocusRef={initialFocusRef} returnFocusRef={returnFocusRef} restoreFocus={restoreFocus} onClose={onClose}><div className="sheet-content">{children}</div><Button variant="ghost" onClick={onClose}>{backLabel}</Button></ModalSurface>;
}
