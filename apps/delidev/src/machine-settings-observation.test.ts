// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { expect, it } from "vitest";
import { document, encode } from "./documents";
import { mergeMachineRead } from "./machine-settings-observation";

const id = newRequestId();
const runner = (revision: bigint, fields: Record<string, unknown>, scope = id) => create(ResourceSchema, { id: scope, kind: EntityKind.MACHINE, schemaVersion: 1, revision, documentJson: encode({ name: "Runner", installations: [], ...fields }) });
it("refreshes equal-revision heartbeat independently of accepted configuration and clears a missing current lease", () => {
 const original=runner(7n,{last_seen:"2026-10-10T00:00:00Z",executables:{codex:"accepted-path"}});
 const fresh=runner(7n,{last_seen:"2026-10-10T00:00:05Z",executables:{codex:"unaccepted-path"}});
 const accepted=mergeMachineRead(original,fresh,id)!;
 expect(accepted.revision).toBe(7n);
 expect(document(accepted)).toMatchObject({last_seen:"2026-10-10T00:00:05Z",executables:{codex:"accepted-path"}});
 expect(document(original).last_seen).toBe("2026-10-10T00:00:00Z");
 expect(document(mergeMachineRead(accepted,runner(7n,{}),id)).last_seen).toBeUndefined();
});
it("keeps highest accepted business revision and rejects malformed or foreign observations", () => {
 const original=runner(8n,{last_seen:"2026-10-10T00:00:05Z",executables:{codex:"new-path"}});
 expect(mergeMachineRead(original,runner(7n,{last_seen:"2026-10-10T00:00:10Z"}),id)).toBe(original);
 expect(mergeMachineRead(original,runner(8n,{disabled:"false"}),id)).toBe(original);
 expect(mergeMachineRead(original,runner(9n,{},newRequestId()),id)).toBe(original);
 const newer=runner(9n,{executables:{codex:"newest-path"}});
 expect(mergeMachineRead(original,newer,id)).toBe(newer);
});
