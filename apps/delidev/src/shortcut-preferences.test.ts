// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { customizationBindings, defaultShortcutSuppressed, captureShortcut, effectiveShortcutDefinitions, parseShortcutOverrides, editableShortcutCatalog, readOnlyShortcutCatalog, fixedNativeShortcutCatalog, shortcutConflicts, ShortcutOverrideState, validShortcutChord } from "./shortcut-preferences";
import { ShortcutId, ShortcutPlatform, ShortcutScope } from "./shortcuts";
import { Surface } from "./surface";
const chord = {state:ShortcutOverrideState.Binding,chord:{key:"j",shift:true}} as const;
it("replaces only customizable primary chords while preserving creation Enter",()=>{
 const original=[{id:ShortcutId.NewSessionSend,scope:Surface.NewSession,label:"shortcuts.createSession" as const,bindings:[{key:"Enter"},{key:"Enter",primary:true}]}];
 expect(effectiveShortcutDefinitions(original,{[ShortcutId.NewSessionSend]:chord})[0]!.bindings).toEqual([{key:"Enter"},{key:"j",shift:true,primary:true}]);
 expect(effectiveShortcutDefinitions(original,{[ShortcutId.NewSessionSend]:{state:ShortcutOverrideState.Disabled}})[0]!.bindings).toEqual([{key:"Enter"}]);
 expect(effectiveShortcutDefinitions([{id:ShortcutId.Help,scope:ShortcutScope.Global,label:"shortcuts.help",bindings:[{key:"?"}]}],{[ShortcutId.Help]:{state:ShortcutOverrideState.Disabled}})[0]!.bindings).toEqual([]);
});
it("validates inactive catalog scopes without rejecting exclusive screen reuse",()=>{
 expect(shortcutConflicts({[ShortcutId.Help]:chord,[ShortcutId.NewSession]:chord})).toEqual([[ShortcutId.Help,ShortcutId.NewSession]]);
 expect(shortcutConflicts({[ShortcutId.SessionFocus]:chord,[ShortcutId.SearchFocus]:chord})).toEqual([]);
 expect(()=>parseShortcutOverrides({[ShortcutId.Help]:{state:"binding",chord:{key:"n",shift:true}}})).toThrow("Conflicting");
});
it("rejects fixed product and editing reservations and closed-schema violations",()=>{
 for(const key of ["k","n","w","1","9","c","v","Escape","é","Tab"])expect(validShortcutChord({key,shift:false})).toBe(false);
 expect(()=>parseShortcutOverrides({unknown:{state:"disabled"}})).toThrow();expect(()=>parseShortcutOverrides({help:{state:"disabled",extra:true}})).toThrow();
 expect(validShortcutChord({key:"j",shift:true})).toBe(true);
 expect(validShortcutChord({key:"v",shift:true})).toBe(false);
 expect(validShortcutChord({key:"z",shift:true})).toBe(false);
});
it("capture requires the renderer primary and rejects unsafe input without matching actions",()=>{
 expect(captureShortcut(new KeyboardEvent("keydown",{key:"J",metaKey:true,shiftKey:true}),ShortcutPlatform.Mac)).toEqual({key:"j",shift:true});
 for(const input of [{key:"j"},{key:"k",ctrlKey:true},{key:"j",ctrlKey:true,repeat:true},{key:"j",ctrlKey:true,isComposing:true},{key:"j",ctrlKey:true,altKey:true},{key:"j",ctrlKey:true,metaKey:true}])expect(captureShortcut(new KeyboardEvent("keydown",input),ShortcutPlatform.Other)).toBeUndefined();
});

it("rejects fixed native menu chords throughout the complete editable map",()=>{for(const action of editableShortcutCatalog)for(const key of ["q","h","m","n","w"]){expect(()=>parseShortcutOverrides({[action.id]:{state:ShortcutOverrideState.Binding,chord:{key,shift:false}}})).toThrow();expect(validShortcutChord({key,shift:true})).toBe(true);}});

it("keeps native menu bindings in a typed read-only catalog separate from seven editable IDs",()=>{
 expect(editableShortcutCatalog).toHaveLength(7);expect(fixedNativeShortcutCatalog.map(action=>action.key)).toEqual(["n","w","q","h","m"]);
 for(const native of fixedNativeShortcutCatalog){expect(editableShortcutCatalog.some(action=>String(action.id)===String(native.id))).toBe(false);for(const action of editableShortcutCatalog)expect(()=>parseShortcutOverrides({[action.id]:{state:ShortcutOverrideState.Binding,chord:{key:native.key,shift:false}}})).toThrow();}
});

it("permits the retired New Window T chord for all seven overrides and capture platforms",()=>{
 for(const action of editableShortcutCatalog){const overrides={[action.id]:{state:ShortcutOverrideState.Binding,chord:{key:"t",shift:false}}};expect(parseShortcutOverrides(overrides)).toEqual(overrides);}
 for(const platform of [ShortcutPlatform.Mac,ShortcutPlatform.Other]){const modifier=platform===ShortcutPlatform.Mac?{metaKey:true}:{ctrlKey:true};expect(captureShortcut(new KeyboardEvent("keydown",{key:"t",...modifier}),platform)).toEqual({key:"t",shift:false});expect(captureShortcut(new KeyboardEvent("keydown",{key:"n",...modifier}),platform)).toBeUndefined();}
});

it("retains fixed Session Enter with default, custom and disabled primary overrides", () => {
 const definitions = [{id:ShortcutId.SessionSend,scope:Surface.Sessions,label:"shortcuts.queueMessage" as const,bindings:[{key:"Enter"},{key:"Enter",primary:true}]}];
 expect(effectiveShortcutDefinitions(definitions,{})[0]!.bindings).toEqual([{key:"Enter"},{key:"Enter",primary:true}]);
 expect(effectiveShortcutDefinitions(definitions,{[ShortcutId.SessionSend]:chord})[0]!.bindings).toEqual([{key:"Enter"},{key:"j",shift:true,primary:true}]);
 expect(effectiveShortcutDefinitions(definitions,{[ShortcutId.SessionSend]:{state:ShortcutOverrideState.Disabled}})[0]!.bindings).toEqual([{key:"Enter"}]);
 expect(editableShortcutCatalog.find(action=>action.id===ShortcutId.SessionSend)?.fixed).toEqual([{key:"Enter"}]);
 expect(readOnlyShortcutCatalog.find(action=>action.id===ShortcutId.SessionNewline)?.defaults).toEqual([{key:"Enter",shift:true}]);
 expect(editableShortcutCatalog).toHaveLength(7);
});


it.each([ShortcutPlatform.Mac, ShortcutPlatform.Other])("captures physical chords without preference migration on %s", platform => {
  const modifiers = platform === ShortcutPlatform.Mac ? { metaKey: true } : { ctrlKey: true };
  for (const [key, code, canonical, shiftKey] of [["t", "KeyT", "t", false], ["ㅅ", "KeyT", "t", false], ["k", "KeyJ", "j", false], ["!", "Digit1", "1", true], ["1", "Numpad1", "1", true], ["j", "Unidentified", "j", false], ["j", "", "j", false], ["Enter", "Enter", "Enter", false]] as const) {
    const captured = captureShortcut(new KeyboardEvent("keydown", { key, code, shiftKey, ...modifiers }), platform);
    expect(captured).toEqual({ key: canonical, shift: shiftKey });
    const persisted = { [ShortcutId.SessionFocus]: { state: ShortcutOverrideState.Binding, chord: captured } };
    expect(parseShortcutOverrides(persisted)).toEqual(persisted);
  }
  for (const flags of [{ isComposing: true }, { keyCode: 229 }, { repeat: true }, { altKey: true }, { ctrlKey: true, metaKey: true }]) {
    expect(captureShortcut(new KeyboardEvent("keydown", { key: "ㅅ", code: "KeyT", ...modifiers, ...flags }), platform)).toBeUndefined();
  }
  const altGraph = new KeyboardEvent("keydown", { key: "ㅅ", code: "KeyT", ...modifiers });
  vi.spyOn(altGraph, "getModifierState").mockImplementation(key => key === "AltGraph");
  expect(captureShortcut(altGraph, platform)).toBeUndefined();
  expect(captureShortcut(new KeyboardEvent("keydown", { key: "ㅏ", code: "KeyK", ...modifiers }), platform)).toBeUndefined();
});


it("lets every saved T override suppress only the new default across all scopes without rewriting preferences", () => {
 const original = [{ id: ShortcutId.OpenTool, scope: Surface.Sessions, label: "session.openTool" as const, bindings: [{ key: "t", primary: true }] }];
 for (const editable of editableShortcutCatalog) {
  const overrides = { [editable.id]: { state: ShortcutOverrideState.Binding, chord: { key: "t", shift: false } } } as const;
  const serialized = JSON.stringify(overrides);
  expect(parseShortcutOverrides(overrides)).toEqual(overrides);
  expect(defaultShortcutSuppressed(ShortcutId.OpenTool, overrides)).toBe(true);
  expect(customizationBindings(ShortcutId.OpenTool, overrides)).toEqual([]);
  expect(effectiveShortcutDefinitions(original, overrides)[0]).toMatchObject({ bindings: [], enabled: false, unavailableReason: "shortcuts.customBindingPriority" });
  expect(JSON.stringify(overrides)).toBe(serialized);
  for (const alternate of [{ state: ShortcutOverrideState.Disabled }, { state: ShortcutOverrideState.Binding, chord: { key: "t", shift: true } }, { state: ShortcutOverrideState.Binding, chord: { key: "j", shift: false } }] as const) {
   expect(defaultShortcutSuppressed(ShortcutId.OpenTool, { [editable.id]: alternate })).toBe(false);
  }
 }
 expect(customizationBindings(ShortcutId.OpenTool, {})).toEqual([{ key: "t", primary: true }]);
 expect(editableShortcutCatalog).toHaveLength(7);
 expect(() => parseShortcutOverrides({ [ShortcutId.OpenTool]: { state: ShortcutOverrideState.Disabled } })).toThrow();
});
