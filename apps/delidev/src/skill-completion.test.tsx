// SPDX-License-Identifier: Apache-2.0
import { useRef, useState } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SessionService, SessionQuery, SkillService, SkillProvenance, newRequestId } from "@delinoio/delidev-api-client";
import { skillToken, skillRanges, editedBindings, useSkillCompletion } from "./skill-completion";
import { MutationIntents, useRetainedMutation } from "./mutation";
import { SupportedLanguage, i18n } from "./localization";
const machine = newRequestId(), agent = newRequestId(), inventory = newRequestId(), worker = newRequestId();
const entries = ["add-issue", "add-note"].map(name => ({ name, description: `${name} fixture`, provenance: SkillProvenance.USER, selection: { $typeName: "delidev.v1.SkillSelection" as const, inventoryId: inventory, workerDeviceId: worker, skillId: newRequestId(), contentRevision: "a".repeat(64) } }));
function Composer({ runner = machine, locked = false, enabled = true, retainTransportContext = false, send }: { runner?: string; locked?: boolean; enabled?: boolean; retainTransportContext?: boolean; send: (value: unknown) => void }) {
 const [value,change]=useState("");const textarea=useRef<HTMLTextAreaElement>(null);const skills=useSkillCompletion({value,change,textarea,machineId:runner,agentId:agent,disabled:locked,enabled,retainTransportContext});
 return <><fieldset disabled={locked}>{skills.wrap(<textarea aria-label="Message" ref={textarea} value={value} onChange={e=>skills.onChange(e.target.value,e.target.selectionStart)} onSelect={skills.onSelect} onKeyDown={skills.onKeyDown} onCompositionStart={skills.onCompositionStart} onCompositionEnd={skills.onCompositionEnd} {...skills.attributes}/>)}{skills.list}{skills.warning}</fieldset><button disabled={skills.blocked} onClick={()=>send({value,skills:skills.selections})}>Send</button></>;
}
function fixture() {const send=vi.fn(),read=vi.fn(async()=>({skills:entries}));const transport=createRouterTransport(router=>router.service(SkillService,{listSkills:read}));const client=new QueryClient();const view=(runner=machine,locked=false)=><TransportProvider transport={transport}><QueryClientProvider client={client}><Composer runner={runner} locked={locked} send={send}/></QueryClientProvider></TransportProvider>;return{send,read,view};}
it("finds whitespace-delimited caret tokens without consuming surrounding Unicode",()=>{expect(skillToken("한글\n$add-iss trailing",11)).toEqual({start:3,end:11,prefix:"add-iss"});expect(skillToken("email$add",9)).toBeUndefined();});
it("binds keyboard selection separately, replaces only the token and does not send",async()=>{const f=fixture();render(f.view());const input=screen.getByRole("textbox");fireEvent.change(input,{target:{value:"한글\n$add-iss",selectionStart:11}});await screen.findByRole("option");fireEvent.keyDown(input,{key:"Enter"});expect((input as HTMLTextAreaElement).value).toBe("한글\n$add-issue");expect(f.send).not.toHaveBeenCalled();fireEvent.click(screen.getByText("Send"));expect(f.send.mock.calls[0]![0]).toMatchObject({skills:[entries[0]!.selection]});});
it("removes an edited binding and requires reselection on Runner changes",async()=>{const f=fixture();const view=render(f.view());const input=screen.getByRole("textbox");fireEvent.change(input,{target:{value:"$add-iss",selectionStart:8}});await screen.findByRole("option");fireEvent.keyDown(input,{key:"Tab"});view.rerender(f.view(newRequestId()));await screen.findByText("The selected skills need reselection before sending.");expect((screen.getByText("Send") as HTMLButtonElement).disabled).toBe(true);fireEvent.change(input,{target:{value:"manual",selectionStart:6}});await waitFor(()=>expect((screen.getByText("Send") as HTMLButtonElement).disabled).toBe(false));fireEvent.click(screen.getByText("Send"));expect(f.send.mock.calls[0]![0]).toMatchObject({skills:[]});});
it("does not select or submit during IME composition and Escape preserves text",async()=>{const f=fixture();render(f.view());const input=screen.getByRole("textbox");fireEvent.change(input,{target:{value:"$add-iss",selectionStart:8}});await screen.findByRole("option");fireEvent.compositionStart(input);fireEvent.keyDown(input,{key:"Enter",isComposing:true});expect((input as HTMLTextAreaElement).value).toBe("$add-iss");fireEvent.compositionEnd(input);fireEvent.keyDown(input,{key:"Escape"});expect(screen.queryByRole("listbox")).toBeNull();expect(f.send).not.toHaveBeenCalled();});
it("keeps manually typed tokens unbound and supports localized loading states",async()=>{await i18n.changeLanguage(SupportedLanguage.Korean);const f=fixture();render(f.view());const input=screen.getByRole("textbox");fireEvent.change(input,{target:{value:"$unknown",selectionStart:8}});await screen.findByText("일치하는 스킬이 없습니다.");fireEvent.click(screen.getByText("Send"));expect(f.send.mock.calls[0]![0]).toMatchObject({skills:[]});await i18n.changeLanguage(SupportedLanguage.English);});
it("keeps bindings shifted by outside edits but removes changed selected tokens",()=>{const binding={start:2,end:12,token:"$add-issue",selection:entries[0]!.selection,stale:false};expect(editedBindings("a $add-issue","prefix a $add-issue",[binding])[0]?.start).toBe(9);expect(editedBindings("a $add-issue","a $other",[binding])).toEqual([]);});

it("includes the remainder of the caret token without matching it as a prefix",()=>{expect(skillToken("before $add-issue after",15)).toEqual({start:7,end:17,prefix:"add-iss"});});

it("does not accept IME commit Enter, repeated keys or AltGraph",async()=>{const f=fixture();render(f.view());const input=screen.getByRole("textbox");fireEvent.change(input,{target:{value:"$add-iss",selectionStart:8}});await screen.findByRole("option");for(const event of [{key:"Enter",keyCode:229,isComposing:false},{key:"Enter",repeat:true},{key:"Enter",modifierAltGraph:true}]) {fireEvent.keyDown(input,event);expect((input as HTMLTextAreaElement).value).toBe("$add-iss");}expect(f.send).not.toHaveBeenCalled();});

it("drops a binding when typing at either token boundary but preserves whitespace",()=>{const binding={start:2,end:7,token:"$same",selection:entries[0]!.selection,stale:false};for(const value of ["a $samex","a $same한글","a x$same"]){expect(editedBindings("a $same",value,[binding])).toEqual([]);}expect(editedBindings("a $same","a $same ",[binding])).toHaveLength(1);expect(editedBindings("a $same","a  $same",[binding])).toHaveLength(1);});

it("rejects native fieldset-locked completion clicks and keyboard edits before React settlement",async()=>{const f=fixture();const view=render(f.view());const input=screen.getByRole("textbox");fireEvent.change(input,{target:{value:"$ad",selectionStart:3}});const option=await screen.findByRole("option",{name:/add-issue/});view.container.querySelector("fieldset")!.disabled=true;fireEvent.click(option);fireEvent.keyDown(input,{key:"Enter"});fireEvent.change(input,{target:{value:"changed",selectionStart:7}});fireEvent.click(screen.getByRole("button",{name:"Send"}));expect(f.send).toHaveBeenLastCalledWith({value:"$ad",skills:[]});});
it("keeps exact draft and accepted bindings through pending or uncertain composer locks",async()=>{const f=fixture();const view=render(f.view());const input=screen.getByRole("textbox");fireEvent.change(input,{target:{value:"$ad",selectionStart:3}});fireEvent.click(await screen.findByRole("option",{name:/add-issue/}));fireEvent.change(input,{target:{value:"$add-issue $ad",selectionStart:14}});await screen.findByRole("option",{name:/add-note/});view.rerender(f.view(machine,true));expect(screen.queryByRole("listbox")).toBeNull();fireEvent.keyDown(input,{key:"Enter"});fireEvent.change(input,{target:{value:"changed",selectionStart:7}});fireEvent.click(screen.getByRole("button",{name:"Send"}));expect(f.send).toHaveBeenLastCalledWith({value:"$add-issue $ad",skills:[entries[0]!.selection]});view.rerender(f.view());await screen.findByRole("option",{name:/add-note/});expect((input as HTMLTextAreaElement).value).toBe("$add-issue $ad");});

function AcceptedComposer() {const [value,change]=useState("");const textarea=useRef<HTMLTextAreaElement>(null);const mutation=useRetainedMutation("skills-accepted-fixture",SessionQuery.enqueueInput,()=>{change("");skills.clearAccepted();});const skills=useSkillCompletion({value,change,textarea,machineId:machine,agentId:agent,disabled:mutation.busy||mutation.uncertain});return <><textarea aria-label="Accepted message" disabled={mutation.busy||mutation.uncertain} ref={textarea} value={value} onChange={e=>skills.onChange(e.target.value,e.target.selectionStart)}/>{skills.list}<output data-selected>{skills.selections.length}</output><button onClick={()=>void mutation.send({requestId:newRequestId(),sessionId:newRequestId(),documentJson:new Uint8Array()})}>Enqueue fixture</button></>;}
it("clears accepted bindings in the real retained-mutation callback before the pending render unlocks",async()=>{let resolve!:()=>void;const pending=new Promise<void>(done=>{resolve=done;});const transport=createRouterTransport(router=>{router.service(SkillService,{listSkills:async()=>({skills:entries})});router.service(SessionService,{enqueueInput:async()=>{await pending;return {};}});});const view=render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient()}><MutationIntents><AcceptedComposer/></MutationIntents></QueryClientProvider></TransportProvider>);const input=screen.getByRole("textbox");fireEvent.change(input,{target:{value:"$ad",selectionStart:3}});fireEvent.click(await screen.findByRole("option",{name:/add-issue/}));expect(view.container.querySelector("[data-selected]")!.textContent).toBe("1");fireEvent.click(screen.getByRole("button",{name:"Enqueue fixture"}));await waitFor(()=>expect((input as HTMLTextAreaElement).disabled).toBe(true));resolve();await waitFor(()=>expect((input as HTMLTextAreaElement).value).toBe(""));fireEvent.change(input,{target:{value:"$add-issue",selectionStart:10}});expect(view.container.querySelector("[data-selected]")!.textContent).toBe("0");});

function RestoredComposer({ runner, reject = false }: { runner: string; reject?: boolean }) {
 const [value,setValue]=useState("$add-issue");const textarea=useRef<HTMLTextAreaElement>(null);
 const skills=useSkillCompletion({value,change:next=>{if(reject)return false;setValue(next);return true;},textarea,machineId:runner,agentId:agent,initialBindings:[{start:0,end:10,token:"$add-issue",selection:entries[0]!.selection,stale:false,context:`${machine}:${agent}::`}]});
 return <><textarea ref={textarea} value={value} onChange={event=>skills.onChange(event.target.value,event.target.selectionStart)}/>{skills.warning}<button disabled={skills.blocked}>Restored send</button><output>{skills.selections.length}</output></>;
}
it("blocks a retained binding during unresolved and changed context until explicit token removal",async()=>{
 const client=new QueryClient();const transport=createRouterTransport(router=>router.service(SkillService,{listSkills:async()=>({skills:entries})}));
 const tree=(runner:string)=><TransportProvider transport={transport}><QueryClientProvider client={client}><RestoredComposer runner={runner}/></QueryClientProvider></TransportProvider>;
 const view=render(tree(""));expect(screen.getByRole("button",{name:"Restored send"})).toHaveProperty("disabled",true);view.rerender(tree(machine));await waitFor(()=>expect(screen.getByRole("button",{name:"Restored send"})).toHaveProperty("disabled",false));
 view.rerender(tree(newRequestId()));await waitFor(()=>expect(screen.getByRole("button",{name:"Restored send"})).toHaveProperty("disabled",true));fireEvent.change(screen.getByRole("textbox"),{target:{value:"$plain",selectionStart:6}});await waitFor(()=>expect(screen.getByRole("button",{name:"Restored send"})).toHaveProperty("disabled",false));expect(screen.queryByText("The selected skills need reselection before sending.")).toBeNull();
});
it("retains text and original bindings atomically when draft growth is rejected",()=>{
 const transport=createRouterTransport(router=>router.service(SkillService,{listSkills:async()=>({skills:entries})}));render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient()}><RestoredComposer runner={machine} reject/></QueryClientProvider></TransportProvider>);
 fireEvent.change(screen.getByRole("textbox"),{target:{value:"rejected",selectionStart:8}});expect(screen.getByRole("textbox")).toHaveProperty("value","$add-issue");expect(screen.getByText("1")).toBeDefined();expect(screen.getByRole("button",{name:"Restored send"})).toHaveProperty("disabled",false);
});

function HistorySkillComposer({ locked = false }: { locked?: boolean }) {
 const [value, change] = useState("$add-issue"); const textarea = useRef<HTMLTextAreaElement>(null);
 const skills = useSkillCompletion({ value, change, textarea, machineId: machine, agentId: agent, disabled: locked, initialBindings: [{ start: 0, end: 10, token: "$add-issue", selection: entries[0]!.selection, stale: false, context: `${machine}:${agent}::` }] });
 return <><textarea aria-label="History skill draft" ref={textarea} disabled={locked} value={value} onChange={event => skills.onChange(event.target.value,event.target.selectionStart)}/><output data-history-selections>{skills.selections.length}</output><button onClick={() => skills.replaceUnbound("$add-issue", 0)}>Recall text only</button></>;
}
it("recalling identical text drops typed skill authority and respects the creation lock", () => {
 const transport = createRouterTransport(router => router.service(SkillService, { listSkills: async () => ({ skills: entries }) })); const client = new QueryClient();
 const tree = (locked: boolean) => <TransportProvider transport={transport}><QueryClientProvider client={client}><HistorySkillComposer locked={locked}/></QueryClientProvider></TransportProvider>;
 const view = render(tree(true)); fireEvent.click(screen.getByRole("button", { name: "Recall text only" })); expect(view.container.querySelector("[data-history-selections]")!.textContent).toBe("1");
 view.rerender(tree(false)); fireEvent.click(screen.getByRole("button", { name: "Recall text only" })); expect(screen.getByRole("textbox", { name: "History skill draft" })).toHaveProperty("value", "$add-issue"); expect(view.container.querySelector("[data-history-selections]")!.textContent).toBe("0");
});


it("retains original option text with description before the separate provenance badge", async () => {
 const long = { ...entries[0]!, name: "길고긴스킬이름-long-name", description: "Original 한글 description ".repeat(100), provenance: SkillProvenance.PROJECT };
 const transport = createRouterTransport(router => router.service(SkillService, { listSkills: async () => ({ skills: [long, { ...entries[1]!, description: "" }] }) }));
 const view = render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient()}><Composer send={vi.fn()}/></QueryClientProvider></TransportProvider>);
 const input = screen.getByRole("textbox"); fireEvent.change(input, { target: { value: "$", selectionStart: 1 } });
 const option = await screen.findByRole("option", { name: new RegExp(long.name) });
 expect([...option.children].map(child => child.className)).toEqual(["skill-completion-name", "skill-completion-description", "skill-completion-provenance"]);
 expect(option.children[1]!.textContent).toBe(long.description); expect(option.children[2]!.textContent).toBe("Project");
 expect(screen.getAllByRole("option")).toHaveLength(2); expect(view.container.querySelectorAll(".skill-completion-description")[0]!.textContent).toBe("");
 expect(input.getAttribute("aria-activedescendant")).toBe(screen.getAllByRole("option")[0]!.id);
});

it.each([1, 2])("scrolls only the completion container at scale %s and retains textarea focus", async (scale) => {
 const f = fixture(); const view = render(f.view()); const input = screen.getByRole("textbox"); input.focus();
 fireEvent.change(input, { target: { value: "$add", selectionStart: 4 } }); await screen.findAllByRole("option");
 const container = view.container.querySelector<HTMLElement>(".skill-completion")!;
 Object.defineProperty(container, "clientHeight", { value: 100 }); Object.defineProperty(container, "clientTop", { value: 1 }); Object.defineProperty(container, "offsetHeight", { value: 102 });
 vi.spyOn(container, "getBoundingClientRect").mockReturnValue({ top: 10, bottom: 10 + 102 * scale, height: 102 * scale } as DOMRect);
 const rows = screen.getAllByRole("option");
 vi.spyOn(rows[0]!, "getBoundingClientRect").mockReturnValue({ top: 10 - 39 * scale, bottom: 10 + scale } as DOMRect);
 vi.spyOn(rows[1]!, "getBoundingClientRect").mockReturnValue({ top: 10 + 101 * scale, bottom: 10 + 141 * scale } as DOMRect);
 fireEvent.keyDown(input, { key: "ArrowDown" }); expect(container.scrollTop).toBe(40); expect(document.activeElement).toBe(input);
 expect(input.getAttribute("aria-activedescendant")).toBe(rows[1]!.id);
 fireEvent.keyDown(input, { key: "ArrowUp" }); expect(container.scrollTop).toBe(0); expect(document.activeElement).toBe(input); expect(f.send).not.toHaveBeenCalled();
});


it("colors exact complete tokens after a successful read without binding text and suppresses overlays during IME", async () => {
 const f = fixture(); const view = render(f.view()); const input = screen.getByRole("textbox");
 const draft = "한글\n$add-issue $removed-skill $removed-skill";
 fireEvent.change(input, { target: { value: draft, selectionStart: draft.length } });
 await waitFor(() => expect(view.container.querySelectorAll(".skill-token-unavailable")).toHaveLength(2));
 expect([...view.container.querySelectorAll(".skill-token-unavailable")].map(node => node.textContent)).toEqual(["$removed-skill", "$removed-skill"]);
 expect(screen.getByRole("textbox", { description: "2 skill tokens are unavailable in this scope." })).toBe(input);
 fireEvent.keyDown(input, { key: "Escape" }); expect(screen.queryByRole("listbox")).toBeNull();
 expect(view.container.querySelectorAll(".skill-token-unavailable")).toHaveLength(2);
 fireEvent.click(screen.getByText("Send")); expect(f.send).toHaveBeenLastCalledWith({ value: draft, skills: [] });
 fireEvent.compositionStart(input); expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
 fireEvent.compositionEnd(input); await waitFor(() => expect(view.container.querySelectorAll(".skill-token-unavailable")).toHaveLength(2));
 fireEvent.change(input, { target: { value: "$ad", selectionStart: 3 } }); await screen.findAllByRole("option"); expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
 expect(skillRanges("$ email$bad $bad$other\n$valid $valid")).toEqual([{start:23,end:29,prefix:"valid"},{start:30,end:36,prefix:"valid"}]);
});
it("retains only displayed missing rows, skips disabled rows, and discards them on dismissal", async () => {
 let current = entries; const send = vi.fn(); const client = new QueryClient();
 const transport = createRouterTransport(router => router.service(SkillService, { listSkills: async () => ({ skills: current }) }));
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Composer send={send}/></QueryClientProvider></TransportProvider>);
 const input = screen.getByRole("textbox"); fireEvent.change(input, { target: { value: "$add", selectionStart: 4 } }); await screen.findAllByRole("option");
 current = [entries[1]!]; await client.invalidateQueries();
 const removed = await screen.findByRole("option", { name: /add-issue.*Unavailable/ }); expect(removed.getAttribute("aria-disabled")).toBe("true");
 fireEvent.click(removed); expect(input).toHaveProperty("value", "$add"); fireEvent.keyDown(input, { key: "ArrowDown" }); expect(input.getAttribute("aria-activedescendant")).not.toBe(removed.id);
 fireEvent.keyDown(input, { key: "Tab" }); expect(input).toHaveProperty("value", "$add-note"); expect(send).not.toHaveBeenCalled();
 fireEvent.change(input, { target: { value: "$add", selectionStart: 4 } }); await screen.findByRole("option", { name: /add-note/ }); expect(screen.queryByRole("option", { name: /add-issue/ })).toBeNull();
 fireEvent.keyDown(input, { key: "Escape" }); fireEvent.select(input, { target: { selectionStart: 4 } }); expect(screen.queryByRole("listbox")).toBeNull();
});
it("fences late previous-Runner inventory and leaves pending, failed and unsupported reads unknown", async () => {
 let release!: () => void; const pending = new Promise<void>(resolve => { release = resolve; }); const next = newRequestId(); const client = new QueryClient();
 const transport = createRouterTransport(router => router.service(SkillService, { listSkills: async request => { if (request.machineId === machine) { await pending; return { skills: entries }; } throw new Error("Private inventory failure"); } }));
 const tree = (runner: string) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Composer runner={runner} send={vi.fn()}/></QueryClientProvider></TransportProvider>;
 const view = render(tree(machine)); const input = screen.getByRole("textbox"); fireEvent.change(input, { target: { value: "$missing", selectionStart: 8 } });
 await screen.findByText("Loading skills…"); expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
 view.rerender(tree(next)); await screen.findByText("Skills are unavailable. Your message is unchanged."); release(); await new Promise(resolve => setTimeout(resolve, 0));
 expect(view.container.querySelector(".skill-text-overlay")).toBeNull(); expect(screen.queryByRole("option")).toBeNull(); expect(view.container.textContent).not.toContain("Private inventory failure");
});

it("disabled-only retained rows never accept mouse, Enter or Tab; restored entries bind their fresh selection", async () => {
 let current = [entries[0]!]; const send = vi.fn(); const client = new QueryClient();
 const transport = createRouterTransport(router => router.service(SkillService, { listSkills: async () => ({ skills: current }) }));
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Composer send={send}/></QueryClientProvider></TransportProvider>);
 const input = screen.getByRole("textbox"); fireEvent.change(input, { target: { value: "$a", selectionStart: 2 } }); await screen.findByRole("option");
 current = []; await client.invalidateQueries(); const removed = await screen.findByRole("option", { name: /Unavailable/ });
 fireEvent.click(removed); fireEvent.keyDown(input, { key: "Enter" }); fireEvent.keyDown(input, { key: "Tab" }); fireEvent.keyDown(input, { key: "ArrowDown" });
 expect(input).toHaveProperty("value", "$a"); expect(input.getAttribute("aria-activedescendant")).toBeNull(); expect(send).not.toHaveBeenCalled();
 const fresh = { ...entries[0]!, selection: { ...entries[0]!.selection, inventoryId: newRequestId(), contentRevision: "b".repeat(64) } };
 current = [fresh]; await client.invalidateQueries(); await waitFor(() => expect(screen.getByRole("option").getAttribute("aria-disabled")).toBeNull());
 fireEvent.keyDown(input, { key: "Enter" }); fireEvent.click(screen.getByText("Send")); expect(send).toHaveBeenLastCalledWith({ value: "$add-issue", skills: [fresh.selection] });
});
it("observes history-like tokens while dismissed, and does not claim absence from incomplete inventory", async () => {
 let current = [entries[0]!, { ...entries[1]!, selection: { ...entries[1]!.selection, inventoryId: newRequestId() } }]; const client = new QueryClient();
 const transport = createRouterTransport(router => router.service(SkillService, { listSkills: async () => ({ skills: current }) }));
 const view = render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Composer send={vi.fn()}/></QueryClientProvider></TransportProvider>);
 const input = screen.getByRole("textbox"); fireEvent.change(input, { target: { value: "$removed", selectionStart: 8 } }); await screen.findByText("Skills are unavailable. Your message is unchanged.");
 expect(view.container.querySelector(".skill-text-overlay")).toBeNull(); fireEvent.keyDown(input, { key: "Escape" }); current = [entries[0]!]; await client.invalidateQueries();
 await waitFor(() => expect(view.container.querySelector(".skill-token-unavailable")?.textContent).toBe("$removed")); expect(screen.queryByRole("listbox")).toBeNull();
});

it("leaves unsupported inventory unknown without reads and fences replacement transports even when binding context is retained", async () => {
 let release!: () => void; const pending = new Promise<void>(resolve => { release=resolve; }); const oldRead = vi.fn(async () => { await pending; return { skills: [...entries, {...entries[0]!,name:"missing",selection:{...entries[0]!.selection,skillId:newRequestId()}}] }; });
 const oldTransport = createRouterTransport(router => router.service(SkillService, { listSkills: oldRead }));
 const freshTransport = createRouterTransport(router => router.service(SkillService, { listSkills: async () => ({ skills: entries }) }));
 const client = new QueryClient(); const tree = (transport: typeof oldTransport, enabled=true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Composer enabled={enabled} retainTransportContext send={vi.fn()}/></QueryClientProvider></TransportProvider>;
 const view=render(tree(oldTransport,false)); const input=screen.getByRole("textbox"); fireEvent.change(input,{target:{value:"$missing",selectionStart:8}}); await screen.findByText("Update the server and Runner Device to use skills."); expect(oldRead).not.toHaveBeenCalled(); expect(view.container.querySelector(".skill-text-overlay")).toBeNull();
 view.rerender(tree(oldTransport)); await waitFor(()=>expect(oldRead).toHaveBeenCalledOnce());
 view.rerender(tree(freshTransport)); await waitFor(()=>expect(view.container.querySelector(".skill-token-unavailable")?.textContent).toBe("$missing"));
 // The old request has a different inventory that would falsely make this name available.
 release(); await new Promise(resolve=>setTimeout(resolve,0));
 expect(view.container.querySelector(".skill-token-unavailable")?.textContent).toBe("$missing");
});

function RecalledSkillComposer() {
 const [value,change]=useState("$add-issue"), textarea=useRef<HTMLTextAreaElement>(null);
 const skills=useSkillCompletion({value,change,textarea,machineId:machine,agentId:agent,initialBindings:[{start:0,end:10,token:"$add-issue",selection:entries[0]!.selection,stale:false}]});
 return <>{skills.wrap(<textarea aria-label="Recall fixture" ref={textarea} value={value} onChange={event=>skills.onChange(event.target.value,event.target.selectionStart)} {...skills.attributes}/>)}{skills.list}<button onClick={()=>skills.replaceUnbound("$add-issue $removed-skill",0)}>Recall missing text</button><output data-recalled-selections>{skills.selections.length}</output></>;
}
it("restores missing history-like tokens as exact text with no candidates or binding authority",async()=>{
 const transport=createRouterTransport(router=>router.service(SkillService,{listSkills:async()=>({skills:entries})}));
 const view=render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient()}><RecalledSkillComposer/></QueryClientProvider></TransportProvider>);
 fireEvent.click(screen.getByText("Recall missing text")); await waitFor(()=>expect(view.container.querySelector(".skill-token-unavailable")?.textContent).toBe("$removed-skill"));
 expect(screen.getByRole("textbox")).toHaveProperty("value","$add-issue $removed-skill"); expect(view.container.querySelector("[data-recalled-selections]")?.textContent).toBe("0"); expect(screen.queryByRole("listbox")).toBeNull();
});
it("prioritizes a complete live inventory at the retention bound instead of carrying displaced rows",async()=>{
 let current=entries;const client=new QueryClient();const transport=createRouterTransport(router=>router.service(SkillService,{listSkills:async()=>({skills:current})}));
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Composer send={vi.fn()}/></QueryClientProvider></TransportProvider>);
 const input=screen.getByRole("textbox");fireEvent.change(input,{target:{value:"$",selectionStart:1}});await screen.findAllByRole("option");
 current=Array.from({length:256},(_,index)=>({...entries[0]!,name:`live-${index}`,selection:{...entries[0]!.selection,skillId:newRequestId()}})); await client.invalidateQueries();
 await waitFor(()=>expect(screen.getAllByRole("option")).toHaveLength(256));expect(screen.queryByText("add-issue",{selector:"strong"})).toBeNull();expect(screen.queryByRole("option",{name:/Unavailable/})).toBeNull();
});
