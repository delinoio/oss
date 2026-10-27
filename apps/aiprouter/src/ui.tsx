// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, getLocale, formatDecimal } from "./i18n";
import {
  cloneElement,
  isValidElement,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type ReactElement,
  type ComponentProps,
} from "react";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { ErrorDetailSchema, type Money } from "@aiprouter/client";
import { ConnectError } from "@connectrpc/connect";
import { errorMessage, useDirtyForm } from "./api";
export function Button({
  variant = "secondary",
  className = "",
  ...props
}: ComponentProps<"button"> & {
  variant?: "primary" | "secondary" | "danger" | "ghost";
}) {
  useLocale();
  return (
    <button
      type="button"
      className={`button ${variant} ${className}`}
      {...props}
    />
  );
}
export function Card({
  children,
  className = "",
  ...props
}: ComponentProps<"section">) {
  useLocale();
  return (
    <section className={`card ${className}`} {...props}>
      {children}
    </section>
  );
}
export function Badge({
  children,
  tone = "neutral",
}: {
  children: ReactNode;
  tone?: "neutral" | "success" | "warning" | "danger" | "info";
}) {
  useLocale();
  return <span className={`badge ${tone}`}>{children}</span>;
}
export function PageHeader({
  title,
  description,
  action,
}: {
  title: string;
  description?: ReactNode;
  action?: ReactNode;
}) {
  useLocale();
  return (
    <header className="page-heading">
      <div>
        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </div>
      {action}
    </header>
  );
}
export function Field({
  label,
  children,
  hint,
}: {
  label: string;
  children: ReactNode;
  hint?: ReactNode;
}) {
  useLocale();
  const id = useId();
  const child = isValidElement(children)
    ? (children as ReactElement<Record<string, unknown>>)
    : undefined;
  const inputId = String(child?.props.id ?? id);
  return (
    <div className="field">
      <label htmlFor={inputId}>{label}</label>
      {child
        ? cloneElement(child, {
            id: inputId,
            "aria-describedby": hint
              ? `${id}-hint`
              : child.props["aria-describedby"],
          })
        : children}
      {hint && <small id={`${id}-hint`}>{hint}</small>}
    </div>
  );
}
export function Notice({
  children,
  tone = "info",
}: {
  children: ReactNode;
  tone?: "info" | "danger" | "warning" | "success";
}) {
  useLocale();
  return (
    <div
      className={`notice ${tone}`}
      role={tone === "danger" ? "alert" : "status"}
    >
      {children}
    </div>
  );
}
export function ErrorNotice({
  error,
  retry,
}: {
  error: unknown;
  retry?: () => void;
}) {
  useLocale();
  if (!error) return null;
  const detail = ConnectError.from(error).findDetails(ErrorDetailSchema)[0];
  return (
    <Notice tone="danger">
      {errorMessage(error)}
      {detail?.requestId && (
        <p>{translate("requestId", { value0: detail.requestId })}</p>
      )}
      {detail?.retryAfterSeconds !== undefined && (
        <p>
          {translate("tryAgainAfterSeconds", {
            value0: detail.retryAfterSeconds,
          })}
        </p>
      )}
      {retry && <Button onClick={retry}>{translate("tryAgain")}</Button>}
    </Notice>
  );
}
export function Loading() {
  useLocale();
  return (
    <div className="loading-state" role="status" aria-busy="true">
      <p>{translate("loading")}</p>
      <div className="skeleton" aria-hidden="true" />
    </div>
  );
}
export function EmptyState({
  title,
  children,
  action,
}: {
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  useLocale();
  return (
    <Card className="empty">
      <h2>{title}</h2>
      {children && <p>{children}</p>}
      {action}
    </Card>
  );
}
export function Dialog({
  title,
  children,
  onClose,
  initialFocus,
  wide = false,
  className = "",
  describedBy,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  initialFocus?: string;
  wide?: boolean;
  className?: string;
  describedBy?: string;
}) {
  useLocale();
  const ref = useRef<HTMLDialogElement>(null);
  const reportDirty = useDirtyForm();
  const reportDirtyRef = useRef(reportDirty);
  reportDirtyRef.current = reportDirty;
  const titleId = useId();
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    const el = ref.current!;
    const previous = document.activeElement as HTMLElement | null;
    el.showModal();
    const target = el.querySelector<HTMLElement>(
      initialFocus ?? "[autofocus],input,select,textarea,[data-initial-focus]",
    );
    target?.focus();
    return () => {
      el.close();
      if (el.querySelector("form")) reportDirtyRef.current(false);
      previous?.focus();
    };
  }, [initialFocus]);
  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      aria-describedby={describedBy}
      className={[wide ? "wide" : "", className].filter(Boolean).join(" ")}
      onKeyDown={(event) => {
        if (event.key !== "Tab") return;
        const items = Array.from(
          event.currentTarget.querySelectorAll<HTMLElement>(
            'button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled),textarea:not(:disabled),summary,[tabindex="0"]',
          ),
        ).filter((item) => !item.hidden && (item.checkVisibility?.() ?? true));
        const first = items[0];
        const last = items.at(-1);
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last?.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first?.focus();
        }
      }}
      onCancel={(e) => {
        e.preventDefault();
        closeRef.current();
      }}
    >
      <div className="dialog-heading">
        <h2 id={titleId}>{title}</h2>
        <Button
          aria-label={translate("closeDialog")}
          variant="ghost"
          onClick={onClose}
        >
          ×
        </Button>
      </div>
      {children}
    </dialog>
  );
}
export function ConfirmDialog({
  title,
  children,
  onClose,
  onConfirm,
  pending = false,
  error,
  confirmLabel = translate("confirm"),
  danger = true,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  onConfirm: () => void;
  pending?: boolean;
  error?: unknown;
  confirmLabel?: string;
  danger?: boolean;
}) {
  useLocale();
  return (
    <Dialog title={title} onClose={onClose} initialFocus="[data-cancel]">
      <p>{children}</p>
      <ErrorNotice error={error} />
      <div className="actions">
        <Button data-cancel onClick={onClose}>
          {translate("cancel")}
        </Button>
        <Button
          variant={danger ? "danger" : "primary"}
          disabled={pending}
          onClick={onConfirm}
        >
          {pending ? translate("message_23e39291d6") : confirmLabel}
        </Button>
      </div>
    </Dialog>
  );
}
export function Pagination({
  next,
  onNext,
  onFirst,
  hasPrevious = false,
  pending = false,
}: {
  next?: string;
  onNext: (token: string) => void;
  onFirst: () => void;
  hasPrevious?: boolean;
  pending?: boolean;
}) {
  useLocale();
  return (
    <div className="actions">
      <Button disabled={!hasPrevious || pending} onClick={onFirst}>
        {translate("firstPage")}
      </Button>
      <Button disabled={!next || pending} onClick={() => next && onNext(next)}>
        {translate("nextPage")}
      </Button>
    </div>
  );
}
export function CopyButton({
  value,
  label = translate("copy"),
  statusId,
  className,
}: {
  value: string;
  label?: string;
  /** Optional id for the status region; the button is described by it. */
  statusId?: string;
  /** Optional extra class for the button. */
  className?: string;
}) {
  useLocale();
  const [message, setMessage] = useState("");
  return (
    <>
      <Button
        className={className}
        aria-describedby={statusId}
        onClick={() => {
          void navigator.clipboard.writeText(value).then(
            () => setMessage(translate("copied")),
            () => setMessage(translate("copyFailedSelectAndCopyTheValue")),
          );
        }}
      >
        {label}
      </Button>
      <span role="status" id={statusId} className="copy-status">
        {message}
      </span>
    </>
  );
}
export function formatDate(value?: Timestamp) {
  return value
    ? new Date(Number(value.seconds) * 1000).toLocaleString(getLocale(), {
        timeZone: "UTC",
        dateStyle: "medium",
        timeStyle: "short",
      }) + " UTC"
    : translate("unknown");
}
export function formatMoney(value?: Money) {
  return value
    ? `${value.currency} ${formatDecimal(value.decimalAmount)}`
    : translate("unknown");
}
export { enumLabel } from "./i18n";
