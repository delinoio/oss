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
  type Ref,
  type ReactElement,
  type ComponentProps,
} from "react";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { ErrorDetailSchema, type Money } from "@tairouter/client";
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
  headingRef,
}: {
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  // Optional ref that makes the h1 a programmatic focus target.
  headingRef?: Ref<HTMLHeadingElement>;
}) {
  useLocale();
  return (
    <header className="page-heading">
      <div>
        <h1 ref={headingRef} tabIndex={headingRef ? -1 : undefined}>
          {title}
        </h1>
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
  error,
}: {
  label: string;
  children: ReactNode;
  hint?: ReactNode;
  /** Field-associated error text; marks the control invalid and describes it. */
  error?: ReactNode;
}) {
  useLocale();
  const id = useId();
  const child = isValidElement(children)
    ? (children as ReactElement<Record<string, unknown>>)
    : undefined;
  const inputId = String(child?.props.id ?? id);
  const describedBy =
    [
      child?.props["aria-describedby"],
      hint ? `${id}-hint` : undefined,
      error ? `${id}-error` : undefined,
    ]
      .filter(Boolean)
      .join(" ") || undefined;
  return (
    <div className="field">
      <label htmlFor={inputId}>{label}</label>
      {child
        ? cloneElement(child, {
            id: inputId,
            "aria-describedby": describedBy,
            ...(error ? { "aria-invalid": true } : {}),
          })
        : children}
      {hint && <small id={`${id}-hint`}>{hint}</small>}
      {error && (
        <small id={`${id}-error`} className="field-error">
          {error}
        </small>
      )}
    </div>
  );
}
export type ErrorSummaryItem = {
  /** Stable key for the item. */
  key: string;
  /** Message explaining why the form cannot be saved. */
  message: string;
  /** DOM id of the control that owns the error; the item links to it. */
  fieldId?: string;
};
/**
 * Focuses a form control by id. Collapsed ancestor disclosures are opened first
 * because a control inside a closed details element cannot take focus.
 */
function focusField(fieldId: string) {
  const target = document.getElementById(fieldId);
  for (let node = target?.parentElement; node; node = node.parentElement) {
    if (node instanceof HTMLDetailsElement) node.open = true;
  }
  target?.focus();
}
export function ErrorSummary({
  errors,
  title,
}: {
  errors: ErrorSummaryItem[];
  title?: string;
}) {
  useLocale();
  const titleId = useId();
  if (errors.length === 0) return null;
  return (
    <div
      className="error-summary"
      role="alert"
      aria-labelledby={titleId}
      data-testid="error-summary"
    >
      <strong id={titleId}>
        {title ?? translate("fixTheFollowingBeforeSaving")}
      </strong>
      <ul>
        {errors.map((item) => (
          <li key={item.key}>
            {item.fieldId ? (
              <a
                href={`#${item.fieldId}`}
                onClick={(event) => {
                  event.preventDefault();
                  focusField(item.fieldId!);
                }}
              >
                {item.message}
              </a>
            ) : (
              item.message
            )}
          </li>
        ))}
      </ul>
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
export function Loading({ label }: { label?: string } = {}) {
  useLocale();
  return (
    <div className="loading-state" role="status" aria-busy="true">
      <p>{label ?? translate("loading")}</p>
      <div className="skeleton" aria-hidden="true" />
    </div>
  );
}
/**
 * Labels already-visible data that is being refetched or whose refresh failed.
 * Renders nothing when neither state applies.
 */
export function RefreshStatus({
  updating = false,
  failed = false,
}: {
  updating?: boolean;
  failed?: boolean;
}) {
  useLocale();
  if (failed)
    return (
      <p className="refresh-status failed" role="status">
        {translate("couldNotRefreshShowingPreviouslyLoadedData")}
      </p>
    );
  if (updating)
    return (
      <p className="refresh-status" role="status">
        {translate("updating")}
      </p>
    );
  return null;
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
export enum DialogSize {
  Default = "default",
  /** Up to 720px. */
  Wide = "wide",
  /** Up to 880px, used by the large editor dialogs. */
  Large = "large",
  /** Up to 760px, used by the pool editor dialog. */
  Editor = "editor",
}
export function Dialog({
  title,
  subtitle,
  children,
  onClose,
  initialFocus,
  wide = false,
  size,
  focusHeading = false,
  className = "",
  describedBy,
}: {
  title: string;
  /** Optional scope line rendered under the heading and used as the dialog description. */
  subtitle?: ReactNode;
  children: ReactNode;
  onClose: () => void;
  initialFocus?: string;
  /** Legacy shorthand for DialogSize.Wide. */
  wide?: boolean;
  size?: DialogSize;
  /** Focus the dialog heading on open instead of the first form control. */
  focusHeading?: boolean;
  className?: string;
  describedBy?: string;
}) {
  useLocale();
  const resolvedSize = size ?? (wide ? DialogSize.Wide : DialogSize.Default);
  const subtitleId = useId();
  const ref = useRef<HTMLDialogElement>(null);
  const reportDirty = useDirtyForm();
  const reportDirtyRef = useRef(reportDirty);
  reportDirtyRef.current = reportDirty;
  const titleId = useId();
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  const focusSelector = focusHeading ? "[data-dialog-heading]" : initialFocus;
  useEffect(() => {
    const el = ref.current!;
    const previous = document.activeElement as HTMLElement | null;
    el.showModal();
    const target = el.querySelector<HTMLElement>(
      focusSelector ?? "[autofocus],input,select,textarea,[data-initial-focus]",
    );
    target?.focus();
    return () => {
      el.close();
      if (el.querySelector("form")) reportDirtyRef.current(false);
      previous?.focus();
    };
  }, [focusSelector]);
  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      aria-describedby={
        [describedBy, subtitle ? subtitleId : undefined]
          .filter(Boolean)
          .join(" ") || undefined
      }
      className={[
        resolvedSize === DialogSize.Default ? "" : resolvedSize,
        className,
      ]
        .filter(Boolean)
        .join(" ")}
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
        <div className="dialog-heading-text">
          <h2
            id={titleId}
            {...(focusHeading
              ? { tabIndex: -1, "data-dialog-heading": "" }
              : {})}
          >
            {title}
          </h2>
          {subtitle && (
            <p id={subtitleId} className="dialog-subtitle">
              {subtitle}
            </p>
          )}
        </div>
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
  label,
}: {
  next?: string;
  onNext: (token: string) => void;
  onFirst: () => void;
  hasPrevious?: boolean;
  pending?: boolean;
  /** Accessible group name; adds role="group" when provided. */
  label?: string;
}) {
  useLocale();
  return (
    <div
      className="actions"
      {...(label ? { role: "group", "aria-label": label } : {})}
    >
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
  variant = "secondary",
  statusId,
  className,
}: {
  value: string;
  label?: string;
  variant?: ComponentProps<typeof Button>["variant"];
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
        variant={variant}
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
      <span
        role="status"
        aria-live="polite"
        id={statusId}
        className="copy-status"
      >
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
