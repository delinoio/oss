// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";

export function subagentFixture(sessionId: string) {
  const id = newRequestId(), root = newRequestId();
  return create(ResourceSchema, { id, sessionId, kind: EntityKind.SUBAGENT, schemaVersion: 1, revision: 1n,
    documentJson: encode({ execution_id: newRequestId(), harness: "codex", native_version: "0.151.0", root_id: root,
      first_sequence: "3", last_sequence: "3", sources: [{ source: "codex-history", source_id: "original-history", sequence: "3" }],
      observation: { id, native_id: newRequestId(), parent_id: root, source: "codex-history", source_id: "original-history", status: "running",
        requested_model: "requested-only", observed_model: "observed-model", output: { native_message_id: "original-child-message", text: "Original child output", partial: true }, usage: null },
    }),
  });
}
