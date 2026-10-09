import { useState } from "react";
import { useConversationDrafts } from "./conversation-drafts";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, SessionService, newRequestId } from "@delinoio/delidev-api-client";
import { QueuedInput, type QueuedInputDraft } from "./queue";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";

function fixture(prompt = "Original queued input") {
  const id = newRequestId(), execution = newRequestId(), turn = newRequestId();
  const session = create(ResourceSchema, { id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 4n, documentJson: encode({ outcome: "running", archive: "active", active_execution_id: execution, execution: { execution_id: execution, native_turn_id: turn } }) });
  const resource = create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 6n, documentJson: encode({ prompt, sequence: 3, delivery: "queued", mode: "plan" }) });
  const edit = vi.fn(async (_request: unknown) => ({ change: { input: create(ResourceSchema, { ...resource, revision: 7n, documentJson: encode({ prompt: "Changed", sequence: 3, delivery: "queued", mode: "plan" }) }) } }));
  const steer = vi.fn(async (_request: unknown) => ({}));
  const remove = vi.fn(async (_request: unknown) => ({}));
  const transport = createRouterTransport((router) => router.service(SessionService, { editQueuedInput: edit, steerQueuedInput: steer, removeQueuedInput: remove }));
  const client = new QueryClient();
  const view = (input = resource, compact = false, readOnly = false) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><QueuedInput readOnly={readOnly} compact={compact} resource={input} session={session} refresh={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { resource, session, execution, turn, edit, steer, remove, view, transport, client };
}

it.each([
  { name: "100,000-byte queued ASCII prompt", prompt: "a".repeat(100000) },
  { name: "65,537-byte ASCII prompt", prompt: "a".repeat(65537) },
  { name: "256 KiB ASCII prompt", prompt: "a".repeat(256 << 10) },
  { name: "256 KiB supplementary Unicode prompt", prompt: "😀".repeat(65536) },
])("edits the complete $name at its original revision", async ({ prompt }) => {
  const value = fixture(prompt);
  render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  const editor = screen.getByRole("textbox", { name: "Edited input" });
  // jsdom does not enforce native maxlength during typing. Check the actual
  // textarea attribute as well as the controlled draft and transmitted bytes.
  expect(editor.hasAttribute("maxlength")).toBe(false);
  expect(editor).toHaveProperty("value", prompt);
  const edited = prompt.endsWith("😀") ? `${prompt.slice(0, -2)}😃` : `${prompt.slice(0, -1)}b`;
  fireEvent.change(editor, { target: { value: edited, selectionStart: edited.length } });
  expect(editor).toHaveProperty("value", edited);
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await waitFor(() => expect(value.edit).toHaveBeenCalledOnce());
  expect(value.edit.mock.calls[0]![0]).toMatchObject({ prompt: edited, mutation: { id: value.resource.id, expectedRevision: value.resource.revision }, sessionId: value.session.id });
  expect(new TextEncoder().encode(edited)).toHaveLength(new TextEncoder().encode(prompt).byteLength);
});

it.each([
  { name: "ASCII", valid: "a".repeat(256 << 10), overflow: "a".repeat((256 << 10) + 1) },
  { name: "supplementary Unicode", valid: "😀".repeat(65536), overflow: `${"😀".repeat(65536)}a` },
])("retains the last valid $name edit on UTF-8 overflow", ({ valid, overflow }) => {
  const value = fixture();
  render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  const editor = screen.getByRole("textbox", { name: "Edited input" });
  fireEvent.change(editor, { target: { value: valid, selectionStart: 0 } });
  fireEvent.change(editor, { target: { value: overflow, selectionStart: 0 } });
  expect(editor).toHaveProperty("value", valid);
  expect(screen.getByRole("alert")).toBeTruthy();
  expect(value.edit).not.toHaveBeenCalled();
  fireEvent.change(editor, { target: { value: "Within the budget", selectionStart: 0 } });
  expect(editor).toHaveProperty("value", "Within the budget");
  expect(screen.queryByRole("alert")).toBeNull();
  fireEvent.change(editor, { target: { value: "  \n  ", selectionStart: 0 } });
  expect(screen.getByRole("button", { name: "Save input" })).toHaveProperty("disabled", true);
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  expect(value.edit).not.toHaveBeenCalled();
});

it("locks a large pending and uncertain edit and explicitly retries the original bytes", async () => {
  const value = fixture("a".repeat(100000));
  let reject!: (reason: ConnectError) => void;
  value.edit.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
  render(value.view());
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  const editor = screen.getByRole("textbox", { name: "Edited input" });
  const edited = `${"a".repeat(99999)}b`;
  fireEvent.change(editor, { target: { value: edited, selectionStart: edited.length } });
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await waitFor(() => expect(value.edit).toHaveBeenCalledOnce());
  expect(editor).toHaveProperty("disabled", true);
  fireEvent.change(editor, { target: { value: "Replacement" } });
  fireEvent.submit(editor.closest("form")!);
  expect(editor).toHaveProperty("value", edited);
  expect(value.edit).toHaveBeenCalledOnce();
  await act(async () => { reject(new ConnectError("Lost response", Code.Unavailable)); });
  const retry = await screen.findByRole("button", { name: "Retry the same edit" });
  expect(editor).toHaveProperty("disabled", true);
  fireEvent.change(editor, { target: { value: "Another replacement" } });
  expect(editor).toHaveProperty("value", edited);
  fireEvent.click(retry);
  await waitFor(() => expect(value.edit).toHaveBeenCalledTimes(2));
  expect(value.edit.mock.calls[1]![0]).toEqual(value.edit.mock.calls[0]![0]);
  expect(value.edit.mock.calls[0]![0]).toMatchObject({ prompt: edited, mutation: { expectedRevision: value.resource.revision } });
});

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

it("disables image Steer and verifies immutable images before accepting an empty text edit", async () => {
  const value = fixture();
  const image = { id: newRequestId(), machine_id: newRequestId(), media_type: "image/png", byte_length: 24, sha256: "a".repeat(64) };
  const input = create(ResourceSchema, { ...value.resource, documentJson: encode({ prompt: "Caption", delivery: "queued", attachments: [image] }) });
  value.edit.mockImplementation(async () => ({ change: { input: create(ResourceSchema, { ...input, revision: 7n, documentJson: encode({ prompt: "", delivery: "queued", attachments: [] }) }) } }));
  render(value.view(input));
  expect(screen.getByRole("button", { name: "Steer with this input" })).toHaveProperty("disabled", true);
  fireEvent.click(screen.getByRole("button", { name: "Edit input" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Edited input" }), { target: { value: "" } });
  fireEvent.click(screen.getByRole("button", { name: "Save input" }));
  await screen.findByRole("button", { name: "Retry the same edit" });
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty("value", "");
  const request = value.edit.mock.calls[0][0] as { mutation: { requestId: string } };
  value.edit.mockImplementation(async () => ({ change: { requestId: request.mutation.requestId, session: value.session, input: create(ResourceSchema, { ...input, revision: 7n, documentJson: encode({ prompt: "", delivery: "queued", attachments: [image] }) }) } }));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same edit" }));
  await waitFor(() => expect(screen.queryByRole("textbox", { name: "Edited input" })).toBeNull());
  expect(value.edit.mock.calls[1][0]).toEqual(value.edit.mock.calls[0][0]);
  expect(value.steer).not.toHaveBeenCalled();
});

it("opens the complete original compact input through More and restores menu focus on Escape", async () => {
  const f = fixture("Long original prompt ".repeat(100)); render(f.view(f.resource, true));
  expect(screen.queryByText("plan · queued")).toBeNull();
  expect(screen.queryByRole("button", { name: "Edit input" })).toBeNull();
  const more = screen.getByRole("button", { name: "More input actions" });
  fireEvent.click(more); const edit = screen.getByRole("menuitem", { name: "Edit input" });
  expect(document.activeElement).toBe(edit); fireEvent.keyDown(edit, { key: "Escape" });
  expect(screen.queryByRole("menu")).toBeNull(); expect(document.activeElement).toBe(more);
  fireEvent.click(more); fireEvent.click(screen.getByRole("menuitem", { name: "Edit input" }));
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty("value", "Long original prompt ".repeat(100));
  expect(f.edit).not.toHaveBeenCalled(); expect(f.remove).not.toHaveBeenCalled(); expect(f.steer).not.toHaveBeenCalled();
});


it("reveals the complete compact input while retained pagination is read-only", () => {
  const prompt = "Complete retained pagination input ".repeat(100), value = fixture(prompt);
  render(value.view(value.resource, true, true));
  fireEvent.click(screen.getByRole("button", { name: "More input actions" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Edit input" }));
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty("value", prompt);
  expect(screen.getByRole("button", { name: "Save input" })).toHaveProperty("disabled", true);
  expect(screen.getByRole("button", { name: "Remove input" })).toHaveProperty("disabled", true);
  expect(value.edit).not.toHaveBeenCalled();
  expect(value.remove).not.toHaveBeenCalled();
});

it("reveals the complete compact input during pending and uncertain removal without replacing the request", async () => {
  const prompt = "Complete original pending removal input ".repeat(100), value = fixture(prompt);
  let reject!: (error: ConnectError) => void;
  value.remove.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
  render(value.view(value.resource, true));
  fireEvent.click(screen.getByRole("button", { name: "Remove input" }));
  await waitFor(() => expect(value.remove).toHaveBeenCalledOnce());
  fireEvent.click(screen.getByRole("button", { name: "More input actions" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Edit input" }));
  expect(screen.getByRole("textbox", { name: "Edited input" })).toHaveProperty("value", prompt);
  expect(screen.getByRole("button", { name: "Save input" })).toHaveProperty("disabled", true);
  await act(async () => reject(new ConnectError("Lost original removal", Code.Unavailable)));
  expect(screen.getByRole("button", { name: "More input actions" })).toHaveProperty("disabled", false);
  expect(screen.getByRole("button", { name: "Save input" })).toHaveProperty("disabled", true);
  fireEvent.click(screen.getByRole("button", { name: "Retry the same removal" }));
  await waitFor(() => expect(value.remove).toHaveBeenCalledTimes(2));
  expect(value.remove.mock.calls[1]![0]).toEqual(value.remove.mock.calls[0]![0]);
  expect(value.edit).not.toHaveBeenCalled();
});
