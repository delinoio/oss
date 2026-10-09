// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { captureShortcut, effectiveShortcutDefinitions, parseShortcutOverrides, editableShortcutCatalog, shortcutConflicts, ShortcutOverrideState, validShortcutChord } from "./shortcut-preferences";
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

it("rejects fixed native menu chords throughout the complete editable map",()=>{for(const action of editableShortcutCatalog)for(const key of ["q","h","m","t","w"]){expect(()=>parseShortcutOverrides({[action.id]:{state:ShortcutOverrideState.Binding,chord:{key,shift:false}}})).toThrow();expect(validShortcutChord({key,shift:true})).toBe(true);}});
