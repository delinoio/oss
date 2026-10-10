// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef } from "react";
import { isTauri } from "@tauri-apps/api/core";
import { InboxQuery, newRequestId, type MarkSessionInboxReadRequest, type MarkSessionInboxReadResponse } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";

export function acknowledgeSessionInboxRead(result: MarkSessionInboxReadResponse, request: MarkSessionInboxReadRequest): boolean {
  return result.requestId === request.requestId && result.sessionId === request.sessionId
    && typeof result.markedCount === "bigint" && result.markedCount >= 0n && result.markedCount <= 18446744073709551615n
    && /^\d{4}-\d{2}-\d{2}T.*Z$/.test(result.observedAt) && Number.isFinite(Date.parse(result.observedAt));
}

/** An activation survives loading, rerenders and reconnects; only departure or
 * an actual background-to-foreground transition permits another submission. */
export class SessionInboxActivation {
  private foreground = false;
  private selection: number | undefined;
  private submitted = false;
  inspect(active: boolean, foreground: boolean, selection: number, loaded: boolean): boolean {
    if (!active || !foreground) { this.foreground = false; return false; }
    if (!this.foreground || this.selection !== selection) this.submitted = false;
    this.foreground = true;
    this.selection = selection;
    if (!loaded || this.submitted) return false;
    this.submitted = true;
    return true;
  }
}

/** Called only by an admitted ordinary desktop conversation. Pending input is
 * retained by the connection registry; later activations retry only that input. */
export function useSessionInboxRead(sessionId: string, active: boolean, loaded: boolean, selection: number) {
  const mutation = useRetainedMutation(`inbox-session-read:${sessionId}`, InboxQuery.markSessionInboxRead, undefined, acknowledgeSessionInboxRead);
  const activation = useRef(new SessionInboxActivation());
  const current = useRef({ active, loaded, selection, mutation });
  current.current = { active, loaded, selection, mutation };
  useEffect(() => {
    const inspect = () => {
      const next = current.current;
      const foreground = isTauri() && document.visibilityState === "visible" && document.hasFocus();
      if (!activation.current.inspect(next.active, foreground, next.selection, next.loaded)) return;
      // A busy or uncertain original request must never be replaced. The hook
      // reads the current registry again before sending, including StrictMode.
      if (next.mutation.busy) return;
      if (next.mutation.input) void next.mutation.retry();
      else void next.mutation.send({ sessionId, requestId: newRequestId() });
    };
    const background = () => { activation.current.inspect(false, false, current.current.selection, false); };
    window.addEventListener("focus", inspect);
    window.addEventListener("blur", background);
    document.addEventListener("visibilitychange", inspect);
    inspect();
    return () => { window.removeEventListener("focus", inspect); window.removeEventListener("blur", background); document.removeEventListener("visibilitychange", inspect); };
  }, [sessionId]);
  useEffect(() => {
    const next = current.current;
    const foreground = isTauri() && document.visibilityState === "visible" && document.hasFocus();
    if (!activation.current.inspect(active, foreground, selection, loaded)) return;
    if (next.mutation.busy) return;
    if (next.mutation.input) void next.mutation.retry();
    else void next.mutation.send({ sessionId, requestId: newRequestId() });
  }, [sessionId, active, loaded, selection]);
}
