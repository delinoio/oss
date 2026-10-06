import { LocalizedText, copy, useLocale, type MessageKey } from "./localization";
import { useEffect, useId, useRef, type ReactNode } from "react";
import { clientFailure, FailureCode, type ClientFailure } from "@delinoio/delidev-api-client";

export function Problem({ error }: { error: unknown }) {
  useLocale();
  if (!error) return null;
  const failure = clientFailure(error);
  return <Failure failure={failure} />;
}
export function Failure({ failure }: { failure?: ClientFailure }) {
  useLocale();
  if (!failure) return null;
  return <div role="alert" className="problem"><strong>{copy("ui.requestFailed")}</strong><p>{copy(failureGuidance[failure.code])}</p><details><summary>{copy("ui.technicalDetails")}</summary><strong>{failure.message}</strong><p>{failure.guidance}</p><code>{failure.code}</code></details>{failure.correlationId ? <small><LocalizedText id="ui.reference_0eac07" components={{ s0: <>{failure.correlationId}</> }} /></small> : null}</div>;
}
export function ServiceProblem({ code, children }: { code?: string; children: ReactNode }) {
  useLocale();
  const normalized = code?.replaceAll("-", "_");
  const key = normalized && Object.hasOwn(failureGuidance, normalized) ? failureGuidance[normalized as FailureCode] : "ui.failure.internal";
  return <div className="problem" role="alert"><p>{copy(key)}</p><details><summary>{copy("ui.technicalDetails")}</summary>{children}</details></div>;
}
export function Modal({ title, close, children, visible = true }: { title: string; close: () => void; children: ReactNode; visible?: boolean }) {
  useLocale();
  const id = useId();
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    if (!visible) return;
    const opener = document.activeElement as HTMLElement | null;
    const dialog = ref.current!;
    dialog.showModal();
    return () => { dialog.close(); if (opener?.isConnected) opener.focus(); };
  }, [visible]);
  return <dialog ref={ref} aria-labelledby={id} onCancel={(event) => { event.preventDefault(); close(); }}>
    <header><h2 id={id}>{title}</h2><button onClick={close} aria-label={copy("ui.close_0fbe2a", { v0: title })}>{copy("ui.close_7d9eb7")}</button></header>
    {children}
  </dialog>;
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
