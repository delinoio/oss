// SPDX-License-Identifier: Apache-2.0
import {useState} from "react";
import {fireEvent,render,screen} from "@testing-library/react";
import {expect,it,vi} from "vitest";
import {SessionTabBar} from "./session-tab-bar";
import {SessionTabKind} from "./session-tabs";
it("roves focus and selection without an absent-position alias and leaves pinned Conversation noncloseable",()=>{
 const close=vi.fn();function View(){const[selected,select]=useState("conversation");return <SessionTabBar id="original" tabs={[{kind:SessionTabKind.Conversation},{kind:SessionTabKind.Files},{kind:SessionTabKind.Browser}]} selected={selected} select={select} close={close}/>;}
 render(<View/>);const tabs=screen.getAllByRole("tab");expect(tabs.map(tab=>tab.tabIndex)).toEqual([0,-1,-1]);expect(screen.getAllByRole("button")).toHaveLength(2);
 fireEvent.keyDown(tabs[0],{key:"ArrowRight"});expect(document.activeElement).toBe(tabs[1]);expect(tabs[1].getAttribute("aria-selected")).toBe("true");
 fireEvent.keyDown(tabs[1],{key:"End"});expect(document.activeElement).toBe(tabs[2]);fireEvent.keyDown(tabs[2],{key:"Home"});expect(document.activeElement).toBe(tabs[0]);expect(tabs[0].getAttribute("aria-controls")).toBe("session-pane-original");
 fireEvent.click(screen.getAllByRole("button")[0]);expect(close).toHaveBeenCalledWith("files");expect(tabs[0].getAttribute("aria-keyshortcuts")).toMatch(/1$/);
});

it("uses friendly original-terminal labels and preserves focus through label rerenders",()=>{
 const first="11111111-1111-7111-8111-111111111111",second="22222222-2222-7222-8222-222222222222";
 const tabs=[{kind:SessionTabKind.Conversation} as const,{kind:SessionTabKind.Terminal,id:first} as const,{kind:SessionTabKind.Terminal,id:second} as const];
 const props={id:"session",tabs,selected:JSON.stringify([SessionTabKind.Terminal,first]),select:vi.fn(),close:vi.fn()};const view=render(<SessionTabBar {...props}/>);
 const selected=screen.getByRole("tab",{name:"Terminal 1"});selected.focus();expect(selected.title).toBe("Terminal 1");
 expect(screen.getByRole("tab",{name:"Terminal 2"}).title).toBe("Terminal 2");expect(screen.queryByText(first)).toBeNull();
 view.rerender(<SessionTabBar {...props} tabs={[tabs[0]!,tabs[2]!,tabs[1]!]}/>);expect(document.activeElement).toBe(selected);expect(selected.title).toBe("Terminal 1");
});
