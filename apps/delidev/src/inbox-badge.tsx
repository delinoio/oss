// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { InboxQuery, InboxService } from "@delinoio/delidev-api-client";
import { useRetainedMutationNotifications } from "./mutation";
interface Selection { scope: string; generation: number }
// Serialize scope replacement across React StrictMode and connection remounts.
let beginQueue: Promise<unknown> = Promise.resolve();
export function unreadBadgeCount(count: unknown, observedAt: unknown): string | undefined {
  if (typeof count !== "bigint" || count < 0n || count > 18446744073709551615n || typeof observedAt !== "string" || !Number.isFinite(Date.parse(observedAt))) return;
  return count.toString();
}
/** Connection-owned background metadata, independent of Inbox and OS notifications. */
export function InboxBadgePresentation({ ready, supported }: { ready: boolean; supported: boolean }) {
  const transport = useTransport();
  const queryClient = useQueryClient();
  // This observer survives detail-pane disposal, but retires with the original
  // connection registry. Only acknowledged exact read-state mutations refresh.
  useRetainedMutationNotifications((key) => {
    if (key.startsWith("inbox-read:")) void queryClient.invalidateQueries({ queryKey: createQueryOptions(InboxQuery.getUnreadInboxCount, {}, { transport }).queryKey });
  });
  const service = useMemo(() => createClient(InboxService, transport), [transport]);
  const [selection, setSelection] = useState<Selection>();
  const owner = useRef<{ scope: string; revision: number; alive: boolean } | undefined>(undefined);
  const latestSelection = useRef<Selection | undefined>(undefined);
  latestSelection.current = selection;
  const desktop = isTauri();
  useEffect(() => {
    if (!desktop) return;
    let canceled = false, running = false, requested = false;
    let dispose: (() => void) | undefined;
    let current: typeof owner.current;
    setSelection(undefined);
    const read = async () => {
      if (!current || canceled) return;
      if (running) { requested = true; return; }
      running = true;
      do {
        requested = false;
        try {
          const next = await invoke<Selection | null>("read_inbox_badge_selection", { scope: current.scope });
          if (!canceled) setSelection(previous => previous?.scope === next?.scope && previous?.generation === next?.generation ? previous : next ?? undefined);
        } catch { if (!canceled) setSelection(undefined); }
      } while (!canceled && requested);
      running = false;
    };
    void (async () => {
      try {
        const scope = await (beginQueue = beginQueue.catch(() => {}).then(() => canceled ? undefined : invoke<string>("begin_inbox_badge"))) as string | undefined;
        if (canceled || !scope) return;
        current = { scope, revision: 0, alive: true }; owner.current = current;
        dispose = await listen("inbox-badge-selection", () => { void read(); });
        if (canceled) dispose(); else await read();
      } catch { if (!canceled) setSelection(undefined); }
    })();
    return () => {
      canceled = true;
      if (current) {
        current.alive = false;
        const selected = latestSelection.current;
        if (selected?.scope === current.scope) void invoke("publish_inbox_badge", { scope: current.scope, generation: selected.generation, revision: ++current.revision, count: null }).catch(() => {});
      }
      dispose?.(); if (owner.current === current) owner.current = undefined;
    };
  }, [desktop, transport, ready]);
  const key = createQueryOptions(InboxQuery.getUnreadInboxCount, {}, { transport }).queryKey;
  // A new selection gets its own uncached read. A previous selected window's
  // response cannot authorize this native generation, even on the same server.
  const unread = useQuery({ queryKey: [...key, selection?.scope, selection?.generation], queryFn: ({ signal }) => service.getUnreadInboxCount({}, { signal }), enabled: desktop && ready && supported && Boolean(selection), retry: false, staleTime: 0, gcTime: 0, refetchInterval: 10000, refetchIntervalInBackground: true });
  useEffect(() => {
    const current = owner.current;
    if (!current?.alive || !selection || current.scope !== selection.scope) return;
    const count = ready && supported && !unread.isError && unread.data ? unreadBadgeCount(unread.data.unreadCount, unread.data.observedAt) : undefined;
    const revision = ++current.revision;
    void invoke("publish_inbox_badge", { scope: current.scope, generation: selection.generation, revision, count: count ?? null }).catch(() => {
      // Native selection/replacement fences are authoritative. Never retry an
      // obsolete publication into another window or connection.
    });
  }, [selection, ready, supported, unread.data, unread.dataUpdatedAt, unread.isError]);
  return null;
}
