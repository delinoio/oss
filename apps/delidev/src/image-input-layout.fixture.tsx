// SPDX-License-Identifier: Apache-2.0
// Synthetic browser fixture; no native account or Worker is used.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { AttachmentService, AttachmentState, AttachmentUploadSchema, ImageAttachmentSchema, EntityKind, ResourceSchema, ResourceService, SessionService, SystemService, SystemCapability, newRequestId, type AttachmentUpload } from "@delinoio/delidev-api-client";
import { ImageDraftProvider } from "./image-drafts";
import { imageDigest } from "./image-input";
import { NewSession, NewSessionKind } from "./new-session";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";
import { i18n } from "./localization";
import "./themes.css";
import "./styles.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
document.documentElement.dataset.theme = args.get("theme") ?? "light";
if (args.get("zoom") === "2") document.body.style.zoom = "2";
const row = (kind: EntityKind, data: object) => create(ResourceSchema, { kind, id: newRequestId(), schemaVersion: 1, revision: 1n, documentJson: encode(data) });
const agent = row(EntityKind.AGENT, { name: "Fixture Codex", harness: "codex" }), machine = row(EntityKind.MACHINE, { name: "Fixture Runner", worker_capabilities: [25] });
const scope = { server_id: newRequestId(), device_id: newRequestId() };
let revision = 1;
const bridge = { read: async () => ({ revision, scope, pair: { agent_id: agent.id, machine_id: machine.id }, problem: null }), update: async () => ({ revision: ++revision, scope, pair: { agent_id: agent.id, machine_id: machine.id }, problem: null }) };
const uploads = new Map<string, AttachmentUpload>(), begins = new Map<string, string>(), bytes = new Map<string, Uint8Array>();
const events: object[] = [];
const reads: object[] = [];
const fixtureMarker = "__imageInputFixture";
Object.assign(window, { [fixtureMarker]: true, imageFixture: { events, reads, bytes, appearance: async (language: string, theme: string) => { document.documentElement.dataset.theme = theme; await i18n.changeLanguage(language); } } });
const transport = createRouterTransport(router => {
 router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.AUTOMATIC_TITLES_V1, SystemCapability.IMAGE_INPUTS_V1] }) });
 router.service(ResourceService, { getResource: request => { reads.push({kind:"get",id:request.id}); return { resource: [agent,machine].find(value => value.id === request.id && value.kind === request.kind) }; }, listResources: request => { reads.push({kind:"list",kindFilter:request.filter?.kind}); return { resources: request.filter?.kind === EntityKind.AGENT ? [agent] : request.filter?.kind === EntityKind.MACHINE ? [machine] : [] }; } });
 router.service(AttachmentService, {
  beginUpload: request => {
   const prior = begins.get(request.requestId); if (prior) return { upload: uploads.get(prior) };
   const attachment = create(ImageAttachmentSchema, { id: newRequestId(), machineId: request.machineId, mediaType: request.mediaType, byteLength: request.byteLength, sha256: request.sha256 });
   const upload = create(AttachmentUploadSchema, { attachment, state: AttachmentState.UPLOADING, draftId: request.draftId, operationId: request.operationId });
   uploads.set(attachment.id, upload); begins.set(request.requestId, attachment.id); bytes.set(attachment.id,new Uint8Array()); events.push({ kind:"begin", operation:request.operationId }); return { upload };
  },
  writeChunk: async request => {
   const upload = uploads.get(request.attachmentId)!, previous = bytes.get(request.attachmentId)!;
   if (BigInt(previous.length) !== request.offset || await imageDigest(request.data) !== request.sha256) throw new Error("Invalid synthetic chunk");
   const next = new Uint8Array(previous.length + request.data.length); next.set(previous); next.set(request.data,previous.length); bytes.set(request.attachmentId,next); upload.uploadedBytes = BigInt(next.length); return { upload };
  },
  finishUpload: async request => { const upload = uploads.get(request.attachmentId)!; if (await imageDigest(bytes.get(request.attachmentId)!) !== upload.attachment!.sha256) throw new Error("Invalid synthetic digest"); upload.state = AttachmentState.READY; return { upload }; },
  getUpload: request => ({ upload: uploads.get(request.attachmentId) }),
  deleteDraftAttachment: request => { const upload=uploads.get(request.attachmentId)!; upload.state=AttachmentState.DELETED; return { upload }; },
 });
 router.service(SessionService, { createSession: request => {
  const session=row(EntityKind.SESSION,{}), document=JSON.parse(new TextDecoder().decode(request.documentJson));
  events.push({ kind:"create", requestId:request.requestId, prompt:document.prompt, images:request.attachments.map(value=>({id:value.id,digest:value.sha256,media:value.mediaType})), documentHasImages:document.attachments !== undefined });
  const input=row(EntityKind.QUEUE,{...document,attachments:request.attachments.map(value=>({id:value.id,machine_id:value.machineId,media_type:["","image/png","image/jpeg","image/webp"][value.mediaType],byte_length:Number(value.byteLength),sha256:value.sha256}))}); input.sessionId=session.id;
  return { change:{requestId:request.requestId,session,input} };
 } });
});
function Fixture() {
 const [kind,setKind]=useState(NewSessionKind.Session),[visible,setVisible]=useState(true),[accepted,setAccepted]=useState(0);
 return <main style={{maxWidth:900,margin:"auto",padding:8,minWidth:0}}><button onClick={()=>setKind(value=>value===NewSessionKind.Session?NewSessionKind.GeneralChat:NewSessionKind.Session)}>Fixture switch composer</button><button onClick={()=>setVisible(value=>!value)}>Fixture navigate</button><output data-accepted>{accepted}</output><NewSession key={kind} kind={kind} active={visible} ownsActivation activation={1} back={()=>{}} openSettings={()=>{}} open={()=>{}} created={()=>setAccepted(value=>value+1)} preferenceBridge={bridge} preferenceScope={scope} />{!visible ? <p>Fixture other view</p> : null}</main>;
}
createRoot(document.getElementById("root")!).render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient()}><MutationIntents><ImageDraftProvider><Fixture /></ImageDraftProvider></MutationIntents></QueryClientProvider></TransportProvider>);
