// SPDX-License-Identifier: Apache-2.0
import type { RefObject } from "react";
import type { MessageKey } from "./localization";
import { Surface } from "./surface";

export enum ShortcutScope { Global = "global" }
export enum ShortcutId { CommandMenu = "command-menu", Help = "help", NewSession = "new-session", SessionFocus = "session-focus", SessionSend = "session-send", SessionNewline = "session-newline", NewSessionFocus = "new-session-focus", NewSessionSend = "new-session-send", NewSessionNewline = "new-session-newline", SearchFocus = "search-focus", SearchSubmit = "search-submit", FilesClose = "files-close", DiffClose = "diff-close", DiagnosticsClose = "diagnostics-close" }
export enum ShortcutInput { Ignore = "ignore", Allow = "allow", Target = "target" }
export enum ShortcutExecution { Action = "action", Native = "native" }
export enum ShortcutPlatform { Mac = "mac", Other = "other" }
export const globalShortcutBindings = {
  [ShortcutId.CommandMenu]: [{ key: "k", primary: true }],
  [ShortcutId.Help]: [{ key: "?", ariaKey: "/", ariaShift: true }],
  [ShortcutId.NewSession]: [{ key: "n", primary: true, shift: true }],
} as const;
export interface ShortcutBinding { key: string; primary?: boolean; shift?: boolean; ariaKey?: string; ariaShift?: boolean }
export interface ShortcutDefinition {
  id: ShortcutId;
  scope: Surface | ShortcutScope;
  label: MessageKey;
  bindings: readonly ShortcutBinding[];
  active?: boolean;
  enabled?: boolean;
  unavailableReason?: MessageKey;
  input?: ShortcutInput;
  target?: RefObject<HTMLElement | null>;
  execution?: ShortcutExecution;
  run?: () => void;
  helpKeydown?: (event: KeyboardEvent) => void;
}

export function shortcutPlatform(): ShortcutPlatform {
  return /mac/i.test(navigator.platform) ? ShortcutPlatform.Mac : ShortcutPlatform.Other;
}
export function bindingMatches(event: KeyboardEvent, binding: ShortcutBinding, platform: ShortcutPlatform): boolean {
  if (event.altKey || event.getModifierState?.("AltGraph")) return false;
  if (event.metaKey !== Boolean(binding.primary && platform === ShortcutPlatform.Mac) || event.ctrlKey !== Boolean(binding.primary && platform === ShortcutPlatform.Other)) return false;
  // '?' is a logical character, independently of its keyboard-layout modifier.
  if (binding.key !== "?" && event.shiftKey !== Boolean(binding.shift)) return false;
  return event.key.toLowerCase() === binding.key.toLowerCase();
}
export function bindingKeys(binding: ShortcutBinding, platform: ShortcutPlatform): string[] {
  return [...(binding.primary ? [platform === ShortcutPlatform.Mac ? "⌘" : "Ctrl"] : []), ...(binding.shift ? [platform === ShortcutPlatform.Mac ? "⇧" : "Shift"] : []), binding.key.length === 1 && binding.key !== "?" ? binding.key.toUpperCase() : binding.key === "Escape" ? "Esc" : binding.key];
}
export function bindingAria(binding: ShortcutBinding, platform: ShortcutPlatform): string {
  // ARIA names the physical chord on English/Korean slash-key layouts; logical
  // character matching remains independent of this presentation annotation.
  const key = binding.ariaKey ?? binding.key;
  return [...(binding.primary ? [platform === ShortcutPlatform.Mac ? "Meta" : "Control"] : []), ...((binding.ariaShift ?? binding.shift) ? ["Shift"] : []), key.length === 1 ? key.toUpperCase() : key].join("+");
}
export function availableShortcutTarget(node: HTMLElement | null): boolean {
  if (!node?.isConnected || node.closest('[hidden], [inert], [aria-hidden="true"]') || node.matches(":disabled")) return false;
  if (node.closest("dialog") && !node.closest<HTMLDialogElement>("dialog")!.open) return false;
  const style = getComputedStyle(node);
  return style.display !== "none" && style.visibility !== "hidden";
}
export function shortcutModalVisible(except?: HTMLDialogElement): boolean {
  return [...document.querySelectorAll<HTMLElement>('dialog[open]:not([role="region"]), [aria-modal="true"]')].some(node => node !== except && availableShortcutTarget(node));
}
function editable(node: Element): boolean {
  return Boolean(node.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"], [role="combobox"]'));
}
export interface ShortcutHelpDispatch { dialog: HTMLDialogElement; beforeRun: () => void }
export function dispatchShortcut(event: KeyboardEvent, definitions: readonly ShortcutDefinition[], surface: Surface, platform: ShortcutPlatform, local = false, help?: ShortcutHelpDispatch, menu?: HTMLDialogElement): boolean {
  if (event.defaultPrevented || event.isComposing || event.keyCode === 229 || event.repeat || event.getModifierState?.("AltGraph")) return false;
  const node = event.target instanceof Element ? event.target : document.activeElement;
  if (!node || node.closest('[data-shortcuts="passthrough"], [hidden], [inert]')) return false;
  const fromMenu = Boolean(menu?.open && menu.contains(node));
  const fromHelp = Boolean(help?.dialog.open && help.dialog.contains(node));
  if (shortcutModalVisible(fromMenu ? menu : fromHelp ? help!.dialog : undefined)) return false;
  const candidates = definitions.filter(item => item.active !== false && (!fromMenu || item.id === ShortcutId.CommandMenu) && (item.scope === ShortcutScope.Global || item.scope === surface) && (!local || item.target) && (!fromHelp || (!item.target && item.id !== ShortcutId.CommandMenu)) && (!item.target || (availableShortcutTarget(item.target.current) && item.target.current!.contains(node))) && (!editable(node) || item.input === ShortcutInput.Allow || (item.input === ShortcutInput.Target && item.target?.current?.contains(node))) && item.bindings.some(binding => bindingMatches(event, binding, platform)));
  const priority = (item: ShortcutDefinition) => item.target ? 2 : item.scope === ShortcutScope.Global ? 0 : 1;
  const highest = Math.max(-1, ...candidates.map(priority));
  const matches = candidates.filter(item => priority(item) === highest);
  if (!matches.length) return false;
  if (matches.length > 1) {
    console.warn({ event: "delidev_shortcut_conflict", scope: matches[0].scope, actionIds: matches.map(item => item.id) });
    event.preventDefault();
    return true;
  }
  const item = matches[0];
  if (item.execution === ShortcutExecution.Native) return false;
  // A disabled matching action cannot fall through to a broader action or submit.
  event.preventDefault();
  if (item.enabled !== false && (item.run || (item.id === ShortcutId.Help && item.helpKeydown))) {
    // Help remains the current modal for its own shortcut and rejected actions.
    // Closing before the callback releases the native inert/focus boundary.
    if (fromHelp && item.id !== ShortcutId.Help) help!.beforeRun();
    if (item.id === ShortcutId.Help && item.helpKeydown) item.helpKeydown(event);
    else item.run?.();
  }
  return true;
}

export class ShortcutStore {
  surface = Surface.Sessions;
  private entries = new Map<symbol, readonly ShortcutDefinition[]>();
  private listeners = new Set<() => void>();
  private snapshot: readonly ShortcutDefinition[] = [];
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  getSnapshot = () => this.snapshot;
  setSurface(surface: Surface) { if (surface !== this.surface) { this.surface = surface; this.publish(); } }
  register(owner: symbol, definitions: readonly ShortcutDefinition[]) { this.entries.set(owner, definitions); this.publish(); }
  remove(owner: symbol) { this.entries.delete(owner); this.publish(); }
  setResolver(resolve: (definitions: readonly ShortcutDefinition[]) => readonly ShortcutDefinition[]) { this.resolve = resolve; this.publish(); }
  private resolve = (definitions: readonly ShortcutDefinition[]): readonly ShortcutDefinition[] => definitions;
  private publish() { this.snapshot = this.resolve( [...this.entries.values()].flat().filter(item => item.active !== false && (item.scope === ShortcutScope.Global || item.scope === this.surface))); for (const listener of this.listeners) listener(); }
}
