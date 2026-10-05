// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SidechatFindings } from "./sidechat";

it("retains selected complete reply revisions across navigation and uncertain findings acknowledgment", async () => {
 const parent = create(ResourceSchema, {schemaVersion:1,kind:EntityKind.SESSION,id:newRequestId(),revision:19n});
 const child = create(ResourceSchema, {schemaVersion:1,kind:EntityKind.SESSION,id:newRequestId(),revision:23n,documentJson:encode({fork:{source_session_id:parent.id,sidechat_parent_snapshot:{}}})});
 const message = create(ResourceSchema, {schemaVersion:1,kind:EntityKind.MESSAGE,id:newRequestId(),sessionId:child.id,revision:2n,documentJson:encode({role:"assistant",state:"complete",text:"Selected entire reply"})});
 const inherited = {...message,id:newRequestId(),documentJson:encode({role:"assistant",state:"complete",text:"Inherited reply",inherited:{source_session_id:parent.id}})};
 const streaming = {...message,id:newRequestId(),documentJson:encode({role:"assistant",state:"streaming",text:"Incomplete reply"})};
 const foreign = {...message,id:newRequestId(),sessionId:parent.id};
 const input = create(ResourceSchema, {schemaVersion:1,kind:EntityKind.QUEUE,id:newRequestId(),sessionId:parent.id,revision:1n});
 const sent:unknown[] = [];
 const send = vi.fn(async (request) => {sent.push(request); if(sent.length===1) throw new ConnectError("lost reply",Code.Unavailable); return {change:{session:parent,input}};});
 const transport=createRouterTransport((router)=>{router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.NATIVE_SIDECHAT_V1]})});router.service(ResourceService,{getResource:()=>({resource:parent})});router.service(SessionService,{sendSidechatFindings:send});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const view=(active:boolean)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{active?<SidechatFindings session={child} messages={[message,inherited,streaming,foreign]} />:<p>Other conversation</p>}</MutationIntents></QueryClientProvider></TransportProvider>;
 const rendered=render(view(true));
 fireEvent.click(await screen.findByRole("checkbox"));
 await waitFor(()=>expect((screen.getByRole("button",{name:"Send selected findings to parent queue"}) as HTMLButtonElement).disabled).toBe(false));
 expect(screen.queryByText("Inherited reply")).toBeNull(); expect(screen.queryByText("Incomplete reply")).toBeNull();
 fireEvent.click(screen.getByRole("button",{name:"Send selected findings to parent queue"}));
 await screen.findByRole("button",{name:"Retry the same findings request"});
 rendered.rerender(view(false));rendered.rerender(view(true));
 fireEvent.click(await screen.findByRole("button",{name:"Retry the same findings request"}));
 await screen.findByText(new RegExp(`Selected findings queued as ${input.id}`));
 expect(sent[1]).toEqual(sent[0]);
 expect(sent[0]).toMatchObject({mutation:{id:child.id,expectedRevision:23n},parentId:parent.id,expectedParentRevision:19n,messages:[{messageId:message.id,expectedRevision:2n}]});
 expect(send).toHaveBeenCalledTimes(2);
});

it("requires explicit server support and refuses replies above the full-content limit",async()=>{
 const parent=create(ResourceSchema,{schemaVersion:1,kind:EntityKind.SESSION,id:newRequestId(),revision:1n});
 const child=create(ResourceSchema,{schemaVersion:1,kind:EntityKind.SESSION,id:newRequestId(),revision:1n,documentJson:encode({fork:{source_session_id:parent.id,sidechat_parent_snapshot:{}}})});
 const message=create(ResourceSchema,{schemaVersion:1,kind:EntityKind.MESSAGE,id:newRequestId(),revision:1n,sessionId:child.id,documentJson:encode({role:"assistant",state:"complete",text:"x".repeat(256*1024)})});
 const send=vi.fn();let supported=false;
 const transport=createRouterTransport((router)=>{router.service(SystemService,{getStatus:()=>({capabilities:supported?[SystemCapability.NATIVE_SIDECHAT_V1]:[]})});router.service(ResourceService,{getResource:()=>({resource:parent})});router.service(SessionService,{sendSidechatFindings:send});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SidechatFindings session={child} messages={[message]} /></MutationIntents></QueryClientProvider></TransportProvider>);
 await screen.findByText("Update the connected server to send Sidechat findings.");
 expect(screen.queryByRole("checkbox")).toBeNull();
 supported=true;await client.invalidateQueries();
 fireEvent.click(await screen.findByRole("checkbox"));
 expect(screen.getByRole("alert").textContent).toContain("Replies are sent in full");
 expect((screen.getByRole("button",{name:"Send selected findings to parent queue"}) as HTMLButtonElement).disabled).toBe(true);
 expect(send).not.toHaveBeenCalled();
});
