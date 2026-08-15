import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { SubmitCrashReportRequestSchema } from "../src/gen/devhud/v1/diagnostics_pb.js";
import {
  assertSha256,
  assertUuidV7,
  validateCanonicalSettingsJson,
  validateCrashReport,
} from "../src/validation.js";

describe("wire validation helpers", () => {
  it("accepts only canonical UUID v7 and 32-byte digests", () => {
    expect(() => assertUuidV7("018f47a2-7b3c-7def-8abc-1234567890ab")).not.toThrow();
    expect(() => assertUuidV7("018F47A2-7B3C-7DEF-8ABC-1234567890AB")).toThrow(TypeError);
    expect(() => assertUuidV7("018f47a2-7b3c-6def-8abc-1234567890ab")).toThrow(TypeError);
    expect(() => assertSha256(new Uint8Array(32))).not.toThrow();
    expect(() => assertSha256(new Uint8Array(31))).toThrow(RangeError);
  });

  it("rejects noncanonical settings JSON", () => {
    expect(() =>
      validateCanonicalSettingsJson(new TextEncoder().encode('{"a":1,"b":2}')),
    ).not.toThrow();
    expect(() =>
      validateCanonicalSettingsJson(new TextEncoder().encode('{ "b": 2, "a": 1 }')),
    ).toThrow(TypeError);
  });

  it("rejects local paths and credential-shaped crash diagnostics", () => {
    const safe = create(SubmitCrashReportRequestSchema, {
      reportSchemaVersion: 1,
      errorCode: "UPLOAD_FINALIZE_FAILED",
      redactedSummary: "Upload finalization failed after a checksum mismatch.",
      redactedStackTrace: "UploadBoundary > Finalize > VerifyChecksum",
    });
    expect(() => validateCrashReport(safe)).not.toThrow();

    const localPath = create(SubmitCrashReportRequestSchema, {
      ...safe,
      redactedStackTrace: "at /Users/example/project/app.ts:10",
    });
    expect(() => validateCrashReport(localPath)).toThrow(TypeError);

    const credential = create(SubmitCrashReportRequestSchema, {
      ...safe,
      redactedSummary: "Authorization: Bearer unsafe-value",
    });
    expect(() => validateCrashReport(credential)).toThrow(TypeError);
  });
});
