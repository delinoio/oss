import { sessionInputReceipt } from "./test-session-input";
// SPDX-License-Identifier: Apache-2.0
import { webcrypto } from "node:crypto";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { EntityKind, InboxService, NotificationPreferencesSchema, ResourceSchema, ResourceService, SessionService, SkillService, SkillProvenance, SystemService, SystemCapability, WorkerCapability, AttachmentService, AttachmentState, AttachmentUploadSchema, ImageAttachmentSchema, type AttachmentUpload, newRequestId, type EnqueueInputRequest } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";

function fixture() {
 const machine=newRequestId(),agent=newRequestId();
 const make=(name:string)=>{const id=newRequestId();return create(ResourceSchema,{id,sessionId:id,kind:EntityKind.SESSION,revision:1n,schemaVersion:1,documentJson:encode({name,workspace:"general-chat",machine_id:machine,agent_id:agent,outcome:"stopped",archive:"active",dispatch:"paused",recovery:"none"})});};
 const original=make("Skill original"),other=make("Skill other");
 const selection={$typeName:"delidev.v1.SkillSelection" as const,workerDeviceId:newRequestId(),inventoryId:newRequestId(),skillId:newRequestId(),contentRevision:"a".repeat(64)};
 const enqueue=vi.fn(async(request:EnqueueInputRequest)=>sessionInputReceipt(original,request));
 const uploads=new Map<string,AttachmentUpload>(),writes:Uint8Array[]=[];
 let preparation: Promise<void> | undefined;
 const transport=createRouterTransport(router=>{
 router.service(AttachmentService,{beginUpload:async request=>{await preparation;const attachment=create(ImageAttachmentSchema,{id:newRequestId(),machineId:request.machineId,mediaType:request.mediaType,byteLength:request.byteLength,sha256:request.sha256});const upload=create(AttachmentUploadSchema,{attachment,state:AttachmentState.UPLOADING,draftId:request.draftId,operationId:request.operationId});uploads.set(attachment.id,upload);return {upload};},writeChunk:request=>{writes.push(request.data);const upload=uploads.get(request.attachmentId)!;upload.uploadedBytes+=BigInt(request.data.length);return {upload};},finishUpload:request=>{const upload=uploads.get(request.attachmentId)!;upload.state=AttachmentState.READY;return {upload};},getUpload:request=>({upload:uploads.get(request.attachmentId)})});
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.IMAGE_INPUTS_V1]})});
  router.service(SkillService,{listSkills:()=>({skills:[{name:"retained",description:"Immutable selected fixture",provenance:SkillProvenance.USER,selection}]})});
  router.service(SessionService,{listSessions:()=>({sessions:[original,other]}),listQueue:()=>({inputs:[]}),enqueueInput:enqueue});
  router.service(ResourceService,{getResource:request=>({resource:create(ResourceSchema,{id:request.id,kind:request.kind,revision:1n,schemaVersion:1,documentJson:encode(request.kind===EntityKind.MACHINE?{worker_capabilities:[WorkerCapability.IMAGE_INPUTS_V1]}:{harness:"codex"})})}),getSnapshot:request=>({resources:[request.filter?.sessionId===other.id?other:original],cursor:"fixture-snapshot"}),listResources:()=>({resources:[]}),async *watchEvents(_request,context){if(!context.signal.aborted)await new Promise<void>(done=>context.signal.addEventListener("abort",()=>done(),{once:true}));}});
  router.service(InboxService,{getNotificationPreferences:()=>({preferences:create(NotificationPreferencesSchema,{revision:1n,interactions:true})}),listInbox:()=>({entries:[]})});
 });
 const select=async()=>{fireEvent.click(await screen.findByRole("button",{name:/General Chat Skill original/}));const input=await screen.findByRole("textbox",{name:"Message"});fireEvent.change(input,{target:{value:"$ret",selectionStart:4}});fireEvent.click(await screen.findByRole("option",{name:/retained/}));expect(input).toHaveProperty("value","$retained");return input;};
 const returnToOriginal=async()=>{fireEvent.click(screen.getByRole("button",{name:/General Chat Skill other/}));await screen.findByRole("heading",{name:"Skill other"});fireEvent.click(screen.getByRole("button",{name:/General Chat Skill original/}));await screen.findByRole("heading",{name:"Skill original"});return screen.getByRole("textbox",{name:"Message"});};
 return {transport,original,other,selection,enqueue,select,returnToOriginal,writes,holdPreparation:(promise:Promise<void>)=>{preparation=promise;}};
}

// Full App fixtures combine native image hashing, skill reads and navigation.
// Hosted CI shares CPU with Go preparation; their harness deadline is separate
// from every product request deadline and does not authorize mutation retries.
const mixedDraftTimeoutMs = 30_000;
afterEach(()=>vi.unstubAllGlobals());
it("retains ordered image bytes and exact selected skill together across retained App session switches",async()=>{
 vi.stubGlobal("crypto",webcrypto);vi.stubGlobal("createImageBitmap",async()=>({width:1,height:1,close:()=>{}}));const BaseURL=URL;vi.stubGlobal("URL",class extends BaseURL {static createObjectURL=vi.fn(()=>"blob:mixed-draft");static revokeObjectURL=vi.fn();});
 const bytes=Uint8Array.from(Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a9d8AAAAASUVORK5CYII=","base64"));const file=new File([bytes],"fixture.png",{type:"image/png"});Object.defineProperty(file,"arrayBuffer",{value:async()=>bytes.buffer});
 const f=fixture();render(<App transport={f.transport}/>);const originalInput=await f.select();fireEvent.paste(screen.getByRole("textbox",{name:"Message"}),{clipboardData:{files:[file]}});await screen.findByRole("img",{name:"Image 1"});
 const input=await f.returnToOriginal();expect(input).toBe(originalInput);expect(input).toHaveProperty("value","$retained");expect(screen.getByRole("img",{name:"Image 1"})).toBeDefined();await waitFor(()=>expect(screen.getByRole("button",{name:"Queue message"})).toHaveProperty("disabled",false));fireEvent.click(screen.getByRole("button",{name:"Queue message"}));await waitFor(()=>expect(f.enqueue).toHaveBeenCalledOnce());const request=f.enqueue.mock.calls[0]![0];expect(request.skills?.selections).toEqual([f.selection]);expect(request.attachments).toHaveLength(1);expect(f.writes).toEqual([bytes]);
}, mixedDraftTimeoutMs);

it("freezes the immediate projection and original skill/mode/images before preparation across navigation",async()=>{
 vi.stubGlobal("crypto",webcrypto);vi.stubGlobal("createImageBitmap",async()=>({width:1,height:1,close:()=>{}}));const BaseURL=URL;vi.stubGlobal("URL",class extends BaseURL {static createObjectURL=vi.fn(()=>"blob:frozen-draft");static revokeObjectURL=vi.fn();});
 const bytes=Uint8Array.from(Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a9d8AAAAASUVORK5CYII=","base64"));const file=new File([bytes],"fixture.png",{type:"image/png"});Object.defineProperty(file,"arrayBuffer",{value:async()=>bytes.buffer});
 const f=fixture();let release!:()=>void;f.holdPreparation(new Promise<void>(done=>{release=done;}));render(<App transport={f.transport}/>);await f.select();
 fireEvent.paste(screen.getByRole("textbox",{name:"Message"}),{clipboardData:{files:[file]}});await screen.findByRole("img",{name:"Image 1"});
 fireEvent.click(screen.getByRole("checkbox",{name:"Plan Mode"}));await waitFor(()=>expect(screen.getByRole("button",{name:"Queue message"})).toHaveProperty("disabled",false));
 fireEvent.click(screen.getByRole("button",{name:"Queue message"}));expect(screen.getByRole("article",{name:"Submitted message"}).textContent).toContain("Preparing attachments");expect(f.enqueue).not.toHaveBeenCalled();expect(screen.getByRole("textbox",{name:"Message"})).toHaveProperty("disabled",true);
 await f.returnToOriginal();expect(screen.getByRole("textbox",{name:"Message"})).toHaveProperty("disabled",true);
 await act(async()=>release());await waitFor(()=>expect(f.enqueue).toHaveBeenCalledOnce());const request=f.enqueue.mock.calls[0][0];expect(JSON.parse(new TextDecoder().decode(request.documentJson))).toEqual({prompt:"$retained",mode:"plan"});expect(request.skills?.selections).toEqual([f.selection]);expect(request.attachments).toHaveLength(1);expect(f.writes).toEqual([bytes]);
 await screen.findByText("Queued");expect(screen.getByRole("textbox",{name:"Message"})).toHaveProperty("value","");
}, mixedDraftTimeoutMs);
