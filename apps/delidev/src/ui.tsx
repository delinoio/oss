import { LocalizedText, copy, useLocale, type MessageKey } from "./localization";
import { useLayoutEffect, useId, useRef, type ReactNode, type RefObject, type ComponentPropsWithRef } from "react";
import { clientFailure, FailureCode, type ClientFailure } from "@delinoio/delidev-api-client";

export interface InlineProblemPresentation { summary?: ReactNode; actions?: ReactNode }

/** Presentation only. The owning workflow supplies original actions and guards. */
export function InlineRemediation({ summary, actions, details }: { summary: ReactNode; actions?: ReactNode; details?: ReactNode }) {
  useLocale();
  return <div role="alert" className="problem"><div>{summary}</div>{actions ? <div className="actions">{actions}</div> : null}{details ? <details><summary>{copy("ui.technicalDetails")}</summary>{details}</details> : null}</div>;
}

export function Problem({ error, ...presentation }: { error: unknown } & InlineProblemPresentation) {
  useLocale();
  if (!error) return null;
  const failure = clientFailure(error);
  return <Failure failure={failure} {...presentation} />;
}
export function Failure({ failure, summary, actions }: { failure?: ClientFailure } & InlineProblemPresentation) {
  useLocale();
  if (!failure) return null;
  return <div role="alert" className="problem"><strong>{copy("ui.requestFailed")}</strong><p>{copy(failureGuidance[failure.code])}</p>{summary ? <div>{summary}</div> : null}{actions ? <div className="actions">{actions}</div> : null}<details><summary>{copy("ui.technicalDetails")}</summary><strong>{failure.message}</strong><p>{failure.guidance}</p><code>{failure.code}</code></details>{failure.correlationId ? <small><LocalizedText id="ui.reference_0eac07" components={{ s0: <>{failure.correlationId}</> }} /></small> : null}</div>;
}
export function ServiceProblem({ code, children, summary, actions }: { code?: string; children: ReactNode } & InlineProblemPresentation) {
  useLocale();
  const normalized = code?.replaceAll("-", "_");
  const key = normalized && Object.hasOwn(failureGuidance, normalized) ? failureGuidance[normalized as FailureCode] : "ui.failure.internal";
  return <div className="problem" role="alert"><p>{copy(key)}</p>{summary ? <div>{summary}</div> : null}{actions ? <div className="actions">{actions}</div> : null}<details><summary>{copy("ui.technicalDetails")}</summary>{children}</details></div>;
}
/** Localized summary only; callers retain the original evidence separately. */
export function failureSummary(code?: string): string {
  const normalized = code?.replaceAll("-", "_");
  return copy(normalized && Object.hasOwn(failureGuidance, normalized) ? failureGuidance[normalized as FailureCode] : "ui.failure.internal");
}
// Shared native surface; presentation owners control opening and focus lifetime.
export function DialogSurface(props: ComponentPropsWithRef<"dialog">) {
  return <dialog {...props} onCancel={event => { event.preventDefault(); props.onCancel?.(event); }}>{props.children}</dialog>;
}
export function Modal({ title, close, children, visible = true, className, initialFocus, focusClose = false, trapFocus = false }: { title: string; close: () => void; children: ReactNode; visible?: boolean; className?: string; initialFocus?: RefObject<HTMLElement | null>; focusClose?: boolean; trapFocus?: boolean }) {
  useLocale();
  const id = useId();
  const ref = useRef<HTMLDialogElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  useLayoutEffect(() => {
    if (!visible) return;
    const opener = document.activeElement as HTMLElement | null;
    const dialog = ref.current!;
    dialog.showModal();
    (focusClose ? closeButton.current : initialFocus?.current)?.focus();
    return () => { dialog.close(); if (opener?.isConnected) opener.focus(); };
  }, [visible, initialFocus, focusClose]);
  return <DialogSurface ref={ref} className={className} aria-labelledby={id} onCancel={(event) => { event.preventDefault(); close(); }} onKeyDown={event => {
    if (!trapFocus || event.key !== "Tab") return;
    // Some desktop browser hosts include their chrome in the native modal's
    // Tab cycle. This opt-in boundary keeps repository actions in the dialog.
    const controls = [...event.currentTarget.querySelectorAll<HTMLElement>("button,input,select,textarea,a[href],[tabindex]")].filter(node => node.tabIndex >= 0 && !node.matches(":disabled") && node.getClientRects().length > 0 && !node.closest("[hidden],[inert]"));
    const first = controls[0], last = controls.at(-1);
    if (!first || !last) return;
    const focus = document.activeElement;
    if (!event.currentTarget.contains(focus) || (event.shiftKey ? focus === first : focus === last)) { event.preventDefault(); (event.shiftKey ? last : first).focus(); }
  }}>
    <header><h2 id={id}>{title}</h2><button ref={closeButton} type="button" onClick={close} aria-label={copy("ui.close_0fbe2a", { v0: title })}>{copy("ui.close_7d9eb7")}</button></header>
    {children}
  </DialogSurface>;
}
export function More({ available, busy, load }: { available: boolean; busy: boolean; load: () => void }) {
  useLocale();
  return available ? <button disabled={busy} onClick={load}>{busy ? copy("ui.loading_ba3bbb") : copy("ui.loadMore_ac8991")}</button> : null;
}

const failureGuidance: Record<FailureCode, MessageKey> = {
  [FailureCode.InvalidArgument]: "ui.failure.invalid_argument",
  [FailureCode.NotFound]: "ui.failure.not_found",
  [FailureCode.Conflict]: "ui.failure.conflict",
  [FailureCode.Unauthenticated]: "ui.failure.unauthenticated",
  [FailureCode.PermissionDenied]: "ui.failure.permission_denied",
  [FailureCode.Unavailable]: "ui.failure.unavailable",
  [FailureCode.ServerUnavailable]: "ui.failure.server_unavailable",
  [FailureCode.ConfirmationRequired]: "ui.failure.confirmation_required",
  [FailureCode.MissingInput]: "ui.failure.missing_input",
  [FailureCode.Unsupported]: "ui.failure.unsupported",
  [FailureCode.RecoveryRequired]: "ui.failure.recovery_required",
  [FailureCode.BudgetReached]: "ui.failure.budget_reached",
  [FailureCode.ResourceExhausted]: "ui.failure.resource_exhausted",
  [FailureCode.CursorExpired]: "ui.failure.cursor_expired",
  [FailureCode.ProviderDisabled]: "ui.failure.provider_disabled",
  [FailureCode.Canceled]: "ui.failure.canceled",
  [FailureCode.Internal]: "ui.failure.internal",
};
