import { SettingsActionButton, SettingsActionIcon, SettingsActionPresentation } from "./settings-action";
import { copy, useLocale } from "./localization";
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
  useLocale();
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
    value.prepare();
    pump.current = value;
    return () => { value.close(); if (pump.current === value) pump.current = undefined; };
  }, [enabled, service, candidates.refetch]);
  useEffect(() => { if (ready && !candidates.isError && candidates.data) pump.current?.update(candidates.data.candidates); }, [ready, candidates.isError, candidates.data, candidates.dataUpdatedAt]);
  return enabled && (failed || candidates.isError || readiness.isError) ? <p role="status">{copy("notification-presentation.desktopNotificationsCouldNotBeConfirmed_8b02e8")}</p> : null;
}

export function NativeNotificationSettings({ active }: { active: boolean }) {
  useLocale();
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
  const unavailable = value?.problem === Reason.BundleRequired ? copy("notification-presentation.extra.0be8addf8c13") : value?.problem === Reason.ActionsUnavailable ? copy("notification-presentation.extra.49bd1f979ff9") : value?.problem === Reason.Capacity ? copy("notification-presentation.extra.d0382f5b548f") : copy("notification-presentation.extra.e8b3afa7a523");
  return <section className="notification-os"><div className="notification-section-heading"><h2>{copy("notification-presentation.onThisComputer_e6af7e")}</h2>{desktop ? <SettingsActionButton icon={SettingsActionIcon.Refresh} presentation={SettingsActionPresentation.Icon} type="button" disabled={readiness.isFetching || request.isPending} onClick={() => { request.reset(); void readiness.refetch(); }} aria-label={copy("notification-presentation.refreshStatusForNativeNotifications_6d39c7")}>{copy("notification-presentation.refreshStatus_442c4b")}</SettingsActionButton> : null}</div>
    <p role="status" className={granted ? "notification-permission-granted" : undefined}>{granted ? <svg aria-hidden="true" viewBox="0 0 24 24"><path d="m5 12 4 4L19 6" /></svg> : null}{!desktop ? copy("notification-presentation.openDelidevOnYourDesktopTo_7da841") : failed ? copy("notification-presentation.nativeNotificationPermissionCouldNotBe_1d14b3") : !value ? copy("notification-presentation.checkingNativeNotificationAvailability_b63c11") : value.permission === Permission.NotDetermined ? copy("notification-presentation.notificationPermissionHasNotBeenRequested_e0df38") : value.permission === Permission.Denied ? copy("notification-presentation.notificationsAreDisabledEnableDelidevIn_6e5797") : granted ? copy("notification-presentation.notificationsAllowed_675cad") : value.permission === Permission.ServiceAvailable ? copy("notification-presentation.theDesktopNotificationServiceSupportsActions_aec2f5") : unavailable}</p>
    {granted ? <p>{copy("notification-presentation.focusOrDoNotDisturbMay_42e527")}</p> : null}
    {desktop && !failed && value?.permission === Permission.NotDetermined ? <SettingsActionButton icon={SettingsActionIcon.Connect} type="button" disabled={request.isPending || readiness.isFetching} onClick={() => request.mutate()}>{copy("notification-presentation.allowDesktopNotifications_c30c0d")}</SettingsActionButton> : null}
    <p className="notification-support">{copy("notification-presentation.permissionIsSharedByDelidevWindows_5376fc")}</p>
  </section>;
}
