// SPDX-License-Identifier: Apache-2.0
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
  if (!error) return null;
  const detail = ConnectError.from(error).findDetails(ErrorDetailSchema)[0];
  return (
    <Notice tone="danger">
      {errorMessage(error)}
      {detail?.requestId && <p>Request ID: {detail.requestId}</p>}
      {detail?.retryAfterSeconds !== undefined && (
        <p>Try again after {detail.retryAfterSeconds} seconds.</p>
      )}
      {retry && <Button onClick={retry}>Try again</Button>}
    </Notice>
  );
}
export function Loading() {
  return (
    <div className="loading-state" role="status" aria-busy="true">
      <p>Loading…</p>
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
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  initialFocus?: string;
  wide?: boolean;
}) {
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
      className={wide ? "wide" : ""}
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
        <Button aria-label="Close dialog" variant="ghost" onClick={onClose}>
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
  confirmLabel = "Confirm",
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
  return (
    <Dialog title={title} onClose={onClose} initialFocus="[data-cancel]">
      <p>{children}</p>
      <ErrorNotice error={error} />
      <div className="actions">
        <Button data-cancel onClick={onClose}>
          Cancel
        </Button>
        <Button
          variant={danger ? "danger" : "primary"}
          disabled={pending}
          onClick={onConfirm}
        >
          {pending ? "Saving…" : confirmLabel}
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
  return (
    <div className="actions">
      <Button disabled={!hasPrevious || pending} onClick={onFirst}>
        First page
      </Button>
      <Button disabled={!next || pending} onClick={() => next && onNext(next)}>
        Next page
      </Button>
    </div>
  );
}
export function CopyButton({
  value,
  label = "Copy",
}: {
  value: string;
  label?: string;
}) {
  const [message, setMessage] = useState("");
  return (
    <>
      <Button
        onClick={() => {
          void navigator.clipboard.writeText(value).then(
            () => setMessage("Copied."),
            () =>
              setMessage("Copy failed. Select and copy the value manually."),
          );
        }}
      >
        {label}
      </Button>
      <span role="status">{message}</span>
    </>
  );
}
export function formatDate(value?: Timestamp) {
  return value
    ? new Date(Number(value.seconds) * 1000).toLocaleString("en-US", {
        timeZone: "UTC",
        dateStyle: "medium",
        timeStyle: "short",
      }) + " UTC"
    : "Unknown";
}
export function formatMoney(value?: Money) {
  return value ? `${value.currency} ${value.decimalAmount}` : "Unknown";
}
export function enumLabel(value: string | undefined) {
  return value
    ? value
        .toLowerCase()
        .replaceAll("_", " ")
        .replace(/^./, (c) => c.toUpperCase())
    : "Unknown";
}
