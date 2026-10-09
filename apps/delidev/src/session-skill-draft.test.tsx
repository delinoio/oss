import { sessionInputReceipt } from "./test-session-input";
// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, InboxService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SkillService, SkillProvenance, SystemService, newRequestId, type EnqueueInputRequest } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

// Restrict role queries to their visible landmark. Retained hidden session
// trees must not multiply the cost of every composer or navigation lookup.
const sidebar = () => within(screen.getByRole("complementary", { name: "Application sidebar" }));
const currentSession = () => within(screen.getByRole("region", { name: "Current session" }));

function fixture() {
 const machine=newRequestId(),agent=newRequestId();
 const make=(name:string)=>{const id=newRequestId();return create(ResourceSchema,{id,sessionId:id,kind:EntityKind.SESSION,revision:1n,schemaVersion:1,documentJson:encode({name,workspace:"general-chat",machine_id:machine,agent_id:agent,outcome:"stopped",archive:"active",dispatch:"paused",recovery:"none"})});};
 const original=make("Skill original"),other=make("Skill other");
 const selection={$typeName:"delidev.v1.SkillSelection" as const,workerDeviceId:newRequestId(),inventoryId:newRequestId(),skillId:newRequestId(),contentRevision:"a".repeat(64)};
 const enqueue=vi.fn(async(request:EnqueueInputRequest)=>sessionInputReceipt(original,request));
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[]})});
  router.service(SkillService,{listSkills:()=>({skills:[{name:"retained",description:"Immutable selected fixture",provenance:SkillProvenance.USER,selection}]})});
  router.service(SessionService,{listSessions:()=>({sessions:[original,other]}),listQueue:()=>({inputs:[]}),enqueueInput:enqueue});
  router.service(ResourceService,{getSnapshot:request=>({resources:[request.filter?.sessionId===other.id?other:original],cursor:"fixture-snapshot"}),listResources:()=>({resources:[]}),async *watchEvents(_request,context){if(!context.signal.aborted)await new Promise<void>(done=>context.signal.addEventListener("abort",()=>done(),{once:true}));}});
  router.service(InboxService,{getNotificationPreferences:()=>({preferences:create(NotificationPreferencesSchema,{revision:1n,interactions:true})}),listInbox:()=>({entries:[]})});
 });
 const select=async()=>{fireEvent.click(await sidebar().findByRole("button",{name:/General Chat Skill original/}));const input=await currentSession().findByRole("textbox",{name:"Message"});fireEvent.change(input,{target:{value:"$ret",selectionStart:4}});fireEvent.click(await currentSession().findByRole("option",{name:/retained/}));expect(input).toHaveProperty("value","$retained");return input;};
 const returnToOriginal=async()=>{fireEvent.click(sidebar().getByRole("button",{name:/General Chat Skill other/}));await currentSession().findByRole("heading",{name:"Skill other"});fireEvent.click(sidebar().getByRole("button",{name:/General Chat Skill original/}));await currentSession().findByRole("heading",{name:"Skill original"});return currentSession().getByRole("textbox",{name:"Message"});};
 return {transport,original,other,selection,enqueue,select,returnToOriginal};
}
it("retains exact skill invocation across retained App session tab switches",async()=>{const f=fixture();render(<App transport={f.transport}/>);const before=await f.select();const after=await f.returnToOriginal();expect(after).toBe(before);expect(after).toHaveProperty("value","$retained");fireEvent.click(currentSession().getByRole("button",{name:"Queue message"}));await waitFor(()=>expect(f.enqueue).toHaveBeenCalledOnce());expect(f.enqueue.mock.calls[0]![0].skills?.selections).toEqual([f.selection]);});

it("retains the original opaque selection across same-identity reconnect",async()=>{
 const f=fixture(),props={pairingAuthority:{endpoint:"http://127.0.0.1:46399",serverId:newRequestId()},currentDeviceId:newRequestId()};
 const view=render(<App transport={f.transport} {...props}/>);await f.select();
 view.rerender(<App transport={{...f.transport}} {...props} connectionEpoch={1}/>);
 await f.returnToOriginal();fireEvent.click(currentSession().getByRole("button",{name:"Queue message"}));await waitFor(()=>expect(f.enqueue).toHaveBeenCalledOnce());expect(f.enqueue.mock.calls[0]![0].skills?.selections).toEqual([f.selection]);
});
it("clears only the original retained draft when its request accepts while hidden",async()=>{
 const f=fixture();let resolve!:()=>void;f.enqueue.mockImplementation(async(request)=>{await new Promise<void>(done=>{resolve=done;});return sessionInputReceipt(f.original,request);});
 render(<App transport={f.transport}/>);await f.select();fireEvent.click(currentSession().getByRole("button",{name:"Queue message"}));await waitFor(()=>expect(f.enqueue).toHaveBeenCalledOnce());
 fireEvent.click(sidebar().getByRole("button",{name:/General Chat Skill other/}));const other=await currentSession().findByRole("textbox",{name:"Message"});fireEvent.change(other,{target:{value:"Keep other draft",selectionStart:16}});
 await act(async()=>resolve());await waitFor(()=>expect(other).toHaveProperty("value","Keep other draft"));
 fireEvent.click(sidebar().getByRole("button",{name:/General Chat Skill original/}));await waitFor(()=>expect(currentSession().getByRole("textbox",{name:"Message"})).toHaveProperty("value",""));
});
