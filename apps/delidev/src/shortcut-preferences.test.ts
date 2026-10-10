// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { captureShortcut, effectiveShortcutDefinitions, parseShortcutOverrides, editableShortcutCatalog, fixedNativeShortcutCatalog, shortcutConflicts, ShortcutOverrideState, validShortcutChord } from "./shortcut-preferences";
import { bindingAria, bindingKeys, bindingMatches, ShortcutId, ShortcutPlatform, ShortcutScope } from "./shortcuts";
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

it.each([ShortcutPlatform.Mac, ShortcutPlatform.Other])("captures one canonical physical chord across input modes on %s", platform => {
 const primary = platform === ShortcutPlatform.Mac ? {metaKey:true} : {ctrlKey:true};
 for (const key of ["t", "ㅅ"]) {
  const event = new KeyboardEvent("keydown", {key,code:"KeyT",...primary});
  expect(captureShortcut(event,platform)).toEqual({key:"t",shift:false});
 }
 expect(captureShortcut(new KeyboardEvent("keydown", {key:"t",code:"KeyJ",...primary}),platform)).toEqual({key:"j",shift:false});
 for(const code of ["", "Unidentified", "Numpad1"]) expect(captureShortcut(new KeyboardEvent("keydown",{key:"t",code,...primary}),platform)).toEqual({key:"t",shift:false});
 for(const options of [{isComposing:true},{keyCode:229},{repeat:true},{altKey:true},{ctrlKey:true,metaKey:true}]) expect(captureShortcut(new KeyboardEvent("keydown",{key:"ㅅ",code:"KeyT",...primary,...options}),platform)).toBeUndefined();
 expect(captureShortcut(new KeyboardEvent("keydown",{key:"ㅏ",code:"KeyK",...primary}),platform)).toBeUndefined();
 expect(captureShortcut(new KeyboardEvent("keydown",{key:"한",code:"Digit1",...primary}),platform)).toBeUndefined();
 const overrides = {[ShortcutId.SessionFocus]:{state:ShortcutOverrideState.Binding,chord:{key:"t",shift:false}}} as const;
 expect(parseShortcutOverrides(overrides)).toEqual(overrides);
 const effective = effectiveShortcutDefinitions([{id:ShortcutId.SessionFocus,scope:Surface.Sessions,label:"shortcuts.focusMessage",bindings:[{key:"i",primary:true}]}],overrides);
 expect(effective[0]!.bindings).toEqual([{key:"t",shift:false,primary:true}]);
 const binding = effective[0]!.bindings[0]!;
 expect(bindingMatches(new KeyboardEvent("keydown",{key:"ㅅ",code:"KeyT",...primary}),binding,platform)).toBe(true);
 expect(bindingKeys(binding,platform)).toEqual([platform===ShortcutPlatform.Mac?"⌘":"Ctrl","T"]);
 expect(bindingAria(binding,platform)).toBe(platform===ShortcutPlatform.Mac?"Meta+T":"Control+T");
});
