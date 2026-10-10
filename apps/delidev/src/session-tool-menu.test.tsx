// SPDX-License-Identifier: Apache-2.0
import { useEffect } from "react";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SessionToolMenu } from "./session-tool-menu";
import { i18n } from "./localization";
import { ShortcutProvider, useShortcutHelp, useShortcutSurface } from "./shortcut-provider";
import { ShortcutPreferenceProvider, type ShortcutPreferenceBridge } from "./shortcut-preference-controller";
import { ShortcutSettings } from "./shortcut-settings";
import { ShortcutId } from "./shortcuts";
import { ShortcutOverrideState } from "./shortcut-preferences";
import { Surface } from "./surface";
afterEach(async () => { await i18n.changeLanguage("en"); });

it("focuses enabled tools, contains Tab and closes before the original callback without stealing destination focus", () => {
 const selected = vi.fn(() => { expect(screen.queryByRole("dialog")).toBeNull(); screen.getByRole("button",{name:"Destination"}).focus(); });
 const view = render(<><SessionToolMenu active ancillary={<><button>Original Sidechat</button><details><summary>Original diagnostic detail</summary>Read-only evidence</details></>}><button disabled>Terminals</button><button onClick={selected}>Files</button><button>Diagnostics</button></SessionToolMenu><button>Destination</button></>);
 const trigger = screen.getByRole("button",{name:"Open tool"}); fireEvent.click(trigger);
 const dialog = screen.getByRole("dialog",{name:"Open tool"}), files = within(dialog).getByRole("button",{name:"Files"}), diagnostics = within(dialog).getByRole("button",{name:"Diagnostics"});
 expect(document.activeElement).toBe(files);
 const composing = new KeyboardEvent("keydown",{key:"Enter",isComposing:true,bubbles:true,cancelable:true});files.dispatchEvent(composing);expect(composing.defaultPrevented).toBe(true);expect(selected).not.toHaveBeenCalled();
 fireEvent.keyDown(files,{key:"ArrowDown"}); expect(document.activeElement).toBe(diagnostics);
 fireEvent.keyDown(diagnostics,{key:"Home"}); expect(document.activeElement).toBe(files);
 fireEvent.keyDown(files,{key:"End"}); expect(document.activeElement).toBe(diagnostics);
 const close = within(dialog).getByRole("button",{name:"Close"}), ancillary = within(dialog).getByText("Original diagnostic detail");
 close.focus(); fireEvent.keyDown(close,{key:"Tab",shiftKey:true}); expect(document.activeElement).toBe(ancillary);
 fireEvent.keyDown(ancillary,{key:"Tab"}); expect(document.activeElement).toBe(close);
 fireEvent.keyDown(close,{key:"Escape"}); expect(screen.queryByRole("dialog")).toBeNull(); expect(document.activeElement).toBe(trigger); expect(selected).not.toHaveBeenCalled();
 fireEvent.click(trigger); fireEvent.click(screen.getByRole("button",{name:"Terminals"})); expect(selected).not.toHaveBeenCalled(); expect(screen.getByRole("dialog")).toBeDefined();
 fireEvent.click(screen.getByRole("button",{name:"Files"})); expect(selected).toHaveBeenCalledOnce(); expect(document.activeElement).toBe(screen.getByRole("button",{name:"Destination"}));
 fireEvent.click(trigger); fireEvent.click(screen.getByRole("dialog")); expect(screen.queryByRole("dialog")).toBeNull(); expect(document.activeElement).toBe(trigger);
 fireEvent.click(trigger); view.rerender(<SessionToolMenu active={false}><button>Files</button></SessionToolMenu>); expect(screen.queryByRole("dialog")).toBeNull();
});

it.each([['en','Open tool'],['ko','도구 열기']])("keeps localized dialog ARIA and a decorative original trigger in %s", async(language,label)=>{
 await act(()=>i18n.changeLanguage(language));
 render(<SessionToolMenu active><button>Files</button></SessionToolMenu>);
 const trigger=screen.getByRole("button",{name:label}); expect(trigger.textContent).toBe(""); expect(trigger.title).toBe(label);
 expect(trigger.getAttribute("aria-haspopup")).toBe("dialog"); expect(trigger.getAttribute("aria-keyshortcuts")).toBe("Control+T");
 expect(trigger.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true');
 fireEvent.click(trigger); const dialog=screen.getByRole('dialog',{name:label}); expect(trigger.getAttribute('aria-controls')).toBe(dialog.id);
 expect(document.activeElement).toBe(within(dialog).getByRole('button',{name:'Files'}));
});

function Workspace({active=true}:{active?:boolean}) { useShortcutSurface(Surface.Sessions); return <><textarea aria-label="Draft" defaultValue="Original draft"/><SessionToolMenu active={active}><button>Diff</button><button>Files</button><button disabled>Terminals</button><button>Browser</button><button>Diagnostics</button></SessionToolMenu></>; }
it("opens the same modal from ordinary input, fences terminal/modal/IME/repeat and restores the original draft focus",()=>{
 render(<ShortcutProvider><Workspace/></ShortcutProvider>); const draft=screen.getByRole('textbox',{name:'Draft'}); draft.focus();
 for(const flags of [{metaKey:true},{ctrlKey:true,shiftKey:true},{ctrlKey:true,altKey:true},{ctrlKey:true,repeat:true},{ctrlKey:true,isComposing:true}]) { fireEvent.keyDown(draft,{key:'t',...flags}); expect(screen.queryByRole('dialog')).toBeNull(); }
 draft.setAttribute('data-shortcuts','passthrough'); fireEvent.keyDown(draft,{key:'t',ctrlKey:true}); expect(screen.queryByRole('dialog')).toBeNull(); draft.removeAttribute('data-shortcuts');
 fireEvent.keyDown(draft,{key:'t',ctrlKey:true}); expect(screen.getAllByRole('dialog')).toHaveLength(1); expect(document.activeElement).toBe(screen.getByRole('button',{name:'Diff'})); expect((draft as HTMLTextAreaElement).value).toBe('Original draft');
 fireEvent.keyDown(document.activeElement!,{key:'t',ctrlKey:true}); expect(screen.getAllByRole('dialog')).toHaveLength(1);
 fireEvent.keyDown(document.activeElement!,{key:'Escape'}); expect(document.activeElement).toBe(draft); expect((draft as HTMLTextAreaElement).value).toBe('Original draft');
});

it("reflects saved custom T priority in ARIA and immediately restores the default without a preference write",async()=>{
 let publish!:(snapshot:unknown)=>void;
 const bridge:ShortcutPreferenceBridge={read:async()=>({revision:1,overrides:{[ShortcutId.SearchFocus]:{state:ShortcutOverrideState.Binding,chord:{key:'t',shift:false}}},problem:null}),update:vi.fn(),subscribe:async changed=>{publish=changed;return()=>{};}};
 render(<ShortcutPreferenceProvider bridge={bridge}><ShortcutProvider><Workspace/></ShortcutProvider></ShortcutPreferenceProvider>);
 await act(async()=>{}); const trigger=screen.getByRole('button',{name:'Open tool'}),draft=screen.getByRole('textbox',{name:'Draft'});
 expect(trigger.hasAttribute('aria-keyshortcuts')).toBe(false); expect(trigger.getAttribute('aria-description')).toMatch(/A saved custom shortcut/); fireEvent.keyDown(draft,{key:'t',ctrlKey:true}); expect(screen.queryByRole('dialog')).toBeNull();
 fireEvent.click(trigger); expect(screen.getByRole('dialog')).toBeDefined(); fireEvent.keyDown(document.activeElement!,{key:'Escape'});
 await act(async()=>publish({revision:2,overrides:{},problem:null})); expect(trigger.getAttribute('aria-keyshortcuts')).toBe('Control+T');
 fireEvent.keyDown(draft,{key:'t',ctrlKey:true}); expect(screen.getByRole('dialog')).toBeDefined(); expect(bridge.update).not.toHaveBeenCalled();
});

it.each(["Diff","Files","Terminals","Browser","Diagnostics"])("invokes the original %s callback once after closing without remounting action owners", name=>{
 const invoked=vi.fn(()=>expect(screen.queryByRole('dialog')).toBeNull()), mounted=vi.fn(), disposed=vi.fn();
 function OriginalActions(){useEffect(()=>{mounted();return disposed;},[]);return <>{["Diff","Files","Terminals","Browser","Diagnostics"].map(label=><button key={label} onClick={label===name?invoked:undefined}>{label}</button>)}</>;}
 const view=render(<SessionToolMenu active><OriginalActions/></SessionToolMenu>);
 expect(mounted).toHaveBeenCalledOnce(); fireEvent.click(screen.getByRole('button',{name:'Open tool'}));
 expect(within(screen.getByRole('dialog')).getAllByRole('button').map(button=>button.textContent).slice(1)).toEqual(["Diff","Files","Terminals","Browser","Diagnostics"]);
 fireEvent.click(screen.getByRole('button',{name}));expect(invoked).toHaveBeenCalledOnce();expect(mounted).toHaveBeenCalledOnce();expect(disposed).not.toHaveBeenCalled();
 view.unmount();expect(disposed).toHaveBeenCalledOnce();
});

it.each(["MacIntel","Win32","Linux x86_64"])("uses the platform primary modifier in %s", platform=>{
 const mock=vi.spyOn(navigator,'platform','get').mockReturnValue(platform);
 try {
  render(<ShortcutProvider><Workspace/></ShortcutProvider>);const draft=screen.getByRole('textbox');draft.focus();
  const mac=platform==='MacIntel'; fireEvent.keyDown(draft,{key:'t',ctrlKey:mac,metaKey:!mac});expect(screen.queryByRole('dialog')).toBeNull();
  fireEvent.keyDown(draft,{key:'t',ctrlKey:!mac,metaKey:mac});expect(screen.getByRole('dialog')).toBeDefined();
 } finally {mock.mockRestore();}
});

function HelpOpener(){const show=useShortcutHelp();return <button onClick={show}>Help</button>;}
it("discloses the same committed suppression in Help and read-only Settings",async()=>{
 const bridge:ShortcutPreferenceBridge={read:async()=>({revision:1,overrides:{[ShortcutId.SearchFocus]:{state:ShortcutOverrideState.Binding,chord:{key:'t',shift:false}}},problem:null}),update:vi.fn(),subscribe:async()=>()=>{}};
 render(<ShortcutPreferenceProvider bridge={bridge}><ShortcutProvider><Workspace/><HelpOpener/><ShortcutSettings/></ShortcutProvider></ShortcutPreferenceProvider>);
 await act(async()=>{});const settings=screen.getByRole('region',{name:'Keyboard shortcuts'});
 expect(within(settings).getByText(/A saved custom shortcut uses this key/)).toBeDefined();
 fireEvent.click(screen.getByRole('button',{name:'Help'}));const help=screen.getByRole('dialog',{name:'Keyboard shortcuts'});
 expect(within(help).getByText('Open tool')).toBeDefined();expect(within(help).getByText(/A saved custom shortcut uses this key/)).toBeDefined();expect(within(help).queryByText('T')).toBeNull();
 expect(bridge.update).not.toHaveBeenCalled();
});

it("disposes on connection replacement and ignores a detached original tool button",()=>{
 const callback=vi.fn();const view=render(<ShortcutProvider key='original'><SessionToolMenu active><button onClick={callback}>Files</button></SessionToolMenu></ShortcutProvider>);
 fireEvent.click(screen.getByRole('button',{name:'Open tool'}));const original=screen.getByRole('button',{name:'Files'});
 view.rerender(<ShortcutProvider key='replacement'><SessionToolMenu active><button>Replacement</button></SessionToolMenu></ShortcutProvider>);
 expect(screen.queryByRole('dialog')).toBeNull();fireEvent.click(original);expect(callback).not.toHaveBeenCalled();
});
