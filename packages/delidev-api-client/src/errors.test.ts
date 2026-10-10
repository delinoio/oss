// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { ErrorDetailSchema } from "./gen/delidev/v1/worker_pb.js";
import { clientFailure, FailureCode, FailureCause } from "./errors.js";

describe("canonical protocol error details", () => {
  it("retains the original typed guidance without a compatibility descriptor", () => {
    const detail = create(ErrorDetailSchema, { code: "conflict", guidance: "Read the original revision." });
    expect(clientFailure(new ConnectError("The source changed.", Code.Aborted, undefined, [{ desc: ErrorDetailSchema, value: detail }]))).toMatchObject({ code: FailureCode.Conflict, message: "The source changed.", guidance: "Read the original revision." });
  });
  it("projects only the known name-conflict cause on a Conflict", () => {
    for (const [code, cause, expected] of [["conflict", "configuration_name_conflict", FailureCause.ConfigurationNameConflict], ["conflict", "sqlite_busy", undefined], ["recovery_required", "configuration_name_conflict", undefined]] as const) {
      const detail = create(ErrorDetailSchema, { code, cause });
      expect(clientFailure(new ConnectError("Refused", Code.Aborted, undefined, [{ desc: ErrorDetailSchema, value: detail }])).cause).toBe(expected);
    }
  });
  it("redacts untyped transport diagnostics and preserves retry classification", () => {
    const failure = clientFailure(new ConnectError("https://secret.invalid/raw", Code.Unavailable));
    expect(failure.code).toBe(FailureCode.ServerUnavailable);
    expect(failure.message).not.toContain("secret");
  });
});
