// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState } from "react";
import { InboxQuery, newRequestId } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";

/** Retained mutation key prefix; the connection-owned badge observer matches it. */
export const sessionInboxReadPrefix = "session-inbox-read:";

function windowForeground(): boolean {
  return typeof document !== "undefined" && document.visibilityState === "visible" && document.hasFocus();
}

/** Visible document inside a focused admitted product window. */
export function useWindowForeground(): boolean {
  const [foreground, setForeground] = useState(windowForeground);
  useEffect(() => {
    const update = () => setForeground(windowForeground());
    update();
    window.addEventListener("focus", update);
    window.addEventListener("blur", update);
    document.addEventListener("visibilitychange", update);
    return () => {
      window.removeEventListener("focus", update);
      window.removeEventListener("blur", update);
      document.removeEventListener("visibilitychange", update);
    };
  }, []);
  return foreground;
}

/**
 * Acknowledges every existing Inbox alert of one foreground conversation once
 * per activation (chat selection or return from background) after its initial
 * transcript loaded. Re-renders, polling and newly arriving alerts never create
 * another request within the same activation. The authoritative unread count is
 * refreshed by the connection-owned badge observer, never decremented locally.
 */
export function useForegroundSessionInboxRead({ sessionId, eligible, loaded }: { sessionId: string; eligible: boolean; loaded: boolean }) {
  const foreground = useWindowForeground();
  const mutation = useRetainedMutation(`${sessionInboxReadPrefix}${sessionId}`, InboxQuery.markSessionInboxRead, undefined, (result, request) => result.requestId === request.requestId && result.sessionId === request.sessionId);
  const send = useRef(mutation.send); send.current = mutation.send;
  const uncertain = useRef(mutation.uncertain); uncertain.current = mutation.uncertain;
  const activation = useRef<{ done: boolean } | undefined>(undefined);
  const busy = mutation.busy;
  const active = eligible && foreground;
  useEffect(() => {
    activation.current = active ? { done: false } : undefined;
    return () => { activation.current = undefined; };
  }, [sessionId, active]);
  useEffect(() => {
    const current = activation.current;
    // A busy retained request rejects replacement sends; wait for it to settle.
    if (!active || !loaded || !current || current.done || busy) return;
    current.done = true;
    console.debug("delidev.session_inbox_read.admitted", { retry: uncertain.current });
    // An uncertain request blocks replacement and permits only its exact retry.
    void (uncertain.current ? send.current() : send.current({ requestId: newRequestId(), sessionId }));
  }, [sessionId, active, loaded, busy]);
}
