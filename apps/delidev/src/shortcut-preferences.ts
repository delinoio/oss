// SPDX-License-Identifier: Apache-2.0
import { globalShortcutBindings, ShortcutId, ShortcutInput, ShortcutPlatform, ShortcutScope, type ShortcutBinding, type ShortcutDefinition } from "./shortcuts";
import { Surface } from "./surface";
import type { MessageKey } from "./localization";

export enum ShortcutOverrideState { Disabled = "disabled", Binding = "binding" }
export interface ShortcutChord { key: string; shift: boolean }
export type ShortcutOverride = { state: ShortcutOverrideState.Disabled } | { state: ShortcutOverrideState.Binding; chord: ShortcutChord };
export type ShortcutOverrides = Partial<Record<ShortcutId, ShortcutOverride>>;
export enum ShortcutGroup { Common = "common", Session = "session", Creation = "creation", Search = "search" }
export enum ShortcutTargetContext { SessionMessage = "session-message", CreationMessage = "creation-message", Search = "search", Files = "files", Diff = "diff", Diagnostics = "diagnostics" }
interface CatalogAction { target?: ShortcutTargetContext; id: ShortcutId; label: MessageKey; group: ShortcutGroup; scopes: readonly (Surface | ShortcutScope)[]; priority: number; input: ShortcutInput; defaults: readonly ShortcutBinding[] }
export const editableShortcutCatalog: readonly CatalogAction[] = [
  { id: ShortcutId.Help, label: "shortcuts.help", group: ShortcutGroup.Common, scopes: [ShortcutScope.Global], priority: 0, input: ShortcutInput.Ignore, defaults: [{ key: "?", ariaKey: "/", ariaShift: true }] },
  { id: ShortcutId.NewSession, label: "shortcuts.newSession", group: ShortcutGroup.Common, scopes: [ShortcutScope.Global], priority: 0, input: ShortcutInput.Allow, defaults: [{ key: "n", primary: true, shift: true }] },
  { id: ShortcutId.SessionFocus, label: "shortcuts.focusMessage", group: ShortcutGroup.Session, scopes: [Surface.Sessions], priority: 1, input: ShortcutInput.Allow, defaults: [{ key: "i", primary: true }] },
  { target: ShortcutTargetContext.SessionMessage, id: ShortcutId.SessionSend, label: "shortcuts.queueMessage", group: ShortcutGroup.Session, scopes: [Surface.Sessions], priority: 2, input: ShortcutInput.Target, defaults: [{ key: "Enter", primary: true }] },
  { id: ShortcutId.NewSessionFocus, label: "shortcuts.focusFirstMessage", group: ShortcutGroup.Creation, scopes: [Surface.NewSession, Surface.NewGeneralChat], priority: 1, input: ShortcutInput.Allow, defaults: [{ key: "i", primary: true }] },
  { target: ShortcutTargetContext.CreationMessage, id: ShortcutId.NewSessionSend, label: "shortcuts.createSession", group: ShortcutGroup.Creation, scopes: [Surface.NewSession, Surface.NewGeneralChat], priority: 2, input: ShortcutInput.Target, defaults: [{ key: "Enter", primary: true }] },
  { id: ShortcutId.SearchFocus, label: "shortcuts.focusSearch", group: ShortcutGroup.Search, scopes: [Surface.Search], priority: 1, input: ShortcutInput.Allow, defaults: [{ key: "i", primary: true }] },
];
export const readOnlyShortcutCatalog: readonly CatalogAction[] = [
  { id: ShortcutId.CommandMenu, label: "command-menu.title", group: ShortcutGroup.Common, scopes: [ShortcutScope.Global], priority: 0, input: ShortcutInput.Allow, defaults: globalShortcutBindings[ShortcutId.CommandMenu] },
  { target: ShortcutTargetContext.SessionMessage, id: ShortcutId.SessionNewline, label: "shortcuts.newline", group: ShortcutGroup.Session, scopes: [Surface.Sessions], priority: 2, input: ShortcutInput.Target, defaults: [{ key: "Enter" }] },
  { target: ShortcutTargetContext.CreationMessage, id: ShortcutId.NewSessionNewline, label: "shortcuts.newline", group: ShortcutGroup.Creation, scopes: [Surface.NewSession, Surface.NewGeneralChat], priority: 2, input: ShortcutInput.Target, defaults: [{ key: "Enter", shift: true }] },
  { target: ShortcutTargetContext.Search, id: ShortcutId.SearchSubmit, label: "shortcuts.searchSubmit", group: ShortcutGroup.Search, scopes: [Surface.Search], priority: 2, input: ShortcutInput.Target, defaults: [{ key: "Enter" }] },
  ...[ShortcutId.FilesClose, ShortcutId.DiffClose, ShortcutId.DiagnosticsClose].map((id, index) => ({ target: ([ShortcutTargetContext.Files, ShortcutTargetContext.Diff, ShortcutTargetContext.Diagnostics] as const)[index]!, id, label: (["shortcuts.closeFiles", "shortcuts.closeDiff", "shortcuts.closeDiagnostics"] as const)[index]!, group: ShortcutGroup.Session, scopes: [Surface.Sessions], priority: 2, input: ShortcutInput.Target, defaults: [{ key: "Escape" }] })),
];
export const shortcutCatalog = [...editableShortcutCatalog, ...readOnlyShortcutCatalog];
// Native menu bindings are read-only and never enter the seven override IDs.
export enum NativeShortcutId { NewWindow="native-new-window", CloseWindow="native-close-window", Quit="native-quit", Hide="native-hide", Minimize="native-minimize" }
export const fixedNativeShortcutCatalog: readonly {id:NativeShortcutId;label:MessageKey;key:string;macOnly:boolean}[]=[
 {id:NativeShortcutId.NewWindow,label:"shortcuts.nativeNewWindow",key:"n",macOnly:false},
 {id:NativeShortcutId.CloseWindow,label:"shortcuts.nativeCloseWindow",key:"w",macOnly:false},
 {id:NativeShortcutId.Quit,label:"shortcuts.nativeQuit",key:"q",macOnly:true},
 {id:NativeShortcutId.Hide,label:"shortcuts.nativeHide",key:"h",macOnly:true},
 {id:NativeShortcutId.Minimize,label:"shortcuts.nativeMinimize",key:"m",macOnly:false},
];
const nativeReservedKeys=new Set(fixedNativeShortcutCatalog.map(action=>action.key));
const editableIds = new Set(editableShortcutCatalog.map(action => action.id));
export function validShortcutChord(chord: unknown): chord is ShortcutChord {
  if (!chord || typeof chord !== "object" || Array.isArray(chord)) return false;
  const value = chord as Record<string, unknown>;
  if (Object.keys(value).sort().join(",") !== "key,shift" || typeof value.shift !== "boolean" || typeof value.key !== "string" || !/^(?:[a-z0-9]|Enter)$/.test(value.key)) return false;
  // Fixed product and native editing chords remain unavailable for rebinding.
  return value.shift ? !/^[vz]$/.test(value.key) : !nativeReservedKeys.has(value.key) && !/^[kn1-9acvxyz]$/.test(value.key);
}
export function customizationBindings(id: ShortcutId, overrides: ShortcutOverrides): readonly ShortcutBinding[] {
  const override = overrides[id];
  return override?.state === ShortcutOverrideState.Disabled ? [] : override?.state === ShortcutOverrideState.Binding ? [{ ...override.chord, primary: true }] : shortcutCatalog.find(action => action.id === id)?.defaults ?? [];
}
export function effectiveShortcutDefinitions(definitions: readonly ShortcutDefinition[], overrides: ShortcutOverrides): ShortcutDefinition[] {
  return definitions.map(action => !editableIds.has(action.id) ? action : { ...action, bindings: [...action.bindings.filter(binding => !binding.primary && binding.key !== "?"), ...customizationBindings(action.id, overrides)] });
}
export function shortcutConflicts(overrides: ShortcutOverrides): [ShortcutId, ShortcutId][] {
  const conflicts: [ShortcutId, ShortcutId][] = [];
  for (let a = 0; a < shortcutCatalog.length; a++) for (let b = a + 1; b < shortcutCatalog.length; b++) {
    const first = shortcutCatalog[a]!, second = shortcutCatalog[b]!;
    if (first.priority !== second.priority || Boolean(first.target && second.target && first.target !== second.target) || !first.scopes.some(scope => second.scopes.includes(scope))) continue;
    if (customizationBindings(first.id, overrides).some(left => customizationBindings(second.id, overrides).some(right => left.key === right.key && Boolean(left.primary) === Boolean(right.primary) && Boolean(left.shift) === Boolean(right.shift)))) conflicts.push([first.id, second.id]);
  }
  return conflicts;
}
export function parseShortcutOverrides(value: unknown): ShortcutOverrides {
  if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).length > 7) throw new Error("Invalid shortcut preferences");
  for (const [id, raw] of Object.entries(value)) {
    if (!editableIds.has(id as ShortcutId) || !raw || typeof raw !== "object" || Array.isArray(raw)) throw new Error("Invalid shortcut action");
    const entry = raw as Record<string, unknown>;
    if (entry.state === ShortcutOverrideState.Disabled ? Object.keys(entry).join(",") !== "state" : entry.state !== ShortcutOverrideState.Binding || Object.keys(entry).sort().join(",") !== "chord,state" || !validShortcutChord(entry.chord)) throw new Error("Invalid shortcut override");
  }
  if (shortcutConflicts(value as ShortcutOverrides).length) throw new Error("Conflicting shortcuts");
  return Object.fromEntries(Object.entries(value).sort(([a], [b]) => a.localeCompare(b))) as ShortcutOverrides;
}
export function captureShortcut(event: KeyboardEvent, platform: ShortcutPlatform): ShortcutChord | undefined {
  if (event.isComposing || event.keyCode === 229 || event.repeat || event.altKey || event.getModifierState("AltGraph") || event.metaKey !== (platform === ShortcutPlatform.Mac) || event.ctrlKey !== (platform === ShortcutPlatform.Other)) return;
  const chord = { key: event.key === "Enter" ? "Enter" : event.key.toLowerCase(), shift: event.shiftKey };
  return validShortcutChord(chord) ? chord : undefined;
}
