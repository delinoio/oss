import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { UuidV7Schema } from "../src/gen/devhud/v1/common_pb.js";
import {
  ClientBuildSchema,
  DiagnosticArchitecture,
  DiagnosticComponent,
  DiagnosticPlatform,
  DiagnosticSeverity,
  SubmitCrashReportRequestSchema,
} from "../src/gen/devhud/v1/diagnostics_pb.js";
import {
  MAX_ADMIN_REASON_BYTES,
  MAX_CRASH_IDENTIFIER_BYTES,
  assertSha256,
  assertUuidV7,
  validateAdminReason,
  validateCanonicalSettingsJson,
  validateCrashReport,
} from "../src/validation.js";

const uuid = "018f47a2-7b3c-7def-8abc-1234567890ab";
const clientBuild = create(ClientBuildSchema, {
  appVersion: "1.0.0",
  buildId: "devhud-20260815.1",
  platform: DiagnosticPlatform.MACOS,
  architecture: DiagnosticArchitecture.ARM64,
  osVersion: "macOS 15.0",
});
const safeCrashReport = create(SubmitCrashReportRequestSchema, {
  reportSchemaVersion: 1,
  clientBuild,
  occurredAt: { seconds: 1_787_000_000n, nanos: 0 },
  component: DiagnosticComponent.UPLOAD,
  severity: DiagnosticSeverity.ERROR,
  errorCode: "UPLOAD_FINALIZE_FAILED",
  redactedSummary: "Upload finalization failed after a checksum mismatch.",
  redactedStackTrace: "UploadBoundary > Finalize > VerifyChecksum",
  relatedCorrelationIds: [{ value: uuid }],
});
const r2SignedCredentialUrls = [
  "https://account.r2.cloudflarestorage.com/bucket/report?X-Amz-Credential=R2ACCESSKEY%2F20260815%2Fauto%2Fs3%2Faws4_request",
  "https://account.r2.cloudflarestorage.com/bucket/report?X-Amz-Signature=0123456789abcdef",
] as const;
const r2UnsignedMetadataUrl =
  "https://account.r2.cloudflarestorage.com/bucket/report?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Date=20260815T180000Z&X-Amz-Expires=900&X-Amz-SignedHeaders=host";
const encodedLocalFileUrl = "file:%2FUsers%2Falice%2Fproject%2Fapp.ts";

describe("wire validation helpers", () => {
  it("accepts only canonical UUID v7 and 32-byte digests", () => {
    expect(() => assertUuidV7(uuid)).not.toThrow();
    expect(() => assertUuidV7("018F47A2-7B3C-7DEF-8ABC-1234567890AB")).toThrow(TypeError);
    expect(() => assertUuidV7("018f47a2-7b3c-6def-8abc-1234567890ab")).toThrow(TypeError);
    expect(() => assertSha256(new Uint8Array(32))).not.toThrow();
    expect(() => assertSha256(new Uint8Array(31))).toThrow(RangeError);
  });

  it("validates bounded sensitive-content-safe administrator reasons", () => {
    expect(() => validateAdminReason("Quarantined after repeated policy violations.")).not.toThrow();
    expect(() => validateAdminReason("Expected yes / no")).not.toThrow();
    expect(() => validateAdminReason("Reviewed incident from 2026/08/15.")).not.toThrow();
    expect(() => validateAdminReason("Rolled back release 1/2/3.")).not.toThrow();
    expect(() => validateAdminReason("\u0085Reviewed policy breach")).not.toThrow();
    expect(() => validateAdminReason("é".repeat(MAX_ADMIN_REASON_BYTES / 2))).not.toThrow();
    expect(() =>
      validateAdminReason("Reviewed https://docs.example.com/policy?v=42#quarantine"),
    ).not.toThrow();
    expect(() =>
      validateAdminReason("Reviewed https://example.com/?na%6de=release"),
    ).not.toThrow();
    expect(() => validateAdminReason(`Reviewed ${r2UnsignedMetadataUrl}`)).not.toThrow();
    expect(() =>
      validateAdminReason("ERROR_CODE=E_UPLOAD RETRY_COUNT=3 TOKEN_COUNT=2"),
    ).not.toThrow();

    expect(() => validateAdminReason("")).toThrow(TypeError);
    expect(() => validateAdminReason(" \n\t ")).toThrow(TypeError);
    expect(() => validateAdminReason("\u0085\u2007\u2028")).toThrow(TypeError);
    expect(() => validateAdminReason("\ud800")).toThrow(TypeError);
    expect(() => validateAdminReason("é".repeat(MAX_ADMIN_REASON_BYTES / 2 + 1))).toThrow(
      RangeError,
    );

    for (const reason of [
      "Authorization: Bearer unsafe-value",
      "refresh_token=unsafe-value",
      "AWS_SECRET_ACCESS_KEY=unsafe-value",
      "AWS_SESSION_TOKEN=unsafe-value",
      "GITHUB_TOKEN=unsafe-value",
      "See /Users/example/private/incident.txt",
      "See src/private/incident.txt",
      "source:src/private/app.ts:10",
      "frame:src\\private\\app.ts:10",
      encodedLocalFileUrl,
      "https://example.com/audit?token=unsafe-value",
      "devhud://auth/callback?co%64e=unsafe-value",
      "https://example.com/?to%6ben=unsafe-value",
      "https://example.com/?to%6=unsafe-value",
      ...r2SignedCredentialUrls,
    ]) {
      expect(() => validateAdminReason(reason), reason).toThrow(TypeError);
    }
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

  it("accepts deeply nested canonical settings JSON", () => {
    const depth = 10_000;
    const source = `${"[".repeat(depth)}0${"]".repeat(depth)}`;

    expect(() =>
      validateCanonicalSettingsJson(new TextEncoder().encode(source)),
    ).not.toThrow();
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

  it("rejects incomplete crash report envelopes", () => {
    expect(() => validateCrashReport(safeCrashReport)).not.toThrow();
    expect(() =>
      validateCrashReport({ ...safeCrashReport, reportSchemaVersion: 0xffff_ffff }),
    ).not.toThrow();
    for (const reportSchemaVersion of [
      -1,
      0,
      1.5,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      0x1_0000_0000,
    ]) {
      expect(() =>
        validateCrashReport({ ...safeCrashReport, reportSchemaVersion }),
      ).toThrow(RangeError);
    }
    expect(() =>
      validateCrashReport(create(SubmitCrashReportRequestSchema, {})),
    ).toThrow(RangeError);
    expect(() =>
      validateCrashReport(
        create(SubmitCrashReportRequestSchema, {
          ...safeCrashReport,
          reportSchemaVersion: 0,
        }),
      ),
    ).toThrow(RangeError);
    expect(() =>
      validateCrashReport(
        create(SubmitCrashReportRequestSchema, {
          ...safeCrashReport,
          clientBuild: undefined,
        }),
      ),
    ).toThrow(TypeError);
    expect(() =>
      validateCrashReport(
        create(SubmitCrashReportRequestSchema, {
          ...safeCrashReport,
          occurredAt: undefined,
        }),
      ),
    ).toThrow(TypeError);
    expect(() =>
      validateCrashReport(
        create(SubmitCrashReportRequestSchema, {
          ...safeCrashReport,
          clientBuild: { ...clientBuild, platform: DiagnosticPlatform.UNSPECIFIED },
        }),
      ),
    ).toThrow(TypeError);
    expect(() =>
      validateCrashReport(
        create(SubmitCrashReportRequestSchema, {
          ...safeCrashReport,
          clientBuild: {
            ...clientBuild,
            architecture: DiagnosticArchitecture.UNSPECIFIED,
          },
        }),
      ),
    ).toThrow(TypeError);
    expect(() =>
      validateCrashReport(
        create(SubmitCrashReportRequestSchema, {
          ...safeCrashReport,
          component: DiagnosticComponent.UNSPECIFIED,
        }),
      ),
    ).toThrow(TypeError);
    expect(() =>
      validateCrashReport(
        create(SubmitCrashReportRequestSchema, {
          ...safeCrashReport,
          severity: DiagnosticSeverity.UNSPECIFIED,
        }),
      ),
    ).toThrow(TypeError);
  });

  it("validates the occurredAt protobuf timestamp range", () => {
    const withTimestamp = (seconds: bigint, nanos: number) => ({
      ...safeCrashReport,
      occurredAt: { ...safeCrashReport.occurredAt!, seconds, nanos },
    });

    expect(() => validateCrashReport(withTimestamp(-62_135_596_800n, 0))).not.toThrow();
    expect(() =>
      validateCrashReport(withTimestamp(253_402_300_799n, 999_999_999)),
    ).not.toThrow();

    for (const [seconds, nanos] of [
      [-62_135_596_801n, 0],
      [253_402_300_800n, 0],
      [0n, -1],
      [0n, 1.5],
      [0n, 1_000_000_000],
    ] as const) {
      expect(() => validateCrashReport(withTimestamp(seconds, nanos))).toThrow(RangeError);
    }
  });

  it("rejects unknown crash report enum values", () => {
    const unknownEnumValue = 99;
    const reports = [
      {
        ...safeCrashReport,
        clientBuild: {
          ...clientBuild,
          platform: unknownEnumValue as DiagnosticPlatform,
        },
      },
      {
        ...safeCrashReport,
        clientBuild: {
          ...clientBuild,
          architecture: unknownEnumValue as DiagnosticArchitecture,
        },
      },
      {
        ...safeCrashReport,
        component: unknownEnumValue as DiagnosticComponent,
      },
      {
        ...safeCrashReport,
        severity: unknownEnumValue as DiagnosticSeverity,
      },
    ];

    for (const report of reports) {
      expect(() => validateCrashReport(report)).toThrow(TypeError);
    }
  });

  it("rejects lone surrogates in every crash diagnostic string", () => {
    const loneSurrogate = "\ud800";
    const invalidDiagnostics = [
      { ...safeCrashReport, errorCode: loneSurrogate },
      { ...safeCrashReport, clientBuild: { ...clientBuild, appVersion: loneSurrogate } },
      { ...safeCrashReport, clientBuild: { ...clientBuild, buildId: loneSurrogate } },
      { ...safeCrashReport, clientBuild: { ...clientBuild, osVersion: loneSurrogate } },
      { ...safeCrashReport, redactedSummary: loneSurrogate },
      { ...safeCrashReport, redactedStackTrace: loneSurrogate },
    ];

    for (const report of invalidDiagnostics) {
      expect(() =>
        validateCrashReport(create(SubmitCrashReportRequestSchema, report)),
      ).toThrow(TypeError);
    }

    expect(() =>
      validateCrashReport(
        create(SubmitCrashReportRequestSchema, {
          ...safeCrashReport,
          redactedSummary: "Upload failed safely 😀",
        }),
      ),
    ).not.toThrow();
  });

  it("accepts slash-delimited diagnostic labels without local-path evidence", () => {
    for (const redactedSummary of [
      "React/Native failure",
      "Upload failed / retry scheduled",
      "Expected yes / no",
    ]) {
      const report = create(SubmitCrashReportRequestSchema, {
        ...safeCrashReport,
        clientBuild: {
          ...clientBuild,
          appVersion: "1.0.0/42",
          osVersion: "iOS/18.6",
        },
        redactedSummary,
      });

      expect(() => validateCrashReport(report), redactedSummary).not.toThrow();
    }

    for (const diagnostic of ["2026/08/15", "1/2/3"]) {
      const reports = [
        { ...safeCrashReport, errorCode: diagnostic },
        { ...safeCrashReport, clientBuild: { ...clientBuild, appVersion: diagnostic } },
        { ...safeCrashReport, clientBuild: { ...clientBuild, buildId: diagnostic } },
        { ...safeCrashReport, clientBuild: { ...clientBuild, osVersion: diagnostic } },
        { ...safeCrashReport, redactedSummary: diagnostic },
        { ...safeCrashReport, redactedStackTrace: diagnostic },
      ];

      for (const report of reports) {
        expect(
          () => validateCrashReport(create(SubmitCrashReportRequestSchema, report)),
          diagnostic,
        ).not.toThrow();
      }
    }
  });

  it("rejects local paths and credential-shaped crash diagnostics", () => {
    const safe = safeCrashReport;
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
      "source: /workspace/private/app.ts:10",
      "frame: C:\\Users\\alice\\app.ts:10",
    ]) {
      const report = create(SubmitCrashReportRequestSchema, {
        ...safe,
        redactedStackTrace: stackTrace,
      });
      expect(() => validateCrashReport(report), stackTrace).toThrow(TypeError);
    }

    const encodedLocalFileUrlReport = create(SubmitCrashReportRequestSchema, {
      ...safe,
      redactedStackTrace: `at load (${encodedLocalFileUrl})`,
    });
    expect(() => validateCrashReport(encodedLocalFileUrlReport)).toThrow(TypeError);

    for (const stackTrace of [
      "at render (app.ts:10:2)",
      "main.rs:12",
      "src/main.rs:12",
      "at render (src/private/customer/app.ts:10:2)",
      "at render (src\\private\\customer\\app.ts:10:2)",
    ]) {
      const report = create(SubmitCrashReportRequestSchema, {
        ...safe,
        redactedStackTrace: stackTrace,
      });
      expect(() => validateCrashReport(report), stackTrace).toThrow(TypeError);
    }

    for (const relativePath of [
      "src/private/customer/app.ts:10:2",
      "config/.env",
      "config/Dockerfile",
      "src/private/module:10",
      "src\\private\\module:10",
      "source:src/private/app.ts:10",
      "frame:src\\private\\app.ts:10",
    ]) {
      const relativePathDiagnostics = [
        { ...safe, errorCode: relativePath },
        { ...safe, clientBuild: { ...clientBuild, appVersion: relativePath } },
        { ...safe, clientBuild: { ...clientBuild, buildId: relativePath } },
        { ...safe, clientBuild: { ...clientBuild, osVersion: relativePath } },
        { ...safe, redactedSummary: relativePath },
        { ...safe, redactedStackTrace: relativePath },
      ];
      for (const report of relativePathDiagnostics) {
        expect(
          () => validateCrashReport(create(SubmitCrashReportRequestSchema, report)),
          relativePath,
        ).toThrow(TypeError);
      }
    }

    for (const remoteUrl of [
      "https://example.com/assets/app.js:10:2",
      "https://cdn.example.com/app.js?v=42",
      "https://docs.example.com/guide#configuration",
      "wss://example.com/socket",
      "devhud://auth/callback",
      "mailto:user@example.com?subject=secret",
      "https://example.com/?na%6de=release",
      r2UnsignedMetadataUrl,
    ]) {
      const report = create(SubmitCrashReportRequestSchema, {
        ...safe,
        redactedStackTrace: `at load (${remoteUrl})`,
      });
      expect(() => validateCrashReport(report), remoteUrl).not.toThrow();
    }

    for (const credentialUrl of [
      "https://alice:password@example.com/app.js",
      "https://example.com/app.js?v=42&token=secret",
      "https://example.com/app.js#access-token",
      "wss://user:pass@example.com/socket",
      "devhud://auth/callback?code=secret&state=x",
      "https://example.com/#access%2Dtoken=secret",
      "https://example.com/?to%6=secret",
      ...r2SignedCredentialUrls,
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

    for (const credentialValue of [
      "password=hunter2",
      "client_secret: unsafe-value",
      "refresh_token=unsafe-value",
      "cookie: session=unsafe-value",
      '{"apiKey":"unsafe-value"}',
      "AUTHORIZATION=unsafe-value",
      "AWS_SECRET_ACCESS_KEY=unsafe-value",
      "AWS_SESSION_TOKEN=unsafe-value",
      "GITHUB_TOKEN=unsafe-value",
      "devhud://auth/callback?co%64e=unsafe-value",
      "https://example.com/?to%6ben=unsafe-value",
    ]) {
      const credentialDiagnostics = [
        { ...safe, errorCode: credentialValue },
        { ...safe, clientBuild: { ...clientBuild, appVersion: credentialValue } },
        { ...safe, clientBuild: { ...clientBuild, buildId: credentialValue } },
        { ...safe, clientBuild: { ...clientBuild, osVersion: credentialValue } },
        { ...safe, redactedSummary: credentialValue },
        { ...safe, redactedStackTrace: credentialValue },
      ];
      for (const report of credentialDiagnostics) {
        expect(() =>
          validateCrashReport(create(SubmitCrashReportRequestSchema, report)),
        ).toThrow(TypeError);
      }
    }

    for (const redactedSummary of [
      "Password validation failed because the field was empty.",
      "Cookie parsing failed after session expiry.",
      "ERROR_CODE=E_UPLOAD RETRY_COUNT=3 TOKEN_COUNT=2",
    ]) {
      expect(() =>
        validateCrashReport(
          create(SubmitCrashReportRequestSchema, { ...safe, redactedSummary }),
        ),
      ).not.toThrow();
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
