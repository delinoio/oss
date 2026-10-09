// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionService, newRequestId, type RenameSessionRequest, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionNameEditorProvider, useSessionNameEditor, validSessionName } from "./session-name-editor";
function Names({ id }: { id: string }) { const open=useSessionNameEditor();return <><button onDoubleClick={e=>open?.(id,e.currentTarget)}>Sidebar name</button><h2 tabIndex={-1} onDoubleClick={e=>open?.(id,e.currentTarget)}>Header name</h2><main id="main" tabIndex={-1}/></>; }
function fixture(){
 const id=newRequestId();let session=create(ResourceSchema,{id,kind:EntityKind.SESSION,revision:8n,schemaVersion:1,documentJson:encode({name:"Original name"})});
 const read=vi.fn(()=>({resource:session}));const rename=vi.fn(async (request:{mutation?:{requestId:string;expectedRevision:bigint};name:string})=>{session={...session,revision:session.revision+1n,documentJson:encode({name:request.name})};return {change:{session,requestId:request.mutation?.requestId}};});
 const transport=createRouterTransport(router=>{router.service(ResourceService,{getResource:read});router.service(SessionService,{renameSession:rename});});const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const view=<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionNameEditorProvider><Names id={id}/></SessionNameEditorProvider></MutationIntents></QueryClientProvider></TransportProvider>;
 return {id,view,read,rename,client,peer:()=>{session={...session,revision:session.revision+1n};}};
}
it("validates UTF-8 bytes and Unicode without altering the original name",()=>{expect(validSessionName("가".repeat(85))).toBe(true);for(const value of [" ","a\0b","가".repeat(86),"x\uD800","\uDC00x"])expect(validSessionName(value)).toBe(false);expect(validSessionName("😀".repeat(64))).toBe(true);});
it("opens only on double-click and shares a retained draft across both original-session gestures",async()=>{const f=fixture();render(f.view);fireEvent.click(screen.getByText("Sidebar name"));expect(f.read).not.toHaveBeenCalled();fireEvent.doubleClick(screen.getByText("Sidebar name"));const input=await screen.findByLabelText("Session name");await waitFor(()=>expect(input).toHaveProperty("value","Original name"));expect(document.activeElement).toBe(input);expect((input as HTMLInputElement).selectionEnd).toBe("Original name".length);expect(f.rename).not.toHaveBeenCalled();fireEvent.change(input,{target:{value:"Retained draft"}});fireEvent.click(screen.getByRole("button",{name:"Close"}));fireEvent.doubleClick(screen.getByText("Header name"));expect(await screen.findByLabelText("Session name")).toBe(input);expect(input).toHaveProperty("value","Retained draft");fireEvent.click(screen.getByRole("button",{name:"Save"}));await waitFor(()=>expect(screen.queryByRole("dialog")).toBeNull());expect(f.rename.mock.calls[0][0]).toMatchObject({mutation:{id:f.id,expectedRevision:8n},name:"Retained draft"});});
it("blocks peer conflicts until explicit draft discard and preserves original uncertain requests",async()=>{const f=fixture();f.rename.mockRejectedValueOnce(new ConnectError("Original acknowledgment lost",Code.Unavailable));render(f.view);fireEvent.doubleClick(screen.getByText("Sidebar name"));const input=await screen.findByLabelText("Session name");await waitFor(()=>expect(input).toHaveProperty("value","Original name"));fireEvent.change(input,{target:{value:"Draft"}});f.peer();await f.client.invalidateQueries({refetchType:"active"});await waitFor(()=>expect(screen.getByRole("button",{name:"Save"})).toHaveProperty("disabled",true));expect(input).toHaveProperty("value","Draft");fireEvent.click(screen.getByRole("button",{name:"Discard draft and reload"}));await waitFor(()=>expect(input).toHaveProperty("value","Original name"));fireEvent.change(input,{target:{value:"Fresh name"}});fireEvent.click(screen.getByRole("button",{name:"Save"}));await screen.findByRole("button",{name:"Retry original rename"});const original=f.rename.mock.calls[0][0];fireEvent.click(screen.getByRole("button",{name:"Close"}));fireEvent.doubleClick(screen.getByText("Header name"));fireEvent.click(await screen.findByRole("button",{name:"Retry original rename"}));await waitFor(()=>expect(f.rename).toHaveBeenCalledTimes(2));expect(f.rename.mock.calls[1][0]).toEqual(original);});

it("retains a second session editor when the original closed editor finishes a rename", async () => {
 const first = newRequestId(), second = newRequestId();
 const resources = new Map([first, second].map(id => [id, create(ResourceSchema, { id, kind: EntityKind.SESSION, revision: 8n, schemaVersion: 1, documentJson: encode({ name: id === first ? "First" : "Second" }) })]));
 let finish!: (reply: { change: { session: Resource; requestId: string } }) => void;
 const rename = vi.fn((_request: RenameSessionRequest) => new Promise<{ change: { session: Resource; requestId: string } }>(resolve => { finish = resolve; }));
 const transport = createRouterTransport(router => {
  router.service(ResourceService, { getResource: request => ({ resource: resources.get(request.id) }) });
  router.service(SessionService, { renameSession: rename });
 });
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 function Openers() { const open = useSessionNameEditor(); return <>{[first, second].map((id, index) => <button key={id} onDoubleClick={event => open?.(id, event.currentTarget)}>{index ? "Second name" : "First name"}</button>)}</>; }
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionNameEditorProvider><Openers /></SessionNameEditorProvider></MutationIntents></QueryClientProvider></TransportProvider>);
 fireEvent.doubleClick(screen.getByText("First name"));
 const firstInput = await screen.findByLabelText("Session name"); await waitFor(() => expect(firstInput).toHaveProperty("value", "First"));
 fireEvent.change(firstInput, { target: { value: "Renamed first" } }); fireEvent.click(screen.getByRole("button", { name: "Save" }));
 await waitFor(() => expect(rename).toHaveBeenCalledTimes(1)); const request = rename.mock.calls[0]![0];
 fireEvent.click(screen.getByRole("button", { name: "Close" })); fireEvent.doubleClick(screen.getByText("Second name"));
 const secondInput = within(await screen.findByRole("dialog")).getByLabelText("Session name"); await waitFor(() => expect(secondInput).toHaveProperty("value", "Second"));
 fireEvent.change(secondInput, { target: { value: "Retained second draft" } });
 await act(async () => finish({ change: { session: create(ResourceSchema, { ...resources.get(first)!, revision: 9n, documentJson: encode({ name: "Renamed first" }) }), requestId: request.mutation!.requestId } }));
 expect(screen.getByRole("dialog").contains(secondInput)).toBe(true); expect(secondInput).toHaveProperty("value", "Retained second draft"); expect(document.activeElement).toBe(secondInput);
});

it("reads a peer revision after a definite rename conflict without losing the draft", async () => {
 const f=fixture(); render(f.view); fireEvent.doubleClick(screen.getByText("Sidebar name"));
 const input=await screen.findByLabelText("Session name"); await waitFor(()=>expect(input).toHaveProperty("value","Original name"));
 fireEvent.change(input,{target:{value:"Retained draft"}}); f.peer();
 f.rename.mockRejectedValueOnce(new ConnectError("Revision changed",Code.Aborted)); fireEvent.click(screen.getByRole("button",{name:"Save"}));
 fireEvent.click(await screen.findByRole("button",{name:"Discard draft and reload"}));
 await waitFor(()=>expect(input).toHaveProperty("value","Original name"));
 fireEvent.change(input,{target:{value:"Fresh name"}});fireEvent.click(screen.getByRole("button",{name:"Save"}));
 await waitFor(()=>expect(f.rename).toHaveBeenCalledTimes(2));expect(f.rename.mock.calls[1][0].mutation?.expectedRevision).toBe(9n);
});
