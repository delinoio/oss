// SPDX-License-Identifier: Apache-2.0
import { readFileSync, existsSync } from "node:fs";
import { createHash } from "node:crypto";
import { createRegistry } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import { GetStatusRequestSchema, ResourceSchema, SystemCapability, SystemService, SessionService, SessionQuery, SwitchSessionAccountRequestSchema, SystemQuery } from "../src/index.js";
import { file_delidev_v1_system } from "../src/gen/delidev/v1/system_pb.js";
import { file_delidev_v1_session } from "../src/gen/delidev/v1/session_pb.js";
import { file_delidev_v1_usage } from "../src/gen/delidev/v1/usage_pb.js";

// Fixed snapshot of the last intact historical map; never derive expectations
// from the editable generator input, which can regress along with the facades.
const historicalOrder = JSON.parse(readFileSync(new URL("../../../protos/delidev/v1/contracttest/testdata/legacy-declaration-order.json", import.meta.url), "utf8")) as {
  sourceRevision: string;
  relocationSha256: string;
  message: string[];
  enum: string[];
  service: string[];
};

it("uses service-owned current descriptors without aggregate facades", () => {
 const registry=createRegistry(ResourceSchema,file_delidev_v1_system,file_delidev_v1_session,file_delidev_v1_usage);
 expect(registry.getMessage("delidev.v1.Resource")).toBe(ResourceSchema);
 expect(registry.getService("delidev.v1.SystemService")).toBe(SystemService);
 expect(GetStatusRequestSchema.typeName).toBe("delidev.v1.GetStatusRequest");
 expect(SystemQuery.getStatus).toBe(SystemService.method.getStatus);
 expect(SessionQuery.switchSessionAccount).toBe(SessionService.method.switchSessionAccount);
 expect(SwitchSessionAccountRequestSchema.typeName).toBe("delidev.v1.SwitchSessionAccountRequest");
 expect(SystemCapability.STOPPED_CODEX_ACCOUNT_SWITCH_V1).toBe(5);
 for(const name of ["delidev_pb.js","delidev_pb.ts","delidev-SessionService_connectquery.ts"]){expect(existsSync(new URL("../src/gen/delidev/v1/"+name,import.meta.url))).toBe(false);}
});

it("preserves frozen declaration history as provenance only", () => {
 expect(historicalOrder.sourceRevision).toBe("54187b780d48e94a49763e87fc140f449869d9f4");
 expect([historicalOrder.message.length,historicalOrder.enum.length,historicalOrder.service.length]).toEqual([303,36,19]);
 expect(historicalOrder.service[18]).toBe("BrowserService");
});

it("retains historical relocation kinds, files and full map order", () => {
  const layout = JSON.parse(readFileSync(new URL("../../../scripts/delidev/proto-layout.json", import.meta.url), "utf8")) as {
    declarations: Record<string, { kind: string; file: string }>;
  };
  // Pin the complete map as well as per-kind aggregate positions. Moving an
  // entry across another kind can leave reflection order unchanged.
  const entries = Object.entries(layout.declarations).map(([name, { kind, file }]) => [name, kind, file]);
  expect(createHash("sha256").update(JSON.stringify(entries)).digest("hex")).toBe(historicalOrder.relocationSha256);
});
