import { LocalAgentSettings } from "./local-agent-settings-ui";
import { ShortcutPaletteTrigger, ShortcutSettings, SynchronizedShortcutBoundary } from "./shortcut-settings-ui";
import { DesktopUpdaterPanel } from "./updater-ui";

export const desktopSettingsIntegration = {
  ShortcutBoundary: SynchronizedShortcutBoundary,
  ShortcutPaletteTrigger,
  sections: {
    Shortcuts: ShortcutSettings,
    LocalAgents: LocalAgentSettings,
    Updates: DesktopUpdaterPanel,
  },
} as const;
