import { useEffect, useMemo, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { useMutation, useQuery as useNativeQuery, useQueryClient } from "@tanstack/react-query";
import { invoke, isTauri } from "@tauri-apps/api/core";
import { InboxQuery, InboxService } from "@delinoio/delidev-api-client";
import { NativeNotificationPermission as Permission, NativeNotificationProblem as Reason, NotificationPump, notificationReadiness, notificationReady } from "./notifications";
import { useSettingsOpening } from "./settings-lifetime";

const readinessKey = ["native-notification-readiness"] as const;
function useReadiness(active = true) {
  const opening = useSettingsOpening();
  return useNativeQuery({ queryKey: opening ? [...opening.queryKey, ...readinessKey] : readinessKey, queryFn: async () => notificationReadiness(await (opening ? opening.native(() => invoke("notification_permission")) : invoke("notification_permission"))), enabled: active && isTauri(), staleTime: 15000, refetchInterval: 30000, refetchIntervalInBackground: true, retry: false });
}

export function NotificationPresentation() {
  const enabled = isTauri();
  const transport = useTransport();
  const service = useMemo(() => createClient(InboxService, transport), [transport]);
  const readiness = useReadiness();
  const ready = enabled && !readiness.isError && notificationReady(readiness.data);
  const candidates = useQuery(InboxQuery.listNotificationCandidates, { limit: 20 }, { enabled: ready, refetchInterval: 10000, refetchIntervalInBackground: true });
  const pump = useRef<NotificationPump>(undefined);
  const [failed, setFailed] = useState(false);
  // Keep the native scope for the lifetime of this connection. Permission polls
  // must not replace callbacks for already submitted notifications.
  useEffect(() => {
    if (!enabled) return;
    const value = new NotificationPump(service, { begin: () => invoke("begin_notifications"), end: (scope) => invoke("end_notifications", { scope }), present: (scope, notice) => invoke("present_notification", { scope, notice }) }, setFailed, () => { void candidates.refetch(); });
    pump.current = value;
    return () => { value.close(); if (pump.current === value) pump.current = undefined; };
  }, [enabled, service, candidates.refetch]);
  useEffect(() => { if (ready && !candidates.isError && candidates.data) pump.current?.update(candidates.data.candidates); }, [ready, candidates.isError, candidates.data, candidates.dataUpdatedAt]);
  return enabled && (failed || candidates.isError || readiness.isError) ? <p role="status">Desktop notifications could not be confirmed. Requests remain in the inbox; notification settings show native availability.</p> : null;
}

export function NativeNotificationSettings({ active }: { active: boolean }) {
  const client = useQueryClient();
  const opening = useSettingsOpening();
  const readiness = useReadiness(active);
  const request = useMutation({ mutationFn: async () => notificationReadiness(await (opening ? opening.native(() => invoke("request_notification_permission")) : invoke("request_notification_permission"))), retry: false, gcTime: 0, meta: opening?.mutationMeta, onSuccess: (value) => {
    if (opening?.disposed) return;
    if (opening) client.setQueryData([...opening.queryKey, ...readinessKey], value);
    client.setQueryData(readinessKey, value);
  } });
  if (!isTauri()) return <p>Open DeliDev on your desktop to manage native notifications.</p>;
  const value = readiness.data;
  const unavailable = value?.problem === Reason.BundleRequired ? "Native notifications require the installed DeliDev app bundle." : value?.problem === Reason.ActionsUnavailable ? "This desktop notification service cannot open notification actions." : value?.problem === Reason.Capacity ? "The native notification limit is reached for this app process. Requests remain in the inbox; restart DeliDev to clear its native presentation state." : "Native notification service is unavailable.";
  return <section className="native-notification-settings"><h2>On this computer</h2><div className="notification-status-row">
    <p role="status">{readiness.isError || request.isError ? "Native notification permission could not be confirmed." : !value ? "Checking native notification availability…" : value.permission === Permission.NotDetermined ? "Notification permission has not been requested." : value.permission === Permission.Denied ? "Notifications are disabled. Enable DeliDev in your operating system's notification settings." : value.permission === Permission.Granted ? "Notifications allowed" : value.permission === Permission.ServiceAvailable ? "The desktop notification service supports actions. This service does not report user permission or whether a banner was shown." : unavailable}</p>
    <div className="actions">{value?.permission === Permission.NotDetermined ? <button type="button" disabled={request.isPending} onClick={() => request.mutate()}>Allow desktop notifications</button> : null}<button type="button" disabled={readiness.isFetching || request.isPending} onClick={() => { request.reset(); void readiness.refetch(); }} aria-label="Refresh native notification status">Refresh status</button></div>
    </div>
    {value?.permission === Permission.Granted ? <p>Focus or Do Not Disturb may still suppress banners.</p> : null}
  </section>;
}
