// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { ConversationInteractions } from "./app";
import { documentBytes, Operation } from "./state";
import { en } from "./localization";
vi.mock("./platform", () => ({ storage: { read: async () => null, write: async () => {} } }));
afterEach(cleanup);
function fixture() {
 const session = newRequestId(), next = "opaque interaction cursor", mutate = vi.fn(async () => {});
 const row = (open: boolean) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.INTERACTION, revision: 3n, schemaVersion: 1, documentJson: documentBytes({ closure: open ? "open" : "closed", type: "approval-request", approval: { harness: "codex", version: "0.151.0", codex: { kind: "command" } } }) });
 const first = Array.from({length:50}, () => row(false)), original = row(true);
 let fail = false, cycle = false;
 const list = vi.fn((request: { filter?: { sessionId: string; pageToken: string } }) => {
  if (request.filter?.pageToken && fail) throw new ConnectError("private expired cursor", Code.InvalidArgument);
  return request.filter?.pageToken ? {resources:[original], nextPageToken: cycle ? next : ""} : {resources:first, nextPageToken:next};
 });
 const inspect = vi.fn(() => ({resource:original}));
 const transport=createRouterTransport(router=>router.service(ResourceService,{listResources:list,getResource:inspect}));
 const cache=new QueryClient({defaultOptions:{queries:{retry:false,gcTime:0}}});
 const view=(enabled=true,id=session)=><QueryClientProvider client={cache}><TransportProvider transport={transport}><ConversationInteractions key={id} id={id} enabled={enabled} available={enabled} mutate={mutate}/></TransportProvider></QueryClientProvider>;
 return {view,list,inspect,mutate,original,next,fail:(v:boolean)=>{fail=v;},cycle:()=>{cycle=true;}};
}
it("loads the 51st original approval and responds with its exact ID/revision",async()=>{
 const value=fixture();render(value.view());await screen.findByText(en.interactionsIncomplete);
 expect(screen.getAllByText(en.closed)).toHaveLength(50);
 fireEvent.click(screen.getByRole("button",{name:en.more}));await screen.findByRole("combobox",{name:en.approval});
 expect(value.list.mock.calls.at(-1)?.[0].filter?.pageToken).toBe(value.next);
 fireEvent.change(screen.getByRole("combobox",{name:en.approval}),{target:{value:"accept"}});
 const select=screen.getByRole("combobox",{name:en.approval});fireEvent.submit(select.closest("form")!);
 await waitFor(()=>expect(value.mutate).toHaveBeenCalledOnce());
 expect(value.inspect).toHaveBeenCalledOnce();
 expect(value.mutate.mock.calls[0]).toMatchObject([Operation.Approval,{mutation:{id:value.original.id,expectedRevision:3n}},value.original.id]);
});
it("keeps failed pages explicit, retries exact tokens and refreshes without mutation or background reads",async()=>{
 const value=fixture(),mounted=render(value.view());await screen.findByText(en.interactionsIncomplete);
 value.fail(true);fireEvent.click(screen.getByRole("button",{name:en.more}));await screen.findByRole("alert");
 expect(screen.queryByText("private expired cursor")).toBeNull();
 value.fail(false);fireEvent.click(screen.getByRole("button",{name:en.refreshRead}));await screen.findByRole("combobox",{name:en.approval});
 expect(value.list.mock.calls.at(-1)?.[0].filter?.pageToken).toBe(value.next);
 const calls=value.list.mock.calls.length;mounted.rerender(value.view(false));
 expect(screen.getByRole("button",{name:en.refresh})).toHaveProperty("disabled",true);expect(value.list).toHaveBeenCalledTimes(calls);
 mounted.rerender(value.view(true));fireEvent.click(screen.getByRole("button",{name:en.refresh}));await screen.findByText(en.interactionsIncomplete);
 expect(screen.queryByRole("combobox",{name:en.approval})).toBeNull();expect(value.mutate).not.toHaveBeenCalled();
 mounted.rerender(value.view(true,newRequestId()));await screen.findByText(en.interactionsIncomplete);
 expect(value.list.mock.calls.at(-1)?.[0].filter?.pageToken).toBe("");
});
it("rejects a repeated token chain with an explicit reload and no response authority",async()=>{
 const value=fixture();value.cycle();render(value.view());await screen.findByText(en.interactionsIncomplete);
 fireEvent.click(screen.getByRole("button",{name:en.more}));await screen.findByRole("alert");
 expect(screen.queryByRole("combobox",{name:en.approval})).toBeNull();expect(value.mutate).not.toHaveBeenCalled();
 expect(screen.getByRole("button",{name:en.refresh})).toHaveProperty("disabled",false);
});
