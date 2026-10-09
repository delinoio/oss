// SPDX-License-Identifier: Apache-2.0
import { createContext, useCallback, useContext, useId, useLayoutEffect, useRef, useState, useSyncExternalStore, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type MutableRefObject } from "react";
import { createPortal, flushSync } from "react-dom";
import { copy, useLocale, type MessageKey } from "./localization";
import { DialogSurface } from "./ui";
import { Surface } from "./surface";
import { ShortcutId, ShortcutScope, ShortcutStore, availableShortcutTarget, bindingAria, bindingKeys, dispatchShortcut, globalShortcutBindings, shortcutModalVisible, shortcutPlatform, type ShortcutDefinition, type ShortcutHelpDispatch, type ShortcutPlatform } from "./shortcuts";
import "./shortcuts.css";

interface Controller { store: ShortcutStore; platform: ShortcutPlatform; openHelp: () => void }
const Context = createContext<Controller | undefined>(undefined);
export function ShortcutProvider({ children }: { children: ReactNode }) {
  const [store] = useState(() => new ShortcutStore());
  const [platform] = useState(shortcutPlatform);
  const [helpOpen, setHelpOpen] = useState(false);
  const helpDispatch = useRef<ShortcutHelpDispatch | undefined>(undefined);
  const openHelp = useCallback(() => { if (!shortcutModalVisible()) setHelpOpen(true); }, []);
  const [controller] = useState(() => ({ store, platform, openHelp }));
  useLayoutEffect(() => {
    const handle = (event: KeyboardEvent) => { dispatchShortcut(event, store.getSnapshot(), store.surface, platform, false, helpDispatch.current); };
    document.addEventListener("keydown", handle);
    return () => document.removeEventListener("keydown", handle);
  }, [store, platform]);
  return <Context.Provider value={controller}>{children}{helpOpen ? <ShortcutHelp store={store} platform={platform} dispatch={helpDispatch} close={() => setHelpOpen(false)} /> : null}</Context.Provider>;
}
export function useShortcutSurface(surface: Surface) {
  const controller = useContext(Context);
  useLayoutEffect(() => { controller?.store.setSurface(surface); }, [controller, surface]);
}
export function useShortcutHelp() { return useContext(Context)?.openHelp ?? (() => undefined); }
export function useGlobalShortcutAria(id: keyof typeof globalShortcutBindings) {
  const controller = useContext(Context);
  return globalShortcutBindings[id].map(binding => bindingAria(binding, controller?.platform ?? shortcutPlatform())).join(" ");
}
export function useShortcuts(definitions: readonly ShortcutDefinition[]) {
  const controller = useContext(Context);
  const [owner] = useState(() => Symbol("shortcuts"));
  useLayoutEffect(() => { controller?.store.register(owner, definitions); });
  useLayoutEffect(() => () => controller?.store.remove(owner), [controller, owner]);
  const platform = controller?.platform ?? shortcutPlatform();
  return {
    aria: (...ids: ShortcutId[]) => definitions.filter(item => ids.includes(item.id)).flatMap(item => item.bindings.map(binding => bindingAria(binding, platform))).join(" "),
    onKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => {
      // Isolated forms keep their original keyboard behavior without a provider.
      const surface = controller?.store.surface ?? (definitions.find(item => item.scope !== ShortcutScope.Global)?.scope as Surface | undefined) ?? Surface.Sessions;
      if (dispatchShortcut(event.nativeEvent, controller?.store.getSnapshot() ?? definitions, surface, platform, true)) event.stopPropagation();
    },
  };
}
const names: Record<Surface, MessageKey> = {
  [Surface.Sessions]: "sidebar.sessions_6fa3cb", [Surface.NewSession]: "shortcuts.newSession", [Surface.NewGeneralChat]: "shortcuts.newSession", [Surface.Search]: "sidebar.search_49c266", [Surface.Settings]: "sidebar.settings_74a883", [Surface.PullRequests]: "sidebar.pullRequests_d9e3f2", [Surface.Usage]: "sidebar.usage_8d5982", [Surface.Schedules]: "sidebar.schedules_221ff1", [Surface.Activity]: "sidebar.activity_38da15", [Surface.Inbox]: "sidebar.inbox_94835e",
};
function ShortcutHelp({ store, platform, close, dispatch }: { store: ShortcutStore; platform: ShortcutPlatform; close: () => void; dispatch: MutableRefObject<ShortcutHelpDispatch | undefined> }) {
  useLocale();
  const definitions = useSyncExternalStore(store.subscribe, store.getSnapshot);
  const id = useId(), dialog = useRef<HTMLDialogElement>(null), closeButton = useRef<HTMLButtonElement>(null);
  const actionDismissal = useRef(false);
  useLayoutEffect(() => {
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const node = dialog.current!;
    node.showModal();
    const owner = { dialog: node, beforeRun: () => { actionDismissal.current = true; flushSync(close); } };
    dispatch.current = owner;
    closeButton.current?.focus();
    return () => {
      if (dispatch.current === owner) dispatch.current = undefined;
      node.close();
      if (actionDismissal.current) return;
      const usableOpener = availableShortcutTarget(opener) && (opener!.tabIndex >= 0 || opener!.hasAttribute("tabindex") || opener!.isContentEditable);
      const target = usableOpener ? opener : document.querySelector<HTMLElement>("#main");
      if (availableShortcutTarget(target)) target?.focus({ preventScroll: true });
    };
  }, []);
  const groups = [definitions.filter(item => item.scope === ShortcutScope.Global), definitions.filter(item => item.scope !== ShortcutScope.Global)];
  return createPortal(<DialogSurface ref={dialog} className="shortcut-help" aria-modal="true" aria-labelledby={`${id}-title`} aria-describedby={`${id}-screen`} onCancel={event => { event.stopPropagation(); close(); }} onKeyDown={event => {
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); }
    if (event.key === "Tab") { event.preventDefault(); closeButton.current?.focus(); }
  }}>
    <header><div><h2 id={`${id}-title`}>{copy("shortcuts.title")}</h2><p id={`${id}-screen`}>{copy("shortcuts.currentScreen", { name: copy(names[store.surface]) })}</p></div><button ref={closeButton} type="button" aria-label={copy("shortcuts.close")} onClick={close}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg></button></header>
    <div className="shortcut-help-body">{groups.map((group, index) => <section key={index} aria-labelledby={`${id}-group-${index}`}><h3 id={`${id}-group-${index}`}>{index === 0 ? copy("shortcuts.global") : copy(names[store.surface])}</h3>{group.length ? <dl>{group.map(item => <div key={item.id} className={item.enabled === false ? "shortcut-disabled" : undefined}><dt>{copy(item.label)}{item.enabled === false ? <small>{copy(item.unavailableReason ?? "shortcuts.unavailable")}</small> : null}</dt><dd>{item.bindings.map((binding, bindingIndex) => <span className="shortcut-binding" key={bindingIndex}>{bindingIndex ? <span>{copy("shortcuts.or")}</span> : null}{bindingKeys(binding, platform).map((key, keyIndex) => <kbd key={keyIndex}>{key}</kbd>)}</span>)}</dd></div>)}</dl> : <p>{copy("shortcuts.empty")}</p>}</section>)}</div>
    <footer><p>{copy("shortcuts.typingHint")}</p><p>{copy("shortcuts.closeHint")} <kbd>Esc</kbd></p></footer>
  </DialogSurface>, document.body);
}
