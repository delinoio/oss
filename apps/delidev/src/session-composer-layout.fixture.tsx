// SPDX-License-Identifier: Apache-2.0
// Synthetic retained-session composer; no native, account or external authority.
import { createRoot } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { EntityKind, ResourceSchema, ResourceService, SessionService, SystemService, SystemCapability, WorkerCapability, AttachmentService, AttachmentState, ImageAttachmentSchema, AttachmentUploadSchema, newRequestId } from "@delinoio/delidev-api-client";
import { ImageDraftProvider } from "./image-drafts";
import { imageDigest } from "./image-input";
import { encode } from "./documents";
import { SessionView } from "./session";
import { MutationIntents } from "./mutation";
import { i18n } from "./localization";
import "./themes.css";
import "./styles.css";
import "./session-remediation-layout.fixture.css";
const args = new URLSearchParams(location.search);
document.documentElement.dataset.theme = args.get("theme") === "dark" ? "dark" : "light";
const machineId = newRequestId(), agentId = newRequestId();
const machine = create(ResourceSchema, { id: machineId, kind: EntityKind.MACHINE, revision: 1n, schemaVersion: 1, documentJson: encode({ worker_capabilities: args.get("state") === "unsupported" ? [] : [WorkerCapability.IMAGE_INPUTS_V1] }) });
const execution = newRequestId(), id = newRequestId();
const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, revision: 9007199254740993n, schemaVersion: 1, documentJson: encode({ name: "Original session", workspace: args.get("workspace") === "local" ? "local" : "general-chat", archive: "active", outcome: "stopped", dispatch: "paused", recovery: "none", machine_id: machineId, agent_id: agentId, initial_execution: { id: execution, configuration: { harness: "codex" } } }) });
const events: { requestId: string; prompt: string; mode: string; images: string[] }[] = [];
Object.assign(window, { __sessionComposerFixture: { events } });
let attempt = 0;
const enqueue = async (request: { requestId: string; documentJson: Uint8Array; attachments: { id: string; machineId: string; mediaType: number; byteLength: bigint; sha256: string }[] }) => {
  const data = JSON.parse(new TextDecoder().decode(request.documentJson));
  events.push({ requestId: request.requestId, prompt: data.prompt, mode: data.mode, images: request.attachments.map(image => image.id) });
  if (args.get("state") === "pending") await new Promise(() => {});
  if (args.get("state") === "uncertain" && attempt++ === 0) throw new ConnectError("Synthetic unavailable receipt", Code.Unavailable);
  const input = create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.QUEUE, revision: 1n, schemaVersion: 1, documentJson: encode({ ...data, attachments: request.attachments.map(image => ({ id: image.id, machine_id: image.machineId, media_type: ["", "image/png", "image/jpeg", "image/webp"][image.mediaType], byte_length: Number(image.byteLength), sha256: image.sha256 })) }) });
  return { change: { requestId: request.requestId, session, input } };
};
const uploads = new Map<string, ReturnType<typeof create<typeof AttachmentUploadSchema>>>();
const bytes = new Map<string, Uint8Array>();
const transport = createRouterTransport(router => {
 router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.IMAGE_INPUTS_V1] }) });
 router.service(SessionService, { listQueue: () => ({ inputs: [] }), getSessionBudget: () => ({ view: { session } }), enqueueInput: enqueue });
 router.service(ResourceService, { getResource: () => ({ resource: machine }), getSnapshot: () => ({ resources: [session], cursor: "synthetic" }), listResources: () => ({ resources: [] }), async *watchEvents(_request, context) { if (!context.signal.aborted) await new Promise<void>(done => context.signal.addEventListener("abort", () => done(), { once: true })); } });
 router.service(AttachmentService, {
  beginUpload: request => { const attachment = create(ImageAttachmentSchema, { id: newRequestId(), machineId: request.machineId, mediaType: request.mediaType, byteLength: request.byteLength, sha256: request.sha256 }); const upload = create(AttachmentUploadSchema, { attachment, state: AttachmentState.UPLOADING, draftId: request.draftId, operationId: request.operationId }); uploads.set(attachment.id, upload); bytes.set(attachment.id, new Uint8Array()); return { upload }; },
  writeChunk: async request => { const upload = uploads.get(request.attachmentId)!, prior = bytes.get(request.attachmentId)!; if (BigInt(prior.length) !== request.offset || await imageDigest(request.data) !== request.sha256) throw new Error("Synthetic invalid chunk"); const next = new Uint8Array(prior.length + request.data.length); next.set(prior); next.set(request.data, prior.length); bytes.set(request.attachmentId, next); upload.uploadedBytes = BigInt(next.length); return { upload }; },
  finishUpload: request => { const upload = uploads.get(request.attachmentId)!; upload.state = AttachmentState.READY; return { upload }; },
  getUpload: request => ({ upload: uploads.get(request.attachmentId) }),
  deleteDraftAttachment: request => { const upload = uploads.get(request.attachmentId)!; upload.state = AttachmentState.DELETED; return { upload }; },
 });
});
const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
function Fixture() { const [draft, setDraft] = useState(""); return <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ImageDraftProvider><div className="session-container fixture-session-owner"><SessionView id={id} draft={draft} setDraft={setDraft} /></div></ImageDraftProvider></MutationIntents></QueryClientProvider></TransportProvider>; }
void i18n.changeLanguage(args.get("language") === "ko" ? "ko" : "en").then(() => createRoot(document.getElementById("root")!).render(<Fixture />));
