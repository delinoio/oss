import type { ComponentType } from "react";

import type { Copy } from "./localization";
import {
  AppearanceIcon,
  ChromeExtensionIcon,
  CloudIcon,
  GitHubIcon,
  LinkIcon,
  LocalAgentIcon,
  NotificationIcon,
  ShortcutIcon,
  UpdateIcon,
  type IconProps,
} from "./ui-icons";

export const SettingsSectionId = {
  Appearance: "appearance",
  Shortcuts: "shortcuts",
  ChromeExtension: "chrome-extension",
  UrlMappings: "url-mappings",
  GitHubCredentials: "github-credentials",
  CloudflareR2: "cloudflare-r2",
  LocalAgents: "local-agents",
  Notifications: "notifications",
  Updates: "updates",
} as const;
export type SettingsSectionId = (typeof SettingsSectionId)[keyof typeof SettingsSectionId];

export interface SettingsSectionCapabilities {
  readonly mobile: boolean;
  readonly shortcuts: boolean;
  readonly nativeMessaging: boolean;
  readonly localAgents: boolean;
  readonly notifications: boolean;
  readonly updates: boolean;
}

export interface SettingsSectionDefinition {
  readonly id: SettingsSectionId;
  readonly title: keyof Copy;
  readonly summary: keyof Copy;
  readonly icon: ComponentType<IconProps>;
  readonly available: (capabilities: SettingsSectionCapabilities) => boolean;
}

const shared = () => true;
export const settingsSections = [
  { id: SettingsSectionId.Appearance, title: "settingsAppearanceTitle", summary: "settingsAppearanceSummary", icon: AppearanceIcon, available: shared },
  { id: SettingsSectionId.Shortcuts, title: "settingsShortcutsTitle", summary: "settingsShortcutsSummary", icon: ShortcutIcon, available: (capabilities) => !capabilities.mobile && capabilities.shortcuts },
  { id: SettingsSectionId.ChromeExtension, title: "settingsChromeTitle", summary: "settingsChromeSummary", icon: ChromeExtensionIcon, available: (capabilities) => !capabilities.mobile && capabilities.nativeMessaging },
  { id: SettingsSectionId.UrlMappings, title: "urlMappingsTitle", summary: "urlMappingsSummary", icon: LinkIcon, available: shared },
  { id: SettingsSectionId.GitHubCredentials, title: "githubSetupTitle", summary: "githubSetupSummary", icon: GitHubIcon, available: shared },
  { id: SettingsSectionId.CloudflareR2, title: "r2SettingsTitle", summary: "r2SettingsSummary", icon: CloudIcon, available: shared },
  { id: SettingsSectionId.LocalAgents, title: "localAgentsTitle", summary: "localAgentsSummary", icon: LocalAgentIcon, available: (capabilities) => !capabilities.mobile && capabilities.localAgents },
  { id: SettingsSectionId.Notifications, title: "settingsNotificationsTitle", summary: "settingsNotificationsSummary", icon: NotificationIcon, available: (capabilities) => capabilities.notifications },
  { id: SettingsSectionId.Updates, title: "settingsUpdatesTitle", summary: "settingsUpdatesSummary", icon: UpdateIcon, available: (capabilities) => capabilities.updates },
] as const satisfies readonly SettingsSectionDefinition[];

export function availableSettingsSections(capabilities: SettingsSectionCapabilities): readonly SettingsSectionDefinition[] {
  return settingsSections.filter((section) => section.available(capabilities));
}
