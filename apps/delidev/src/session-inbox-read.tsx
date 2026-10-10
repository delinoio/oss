// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState, type RefObject } from "react";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { isTauri } from "@tauri-apps/api/core";
import { EntityKind, InboxQuery, ResourceService, clientFailure, isEntityId, newRequestId, supportsResourceSchema, decodeResourceDocument } from "@delinoio/delidev-api-client";
import { document as readDocument } from "./documents";
import { validSessionActionResource } from "./session-control";
import { validateConversationPage } from "./conversation-pagination";
import { useRetainedMutation, useRetainedMutationNotifications } from "./mutation";

/** Lives above conversation disposal in the original authenticated connection. */
export function SessionInboxReadInvalidation() {
  const client = useQueryClient();
  useRetainedMutationNotifications(key => {
    if (!key.startsWith("session-inbox-read:")) return;
    void client.invalidateQueries({ predicate: query => query.queryKey.some(value => typeof value === "object" && value !== null && "serviceName" in value && value.serviceName === "delidev.v1.InboxService") });
  });
  return null;
}

/** A mounted conversation must remain available beneath native modal ownership. */
function availableConversation(region?: RefObject<HTMLElement | null>) {
  if (!region) return true;
  const root = region.current;
  if (!root || !root.isConnected) return false;
  const visible = (element: Element) => {
    for (let parent: Element | null = element; parent; parent = parent.parentElement) {
      if (parent.hasAttribute("hidden") || parent.hasAttribute("inert")) return false;
      const style = getComputedStyle(parent);
      if (style.display === "none" || style.visibility === "hidden" || style.visibility === "collapse") return false;
    }
    return true;
  };
  if (!visible(root)) return false;
  return !Array.from(document.querySelectorAll('dialog[open]:not([role="region"]), [aria-modal="true"]')).some(modal => visible(modal) && !modal.contains(root));
}

/** Selection/background return defines an activation, independently of reads. */
export function useSessionInboxRead(sessionId: string, active: boolean, admitted: boolean, loaded: boolean, expectedRevision = 1n, region?: RefObject<HTMLElement | null>) {
  const transport = useTransport();
  const [epoch, setEpoch] = useState(0);
  const [presented, setPresented] = useState(!region);
  const foreground = useRef(false), attempted = useRef<number | undefined>(undefined), selection = useRef(sessionId);
  const current = useRef({ sessionId, active, admitted, loaded });
  current.current = { sessionId, active, admitted, loaded };
  const mutation = useRetainedMutation(`session-inbox-read:${sessionId}`, InboxQuery.markSessionInboxRead, undefined, (result, request) => result.requestId === request.requestId && result.sessionId === request.sessionId && typeof result.markedCount === "bigint" && result.markedCount >= 0n && result.markedCount <= 18446744073709551615n && /^\d{4}-\d{2}-\d{2}T.*Z$/.test(result.observedAt) && Number.isFinite(Date.parse(result.observedAt)));
  useEffect(() => {
    if (selection.current !== sessionId) { selection.current = sessionId; foreground.current = false; }
    const blur = () => { foreground.current = false; };
    const update = () => {
      const next = active && isTauri() && document.visibilityState === "visible" && document.hasFocus();
      if (next && !foreground.current) setEpoch(value => value + 1);
      foreground.current = next;
    };
    const presentation = () => setPresented(availableConversation(region));
    presentation(); update();
    // Modal and inert changes can unblock an unattempted activation, but never
    // create a new activation after acknowledgment or a settled failed read.
    const observer = new MutationObserver(presentation);
    observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["hidden", "inert", "open", "role", "aria-modal", "class", "style"] });
    window.addEventListener("focus", update); window.addEventListener("blur", blur);
    document.addEventListener("visibilitychange", update);
    return () => { observer.disconnect(); window.removeEventListener("focus", update); window.removeEventListener("blur", blur); document.removeEventListener("visibilitychange", update); };
  }, [active, sessionId, region]);
  useEffect(() => {
    if (!presented || !availableConversation(region) || !epoch || !foreground.current || !active || !admitted || !loaded || !isEntityId(sessionId) || mutation.busy || attempted.current === epoch) return;
    attempted.current = epoch;
    const original = { sessionId, epoch }, controller = new AbortController();
    let sent = false, inspecting = true;
    const eligible = () => availableConversation(region) && !controller.signal.aborted && current.current.sessionId === original.sessionId && current.current.active && current.current.admitted && current.current.loaded && foreground.current && document.visibilityState === "visible" && document.hasFocus();
    // Reinspect the initial transcript and session on this activation. Retained
    // payloads alone cannot prove that a stale/reconnected scope loaded safely.
    // These bounded reads carry the original authenticated connection transport.
    const inspect = async () => {
      try {
        const client = createClient(ResourceService, transport);
        const [session, transcript] = await Promise.all([
          client.getResource({ kind: EntityKind.SESSION, id: sessionId }, { signal: controller.signal }),
          client.listResources({ filter: { kind: EntityKind.MESSAGE, sessionId, pageSize: 50 } }, { signal: controller.signal }),
        ]);
        const row = session.resource;
        if (!validSessionActionResource(row, sessionId) || row.revision < expectedRevision || !supportsResourceSchema(row) || readDocument(row).source === "SIDECHAT" || transcript.resources.length > 50) throw new ConnectError("The original conversation could not be verified.", Code.DataLoss);
        validateConversationPage(transcript.resources, EntityKind.MESSAGE, sessionId);
        if (transcript.resources.some(message => !isEntityId(message.id) || !supportsResourceSchema(message) || !decodeResourceDocument(message))) throw new ConnectError("The original transcript could not be verified.", Code.DataLoss);
        if (!eligible()) return;
        sent = true;
        console.info("delidev.session_inbox_read.admitted", { reconciliation: mutation.uncertain });
        if (mutation.uncertain) await mutation.retry();
        else await mutation.send({ requestId: newRequestId(), sessionId });
      } catch (error) {
        if (!controller.signal.aborted) console.warn("delidev.session_inbox_read.inspection_failed", { classification: clientFailure(error).code });
      } finally { inspecting = false; }
    };
    void inspect();
    return () => {
      controller.abort();
      // Strict Mode may cancel preflight before publication. Only unsent work
      // may be reinspected; retained mutations own every accepted/uncertain ID.
      if (!sent && inspecting && attempted.current === original.epoch) attempted.current = undefined;
    };
  }, [epoch, presented, active, admitted, loaded, expectedRevision, sessionId, transport, mutation.busy, mutation.uncertain, region]);
}
