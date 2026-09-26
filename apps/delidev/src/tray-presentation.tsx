import { useEffect, useEffectEvent, useMemo, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { EntityKind, ResourceQuery, SystemQuery, UsageQuery } from "@delinoio/delidev-api-client";
import { TrayDestination, TrayPublisher, traySummary, type TraySummary } from "./tray";

const polling = { refetchInterval: 15000, refetchIntervalInBackground: true };
export function TrayPresentation({ navigate }: { navigate: (destination: TrayDestination) => void }) {
  const enabled = isTauri();
  const overview = useQuery(SystemQuery.getOverview, {}, { ...polling, enabled });
  const usage = useQuery(UsageQuery.getUsageSummary, { fromUnixMs: overview.data?.todayFromUnixMs ?? 0n, untilUnixMs: overview.data?.todayUntilUnixMs ?? 0n }, { enabled: enabled && Boolean(overview.data) && !overview.isError });
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
    const activate = async () => {
      try {
        const action = await invoke<{ id: string; destination: TrayDestination } | null>("read_tray_action");
        if (!canceled && action && Object.values(TrayDestination).includes(action.destination)) {
          onNavigate(action.destination);
          await invoke("acknowledge_tray_action", { id: action.id });
        }
      } catch { if (!canceled) setFailed(true); }
    };
    void listen("tray-activate", () => void activate()).then((dispose) => { if (canceled) dispose(); else { unlisten = dispose; void activate(); } }).catch(() => { if (!canceled) setFailed(true); });
    return () => { canceled = true; unlisten?.(); value.close(); if (publisher.current === value) publisher.current = undefined; };
  }, [enabled]);
  useEffect(() => { publisher.current?.update(summary); }, [summary]);
  return failed ? <p role="status">Tray status is unavailable. Sessions and inbox requests remain available in this window.</p> : null;
}
