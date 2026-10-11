// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { isTauri } from "@tauri-apps/api/core";
import {
  InboxQuery,
  SystemCapability,
  SystemQuery,
  isEntityId,
  newRequestId,
  type MarkSessionInboxReadRequest,
  type MarkSessionInboxReadResponse,
} from "@delinoio/delidev-api-client";
import { useQuery } from "@connectrpc/connect-query";
import { sessionInboxReadPrefix, useRetainedMutation } from "./mutation";

const Context = createContext({
  ready: false,
  foreground: false,
  activation: 0,
});
function foregroundDocument() {
  return document.visibilityState === "visible" && document.hasFocus();
}
/** Only the authenticated product App admits visits; Sidechat/mobile never mount this boundary. */
export function SessionInboxReadBoundary({
  ready,
  children,
}: {
  ready: boolean;
  children: ReactNode;
}) {
  const [state, setState] = useState({ foreground: false, activation: 0 });
  useEffect(() => {
    if (!isTauri()) return;
    const update = () => {
      const foreground = foregroundDocument();
      setState((previous) =>
        previous.foreground === foreground
          ? previous
          : {
              foreground,
              activation: previous.activation + (foreground ? 1 : 0),
            },
      );
    };
    update();
    document.addEventListener("visibilitychange", update);
    window.addEventListener("focus", update);
    window.addEventListener("blur", update);
    return () => {
      document.removeEventListener("visibilitychange", update);
      window.removeEventListener("focus", update);
      window.removeEventListener("blur", update);
    };
  }, []);
  return (
    <Context.Provider value={{ ready, ...state }}>{children}</Context.Provider>
  );
}
export function acknowledgeSessionInboxRead(
  result: MarkSessionInboxReadResponse,
  request: MarkSessionInboxReadRequest,
) {
  return (
    result.requestId === request.requestId &&
    result.sessionId === request.sessionId &&
    isEntityId(result.sessionId) &&
    result.markedCount >= 0n &&
    result.markedCount <= 18446744073709551615n &&
    /^\d{4}-\d{2}-\d{2}T.*Z$/.test(result.observedAt) &&
    Number.isFinite(Date.parse(result.observedAt))
  );
}

/** Readiness may change repeatedly; only a new visible activation admits a request. */
export function useSessionInboxRead(
  id: string,
  active: boolean,
  loaded: boolean,
) {
  const product = useContext(Context);
  const status = useQuery(
    SystemQuery.getStatus,
    {},
    { enabled: product.ready && active },
  );
  const supported =
    !status.isError &&
    status.data?.capabilities.includes(SystemCapability.SESSION_INBOX_READ_V1);
  const mutation = useRetainedMutation(
    `${sessionInboxReadPrefix}${id}`,
    InboxQuery.markSessionInboxRead,
    undefined,
    acknowledgeSessionInboxRead,
    true,
  );
  const latest = useRef(mutation);
  latest.current = mutation;
  const visit = useRef<
    { id: string; activation: number; submitted: boolean } | undefined
  >(undefined);
  useEffect(() => {
    if (!active || !product.foreground) {
      visit.current = undefined;
      return;
    }
    if (
      !visit.current ||
      visit.current.id !== id ||
      visit.current.activation !== product.activation
    )
      visit.current = { id, activation: product.activation, submitted: false };
    if (
      visit.current.submitted ||
      !product.ready ||
      !loaded ||
      !supported ||
      !isEntityId(id) ||
      !foregroundDocument()
    )
      return;
    visit.current.submitted = true;
    const pending = latest.current;
    // Navigation/background never replaces uncertain bytes. A later qualifying
    // visit may reconcile only the original retained request, including rejection.
    console.info("delidev.inbox.session_read_admitted", {
      retry: Boolean(pending.input),
    });
    if (pending.input) {
      if (!pending.busy) void pending.retry();
    } else void pending.send({ requestId: newRequestId(), sessionId: id });
  }, [
    id,
    active,
    loaded,
    supported,
    product.ready,
    product.foreground,
    product.activation,
  ]);
}
