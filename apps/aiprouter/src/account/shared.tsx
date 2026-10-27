// SPDX-License-Identifier: Apache-2.0
import {
  ErrorDetailSchema,
  ErrorReason,
  OrganizationService,
  InvitationService,
} from "@aiprouter/client";
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
    ? "Not available"
    : new Date(ms).toLocaleString("en", { timeZoneName: "short" });
}

export function useNow() {
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
  if (!error) return null;
  const rpc = ConnectError.from(error);
  const detail = rpc.findDetails(ErrorDetailSchema)[0];
  let message = "The request could not be completed. Try again.";
  if (detail?.reason === ErrorReason.INVITATION_EMAIL_MISMATCH)
    message = "Sign in with the invited email address.";
  else if (rpc.code === Code.Unauthenticated)
    message =
      "Your session has expired. Sign in again, then review and submit the action.";
  else if (rpc.code === Code.PermissionDenied)
    message =
      "Your permissions changed or this action is not available to your account.";
  else if (rpc.code === Code.Unavailable)
    message = "The service is unavailable. No change has been confirmed.";
  else if (rpc.code === Code.NotFound)
    message = "This link or code is invalid or no longer available.";
  else if (rpc.code === Code.InvalidArgument)
    message = "Check the entered value and try again.";
  else if (detail?.reason === ErrorReason.EXPIRED)
    message = "This request has expired. Start a new request.";
  else if (detail?.reason === ErrorReason.CANCELED)
    message = "This request was canceled.";
  else if (detail?.reason === ErrorReason.OWNED_ORGANIZATIONS_REMAIN)
    message =
      "Transfer or delete all organizations you own before deleting your account.";
  else if (detail?.reason === ErrorReason.CONFLICT)
    message =
      "This request has already changed. Refresh its details before continuing.";
  return (
    <div className="account-error" role="alert">
      <p>{message}</p>
      {detail?.requestId && <p>Request ID: {detail.requestId}</p>}
      {rpc.code === Code.Unauthenticated ||
      detail?.reason === ErrorReason.INVITATION_EMAIL_MISMATCH ? (
        <a href={signInHref()}>Sign in again</a>
      ) : (
        retry && (
          <button type="button" onClick={retry}>
            Try again
          </button>
        )
      )}
    </div>
  );
}

export function useOrganizations() {
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
  children,
}: {
  title: string;
  subtitle?: string;
  children: ReactNode;
}) {
  return (
    <section className="account-surface" aria-label={title}>
      <header>
        <h1>{title}</h1>
        {subtitle && <p>{subtitle}</p>}
      </header>
      {children}
    </section>
  );
}

export function LoadingAccount() {
  return <p role="status">Loading your account…</p>;
}

export function AccountDialog({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  useEffect(() => {
    const trigger = document.activeElement as HTMLElement | null;
    const dialog = ref.current;
    dialog?.showModal();
    return () => {
      dialog?.close();
      trigger?.focus();
    };
  }, []);
  return (
    <dialog
      className="account-dialog"
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
      <h2 id={titleId}>{title}</h2>
      {children}
      <button type="button" onClick={onClose} aria-label={`Close ${title}`}>
        Close
      </button>
    </dialog>
  );
}
