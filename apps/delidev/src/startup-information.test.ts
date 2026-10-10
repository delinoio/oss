// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { initialStartupInformationHidden, SessionProgressPhase } from "./session-progress";
import { expect, it } from "vitest";
const id = newRequestId(), input = newRequestId(), execution = newRequestId();
const session = (data: object) => create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode(data) });
const queue = (data: object = {}) => create(ResourceSchema, { id: input, sessionId: id, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 1n, documentJson: encode({ sequence: 1, content_revision: 1, prompt: "Original input", mode: "execute", delivery: "queued", ...data }) });
it.each([SessionProgressPhase.Preparing, SessionProgressPhase.Queued])("requires original first queue ownership for %s", phase => {
 const s = session({ last_input_sequence: 1 });
 expect(initialStartupInformationHidden(s, phase, [queue()], true)).toBe(true);
 for (const q of [[], [queue(), queue()], [queue({ sequence: 2 })], [queue({ native_request_id: newRequestId() })], [queue({ delivery: "uncertain" })]]) expect(initialStartupInformationHidden(s, phase, q, true)).toBe(false);
 expect(initialStartupInformationHidden(s, phase, [queue()], false)).toBe(false);
});
it.each([SessionProgressPhase.Starting, SessionProgressPhase.Response])("requires exact original initial execution for %s", phase => {
 const d = { last_input_sequence: 1, active_execution_id: execution, initial_execution: { id: execution, input_id: input } };
 expect(initialStartupInformationHidden(session(d), phase, [], false)).toBe(true);
 for (const extra of [{ last_input_sequence: 2 }, { last_input_sequence: undefined }, { initial_execution: undefined }, { current_execution: { id: execution, input_id: newRequestId() } }, { active_execution_id: newRequestId() }]) expect(initialStartupInformationHidden(session({ ...d, ...extra }), phase, [], true)).toBe(false);
 expect(initialStartupInformationHidden(session(d), undefined, [], true)).toBe(false);
});
