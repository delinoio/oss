// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useRef } from "react";
import { SessionToolMenu } from "./session-tool-menu";
import { ShortcutProvider, useShortcutHelp, useShortcutSurface, useShortcuts } from "./shortcut-provider";
import { ShortcutPreferenceProvider, type ShortcutPreferenceBridge } from "./shortcut-preference-controller";
import { ShortcutId, ShortcutInput } from "./shortcuts";
import { ShortcutOverrideState, type ShortcutOverrides } from "./shortcut-preferences";
import { ShortcutSettings } from "./shortcut-settings";
import { Surface } from "./surface";
import { i18n } from "./localization";
afterEach(async () => { vi.restoreAllMocks(); await i18n.changeLanguage("en"); });
async function fixture(platform = "Win32", overrides: ShortcutOverrides = {}, settings = false) {
 vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
 let changed!: (value: unknown) => void;
 const bridge: ShortcutPreferenceBridge = {read:vi.fn(async()=>({revision:1,overrides,problem:null})),update:vi.fn(),subscribe:async listener=>{changed=listener;return ()=>{};}};
 const selected = vi.fn(), custom = vi.fn();
 function Consumer({active=true,owner="original",surface=Surface.Sessions}:{active?:boolean;owner?:string;surface?:Surface}) {
  useShortcutSurface(surface); const help=useShortcutHelp(), target=useRef<HTMLButtonElement>(null);
  useShortcuts([{id:ShortcutId.SessionFocus,scope:Surface.Sessions,label:"shortcuts.focusMessage",bindings:[{key:"i",primary:true}],input:ShortcutInput.Allow,run:custom}]);
  return <main id="main" tabIndex={-1}><textarea aria-label="Original draft" defaultValue="Keep the composer draft"/><SessionToolMenu active={active} owner={owner}>{["Diff","Files","Terminals","Browser","Diagnostics"].map(name=><button key={name} role="menuitem" disabled={name==="Terminals"} onClick={()=>{expect(document.querySelector('dialog[open]')).toBeNull();selected(name);target.current?.focus();}}>{name}</button>)}</SessionToolMenu><button ref={target}>Destination</button><button onClick={help}>Help</button>{settings?<ShortcutSettings/>:null}</main>;
 }
 const view = (props: Parameters<typeof Consumer>[0] = {}) => <ShortcutPreferenceProvider bridge={bridge}><ShortcutProvider><Consumer {...props}/></ShortcutProvider></ShortcutPreferenceProvider>;
 const rendered=render(view()); await waitFor(()=>expect(bridge.read).toHaveBeenCalled()); await act(async()=>{});
 return {selected,custom,bridge,rendered,view,publish:(overrides:ShortcutOverrides,revision=2)=>act(()=>changed({revision,overrides,problem:null}))};
}
it.each(["MacIntel","Win32","Linux x86_64"])("button and renderer-primary T open the same modal without tool action in %s",async platform=>{
 const f=await fixture(platform),input=screen.getByRole("textbox",{name:"Original draft"}),trigger=screen.getByRole("button",{name:"Open tool"}); input.focus();
 const modifier=platform==="MacIntel"?{metaKey:true}:{ctrlKey:true};
 fireEvent.keyDown(input,{key:"t",...modifier}); expect(screen.getAllByRole("dialog")).toHaveLength(1);expect(f.selected).not.toHaveBeenCalled();
 expect(document.activeElement).toBe(screen.getByRole("menuitem",{name:"Diff"})); expect(trigger.getAttribute("aria-keyshortcuts")).toBe(platform==="MacIntel"?"Meta+T":"Control+T");
 fireEvent.keyDown(document.activeElement!,{key:"Escape"});expect(document.activeElement).toBe(input);expect(input).toHaveProperty("value","Keep the composer draft");
 fireEvent.click(trigger);expect(screen.getAllByRole("dialog")).toHaveLength(1);fireEvent.click(screen.getByRole("button",{name:"Close Open tool"}));expect(document.activeElement).toBe(trigger);
 fireEvent.keyDown(input,{key:"t",...(platform==="MacIntel"?{ctrlKey:true}:{metaKey:true})});expect(screen.queryByRole("dialog")).toBeNull();expect(f.selected).not.toHaveBeenCalled();
});
it.each(["Diff","Files","Browser","Diagnostics"])("closes before the original %s callback exactly once and preserves destination focus",async name=>{
 const f=await fixture();fireEvent.click(screen.getByRole("button",{name:"Open tool"}));fireEvent.click(screen.getByRole("menuitem",{name}));expect(f.selected).toHaveBeenCalledExactlyOnceWith(name);expect(document.activeElement).toBe(screen.getByRole("button",{name:"Destination"}));
});
it("contains Tab and skips disabled entries without activating them",async()=>{
 const f=await fixture();fireEvent.click(screen.getByRole("button",{name:"Open tool"}));const terminals=screen.getByRole("menuitem",{name:"Terminals"});fireEvent.click(terminals);expect(f.selected).not.toHaveBeenCalled();
 fireEvent.keyDown(screen.getByRole("menuitem",{name:"Diff"}),{key:"End"});const last=screen.getByRole("menuitem",{name:"Diagnostics"});expect(document.activeElement).toBe(last);fireEvent.keyDown(last,{key:"Tab"});expect(document.activeElement).toBe(screen.getByRole("button",{name:"Close Open tool"}));fireEvent.keyDown(document.activeElement!,{key:"Tab",shiftKey:true});expect(document.activeElement).toBe(last);
});
it.each([ShortcutId.SessionFocus,ShortcutId.SearchFocus])("committed custom T suppresses default consistently across scopes: %s",async id=>{
 const overrides={[id]:{state:ShortcutOverrideState.Binding,chord:{key:"t",shift:false}}} as const;
 const f=await fixture("Win32",overrides,true),trigger=screen.getByRole("button",{name:"Open tool"});expect(trigger.hasAttribute("aria-keyshortcuts")).toBe(false);
 expect(screen.getByText(/A saved custom shortcut uses/)).toBeTruthy();fireEvent.keyDown(screen.getByRole("textbox",{name:"Original draft"}),{key:"t",ctrlKey:true});expect(screen.queryByRole("dialog")).toBeNull();expect(f.custom).toHaveBeenCalledTimes(id===ShortcutId.SessionFocus?1:0);
 fireEvent.click(screen.getByRole("button",{name:"Help"}));expect(screen.getAllByText(/A saved custom shortcut uses/)).toHaveLength(2);fireEvent.click(screen.getByRole("button",{name:"Close keyboard shortcuts"}));
 expect(f.bridge.update).not.toHaveBeenCalled();await f.publish({});expect(trigger.getAttribute("aria-keyshortcuts")).toBe("Control+T");fireEvent.keyDown(trigger,{key:"t",ctrlKey:true});expect(screen.getByRole("dialog",{name:"Open tool"})).toBeTruthy();expect(f.bridge.update).not.toHaveBeenCalled();
});
it("active owner/surface departure disposes without restoring hidden ownership",async()=>{
 const f=await fixture();fireEvent.click(screen.getByRole("button",{name:"Open tool"}));const destination=screen.getByRole("button",{name:"Destination"});destination.focus();f.rendered.rerender(f.view({active:false,surface:Surface.Settings}));expect(screen.queryByRole("dialog")).toBeNull();expect(document.activeElement).toBe(destination);
 fireEvent.keyDown(destination,{key:"t",ctrlKey:true});expect(screen.queryByRole("dialog")).toBeNull();f.rendered.rerender(f.view());fireEvent.click(screen.getByRole("button",{name:"Open tool"}));f.rendered.rerender(f.view({owner:"replacement"}));expect(screen.queryByRole("dialog")).toBeNull();
});
it.each([{repeat:true},{isComposing:true},{altKey:true},{shiftKey:true}])("shared dispatcher refuses composing/repeated/extra-modifier T: %j",async extra=>{
 const f=await fixture();fireEvent.keyDown(screen.getByRole("textbox",{name:"Original draft"}),{key:"t",ctrlKey:true,...extra});expect(screen.queryByRole("dialog")).toBeNull();expect(f.selected).not.toHaveBeenCalled();
});
it("typing, terminal passthrough, handled events and another modal retain existing fences",async()=>{
 await fixture();const input=screen.getByRole("textbox",{name:"Original draft"});input.setAttribute("data-shortcuts","passthrough");fireEvent.keyDown(input,{key:"t",ctrlKey:true});expect(screen.queryByRole("dialog")).toBeNull();input.removeAttribute("data-shortcuts");
 const handled=new KeyboardEvent("keydown",{key:"t",ctrlKey:true,bubbles:true,cancelable:true});handled.preventDefault();fireEvent(input,handled);expect(screen.queryByRole("dialog")).toBeNull();
 const modal=document.createElement("dialog");modal.open=true;document.body.append(modal);fireEvent.keyDown(input,{key:"t",ctrlKey:true});expect(document.querySelector('.session-tool-dialog')).toBeNull();modal.remove();
});
