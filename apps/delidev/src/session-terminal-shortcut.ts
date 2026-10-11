// SPDX-License-Identifier: Apache-2.0
import { ShortcutId, ShortcutInput, type ShortcutDefinition } from "./shortcuts";
import { Surface } from "./surface";

export const openTerminalsBinding = { key: "`", primary: true } as const;

// Reuse the original tool callback; this registration has no terminal lifecycle
// or request authority and is deliberately excluded from terminal passthrough.
export function openTerminalsShortcut(active: boolean, embedded: boolean, sidechat: boolean, open: () => void): ShortcutDefinition {
  return {
    id: ShortcutId.OpenTerminals, scope: Surface.Sessions,
    label: "shortcuts.openTerminals", bindings: [openTerminalsBinding],
    input: ShortcutInput.Allow, active: active && !embedded, enabled: !sidechat,
    unavailableReason: "shortcuts.terminalsUnavailable", run: open,
  };
}
