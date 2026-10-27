// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  AccountService,
  OrganizationService,
  Role,
  ErrorDetailSchema,
  type Money,
} from "@aiprouter/client";
import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";

export const permissionMessage =
  "Your permissions changed. This action is no longer available.";
export const AdminAuthority = createContext({
  organizationId: "",
  role: Role.UNSPECIFIED,
  onDenied: () => {},
});
export function isAdmin(role: Role) {
  return role === Role.OWNER || role === Role.ADMINISTRATOR;
}
export function canRemove(role: Role, target: Role) {
  return (
    target !== Role.OWNER &&
    (role === Role.OWNER ||
      (role === Role.ADMINISTRATOR && target === Role.MEMBER))
  );
}
export function date(value?: Timestamp) {
  return value
    ? `${timestampDate(value).toLocaleString()} (${Intl.DateTimeFormat().resolvedOptions().timeZone})`
    : "Unknown";
}
export function milliseconds(value?: Timestamp) {
  return value ? timestampDate(value).getTime() : undefined;
}
export function money(value?: Money) {
  return value?.decimalAmount !== undefined && value.decimalAmount !== ""
    ? `${value.currency} ${value.decimalAmount}`
    : "Unknown";
}
export function label(value: string | undefined) {
  return value
    ? value
        .toLowerCase()
        .replaceAll("_", " ")
        .replace(/^./, (c) => c.toUpperCase())
    : "Unknown";
}
export function useNow() {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  return now;
}
export function ErrorNotice({
  error,
  retry,
}: {
  error: unknown;
  retry?: () => void;
}) {
  if (!error) return null;
  const e = ConnectError.from(error);
  const detail = e.findDetails(ErrorDetailSchema)[0];
  const message =
    e.code === Code.Unimplemented
      ? "This capability is not available on this server yet."
      : e.code === Code.PermissionDenied
        ? permissionMessage
        : e.code === Code.Unauthenticated
          ? "Your session expired. Sign in again before continuing."
          : e.code === Code.Unavailable
            ? "The service is unavailable. No change is confirmed. Try again when connected."
            : e.code === Code.ResourceExhausted
              ? "Rate limited. Wait before trying again."
              : e.code === Code.FailedPrecondition
                ? "This action is no longer valid. Refresh to check expiry and current state."
                : e.code === Code.Aborted
                  ? "This record changed. Refresh and review before saving again."
                  : "The request failed. Your change is not confirmed.";
  return (
    <div role="alert" className="admin-error">
      <p>{message}</p>
      {detail?.requestId && <p>Request ID: {detail.requestId}</p>}
      {detail?.retryAfterSeconds !== undefined && (
        <p>Retry after {detail.retryAfterSeconds} seconds.</p>
      )}
      {retry && <button onClick={retry}>Try again</button>}
    </div>
  );
}
export function Loading() {
  return (
    <div className="admin-card admin-skeleton" role="status" aria-busy="true">
      Loading…
    </div>
  );
}
export function Empty({
  children = "No records yet.",
}: {
  children?: ReactNode;
}) {
  return <p className="admin-empty">{children}</p>;
}
export function Pager({
  next,
  token,
  onChange,
}: {
  next?: string;
  token: string;
  onChange: (token: string) => void;
}) {
  return (
    <nav aria-label="Record pages" className="admin-actions">
      {token && <button onClick={() => onChange("")}>First page</button>}
      {next && <button onClick={() => onChange(next)}>Next page</button>}
    </nav>
  );
}

// Preflight checks prevent a stale editor from sending a privileged mutation. The
// server must still check authority atomically when applying the mutation.
export function useAction(operator = false) {
  const authority = useContext(AdminAuthority);
  const transport = useTransport();
  const cache = useQueryClient();
  const busy = useRef(false);
  const mounted = useRef(true);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>();
  const [notice, setNotice] = useState("");
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  async function run(
    action: () => Promise<unknown>,
    options: { owner?: boolean; success?: string; done?: () => void } = {},
  ) {
    if (busy.current) return;
    busy.current = true;
    setPending(true);
    setError(undefined);
    setNotice("");
    try {
      if (operator) {
        const session = await createClient(
          AccountService,
          transport,
        ).getSession({});
        if (!session.isInstanceOperator)
          throw new ConnectError(permissionMessage, Code.PermissionDenied);
      } else {
        const current = await createClient(
          OrganizationService,
          transport,
        ).getOrganization({ organizationId: authority.organizationId });
        const role = current.organization?.role;
        if (
          role === undefined ||
          !isAdmin(role) ||
          (options.owner && role !== Role.OWNER)
        )
          throw new ConnectError(permissionMessage, Code.PermissionDenied);
      }
      // A scope switch unmounts the editor, including while preflight is in flight.
      if (!mounted.current) return;
      await action();
      await cache.invalidateQueries();
      if (mounted.current) {
        setNotice(options.success ?? "Changes saved.");
        options.done?.();
      }
    } catch (cause) {
      if (mounted.current) {
        setError(cause);
        if (
          [Code.PermissionDenied, Code.Unauthenticated].includes(
            ConnectError.from(cause).code,
          )
        ) {
          authority.onDenied();
          await cache.invalidateQueries();
        }
      }
    } finally {
      busy.current = false;
      if (mounted.current) setPending(false);
    }
  }
  return {
    run,
    clearError: () => setError(undefined),
    pending,
    error,
    feedback: (
      <>
        <ErrorNotice error={error} />
        <p role="status">
          {pending
            ? "Saving… Closing a dialog does not cancel a submitted change."
            : notice}
        </p>
      </>
    ),
  };
}

export function Dialog({
  title,
  children,
  onClose,
  initial = "input",
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  initial?: "input" | "cancel";
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const id = useId();
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const element = ref.current!;
    element.showModal();
    element
      .querySelector<HTMLElement>(
        initial === "cancel"
          ? "[data-cancel]"
          : "input,select,textarea,[data-cancel]",
      )
      ?.focus();
    return () => {
      element.close();
      if (previous?.isConnected) previous.focus();
    };
  }, [initial]);
  return (
    <dialog
      ref={ref}
      aria-labelledby={id}
      className="admin-dialog"
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          onClose();
        }
        if (e.key !== "Tab") return;
        const nodes = ref.current?.querySelectorAll<HTMLElement>(
          "button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled),a[href]",
        );
        const first = nodes?.[0],
          last = nodes?.[nodes.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last?.focus();
        }
        if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first?.focus();
        }
      }}
    >
      <div className="admin-dialog-heading">
        <h2 id={id}>{title}</h2>
        <button aria-label="Close dialog" onClick={onClose}>
          ×
        </button>
      </div>
      {children}
    </dialog>
  );
}
export function Confirm({
  title,
  name,
  children,
  action,
  pending,
  onClose,
}: {
  title: string;
  name?: string;
  children: ReactNode;
  action: () => void;
  pending: boolean;
  onClose: () => void;
}) {
  const [typed, setTyped] = useState("");
  return (
    <Dialog title={title} onClose={onClose} initial="cancel">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!pending && (!name || name === typed)) action();
        }}
      >
        <div>{children}</div>
        {name && (
          <label>
            Type {name} to confirm
            <input
              required
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
            />
          </label>
        )}
        <div className="admin-actions">
          <button type="button" data-cancel onClick={onClose}>
            Cancel
          </button>
          <button
            className="admin-danger"
            disabled={pending || (!!name && typed !== name)}
          >
            {title}
          </button>
        </div>
      </form>
    </Dialog>
  );
}
