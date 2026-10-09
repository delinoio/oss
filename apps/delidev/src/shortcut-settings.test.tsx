// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ShortcutPreferenceProvider, ShortcutPreferenceProblem, useShortcutPreferences, type ShortcutPreferenceBridge, type ShortcutPreferenceSnapshot } from "./shortcut-preference-controller";
import { ShortcutSettings } from "./shortcut-settings";
import { ShortcutOverrideState, type ShortcutOverrides } from "./shortcut-preferences";
import { ShortcutProvider, useGlobalShortcutAria, useShortcuts } from "./shortcut-provider";
import { ShortcutId, ShortcutScope, ShortcutInput, globalShortcutBindings } from "./shortcuts";
import { i18n, SupportedLanguage } from "./localization";
function fixture() {
 let snapshot:ShortcutPreferenceSnapshot={revision:1,overrides:{},problem:null};
 const listeners=new Set<(raw:unknown)=>void>();
 const publish=(next:ShortcutPreferenceSnapshot)=>{snapshot=next;for(const listener of listeners)listener(next);};
 const bridge:ShortcutPreferenceBridge={read:vi.fn(async()=>snapshot),update:vi.fn(async(overrides,revision)=>{if(revision!==snapshot.revision)return{...snapshot,problem:ShortcutPreferenceProblem.Changed};const next={revision:revision+1,overrides,problem:null};publish(next);return next;}),subscribe:vi.fn(async listener=>{listeners.add(listener);return()=>{listeners.delete(listener);};})};
 return {bridge,publish,state:()=>snapshot};
}
function Probe({run}:{run:()=>void}) {
 const aria=useGlobalShortcutAria(ShortcutId.NewSession),{snapshot}=useShortcutPreferences();
 useShortcuts([{id:ShortcutId.NewSession,scope:ShortcutScope.Global,label:"shortcuts.newSession",bindings:globalShortcutBindings[ShortcutId.NewSession],input:ShortcutInput.Allow,run}]);
 return <><button aria-keyshortcuts={aria}>Ordinary action</button><output data-testid="revision">{snapshot.revision}</output></>;
}
function Owner({bridge,run=()=>{}}:{bridge:ShortcutPreferenceBridge;run?:()=>void}) {
 const [visible,setVisible]=useState(true);
 return <ShortcutPreferenceProvider bridge={bridge}><ShortcutProvider><button onClick={()=>setVisible(value=>!value)}>Leave category</button><Probe run={run}/>{visible?<ShortcutSettings/>:null}</ShortcutProvider></ShortcutPreferenceProvider>;
}
const capture = async(name="New session",key="j")=>{
 fireEvent.click(screen.getByRole("button",{name:`Capture shortcut for ${name}`}));
 fireEvent.keyDown(document.activeElement!,{key,ctrlKey:true,shiftKey:true});
 await screen.findByText("Unsaved changes");
};
it("keeps draft bindings inactive until Save and updates dispatch and ARIA without remount",async()=>{
 const f=fixture(),run=vi.fn();render(<StrictMode><Owner bridge={f.bridge} run={run}/></StrictMode>);await screen.findByText("Current saved shortcuts");
 const action=screen.getByRole("button",{name:"Ordinary action"});await capture();expect(action.getAttribute("aria-keyshortcuts")).toBe("Control+Shift+N");fireEvent.keyDown(action,{key:"j",ctrlKey:true,shiftKey:true});expect(run).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button",{name:"Save changes"}));await waitFor(()=>expect(action.getAttribute("aria-keyshortcuts")).toBe("Control+Shift+J"));expect(f.bridge.update).toHaveBeenCalledOnce();fireEvent.keyDown(action,{key:"j",ctrlKey:true,shiftKey:true});expect(run).toHaveBeenCalledOnce();fireEvent.keyDown(action,{key:"n",ctrlKey:true,shiftKey:true});expect(run).toHaveBeenCalledOnce();expect(screen.getByRole("button",{name:"Ordinary action"})).toBe(action);
});
it("consumes captures, local resets, disable and category departure without a native save",async()=>{
 const f=fixture(),run=vi.fn();render(<Owner bridge={f.bridge} run={run}/>);await screen.findByText("Current saved shortcuts");
 const opener=screen.getByRole("button",{name:"Capture shortcut for New session"});fireEvent.click(opener);fireEvent.keyDown(opener,{key:"n",ctrlKey:true});expect(run).not.toHaveBeenCalled();await screen.findByRole("alert");fireEvent.keyDown(opener,{key:"Escape"});await waitFor(()=>expect(document.activeElement).toBe(opener));
 await capture();fireEvent.click(screen.getByRole("button",{name:"Discard changes"}));expect(f.bridge.update).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button",{name:"Disable Open shortcut help shortcut"}));fireEvent.click(screen.getByRole("button",{name:"Leave category"}));fireEvent.click(screen.getByRole("button",{name:"Leave category"}));await screen.findByText("Current saved shortcuts");expect(f.bridge.update).not.toHaveBeenCalled();
});
it("retains dirty drafts on a concurrent committed change and blocks until explicit discard",async()=>{
 const f=fixture();render(<Owner bridge={f.bridge}/>);await screen.findByText("Current saved shortcuts");await capture();
 await act(async()=>f.publish({revision:2,overrides:{help:{state:ShortcutOverrideState.Disabled}},problem:null}));expect(screen.getByRole("button",{name:"Save changes"}).hasAttribute("disabled")).toBe(true);await screen.findByText(/Shortcuts changed in another window/);expect(screen.getByText("Ctrl + Shift + J")).toBeTruthy();
 fireEvent.click(screen.getByRole("button",{name:"Discard changes"}));await screen.findByText("Current saved shortcuts");expect(screen.getByText("Disabled")).toBeTruthy();
});
it("does not replay an uncertain native save, preserving effective bindings until explicit reinspection",async()=>{
 const f=fixture();vi.mocked(f.bridge.update).mockRejectedValueOnce(new Error("Lost acknowledgment"));render(<Owner bridge={f.bridge}/>);await screen.findByText("Current saved shortcuts");await capture();fireEvent.click(screen.getByRole("button",{name:"Save changes"}));await screen.findByText("The save outcome is unknown. Reinspect without repeating the save.");expect(screen.getByRole("button",{name:"Ordinary action"}).getAttribute("aria-keyshortcuts")).toBe("Control+Shift+N");
 await act(async()=>f.publish({revision:2,overrides:{"new-session":{state:ShortcutOverrideState.Binding,chord:{key:"j",shift:true}}},problem:null}));expect(screen.getByRole("button",{name:"Ordinary action"}).getAttribute("aria-keyshortcuts")).toBe("Control+Shift+N");fireEvent.click(screen.getByRole("button",{name:"Reinspect saved shortcuts"}));await waitFor(()=>expect(screen.getByRole("button",{name:"Ordinary action"}).getAttribute("aria-keyshortcuts")).toBe("Control+Shift+J"));expect(f.bridge.update).toHaveBeenCalledOnce();
});
it("localizes the editor and exposes truthful browser persistence limits",async()=>{
 await i18n.changeLanguage(SupportedLanguage.Korean);render(<ShortcutSettings/>);expect(screen.getByText(/이 환경에서는 단축키를 저장할 수 없습니다/)).toBeTruthy();expect(screen.getByRole("button",{name:"변경 사항 저장"}).hasAttribute("disabled")).toBe(true);
});
it("keeps an admitted save owned above the category and publishes it once after departure",async()=>{
 const f=fixture();let complete!:(snapshot:ShortcutPreferenceSnapshot)=>void;
 vi.mocked(f.bridge.update).mockImplementationOnce(()=>new Promise(resolve=>{complete=resolve;}));
 render(<Owner bridge={f.bridge}/>);await screen.findByText("Current saved shortcuts");await capture();
 fireEvent.click(screen.getByRole("button",{name:"Save changes"}));await screen.findByText("Saving shortcut settings…");
 fireEvent.click(screen.getByRole("button",{name:"Leave category"}));
 await act(async()=>complete({revision:2,overrides:{"new-session":{state:ShortcutOverrideState.Binding,chord:{key:"j",shift:true}}},problem:null}));
 await waitFor(()=>expect(screen.getByRole("button",{name:"Ordinary action"}).getAttribute("aria-keyshortcuts")).toBe("Control+Shift+J"));
 fireEvent.click(screen.getByRole("button",{name:"Leave category"}));await screen.findByText("Current saved shortcuts");expect(f.bridge.update).toHaveBeenCalledOnce();
});
