// SPDX-License-Identifier: Apache-2.0
import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  FailureCode,
  FailureCause,
  failureCause,
} from "@delinoio/delidev-api-client";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { settleEnclosingEntrances } from "./entrance-motion";
import {
  copy,
  i18n,
  SupportedLanguage,
  useLocale,
  type MessageKey,
} from "./localization";

export enum SessionIconKind {
  Diff = "diff",
  Files = "files",
  Terminals = "terminals",
  Browser = "browser",
  Sidechat = "sidechat",
  Tools = "tools",
  Diagnostics = "diagnostics",
  Conversation = "conversation",
  Warning = "warning",
}

const paths: Record<SessionIconKind, ReactNode> = {
  [SessionIconKind.Sidechat]: <path d="M4 4h16v12H9l-5 4V4Z" />,
  [SessionIconKind.Tools]: (
    <path d="m14 6 4 4 3-3a6 6 0 0 1-8 8l-6 6a3 3 0 0 1-4-4l6-6a6 6 0 0 1 8-8z" />
  ),
  [SessionIconKind.Diff]: (
    <>
      <path d="M5 3h8l4 4v14H5zM13 3v5h4M8 12h6M11 9v6M8 18h6" />
    </>
  ),
  [SessionIconKind.Files]: (
    <>
      <path d="M5 3h8l4 4v14H5zM13 3v5h4" />
    </>
  ),
  [SessionIconKind.Terminals]: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="m7 9 3 3-3 3M13 16h4" />
    </>
  ),
  [SessionIconKind.Browser]: (
    <>
      <circle cx="12" cy="12" r="9" />
      <ellipse cx="12" cy="12" rx="4" ry="9" />
      <path d="M3 12h18M5 7h14M5 17h14" />
    </>
  ),
  [SessionIconKind.Diagnostics]: <path d="M2 12h5l3-8 4 16 3-8h5" />,
  [SessionIconKind.Conversation]: (
    <path d="M21 11a9 9 0 0 1-9 9H7l-5 3 2-6a9 9 0 1 1 17-6ZM8 11h.01M12 11h.01M16 11h.01" />
  ),
  [SessionIconKind.Warning]: (
    <>
      <path d="m12 3 10 18H2zM12 9v5M12 17v1" />
    </>
  ),
};

export function SessionIcon({ kind }: { kind: SessionIconKind }) {
  return (
    <svg
      className="session-icon"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {paths[kind]}
    </svg>
  );
}

export enum SessionNoticeSeverity {
  Warning = "warning",
  Error = "error",
}

/** True when the text equals this catalog entry in any supported locale, independent of the active one. */
function matchesCatalogCopy(value: string, key: MessageKey): boolean {
  return Object.values(SupportedLanguage).some(
    (lng) => i18n.t(key, { lng }) === value,
  );
}

/**
 * Localized recovery-card heading for a typed failure. Classification uses the
 * closed code/cause values only, never message text. `fallbackTitle` applies to
 * codes without a dedicated heading.
 */
function sessionProblemTitle(
  code: unknown,
  cause?: unknown,
  fallbackTitle?: string,
): string {
  if (failureCause(code, cause) === FailureCause.QuotaExhausted)
    return copy("session.quotaTitle");
  switch (code) {
    case FailureCode.Unauthenticated:
      return copy("session.authenticationTitle");
    case FailureCode.RecoveryRequired:
      return copy("session.recoveryTitle");
    case FailureCode.BudgetReached:
      return copy("session.budgetTitle");
    case FailureCode.ServerUnavailable:
    case FailureCode.Unavailable:
      return copy("session.connectionTitle");
    default:
      return fallbackTitle ?? copy("session.attentionRequired");
  }
}

/**
 * Localized heading for the live-stream failure card. Only the typed
 * `unauthenticated` code has a dedicated heading; every other code, including
 * recovery, budget and quota classifications, keeps the connection heading.
 */
export function sessionStreamFailureTitle(code: unknown): string {
  return code === FailureCode.Unauthenticated
    ? copy("session.authenticationTitle")
    : copy("session.connectionTitle");
}

/** Presentation only: callers retain actions, confirmations and request identity. */
export function SessionProblemNotice({
  code,
  cause,
  message,
  guidance,
  correlationId,
  details,
  fallbackTitle,
  children,
}: {
  code: unknown;
  fallbackTitle?: string;
  children?: ReactNode;
  cause?: unknown;
  message: string;
  guidance?: string;
  correlationId?: string;
  details?: (opener: HTMLButtonElement) => void;
}) {
  useLocale();
  const quota = failureCause(code, cause) === FailureCause.QuotaExhausted;
  const title = sessionProblemTitle(code, cause, fallbackTitle);
  return (
    <SessionNotice title={title} details={details}>
      {quota ? (
        <>
          <p>{copy("session.quotaExplanation")}</p>
          <p>{copy("session.quotaGuidance")}</p>
          {/* Original safe evidence stays exact and visible after the localized catalog copy.
              Catalog-identical originals are skipped in every supported locale so the
              paragraph count never depends on the active display language. */}
          {message &&
          !matchesCatalogCopy(message, "session.quotaExplanation") ? (
            <p>{message}</p>
          ) : null}
          {guidance &&
          !matchesCatalogCopy(guidance, "session.quotaGuidance") ? (
            <p>{guidance}</p>
          ) : null}
        </>
      ) : (
        <>
          <p>{message}</p>
          {guidance ? <p>{guidance}</p> : null}
        </>
      )}
      {children}
      <Disclosure>
        <DisclosureSummary>{copy("ui.technicalDetails")}</DisclosureSummary>
        <dl>
          {typeof code === "string" ? (
            <>
              <dt>{copy("session.startupCode")}</dt>
              <dd>{code}</dd>
            </>
          ) : null}
          {typeof cause === "string" && cause ? (
            <>
              <dt>{copy("session.problemCause")}</dt>
              <dd>{cause}</dd>
            </>
          ) : null}
          {correlationId ? (
            <>
              <dt>{copy("session.startupReference")}</dt>
              <dd>{correlationId}</dd>
            </>
          ) : null}
        </dl>
      </Disclosure>
    </SessionNotice>
  );
}

export function SessionNotice({
  children,
  details,
  title,
  severity = SessionNoticeSeverity.Warning,
}: {
  children: ReactNode;
  title?: string;
  severity?: SessionNoticeSeverity;
  details?: (opener: HTMLButtonElement) => void;
}) {
  useLocale();
  return (
    <div className="session-notice" data-severity={severity} role="alert">
      <SessionIcon kind={SessionIconKind.Warning} />
      <div>
        {title ? (
          <strong className="session-notice-title">{title}</strong>
        ) : null}
        {children}
      </div>
      {details ? (
        <button type="button" onClick={(event) => details(event.currentTarget)}>
          {copy("session.showDetails")}
        </button>
      ) : null}
    </div>
  );
}

/** The action children stay mounted when the popup closes. */
export function SessionActions({ children }: { children: ReactNode }) {
  useLocale();
  const id = useId();
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const body = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (!open) return;
    // An animated ancestor is a stacking context that would cap this popup's
    // z-index beneath later siblings, so the opening settles any in-flight
    // entrance in the same commit. Focus never waits for the motion.
    settleEnclosingEntrances(root.current);
    body.current
      ?.querySelector<HTMLButtonElement>("button:not(:disabled)")
      ?.focus();
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      if (event.target instanceof Node && !root.current?.contains(event.target))
        setOpen(false);
    };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [open]);
  return (
    <div
      className="session-actions-popup"
      ref={root}
      onKeyDown={(event) => {
        if (event.key === "Escape" && open) {
          event.stopPropagation();
          setOpen(false);
          trigger.current?.focus();
        }
      }}
    >
      <button
        ref={trigger}
        type="button"
        aria-label={copy("session.moreActions")}
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen((value) => !value)}
      >
        ···
      </button>
      <div
        ref={body}
        id={id}
        className="session-actions-list"
        role="group"
        aria-label={copy("session.moreActions")}
        hidden={!open}
        onClick={(event) => {
          if (
            !(event.target instanceof Element) ||
            !event.target.closest("button:not(:disabled)")
          )
            return;
          // Child dialogs capture this visible opener after the popup is hidden.
          trigger.current?.focus();
          setOpen(false);
        }}
      >
        {children}
      </div>
    </div>
  );
}
