import { copy, useLocale } from "./localization";
import { useEffect, useEffectEvent, useMemo, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { EntityKind, ResourceQuery, SystemQuery, UsageQuery, UsageAccountingProfile, isEntityId } from "@delinoio/delidev-api-client";
import { TrayDestination, TrayPublisher, traySummary, type TraySummary } from "./tray";

const polling = { refetchInterval: 15000, refetchIntervalInBackground: true };
export function TrayPresentation({ navigate }: { navigate: (destination: TrayDestination, inboxId?: string) => void }) {
  useLocale();
  const enabled = isTauri();
  const overview = useQuery(SystemQuery.getOverview, {}, { ...polling, enabled });
  const usage = useQuery(UsageQuery.getUsageSummary, { accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1, fromUnixMs: overview.data?.todayFromUnixMs ?? 0n, untilUnixMs: overview.data?.todayUntilUnixMs ?? 0n }, { ...polling, enabled: enabled && Boolean(overview.data) && !overview.isError });
  const accounts = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.ACCOUNT, pageSize: 20 } }, { ...polling, refetchInterval: 30000, enabled });
  const summary = useMemo(() => traySummary(overview.data, overview.isError, usage.isError ? undefined : usage.data, accounts.isError ? undefined : accounts.data), [overview.data, overview.isError, usage.data, usage.isError, accounts.data, accounts.isError]);
  const latest = useRef<TraySummary>(summary);
  latest.current = summary;
  const publisher = useRef<TrayPublisher>(undefined);
  const [failed, setFailed] = useState(false);
  const onNavigate = useEffectEvent(navigate);
  useEffect(() => {
    if (!enabled) return;
    let canceled = false;
    const value = new TrayPublisher({ begin: () => invoke("begin_tray"), publish: (scope, revision, summary) => invoke("publish_tray", { scope, revision, summary }) }, (failed) => { if (!canceled) setFailed(failed); });
    publisher.current = value;
    value.update(latest.current);
    let unlisten: (() => void) | undefined;
    let running = false, requested = false, retries = 0, navigated = "";
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    const activate = async () => {
      if (canceled) return;
      if (running) { requested = true; return; }
      running = true;
      do {
        requested = false;
        try {
          const action = await invoke<{ id: string; destination: TrayDestination; inbox_id?: string } | null>("read_tray_action");
          if (!canceled && action) {
            if (!isEntityId(action.id) || !Object.values(TrayDestination).includes(action.destination) || (action.inbox_id !== undefined && (action.destination !== TrayDestination.Inbox || !isEntityId(action.inbox_id)))) throw new Error("Invalid native navigation");
            if (navigated !== action.id) { onNavigate(action.destination, action.inbox_id); navigated = action.id; }
            await invoke("acknowledge_tray_action", { id: action.id });
          }
          retries = 0;
        } catch {
          if (!canceled) {
            setFailed(true);
            // A concurrent native label publication can briefly return Busy.
            // Retry only this retained read/idempotent acknowledgment, never a
            // product mutation or a new notification display.
            if (retries < 5 && !retryTimer) retryTimer = setTimeout(() => { retryTimer = undefined; void activate(); }, 100 * 2 ** retries++);
          }
        }
      } while (!canceled && requested);
      running = false;
    };
    void listen("tray-activate", () => { retries = 0; void activate(); }).then((dispose) => { if (canceled) dispose(); else { unlisten = dispose; void activate(); } }).catch(() => { if (!canceled) setFailed(true); });
    return () => { canceled = true; if (retryTimer) clearTimeout(retryTimer); unlisten?.(); value.close(); if (publisher.current === value) publisher.current = undefined; };
  }, [enabled]);
  useEffect(() => { publisher.current?.update(summary); }, [summary]);
  return failed ? <p role="status">{copy("tray-presentation.trayStatusIsUnavailableSessionsAnd_7fdf0e")}</p> : null;
}
