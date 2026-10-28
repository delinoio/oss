// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale, getLocale } from "../i18n";
import {
  ErrorDetailSchema,
  ErrorReason,
  OrganizationService,
  InvitationService,
} from "@tairouter/client";
import { useInfiniteQuery } from "@connectrpc/connect-query";
import { Code, ConnectError } from "@connectrpc/connect";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import "./account.css";

export function timestampMs(value?: Timestamp): number | undefined {
  return value
    ? Number(value.seconds) * 1000 + value.nanos / 1_000_000
    : undefined;
}

export function dateLabel(value?: Timestamp): string {
  const ms = timestampMs(value);
  return ms === undefined
    ? translate("notAvailable")
    : new Date(ms).toLocaleString(getLocale(), { timeZoneName: "short" });
}

export function useNow() {
  useLocale();
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [setNow]);
  return Math.max(now, Date.now());
}

export function signInHref(
  returnTo = window.location.pathname + window.location.search,
) {
  // Only local routes can be carried through the identity-provider redirect.
  const safe =
    returnTo.startsWith("/") &&
    !returnTo.startsWith("//") &&
    !returnTo.includes("\\")
      ? returnTo
      : "/overview";
  return `/auth/login?return_to=${encodeURIComponent(safe)}`;
}

export function AccountError({
  error,
  retry,
}: {
  error: unknown;
  retry?: () => void;
}) {
  useLocale();
  if (!error) return null;
  const rpc = ConnectError.from(error);
  const detail = rpc.findDetails(ErrorDetailSchema)[0];
  let message = translate("theRequestCouldNotBeCompletedTry");
  if (detail?.reason === ErrorReason.INVITATION_EMAIL_MISMATCH)
    message = translate("message_3596e6bf6e");
  else if (rpc.code === Code.Unauthenticated)
    message = translate("message_38d7e6c12c");
  else if (rpc.code === Code.PermissionDenied)
    message = translate("message_92374e8fd1");
  else if (rpc.code === Code.Unavailable)
    message = translate("message_5eace95909");
  else if (rpc.code === Code.NotFound)
    message = translate("message_34fb745477");
  else if (rpc.code === Code.InvalidArgument)
    message = translate("message_18c27a80d2");
  else if (detail?.reason === ErrorReason.EXPIRED)
    message = translate("message_eaf68610b9");
  else if (detail?.reason === ErrorReason.CANCELED)
    message = translate("message_1ccc7a1400");
  else if (detail?.reason === ErrorReason.OWNED_ORGANIZATIONS_REMAIN)
    message = translate("message_4af227dc42");
  else if (detail?.reason === ErrorReason.CONFLICT)
    message = translate("message_f98ec6e0c8");
  return (
    <div className="account-error" role="alert">
      <p>{message}</p>
      {detail?.requestId && (
        <p>{translate("requestId", { value0: detail.requestId })}</p>
      )}
      {rpc.code === Code.Unauthenticated ||
      detail?.reason === ErrorReason.INVITATION_EMAIL_MISMATCH ? (
        <a href={signInHref()}>{translate("signInAgain")}</a>
      ) : (
        retry && (
          <button type="button" onClick={retry}>
            {translate("tryAgain")}
          </button>
        )
      )}
    </div>
  );
}

export function useOrganizations() {
  useLocale();
  const query = useInfiniteQuery(
    OrganizationService.method.listOrganizations,
    { page: { pageSize: 100 } },
    {
      pageParamKey: "page",
      getNextPageParam: (last) =>
        last.page?.nextPageToken
          ? { pageSize: 100, pageToken: last.page.nextPageToken }
          : undefined,
      retry: false,
    },
  );
  return {
    ...query,
    organizations:
      query.data?.pages.flatMap((page) => page.organizations) ?? [],
  };
}

export function useMyInvitations(enabled = true) {
  useLocale();
  const query = useInfiniteQuery(
    InvitationService.method.listMyInvitations,
    { page: { pageSize: 100 } },
    {
      pageParamKey: "page",
      enabled,
      retry: false,
      getNextPageParam: (last) =>
        last.page?.nextPageToken
          ? { pageSize: 100, pageToken: last.page.nextPageToken }
          : undefined,
    },
  );
  return {
    ...query,
    invitations: query.data?.pages.flatMap((page) => page.invitations) ?? [],
  };
}

export function AccountSurface({
  title,
  subtitle,
  className,
  focusHeading = false,
  children,
}: {
  title: string;
  subtitle?: string;
  /** Optional scoped modifier class appended to the base surface class. */
  className?: string;
  /** Moves programmatic focus to the h1 once on first render. */
  focusHeading?: boolean;
  children: ReactNode;
}) {
  useLocale();
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (focusHeading) heading.current?.focus();
  }, [focusHeading]);
  return (
    <section
      className={className ? `account-surface ${className}` : "account-surface"}
      aria-label={title}
    >
      <header>
        <h1 ref={heading} tabIndex={focusHeading ? -1 : undefined}>
          {title}
        </h1>
        {subtitle && <p>{subtitle}</p>}
      </header>
      {children}
    </section>
  );
}

export function LoadingAccount() {
  useLocale();
  return <p role="status">{translate("loadingYourAccount")}</p>;
}

/** Where AccountDialog renders its close control. */
export enum AccountDialogClosePlacement {
  /** Legacy default: a trailing "Close" button after the children. */
  Footer = "footer",
  /** Top-right icon button in the dialog header. */
  Header = "header",
}

export function AccountDialog({
  title,
  onClose,
  className,
  closePlacement = AccountDialogClosePlacement.Footer,
  children,
}: {
  title: string;
  onClose: () => void;
  /** Optional scoped modifier class appended to the base dialog class. */
  className?: string;
  /** Defaults to the legacy trailing Close button. */
  closePlacement?: AccountDialogClosePlacement;
  children: ReactNode;
}) {
  useLocale();
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  // Remember the opener during the first render, before an autofocused child
  // inside the dialog can take focus, so closing restores focus to the opener.
  const opener = useRef<HTMLElement | null | undefined>(undefined);
  if (opener.current === undefined)
    opener.current = (document.activeElement as HTMLElement | null) ?? null;
  useEffect(() => {
    const trigger = opener.current;
    const dialog = ref.current;
    dialog?.showModal();
    // showModal() focuses the first focusable control (the header Close), so
    // honor an explicit safest-action marker instead.
    dialog?.querySelector<HTMLElement>("[data-dialog-initial-focus]")?.focus();
    return () => {
      dialog?.close();
      trigger?.focus();
    };
  }, []);
  const header = closePlacement === AccountDialogClosePlacement.Header;
  return (
    <dialog
      className={className ? `account-dialog ${className}` : "account-dialog"}
      ref={ref}
      aria-labelledby={titleId}
      onKeyDown={(event) => {
        if (event.key !== "Tab") return;
        const elements = Array.from(
          ref.current?.querySelectorAll<HTMLElement>(
            'button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled),textarea:not(:disabled),[tabindex="0"]',
          ) ?? [],
        );
        const first = elements[0];
        const last = elements.at(-1);
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last?.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first?.focus();
        }
      }}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      {header ? (
        <header className="account-dialog-header">
          <h2 id={titleId}>{title}</h2>
          <button
            type="button"
            className="account-dialog-close"
            onClick={onClose}
            aria-label={translate("close_312254", { value0: title })}
          >
            <svg
              aria-hidden="true"
              focusable="false"
              viewBox="0 0 20 20"
              width="20"
              height="20"
            >
              <path
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                d="M4 4l12 12M16 4 4 16"
              />
            </svg>
          </button>
        </header>
      ) : (
        <h2 id={titleId}>{title}</h2>
      )}
      {children}
      {!header && (
        <button
          type="button"
          onClick={onClose}
          aria-label={translate("close_312254", { value0: title })}
        >
          {translate("close")}
        </button>
      )}
    </dialog>
  );
}
