import { useState } from "react";
import { useConversationDrafts } from "./conversation-drafts";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, SessionService, newRequestId } from "@delinoio/delidev-api-client";
import { QueuedInput, type QueuedInputDraft } from "./queue";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";

function fixture() {
  const id = newRequestId(), execution = newRequestId(), turn = newRequestId();
  const session = create(ResourceSchema, { id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 4n, documentJson: encode({ outcome: "running", archive: "active", active_execution_id: execution, execution: { execution_id: execution, native_turn_id: turn } }) });
  const resource = create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 6n, documentJson: encode({ prompt: "Original queued input", sequence: 3, delivery: "queued", mode: "plan" }) });
  const edit = vi.fn(async (_request: unknown) => ({ change: { input: create(ResourceSchema, { ...resource, revision: 7n, documentJson: encode({ prompt: "Changed", sequence: 3, delivery: "queued", mode: "plan" }) }) } }));
  const steer = vi.fn(async (_request: unknown) => ({}));
  const remove = vi.fn(async (_request: unknown) => ({}));
  const transport = createRouterTransport((router) => router.service(SessionService, { editQueuedInput: edit, steerQueuedInput: steer, removeQueuedInput: remove }));
  const client = new QueryClient();
  const view = (input = resource) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><QueuedInput resource={input} session={session} refresh={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { resource, session, execution, turn, edit, steer, remove, view, transport, client };
}

it("binds an edit to the revision at which editing began and preserves its draft after peer changes", async () => {
  const value = fixture();
  const mounted = render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Edited input" }), { target: { value: "My staged change" } });
  mounted.rerender(value.view(create(ResourceSchema, { ...value.resource, revision: 8n, documentJson: encode({ prompt: "Peer change", sequence: 3, delivery: "queued", mode: "plan" }) })));
  expect((screen.getByRole("textbox", { name: "Edited input" }) as HTMLTextAreaElement).value).toBe("My staged change");
  fireEvent.submit(screen.getByRole("button", { name: "Save input" }).closest("form")!);
  expect(value.edit).not.toHaveBeenCalled();
  expect((screen.getByRole("button", { name: "Save input" }) as HTMLButtonElement).disabled).toBe(true);
});

it("Steers exactly the selected queue item into the observed active execution and turn", async () => {
  const value = fixture();
  render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Steer with this input" }));
  await waitFor(() => expect(value.steer).toHaveBeenCalledTimes(1));
  expect(value.steer.mock.calls[0][0]).toMatchObject({ mutation: { id: value.resource.id, expectedRevision: 6n }, sessionId: value.session.id, expectedExecutionId: value.execution, expectedTurnId: value.turn });
  expect(value.edit).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
});

it("does not expose editing or removal once native delivery has claimed the input", () => {
  const value = fixture();
  render(value.view(create(ResourceSchema, { ...value.resource, documentJson: encode({ prompt: "Claimed input", sequence: 3, delivery: "claimed", mode: "plan" }) })));
  expect(screen.queryByRole("button", { name: "Edit input" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Remove input" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Steer with this input" })).toBeNull();
});


it("restores a queue edit after payload eviction without replay or autofocus", async () => {
  const f = fixture();
  function View() {
    const [visible, setVisible] = useState(true);
    const drafts = useConversationDrafts<QueuedInputDraft>();
    return <><input aria-label="Composer outside payload" /><button onClick={() => setVisible(value => !value)}>Toggle payload</button>{visible ? <QueuedInput resource={f.resource} session={f.session} refresh={() => {}} draft={drafts.values.get(f.resource.id)} changeDraft={value => drafts.save(f.resource.id, value)} /> : null}</>;
  }
  render(<TransportProvider transport={f.transport}><QueryClientProvider client={f.client}><MutationIntents><View /></MutationIntents></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Edited input" }), { target: { value: "Preserved original edit" } });
  fireEvent.click(screen.getByRole("button", { name: "Toggle payload" }));
  const composer = screen.getByRole("textbox", { name: "Composer outside payload" });
  composer.focus();
  fireEvent.click(screen.getByRole("button", { name: "Toggle payload" }));
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty("value", "Preserved original edit");
  expect(document.activeElement).toBe(composer);
  expect(f.edit).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await waitFor(() => expect(f.edit).toHaveBeenCalledTimes(1));
  expect(f.edit.mock.calls[0][0]).toMatchObject({ prompt: "Preserved original edit", mutation: { id: f.resource.id, expectedRevision: f.resource.revision } });
});

it("edits skill-bound input with explicit retained selections and clears them deliberately",async()=>{
 const f=fixture(), skillId=newRequestId(),inventoryId=newRequestId(),workerDeviceId=newRequestId();
 const bound=create(ResourceSchema,{...f.resource,documentJson:encode({prompt:"Before $add-issue after",sequence:3,delivery:"queued",mode:"plan",skills:[{skill_id:skillId,inventory_id:inventoryId,worker_device_id:workerDeviceId,content_revision:"a".repeat(64),snapshot_id:newRequestId()}],skill_names:{[skillId]:"add-issue"}})});
 render(f.view(bound));expect((screen.getByRole("button",{name:"Steer with this input"}) as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByRole("button",{name:"Edit input"}));fireEvent.change(screen.getByRole("textbox",{name:"Edited input"}),{target:{value:"New Before $add-issue after",selectionStart:0}});
 fireEvent.click(screen.getByRole("button",{name:"Save input"}));await waitFor(()=>expect(f.edit).toHaveBeenCalledTimes(1));expect(f.edit.mock.calls[0]![0]).toMatchObject({skills:{selections:[{skillId,inventoryId,workerDeviceId,contentRevision:"a".repeat(64)}]}});
});
it("explicitly clears selected bindings without altering visible text",async()=>{
 const f=fixture(),skillId=newRequestId();const bound=create(ResourceSchema,{...f.resource,documentJson:encode({prompt:"$add-issue remains plain",sequence:3,delivery:"queued",mode:"plan",skills:[{skill_id:skillId,inventory_id:newRequestId(),worker_device_id:newRequestId(),content_revision:"a".repeat(64),snapshot_id:newRequestId()}],skill_names:{[skillId]:"add-issue"}})});
 render(f.view(bound));fireEvent.click(screen.getByRole("button",{name:"Edit input"}));fireEvent.click(screen.getByRole("button",{name:"Clear selected skills"}));expect(screen.getByRole("textbox",{name:"Edited input"})).toHaveProperty("value","$add-issue remains plain");fireEvent.click(screen.getByRole("button",{name:"Save input"}));await waitFor(()=>expect(f.edit).toHaveBeenCalledTimes(1));expect(f.edit.mock.calls[0]![0]).toMatchObject({skills:{selections:[]}});
});

it("retains skill bindings in the external queue draft across payload eviction",async()=>{
 const f=fixture(),skillId=newRequestId(),inventoryId=newRequestId(),workerDeviceId=newRequestId();const resource=create(ResourceSchema,{...f.resource,documentJson:encode({prompt:"Original $add-issue",sequence:3,delivery:"queued",mode:"plan",skills:[{skill_id:skillId,inventory_id:inventoryId,worker_device_id:workerDeviceId,content_revision:"a".repeat(64),snapshot_id:newRequestId()}],skill_names:{[skillId]:"add-issue"}})});
 function View(){const [visible,setVisible]=useState(true),drafts=useConversationDrafts<QueuedInputDraft>();return <><button onClick={()=>setVisible(v=>!v)}>Toggle payload</button>{visible?<QueuedInput resource={resource} session={f.session} refresh={()=>{}} draft={drafts.values.get(resource.id)} changeDraft={value=>drafts.save(resource.id,value)}/>:null}</>;}
 render(<TransportProvider transport={f.transport}><QueryClientProvider client={f.client}><MutationIntents><View/></MutationIntents></QueryClientProvider></TransportProvider>);fireEvent.click(screen.getByRole("button",{name:"Edit input"}));fireEvent.change(screen.getByRole("textbox",{name:"Edited input"}),{target:{value:"Changed Original $add-issue",selectionStart:0}});fireEvent.click(screen.getByRole("button",{name:"Toggle payload"}));fireEvent.click(screen.getByRole("button",{name:"Toggle payload"}));expect(screen.getByRole("textbox",{name:"Edited input"})).toHaveProperty("value","Changed Original $add-issue");fireEvent.click(screen.getByRole("button",{name:"Save input"}));await waitFor(()=>expect(f.edit).toHaveBeenCalledTimes(1));expect(f.edit.mock.calls[0]![0]).toMatchObject({skills:{selections:[{skillId,inventoryId,workerDeviceId}]}});
});

it.each(["two packages","earlier literal"])("blocks ambiguous restored token spans through edits: %s",async(kind)=>{
 const f=fixture(),ids=kind==="two packages"?[newRequestId(),newRequestId()]:[newRequestId()];
 const resource=create(ResourceSchema,{...f.resource,documentJson:encode({prompt:"$same $same",sequence:3,delivery:"queued",mode:"plan",skills:ids.map(skill_id=>({skill_id,inventory_id:newRequestId(),worker_device_id:newRequestId(),content_revision:"a".repeat(64),snapshot_id:newRequestId()})),skill_names:Object.fromEntries(ids.map(id=>[id,"same"]))})});
 render(f.view(resource));fireEvent.click(screen.getByRole("button",{name:"Edit input"}));const input=screen.getByRole("textbox",{name:"Edited input"});
 for(const value of ["$other $same","$same $other","plain text"]){fireEvent.change(input,{target:{value,selectionStart:0}});expect((screen.getByRole("button",{name:"Save input"}) as HTMLButtonElement).disabled).toBe(true);fireEvent.submit(input.closest("form")!);expect(f.edit).not.toHaveBeenCalled();}
 fireEvent.click(screen.getByRole("button",{name:"Clear selected skills"}));fireEvent.click(screen.getByRole("button",{name:"Save input"}));await waitFor(()=>expect(f.edit).toHaveBeenCalledTimes(1));expect(f.edit.mock.calls[0]![0]).toMatchObject({skills:{selections:[]}});
});
