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
  const desktop = isTauri();
  const value = readiness.data;
  const failed = readiness.isError || request.isError;
  const granted = desktop && !failed && value?.permission === Permission.Granted;
  const unavailable = value?.problem === Reason.BundleRequired ? "Native notifications require the installed DeliDev app bundle." : value?.problem === Reason.ActionsUnavailable ? "This desktop notification service cannot open notification actions." : value?.problem === Reason.Capacity ? "The native notification limit is reached for this app process. Requests remain in the inbox; restart DeliDev to clear its native presentation state." : "Native notification service is unavailable.";
  return <section className="notification-os"><div className="notification-section-heading"><h2>On this computer</h2>{desktop ? <button type="button" disabled={readiness.isFetching || request.isPending} onClick={() => { request.reset(); void readiness.refetch(); }}>Refresh status</button> : null}</div>
    <p role="status" className={granted ? "notification-permission-granted" : undefined}>{granted ? <svg aria-hidden="true" viewBox="0 0 24 24"><path d="m5 12 4 4L19 6" /></svg> : null}{!desktop ? "Open DeliDev on your desktop to manage native notifications." : failed ? "Native notification permission could not be confirmed." : !value ? "Checking native notification availability…" : value.permission === Permission.NotDetermined ? "Notification permission has not been requested." : value.permission === Permission.Denied ? "Notifications are disabled. Enable DeliDev in your operating system's notification settings." : granted ? "Allowed by the operating system" : value.permission === Permission.ServiceAvailable ? "The desktop notification service supports actions. This service does not report user permission or whether a banner was shown." : unavailable}</p>
    {granted ? <p>Focus or Do Not Disturb may still hide banners.</p> : null}
    {desktop && !failed && value?.permission === Permission.NotDetermined ? <button type="button" disabled={request.isPending || readiness.isFetching} onClick={() => request.mutate()}>Allow desktop notifications</button> : null}
    <p className="notification-support">Permission is shared by DeliDev windows on this computer.</p>
  </section>;
}
