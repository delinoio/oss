import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { UuidV7Schema } from "../src/gen/devhud/v1/common_pb.js";
import {
  ClientBuildSchema,
  SubmitCrashReportRequestSchema,
} from "../src/gen/devhud/v1/diagnostics_pb.js";
import {
  MAX_CRASH_IDENTIFIER_BYTES,
  assertSha256,
  assertUuidV7,
  validateCanonicalSettingsJson,
  validateCrashReport,
} from "../src/validation.js";

const uuid = "018f47a2-7b3c-7def-8abc-1234567890ab";

describe("wire validation helpers", () => {
  it("accepts only canonical UUID v7 and 32-byte digests", () => {
    expect(() => assertUuidV7(uuid)).not.toThrow();
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
    expect(() =>
      validateCanonicalSettingsJson(
        new Uint8Array([0xef, 0xbb, 0xbf, ...new TextEncoder().encode("{}")]),
      ),
    ).toThrow(TypeError);
  });

  it("rejects lone surrogates in settings JSON strings and object keys", () => {
    const textEncoder = new TextEncoder();

    expect(() => validateCanonicalSettingsJson(textEncoder.encode('"😀"'))).not.toThrow();
    expect(() => validateCanonicalSettingsJson(textEncoder.encode('"\\ud800"'))).toThrow(
      TypeError,
    );
    expect(() => validateCanonicalSettingsJson(textEncoder.encode('{"\\udc00":1}'))).toThrow(
      TypeError,
    );
  });

  it("rejects local paths and credential-shaped crash diagnostics", () => {
    const clientBuild = create(ClientBuildSchema, {
      appVersion: "1.0.0",
      buildId: "devhud-20260815.1",
      osVersion: "macOS 15.0",
    });
    const safe = create(SubmitCrashReportRequestSchema, {
      reportSchemaVersion: 1,
      clientBuild,
      errorCode: "UPLOAD_FINALIZE_FAILED",
      redactedSummary: "Upload finalization failed after a checksum mismatch.",
      redactedStackTrace: "UploadBoundary > Finalize > VerifyChecksum",
      relatedCorrelationIds: [{ value: uuid }],
    });
    expect(() => validateCrashReport(safe)).not.toThrow();

    const invalidCorrelationId = create(SubmitCrashReportRequestSchema, {
      ...safe,
      relatedCorrelationIds: [create(UuidV7Schema, { value: "not-a-uuid" })],
    });
    expect(() => validateCrashReport(invalidCorrelationId)).toThrow(TypeError);

    const localPath = create(SubmitCrashReportRequestSchema, {
      ...safe,
      redactedStackTrace: "at /Users/example/project/app.ts:10",
    });
    expect(() => validateCrashReport(localPath)).toThrow(TypeError);

    const parenthesizedLocalPath = create(SubmitCrashReportRequestSchema, {
      ...safe,
      redactedStackTrace: "at render (/Users/example/project/app.ts:10:2)",
    });
    expect(() => validateCrashReport(parenthesizedLocalPath)).toThrow(TypeError);

    const parenthesizedWindowsPath = create(SubmitCrashReportRequestSchema, {
      ...safe,
      redactedStackTrace: "at render (C:\\Users\\example\\project\\app.ts:10:2)",
    });
    expect(() => validateCrashReport(parenthesizedWindowsPath)).toThrow(TypeError);

    for (const absolutePath of [
      "/workspace/oss/app.ts",
      "/root/app.ts",
      "/usr/src/app/index.js",
      "C:/Users/example/project/app.ts",
    ]) {
      const report = create(SubmitCrashReportRequestSchema, {
        ...safe,
        redactedStackTrace: `at render (${absolutePath}:10:2)`,
      });
      expect(() => validateCrashReport(report), absolutePath).toThrow(TypeError);
    }

    for (const stackTrace of [
      "/workspace/oss/app.ts:10:2",
      " /workspace/oss/app.ts:10:2",
      "(/workspace/oss/app.ts:10:2)",
    ]) {
      const report = create(SubmitCrashReportRequestSchema, {
        ...safe,
        redactedStackTrace: stackTrace,
      });
      expect(() => validateCrashReport(report), stackTrace).toThrow(TypeError);
    }

    const remoteUrl = create(SubmitCrashReportRequestSchema, {
      ...safe,
      redactedStackTrace: "at load (https://example.com/assets/app.js:10:2)",
    });
    expect(() => validateCrashReport(remoteUrl)).not.toThrow();

    for (const credentialUrl of [
      "https://alice:password@example.com/app.js",
      "https://example.com/app.js?token=secret",
      "https://example.com/app.js#access-token",
    ]) {
      const report = create(SubmitCrashReportRequestSchema, {
        ...safe,
        redactedStackTrace: `at load (${credentialUrl})`,
      });
      expect(() => validateCrashReport(report), credentialUrl).toThrow(TypeError);
    }

    const credential = create(SubmitCrashReportRequestSchema, {
      ...safe,
      redactedSummary: "Authorization: Bearer unsafe-value",
    });
    expect(() => validateCrashReport(credential)).toThrow(TypeError);

    const credentialValue = "Authorization: Bearer unsafe-value";
    const sensitiveIdentifiers = [
      { ...safe, errorCode: credentialValue },
      { ...safe, clientBuild: { ...clientBuild, appVersion: credentialValue } },
      { ...safe, clientBuild: { ...clientBuild, buildId: credentialValue } },
      { ...safe, clientBuild: { ...clientBuild, osVersion: credentialValue } },
    ];
    for (const report of sensitiveIdentifiers) {
      expect(() =>
        validateCrashReport(create(SubmitCrashReportRequestSchema, report)),
      ).toThrow(TypeError);
    }

    const oversizedValue = "a".repeat(MAX_CRASH_IDENTIFIER_BYTES + 1);
    const oversizedIdentifiers = [
      { ...safe, errorCode: oversizedValue },
      { ...safe, clientBuild: { ...clientBuild, appVersion: oversizedValue } },
      { ...safe, clientBuild: { ...clientBuild, buildId: oversizedValue } },
      { ...safe, clientBuild: { ...clientBuild, osVersion: oversizedValue } },
    ];
    for (const report of oversizedIdentifiers) {
      expect(() =>
        validateCrashReport(create(SubmitCrashReportRequestSchema, report)),
      ).toThrow(RangeError);
    }
  });
});
