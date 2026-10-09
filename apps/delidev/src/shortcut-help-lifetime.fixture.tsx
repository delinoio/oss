// SPDX-License-Identifier: Apache-2.0
import { useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { i18n } from "./localization";
import { ShortcutProvider, useHeldShortcutHelp, useShortcutHelp, useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutScope, ShortcutInput, globalShortcutBindings } from "./shortcuts";
import "./themes.css";
import "./styles.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
document.documentElement.dataset.theme = args.get("theme") ?? "light";
function Fixture() {
  const holdHelp = useHeldShortcutHelp(), openHelp = useShortcutHelp(), destination = useRef<HTMLInputElement>(null);
  const [runs, setRuns] = useState(0);
  useShortcuts([
    { id: ShortcutId.Help, scope: ShortcutScope.Global, label: "shortcuts.help", bindings: globalShortcutBindings[ShortcutId.Help], helpKeydown: holdHelp },
    { id: ShortcutId.NewSession, scope: ShortcutScope.Global, label: "shortcuts.newSession", bindings: globalShortcutBindings[ShortcutId.NewSession], input: ShortcutInput.Allow, run: () => { setRuns(value => value + 1); destination.current?.focus(); } },
  ]);
  return <main id="main" tabIndex={-1}><button data-help onClick={openHelp}>Help</button><input ref={destination} aria-label="Destination" /><output>{runs}</output></main>;
}
createRoot(document.getElementById("root")!).render(<ShortcutProvider><Fixture /></ShortcutProvider>);
