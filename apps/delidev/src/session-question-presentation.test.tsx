// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, EventAction, InboxService, InteractionService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SystemService, WatchEventsResponseSchema, newRequestId, type WatchEventsResponse } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";
import { i18n } from "./localization";

function fixture(uncertain = false, mixed = false) {
 const id = newRequestId(), questionId = newRequestId();
 const session = create(ResourceSchema,{id,sessionId:id,kind:EntityKind.SESSION,schemaVersion:1,revision:1n,documentJson:encode({name:"Question collapse",workspace:"general-chat",outcome:"stopped",archive:"active",dispatch:"paused",recovery:"none"})});
 const data = {type:"user-question",closure:"open",questions:{questions:[{id:"q",header:"Pick",text:"Original question",secret:false,other:true,options:[]}]}};
 const original = create(ResourceSchema,{id:questionId,sessionId:id,kind:EntityKind.INTERACTION,schemaVersion:1,revision:9n,documentJson:encode(data)});
 const second = create(ResourceSchema, { ...original, id: newRequestId(), documentJson: encode({ ...data, questions: { questions: [{ ...data.questions.questions[0], text: "Second original question" }] } }) });
 const approval = create(ResourceSchema, { ...original, id: newRequestId(), documentJson: encode({ type: "native-approval", closure: "open", approval: { harness: "codex", version: "0.151.0", codex: { kind: "command", command: { command: "original command", available_decisions: [{ kind: "accept", execpolicy: null, network_policy: null }] } } } }) });
 let current = original;
 const respond = vi.fn(async (_request: unknown) => { if(uncertain && respond.mock.calls.length===1)throw new ConnectError("Lost original acknowledgement",Code.Unavailable); current=create(ResourceSchema,{...original,revision:10n,documentJson:encode({...data,response:{state:"queued"}})});return {interaction:current}; });
 const channels = new Set<{events:WatchEventsResponse[];notify:()=>void}>();
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[]})});
  router.service(SessionService,{listSessions:()=>({sessions:[session]}),listQueue:()=>({inputs:[]})});
  router.service(InteractionService,{respondQuestion:respond});
  router.service(ResourceService,{getSnapshot:()=>({resources:[session],cursor:newRequestId()}),getResource:request=>({resource:request.id===questionId?current:session}),listResources:request=>({resources:request.filter?.kind===EntityKind.INTERACTION?(mixed?[original,second,approval]:[original]):[]}),async *watchEvents(_request,context){const channel={events:[] as WatchEventsResponse[],notify:()=>{}};channels.add(channel);const abort=()=>channel.notify();context.signal.addEventListener("abort",abort);try{while(!context.signal.aborted){if(!channel.events.length)await new Promise<void>(resolve=>{channel.notify=resolve;if(context.signal.aborted)resolve();});while(channel.events.length&&!context.signal.aborted)yield channel.events.shift()!;}}finally{channels.delete(channel);context.signal.removeEventListener("abort",abort);}}});
  router.service(InboxService,{listInbox:()=>({entries:[]}),getNotificationPreferences:()=>({preferences:create(NotificationPreferencesSchema,{revision:1n})})});
 });
 return {transport,respond,original,publish:async(response?:Record<string,unknown>)=>{current=create(ResourceSchema,{...original,revision:11n,documentJson:encode({...data,closure:"native-closed",...(response?{response}:{})})});await act(async()=>{for(const channel of channels){channel.events.push(create(WatchEventsResponseSchema,{id:newRequestId(),cursor:newRequestId(),entityId:questionId,kind:EntityKind.INTERACTION,sessionId:id,revision:11n,action:EventAction.UPDATED}));channel.notify();}});}};
}
it("retire answer controls and verified empty disclosure using the same latest resource without remounting composer",async()=>{
 const f=fixture();render(<App transport={f.transport}/>);
 fireEvent.click(await screen.findByRole("button",{name:/General Chat Question collapse/}));
 const region=within(await screen.findByRole("region",{name:"Current session"}));
 const composer=await region.findByRole("textbox",{name:"Message"});fireEvent.change(composer,{target:{value:"Retained composer draft"}});
 const answer=await region.findByRole("textbox",{name:"Your answer"});fireEvent.change(answer,{target:{value:"Original response"}});
 const submit=region.getByRole("button",{name:"Send answers"});submit.focus();fireEvent.click(submit);
 await region.findByText("Response: queued");expect(region.queryByRole("button",{name:"Send answers"})).toBeNull();expect(region.queryByRole("textbox",{name:"Your answer"})).toBeNull();expect(f.respond).toHaveBeenCalledTimes(1);
 await waitFor(()=>expect(document.activeElement).toBe(composer));
 expect(screen.getByRole("region", { name: "Current session" }).querySelector(".requests")?.textContent).toContain("Agent requests · 1 on this page");
 await f.publish({state:"accepted"});
 await waitFor(()=>expect(screen.getByRole("region", { name: "Current session" }).querySelector(".requests")).toBeNull());
 expect(region.getByRole("textbox",{name:"Message"})).toBe(composer);expect(composer).toHaveProperty("value","Retained composer draft");
 await act(()=>i18n.changeLanguage("ko"));expect(screen.getByRole("textbox",{name:"메시지"})).toBe(composer);expect(composer).toHaveProperty("value","Retained composer draft");
});
it("retains the exact lost-acknowledgement receipt retry through authoritative closure",async()=>{
 const f=fixture(true);render(<App transport={f.transport}/>);fireEvent.click(await screen.findByRole("button",{name:/General Chat Question collapse/}));
 const answer=await screen.findByRole("textbox",{name:"Your answer"});fireEvent.change(answer,{target:{value:"Exact response"}});fireEvent.click(screen.getByRole("button",{name:"Send answers"}));
 await screen.findByRole("button",{name:"Retry the same answers"});await f.publish();
 const retry=await screen.findByRole("button",{name:"Retry the same answers"});expect(screen.queryByRole("textbox",{name:"Your answer"})).toBeNull();expect(f.respond).toHaveBeenCalledTimes(1);
 fireEvent.click(retry);await waitFor(()=>expect(f.respond).toHaveBeenCalledTimes(2));expect(f.respond.mock.calls[1][0]).toEqual(f.respond.mock.calls[0][0]);
});

it("retires only the resolved question, preserves unanswered/approval controllers and restores the next actionable focus", async () => {
 const f = fixture(false, true); render(<App transport={f.transport}/>); fireEvent.click(await screen.findByRole("button", { name: /General Chat Question collapse/ }));
 const firstRow = (await screen.findByText("Original question")).closest("article")!;
 const secondRow = (await screen.findByText("Second original question")).closest("article")!;
 const first = within(firstRow).getByRole("textbox", { name: "Your answer" }), second = within(secondRow).getByRole("textbox", { name: "Your answer" });
 const decision = screen.getByRole("combobox", { name: "Decision" });
 fireEvent.change(second, { target: { value: "Unanswered retained draft" } }); fireEvent.change(first, { target: { value: "Original answer" } });
 const submit = within(firstRow).getByRole("button", { name: "Send answers" }); submit.focus(); fireEvent.click(submit);
 await screen.findByText("Response: queued"); await waitFor(() => expect(document.activeElement).toBe(second));
 await f.publish({ state: "accepted" });
 await waitFor(() => expect(screen.getByRole("region", { name: "Current session" }).querySelector(".requests")?.textContent).toContain("Agent requests · 2 on this page"));
 expect(screen.getByText("Second original question").closest("article")).toBe(secondRow); expect(second).toHaveProperty("value", "Unanswered retained draft");
 expect(screen.getByRole("combobox", { name: "Decision" })).toBe(decision); expect(document.activeElement).toBe(second); expect(f.respond).toHaveBeenCalledTimes(1);
});
