import { describe, expect, it } from "vitest";
import { GetStatusRequestSchema, ResourceSchema, SystemCapability, SystemService } from "../src/gen/delidev/v1/delidev_pb.js";
import { getStatus } from "../src/gen/delidev/v1/delidev-SystemService_connectquery.js";
import { ResourceSchema as CurrentResourceSchema } from "../src/gen/delidev/v1/common_pb.js";
import { SystemService as CurrentSystemService } from "../src/gen/delidev/v1/system_pb.js";

describe("historical generated imports", () => {
  it("retains declaration identity, wire names and query methods", () => {
    expect(ResourceSchema).toBe(CurrentResourceSchema);
    expect(SystemService).toBe(CurrentSystemService);
    expect(GetStatusRequestSchema.typeName).toBe("delidev.v1.GetStatusRequest");
    expect(getStatus).toBe(SystemService.method.getStatus);
    expect(SystemCapability.AUTOMATIC_TITLES_V1).toBe(1);
    expect(SystemCapability.SESSION_FORWARDING_V1).toBe(2);
    expect(SystemCapability.USER_SERVICES_V1).toBe(3);
  });
});
