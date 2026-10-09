// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, SessionService, newRequestId, type MoveQueuedInputRequest } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { WaitingQueue } from "./waiting-queue";

function fixture() {
 const session=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,schemaVersion:1,revision:1n,documentJson:encode({archive:"active"})});
 const rows=["First","Second","Third"].map((prompt,index)=>create(ResourceSchema,{id:newRequestId(),sessionId:session.id,kind:EntityKind.QUEUE,schemaVersion:1,revision:BigInt(index+3),documentJson:encode({prompt,sequence:index+1,delivery:"queued",mode:"execute"})}));
 let visible=rows.slice();
 const read=vi.fn(async()=>({inputs:visible,nextPageToken:"",currentQueueGeneration:9007199254740993n,waitingCount:visible.length}));
 const move=vi.fn(async(request:MoveQueuedInputRequest)=>({change:{requestId:request.mutation!.requestId,session},currentQueueGeneration:9007199254740994n}));
 const transport=createRouterTransport(router=>router.service(SessionService,{listWaitingQueue:read,moveQueuedInput:move}));
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const view=(revision="1")=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><WaitingQueue sessionId={session.id} session={session} active revision={revision} drafts={new Map()} saveDraft={()=>{}} readOnly={false} refreshHistory={()=>{}}/></MutationIntents></QueryClientProvider></TransportProvider>;
 return {session,rows,read,move,view,replace:(next:typeof rows)=>{visible=next;}};
}
it("uses authoritative move-before revisions and exact bigint generations without optimistic ordering",async()=>{
 const f=fixture();let acknowledge:()=>void=()=>{};
 f.move.mockImplementationOnce(async request=>{await new Promise<void>(resolve=>{acknowledge=resolve;});return {change:{requestId:request.mutation!.requestId,session:f.session},currentQueueGeneration:9007199254740994n};});
 render(f.view());await screen.findByText("Third");
 const second=screen.getByText("Second").closest("article")!;
 fireEvent.click(within(second).getByRole("button",{name:"More input actions"}));fireEvent.click(within(second).getByRole("menuitem",{name:"Move up"}));
 await waitFor(()=>expect(f.move).toHaveBeenCalledOnce());
 expect(f.move.mock.calls[0][0]).toMatchObject({mutation:{id:f.rows[1].id,expectedRevision:4n},sessionId:f.session.id,expectedQueueGeneration:9007199254740993n,beforeInputId:f.rows[0].id,beforeInputRevision:3n});
 expect(screen.getAllByRole("article").map(row=>row.querySelector(".queue-preview")?.textContent)).toEqual(["First","Second","Third"]);
 f.replace([f.rows[1],f.rows[0],f.rows[2]]);await act(async()=>acknowledge());
 await waitFor(()=>expect(screen.getAllByRole("article").map(row=>row.querySelector(".queue-preview")?.textContent)).toEqual(["Second","First","Third"]));
 expect(screen.getByRole("status").textContent).toBe("Waiting input moved.");
});
it("retains an uncertain movement outside retired rows and retries its original request",async()=>{
 const f=fixture();f.move.mockRejectedValueOnce(new ConnectError("Unknown original outcome",Code.Unavailable));
 const view=render(f.view());await screen.findByText("Third");
 const third=screen.getByText("Third").closest("article")!;fireEvent.click(within(third).getByRole("button",{name:"More input actions"}));fireEvent.click(within(third).getByRole("menuitem",{name:"Move up"}));
 await screen.findByRole("button",{name:"Retry the same movement"});const original=f.move.mock.calls[0][0];
 f.replace([]);view.rerender(f.view("2"));await waitFor(()=>expect(screen.queryByText("Third")).toBeNull());
 fireEvent.click(screen.getByRole("button",{name:"Retry the same movement"}));await waitFor(()=>expect(f.move).toHaveBeenCalledTimes(2));
 expect(f.move.mock.calls[1][0]).toEqual(original);await waitFor(()=>expect(screen.queryByRole("button",{name:"Retry the same movement"})).toBeNull());
});

it("retains movement when the response has no original acknowledgement",async()=>{
 const f=fixture();f.move.mockImplementationOnce(async()=>({currentQueueGeneration:9007199254740994n} as Awaited<ReturnType<typeof f.move>>));
 render(f.view());await screen.findByText("Third");
 const second=screen.getByText("Second").closest("article")!;
 fireEvent.click(within(second).getByRole("button",{name:"More input actions"}));fireEvent.click(within(second).getByRole("menuitem",{name:"Move up"}));
 await screen.findByRole("button",{name:"Retry the same movement"});
 expect(screen.getByRole("status").textContent).not.toBe("Waiting input moved.");
 const original=f.move.mock.calls[0][0];
 fireEvent.click(screen.getByRole("button",{name:"Retry the same movement"}));await waitFor(()=>expect(f.move).toHaveBeenCalledTimes(2));
 expect(f.move.mock.calls[1][0]).toEqual(original);
 await waitFor(()=>expect(screen.queryByRole("button",{name:"Retry the same movement"})).toBeNull());
});
