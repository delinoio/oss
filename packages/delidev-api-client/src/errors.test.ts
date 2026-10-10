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
  it("redacts untyped transport diagnostics and preserves retry classification", () => {
    const failure = clientFailure(new ConnectError("https://secret.invalid/raw", Code.Unavailable));
    expect(failure.code).toBe(FailureCode.ServerUnavailable);
    expect(failure.message).not.toContain("secret");
  });
});

it("projects only the typed conflict naming cause, preserving other conflict classes", () => {
  const reason = (code: string, cause: string) => new ConnectError("Original safe message.", Code.Aborted, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code, cause, guidance: "Original guidance." }) }]);
  expect(clientFailure(reason("conflict", "configuration_name_conflict"))).toMatchObject({ code: FailureCode.Conflict, cause: FailureCause.ConfigurationNameConflict });
  for (const failure of [reason("conflict", "revision_changed"), reason("invalid_argument", "configuration_name_conflict"), new ConnectError("configuration_name_conflict", Code.Aborted)]) expect(clientFailure(failure).cause).toBeUndefined();
});
