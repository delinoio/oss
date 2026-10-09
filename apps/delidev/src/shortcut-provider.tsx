// SPDX-License-Identifier: Apache-2.0
import { createContext, useCallback, useContext, useId, useLayoutEffect, useRef, useState, useSyncExternalStore, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type MutableRefObject } from "react";
import { createPortal, flushSync } from "react-dom";
import { copy, useLocale, type MessageKey } from "./localization";
import { DialogSurface } from "./ui";
import { Surface } from "./surface";
import { ShortcutId, ShortcutScope, ShortcutStore, availableShortcutTarget, bindingAria, bindingKeys, bindingMatches, dispatchShortcut, globalShortcutBindings, shortcutModalVisible, shortcutPlatform, type ShortcutDefinition, type ShortcutHelpDispatch, type ShortcutPlatform } from "./shortcuts";
import "./shortcuts.css";
import { useShortcutPreferences } from "./shortcut-preference-controller";
import { fixedNativeShortcutCatalog, customizationBindings, effectiveShortcutDefinitions } from "./shortcut-preferences";

enum HelpLifetime { Held = "held", Button = "button" }
interface HeldKey { code: string; key: string }
interface Controller { store: ShortcutStore; platform: ShortcutPlatform; openHelp: () => void; holdHelp: (event: KeyboardEvent) => void; menu: MutableRefObject<HTMLDialogElement | undefined> }
const Context = createContext<Controller | undefined>(undefined);
export function ShortcutProvider({ children }: { children: ReactNode }) {
  const { snapshot } = useShortcutPreferences();
  const [store] = useState(() => new ShortcutStore());
  useLayoutEffect(() => store.setResolver(definitions => effectiveShortcutDefinitions(definitions, snapshot.overrides)), [store, snapshot.overrides]);
  const [platform] = useState(shortcutPlatform);
  const [helpLifetime, setHelpLifetime] = useState<HelpLifetime | null>(null);
  const lifetime = useRef<HelpLifetime | null>(null), held = useRef<HeldKey | null>(null);
  const closeHelp = useCallback(() => { lifetime.current = null; setHelpLifetime(null); }, []);
  const menu = useRef<HTMLDialogElement | undefined>(undefined);
  const helpDispatch = useRef<ShortcutHelpDispatch | undefined>(undefined);
  const openHelp = useCallback(() => { if (!shortcutModalVisible()) { lifetime.current = HelpLifetime.Button; setHelpLifetime(HelpLifetime.Button); } }, []);
  const holdHelp = useCallback((event: KeyboardEvent) => {
    if (held.current || lifetime.current || shortcutModalVisible()) return;
    held.current = { code: event.code && event.code !== "Unidentified" ? event.code : "", key: event.key.toLowerCase() };
    lifetime.current = HelpLifetime.Held;
    setHelpLifetime(HelpLifetime.Held);
  }, []);
  const [controller] = useState(() => ({ store, platform, openHelp, holdHelp, menu }));
  useLayoutEffect(() => {
    const handle = (event: KeyboardEvent) => { dispatchShortcut(event, store.getSnapshot(), store.surface, platform, false, helpDispatch.current, menu.current); };
    const release = (event: KeyboardEvent) => {
      const origin = held.current;
      if (!origin || (origin.code ? event.code !== origin.code : event.key.toLowerCase() !== origin.key)) return;
      held.current = null;
      if (lifetime.current === HelpLifetime.Held) closeHelp();
    };
    const abandon = () => { held.current = null; if (lifetime.current === HelpLifetime.Held) closeHelp(); };
    const visibility = () => { if (document.visibilityState === "hidden") abandon(); };
    document.addEventListener("keydown", handle);
    document.addEventListener("keyup", release, true);
    window.addEventListener("blur", abandon);
    document.addEventListener("visibilitychange", visibility);
    return () => {
      document.removeEventListener("keydown", handle);
      document.removeEventListener("keyup", release, true);
      window.removeEventListener("blur", abandon);
      document.removeEventListener("visibilitychange", visibility);
      held.current = null;
    };
  }, [store, platform, closeHelp]);
  return <Context.Provider value={controller}>{children}{helpLifetime ? <ShortcutHelp store={store} platform={platform} dispatch={helpDispatch} close={closeHelp} /> : null}</Context.Provider>;
}
export function useShortcutSurface(surface: Surface) {
  const controller = useContext(Context);
  useLayoutEffect(() => { controller?.store.setSurface(surface); }, [controller, surface]);
}
export function useHeldShortcutHelp() { return useContext(Context)?.holdHelp ?? (() => undefined); }
// cmdk uses Control+K as an internal arrow alias. Route only the fixed palette
// chord through this same owner before cmdk can mark that event handled.
export function useCommandMenuKeyDown() {
 const controller = useContext(Context);
 return (event: ReactKeyboardEvent<HTMLElement>) => {
  if (!controller || !globalShortcutBindings[ShortcutId.CommandMenu].some(binding => bindingMatches(event.nativeEvent,binding,controller.platform))) return;
  if (dispatchShortcut(event.nativeEvent,controller.store.getSnapshot(),controller.store.surface,controller.platform,false,undefined,controller.menu.current)) event.stopPropagation();
 };
}
export function useCommandMenuOwner() { return useContext(Context)?.menu; }
export function useShortcutHelp() { return useContext(Context)?.openHelp ?? (() => undefined); }
export function useGlobalShortcutAria(id: keyof typeof globalShortcutBindings) {
  const controller = useContext(Context);
  const { snapshot } = useShortcutPreferences();
  return customizationBindings(id, snapshot.overrides).map(binding => bindingAria(binding, controller?.platform ?? shortcutPlatform())).join(" ");
}
export function useShortcuts(definitions: readonly ShortcutDefinition[]) {
  const controller = useContext(Context);
  const { snapshot } = useShortcutPreferences();
  const resolved = effectiveShortcutDefinitions(definitions, snapshot.overrides);
  const [owner] = useState(() => Symbol("shortcuts"));
  useLayoutEffect(() => { controller?.store.register(owner, definitions); });
  useLayoutEffect(() => () => controller?.store.remove(owner), [controller, owner]);
  const platform = controller?.platform ?? shortcutPlatform();
  return {
    aria: (...ids: ShortcutId[]) => resolved.filter(item => ids.includes(item.id)).flatMap(item => item.bindings.map(binding => bindingAria(binding, platform))).join(" "),
    onKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => {
      // Isolated forms keep their original keyboard behavior without a provider.
      const surface = controller?.store.surface ?? (definitions.find(item => item.scope !== ShortcutScope.Global)?.scope as Surface | undefined) ?? Surface.Sessions;
      if (dispatchShortcut(event.nativeEvent, controller?.store.getSnapshot() ?? resolved, surface, platform, true)) event.stopPropagation();
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
    <div className="shortcut-help-body">{groups.map((group, index) => <section key={index} aria-labelledby={`${id}-group-${index}`}><h3 id={`${id}-group-${index}`}>{index === 0 ? copy("shortcuts.global") : copy(names[store.surface])}</h3>{group.length ? <dl>{group.map(item => <div key={item.id} className={item.enabled === false ? "shortcut-disabled" : undefined}><dt>{copy(item.label)}{item.enabled === false ? <small>{copy(item.unavailableReason ?? "shortcuts.unavailable")}</small> : null}</dt><dd>{!item.bindings.length ? <span>{copy("shortcut-settings.disabled")}</span> : null}{item.bindings.map((binding, bindingIndex) => <span className="shortcut-binding" key={bindingIndex}>{bindingIndex ? <span>{copy("shortcuts.or")}</span> : null}{bindingKeys(binding, platform).map((key, keyIndex) => <kbd key={keyIndex}>{key}</kbd>)}</span>)}</dd></div>)}</dl> : <p>{copy("shortcuts.empty")}</p>}</section>)}<section><h3>{copy("shortcut-settings.fixed")}</h3><dl>{fixedNativeShortcutCatalog.filter(action=>!action.macOnly||platform==="mac").map(action=><div key={action.id}><dt>{copy(action.label)}</dt><dd><span className="shortcut-binding">{bindingKeys({key:action.key,primary:true},platform).map((key,index)=><kbd key={index}>{key}</kbd>)}</span></dd></div>)}</dl></section></div>
    <footer>{platform!=="mac" && definitions.some(item=>item.id===ShortcutId.SessionTab1)?<p>{copy("shortcuts.externalBrowserTabLimit")}</p>:null}<p>{copy("shortcuts.typingHint")}</p><p>{copy("shortcuts.closeHint")} <kbd>Esc</kbd></p></footer>
  </DialogSurface>, document.body);
}

export function useTerminalTabShortcuts() { const controller=useContext(Context);return (event:KeyboardEvent)=>event.type === "keydown" && controller ? dispatchShortcut(event, controller.store.getSnapshot().filter(item=>item.terminal), controller.store.surface,controller.platform):false; }
