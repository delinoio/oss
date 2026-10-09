// SPDX-License-Identifier: Apache-2.0
// Isolated branch of the existing Settings fixture: no native/account adapters.
import type { Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { NotificationSettings } from "./notification-settings";
import { SettingsHeading } from "./settings-presentation";
import { SettingsTaskBackground, SettingsTasks } from "./settings-task";
import { SettingsLifetime } from "./settings-lifetime";
import { MutationIntents } from "./mutation";
import { copy } from "./localization";
import { NativeNotificationPermission, NativeNotificationProblem } from "./notifications";

export const notificationFixtureCounts = { nativeReads: 0, permissionRequests: 0, preferenceReads: 0, preferenceWrites: 0 };
export function prepareNotificationLayoutFixture(args: URLSearchParams) {
  const permission = args.get("permission") ?? NativeNotificationPermission.Granted;
  Object.assign(window, { isTauri: true, notificationFixtureCounts, __TAURI_INTERNALS__: { invoke: async (operation: string) => {
    if (operation === "notification_permission") {
      notificationFixtureCounts.nativeReads++;
      if (permission === "read-failure") throw new Error("Synthetic status read failure");
      return { permission, problem: permission === NativeNotificationPermission.Unavailable ? NativeNotificationProblem.OsUnavailable : NativeNotificationProblem.None };
    }
    if (operation === "request_notification_permission") { notificationFixtureCounts.permissionRequests++; return { permission: NativeNotificationPermission.Granted, problem: NativeNotificationProblem.None }; }
    throw new Error(`Unsupported synthetic operation: ${operation}`);
  } } });
}
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
export function NotificationLayoutFixture({ transport }: { transport: Transport }) {
  return <TransportProvider transport={transport}><QueryClientProvider client={client}><SettingsLifetime>{() => <MutationIntents><SettingsTasks><main className="settings-content" aria-label="Synthetic Notifications settings" style={{ minHeight: "100dvh" }}><SettingsTaskBackground><div className="settings-content-column"><SettingsHeading title={copy("notification-settings.notifications_788011")} description={copy("settings.thesePreferencesBelongToThisClient_082e1e")} scope="" /><NotificationSettings active showCategoryIntro={false} /></div></SettingsTaskBackground></main></SettingsTasks></MutationIntents>}</SettingsLifetime></QueryClientProvider></TransportProvider>;
}
