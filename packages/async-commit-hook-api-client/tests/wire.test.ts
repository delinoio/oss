import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";
import { ExecutionState, RunSchema, LocalQuery } from "../src/index.js";
describe("v1 generated wire", () => {
  it("retains exact sequence and explicit nonpassing states", () => {
    const run = create(RunSchema, { id: "01900000-0000-7000-8000-000000000001", sequence: 9007199254740993n, state: ExecutionState.INTERRUPTED });
    expect(fromBinary(RunSchema, toBinary(RunSchema, run))).toEqual(run);
    expect(LocalQuery.acknowledge.name).toBe("Acknowledge");
  });
});
