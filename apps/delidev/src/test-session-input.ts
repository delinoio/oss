// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId, type EnqueueInputRequest, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { imageMime } from "./image-input";

/** A synthetic admission receipt, not native input acceptance. */
export function sessionInputReceipt(session: Resource, request: Pick<EnqueueInputRequest, "requestId" | "sessionId" | "documentJson"> & Partial<Pick<EnqueueInputRequest, "attachments">>) {
  return { change: { requestId: request.requestId, session, input: create(ResourceSchema, {
    id: newRequestId(), sessionId: request.sessionId, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 1n,
    documentJson: encode({ ...JSON.parse(new TextDecoder().decode(request.documentJson)), delivery: "queued", attachments: request.attachments?.map(image => ({ id: image.id, machine_id: image.machineId, media_type: imageMime[image.mediaType], byte_length: Number(image.byteLength), sha256: image.sha256 })) }),
  }) } };
}
