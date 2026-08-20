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
  MAX_CRASH_STACK_BYTES,
  assertSha256,
  assertUuidV7,
  canonicalizeSettingsJson,
  encodeCanonicalSettingsJson,
  validateAdminReason,
  validateCanonicalSettingsJson,
  validateCrashReport,
} from "../src/validation.js";

const uuid = "018f47a2-7b3c-7def-8abc-1234567890ab";
const relatedUuid = "018f47a2-7b3c-7def-9abc-1234567890ab";
const clientBuild = create(ClientBuildSchema, {
  appVersion: "1.0.0",
  buildId: "devhud-20260815.1",
  platform: DiagnosticPlatform.MACOS,
  architecture: DiagnosticArchitecture.ARM64,
  osVersion: "macOS 15.0",
  tauriRevision: "4af26a3f7f8b692d62cca549bbacd93f5ce90b41",
  cefRevision: "150.0.10+g8042e43+chromium-150.0.7871.101",
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
  relatedCorrelationIds: [{ value: relatedUuid }],
  clientCorrelationId: { value: uuid },
  durationMilliseconds: 1200n,
});
const r2SignedCredentialUrls = [
  "https://account.r2.cloudflarestorage.com/bucket/report?X-Amz-Credential=R2ACCESSKEY%2F20260815%2Fauto%2Fs3%2Faws4_request",
  "https://account.r2.cloudflarestorage.com/bucket/report?X-Amz-Signature=0123456789abcdef",
] as const;
const r2UnsignedMetadataUrl =
  "https://account.r2.cloudflarestorage.com/bucket/report?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Date=20260815T180000Z&X-Amz-Expires=900&X-Amz-SignedHeaders=host";
const encodedLocalFileUrl = "file:%2FUsers%2Falice%2Fproject%2Fapp.ts";
const bareJwt = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature";
const encodedCredentialParameterUrls = [
  "https://example.com/?context=password%3Dhunter2",
  "https://example.com/#context=password%3Dhunter2",
  "https://example.com/?safe=code%3Dsecret",
  "https://example.com/?safe=x%26code%3Dsecret",
  "https://example.com/#safe=x%3Bcode%3Dsecret",
] as const;
const encodedLocalPathParameterUrls = [
  "https://example.com/?source=%2Fworkspace%2Fprivate%2Fapp.ts",
  "https://example.com/#source=%2Fworkspace%2Fprivate%2Fapp.ts",
] as const;
const publicAssetBaseUrl = "https://assets.example.com/uploads/";
const validateReason = (reason: string) => validateAdminReason(reason, publicAssetBaseUrl);

describe("wire validation helpers", () => {
  it("accepts only canonical UUID v7 and 32-byte digests", () => {
    expect(() => assertUuidV7(uuid)).not.toThrow();
    expect(() => assertUuidV7("018F47A2-7B3C-7DEF-8ABC-1234567890AB")).toThrow(TypeError);
    expect(() => assertUuidV7("018f47a2-7b3c-6def-8abc-1234567890ab")).toThrow(TypeError);
    expect(() => assertSha256(new Uint8Array(32))).not.toThrow();
    expect(() => assertSha256(new Uint8Array(31))).toThrow(RangeError);
  });

  it("validates bounded sensitive-content-safe administrator reasons", () => {
    expect(() => validateReason("Quarantined after repeated policy violations.")).not.toThrow();
    expect(() => validateReason("Expected yes / no")).not.toThrow();
    expect(() => validateReason("Reviewed incident from 2026/08/15.")).not.toThrow();
    expect(() => validateReason("Rolled back release 1/2/3.")).not.toThrow();
    expect(() => validateReason("Reviewed callback?code=review")).not.toThrow();
    expect(() => validateReason("\u0085Reviewed policy breach")).not.toThrow();
    expect(() => validateReason("é".repeat(MAX_ADMIN_REASON_BYTES / 2))).not.toThrow();
    expect(() =>
      validateReason("Reviewed https://docs.example.com/policy?v=42#quarantine"),
    ).not.toThrow();
    expect(() => validateReason("Escalated to mailto:ops@example.com")).not.toThrow();
    expect(() => validateReason("Observed via wss://monitor.example.test/events")).not.toThrow();
    expect(() =>
      validateReason("Reviewed https://example.com/?na%6de=release"),
    ).not.toThrow();
    expect(() =>
      validateReason(
        "Reviewed https://example.com/?context=release%3D2026#component=React%2FNative",
      ),
    ).not.toThrow();
    expect(() =>
      validateReason("Reviewed https://example.com/?safe=x%26release%3D2026"),
    ).not.toThrow();
    expect(() =>
      validateReason("Reviewed https://assets.example.com/docs/upload-policy"),
    ).not.toThrow();
    expect(() =>
      validateReason("Reviewed https://assets.example.com/uploads-archive/image-policy"),
    ).not.toThrow();
    expect(() => validateReason(`Reviewed ${r2UnsignedMetadataUrl}`)).not.toThrow();
    expect(() =>
      validateReason("ERROR_CODE=E_UPLOAD RETRY_COUNT=3 TOKEN_COUNT=2"),
    ).not.toThrow();
    expect(() => validateReason("Reviewed service.component.error.")).not.toThrow();

    expect(() => validateReason("")).toThrow(TypeError);
    expect(() => validateReason(" \n\t ")).toThrow(TypeError);
    expect(() => validateReason("\u0085\u2007\u2028")).toThrow(TypeError);
    expect(() => validateReason("\ud800")).toThrow(TypeError);
    expect(() => validateReason("Reviewed policy\0violation")).toThrow(TypeError);
    expect(() => validateReason("é".repeat(MAX_ADMIN_REASON_BYTES / 2 + 1))).toThrow(
      RangeError,
    );

    for (const reason of [
      "Authorization: Bearer unsafe-value",
      `Authentication failure exposed ${bareJwt}`,
      "refresh_token=unsafe-value",
      "AWS_SECRET_ACCESS_KEY=unsafe-value",
      "AWS_SESSION_TOKEN=unsafe-value",
      "GITHUB_TOKEN=unsafe-value",
      "See /Users/example/private/incident.txt",
      "See src/private/incident.txt",
      "source:src/private/app.ts:10",
      "frame:src\\private\\app.ts:10",
      "source=%2Fworkspace%2Fprivate%2Fapp.ts",
      "C:%5CUsers%5Calice%5Capp.ts",
      encodedLocalFileUrl,
      "https://example.com/audit?token=unsafe-value",
      "devhud://auth/callback?co%64e=unsafe-value",
      "https://example.com/?to%6ben=unsafe-value",
      "https://example.com/?to%6=unsafe-value",
      "https://assets.example.com/uploads",
      "https://assets.example.com/uploads/018f47a2-7b3c-7def-8abc-1234567890ab/image.png?size=full#preview",
      "https://assets.example.com/%75ploads/018f47a2-7b3c-7def-8abc-1234567890ab/image.png",
      ...encodedCredentialParameterUrls,
      ...encodedLocalPathParameterUrls,
      ...r2SignedCredentialUrls,
    ]) {
      expect(() => validateReason(reason), reason).toThrow(TypeError);
    }

    expect(() => validateAdminReason("Reviewed policy", "relative/assets")).toThrow(TypeError);
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

  it("encodes settings with RFC 8785 key order and the shared size limit", () => {
    const value = { theme: "dark", nested: { z: 1, a: true }, decks: [] };
    const source = '{"decks":[],"nested":{"a":true,"z":1},"theme":"dark"}';

    expect(canonicalizeSettingsJson(value)).toBe(source);
    expect(new TextDecoder().decode(encodeCanonicalSettingsJson(value))).toBe(source);
    expect(validateCanonicalSettingsJson(encodeCanonicalSettingsJson(value))).toEqual(value);
    expect(() => encodeCanonicalSettingsJson("x".repeat(1_048_576))).toThrow(RangeError);
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
    for (const reportSchemaVersion of [
      -1,
      0,
      2,
      1.5,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      0xffff_ffff,
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
      validateCrashReport({ ...safeCrashReport, durationMilliseconds: -1n }),
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
    for (const field of ["appVersion", "buildId", "osVersion"] as const) {
      expect(() => validateCrashReport(create(SubmitCrashReportRequestSchema, {
        ...safeCrashReport,
        clientBuild: { ...clientBuild, [field]: "" },
      }))).toThrow(TypeError);
    }
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

  it("allows unknown architecture only for browser reports", () => {
    const browserBuild = {
      ...clientBuild,
      platform: DiagnosticPlatform.BROWSER,
      architecture: DiagnosticArchitecture.UNSPECIFIED,
      osVersion: "browser",
      tauriRevision: "",
      cefRevision: "",
    };
    expect(() => validateCrashReport({ ...safeCrashReport, clientBuild: browserBuild })).not.toThrow();
    expect(() => validateCrashReport({
      ...safeCrashReport,
      clientBuild: { ...clientBuild, architecture: DiagnosticArchitecture.UNSPECIFIED },
    })).toThrow(TypeError);
  });

  it("allows ARMv7 only for Android reports", () => {
    for (const platform of [
      DiagnosticPlatform.MACOS,
      DiagnosticPlatform.WINDOWS,
      DiagnosticPlatform.LINUX,
      DiagnosticPlatform.IOS,
      DiagnosticPlatform.BROWSER,
    ]) {
      const browser = platform === DiagnosticPlatform.BROWSER;
      const mobile = platform === DiagnosticPlatform.IOS;
      expect(() => validateCrashReport({
        ...safeCrashReport,
        clientBuild: {
          ...clientBuild,
          platform,
          architecture: DiagnosticArchitecture.ARMV7,
          tauriRevision: browser ? "" : clientBuild.tauriRevision,
          cefRevision: browser || mobile ? "" : clientBuild.cefRevision,
        },
      })).toThrow(TypeError);
    }
    expect(() => validateCrashReport({
      ...safeCrashReport,
      clientBuild: {
        ...clientBuild,
        platform: DiagnosticPlatform.ANDROID,
        architecture: DiagnosticArchitecture.ARMV7,
        cefRevision: "",
      },
    })).not.toThrow();
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
    expect(() => validateCrashReport({ ...safeCrashReport, errorCode: "2026/08/15" })).toThrow(TypeError);
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
      "source=%2Fworkspace%2Fprivate%2Fapp.ts",
      "C:%5CUsers%5Calice%5Capp.ts",
      "vscode://file/home/alice/app.ts",
      "vscode-insiders://file/C:/Users/alice/app.ts",
      "subl://open/home/alice/app.ts",
      "devhud://auth/callback",
      "wss://example.com/socket",
      "mailto:user@example.com?subject=secret",
      "http:/home/alice/app.ts",
      "https:C:/Users/alice/file.txt",
      "http:///home/alice/app.ts",
      ...encodedLocalPathParameterUrls,
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
      "https://example.com/assets%2Fapp.js:10:2",
      "https://cdn.example.com/app.js?v=42",
      "https://example.com/?a=1&b=2&c=3&d=4&e=5&f=6&g=7&h=8&i=9",
      "http://example.com/assets/app.js:10:2",
      "https://example.com/?na%6de=release",
      "https://example.com/?safe=x%26release%3D2026",
      "callback=https%253A%252F%252Fexample.com%252Fauth%253Fstate%253Dopaque",
      "callback?state=opaque",
      "auth/callback#component=React%2FNative",
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
      "devhud://auth/callback?safe=x;code=secret",
      "https://example.com/app.js#access-token",
      "https://docs.example.com/guide#configuration",
      "https://example.com/?context=release%3D2026#component=React%2FNative",
      "wss://user:pass@example.com/socket",
      "devhud://auth/callback?code=secret&state=x",
      "callback_devhud://auth/callback?code=secret&state=x",
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
      bareJwt,
      "Bearer unsafe-value",
      "password=hunter2",
      "client_secret: unsafe-value",
      "refresh_token=unsafe-value",
      "cookie: session=unsafe-value",
      '{"apiKey":"unsafe-value"}',
      "AUTHORIZATION=unsafe-value",
      "AWS_SECRET_ACCESS_KEY=unsafe-value",
      "AWS_SESSION_TOKEN=unsafe-value",
      "GITHUB_TOKEN=unsafe-value",
      "GITHUB_PAT=unsafe-value",
      "DEVHUD_SESSION_ID=unsafe-value",
      "DEVHUD_SIGNING_KEY=unsafe-value",
      "code=unsafe-value",
      "oauth_code: unsafe-value",
      "pat=hunter2",
      "session_id=secret",
      "signing_value=secret",
      "r2_access_key_id=0123456789abcdef",
      "DEVHUD_R2_ACCESS_KEY_ID=0123456789abcdef",
      "r2.access-key-id=0123456789abcdef",
      "callback?r2.access-key-id=0123456789abcdef",
      '{"code":"unsafe-value"}',
      "request_body=email=alice@example.test",
      "response-body=email=alice@example.test",
      "request_headers=Authorization: redacted",
      "response-headers: Set-Cookie: session=abc",
      "devhud://auth/callback?co%64e=unsafe-value",
      "https://example.com/?to%6ben=unsafe-value",
      "callback=https%253A%252F%252Fexample.test%252Fauth%253Fcode%253Dsecret",
      "callback?code=secret",
      "/auth/callback#access_token=secret",
      "auth/callback#access_token=secret",
      "callback?co%64e=secret",
      "auth/callback#access%2Dtoken=secret",
      "callback?safe=code%3Dsecret",
      "callback?safe=x%26code%3Dsecret",
      "callback%3Fcode%3Dsecret",
      ...encodedCredentialParameterUrls,
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
      "service.component.error",
    ]) {
      expect(() =>
        validateCrashReport(
          create(SubmitCrashReportRequestSchema, { ...safe, redactedSummary }),
        ),
      ).not.toThrow();
    }

    const oversizedValue = "a".repeat(MAX_CRASH_IDENTIFIER_BYTES + 1);
    expect(() => validateCrashReport({ ...safe, errorCode: oversizedValue })).toThrow(TypeError);
    const oversizedIdentifiers = [
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

  it("bounds fixed-point decoding for crash diagnostic text", () => {
    let atBound = "%41";
    for (let decoding = 1; decoding < 8; decoding += 1) {
      atBound = atBound.replaceAll("%", "%25");
    }
    expect(() => validateCrashReport({ ...safeCrashReport, redactedSummary: atBound })).not.toThrow();

    const beyondBound = atBound.replaceAll("%", "%25");
    expect(() => validateCrashReport({ ...safeCrashReport, redactedSummary: beyondBound })).toThrow(TypeError);
  });

  it("bounds nested crash diagnostic parameter scanning", () => {
    const safeNestedParameters = `${"?x=".repeat(16)}safe`;
    expect(() =>
      validateCrashReport(
        create(SubmitCrashReportRequestSchema, {
          ...safeCrashReport,
          redactedSummary: safeNestedParameters,
        }),
      ),
    ).not.toThrow();

    const excessiveNestedParameters = `${"?x=".repeat(17)}safe`;
    expect(() =>
      validateCrashReport(
        create(SubmitCrashReportRequestSchema, {
          ...safeCrashReport,
          redactedSummary: excessiveNestedParameters,
        }),
      ),
    ).toThrow(TypeError);

    const maximumSizeNestedParameters = `${"?x=".repeat(
      Math.floor((MAX_CRASH_STACK_BYTES - "safe".length) / "?x=".length),
    )}safe`;
    expect(() =>
      validateCrashReport(
        create(SubmitCrashReportRequestSchema, {
          ...safeCrashReport,
          redactedStackTrace: maximumSizeNestedParameters,
        }),
      ),
    ).toThrow(TypeError);
  });

  it("keeps narrative filtering off enum error codes", () => {
    for (const errorCode of ["SCREENSHOT_CAPTURE_FAILED", "BROWSER_DOM_REDACTED"]) {
      expect(() => validateCrashReport({ ...safeCrashReport, errorCode })).not.toThrow();
    }
  });

  it("rejects NUL bytes in every persisted crash text group", () => {
    const invalidDiagnostics = [
      { ...safeCrashReport, clientBuild: { ...clientBuild, osVersion: "macOS\u000015.0" } },
      { ...safeCrashReport, redactedSummary: "classified\0summary" },
      { ...safeCrashReport, redactedStackTrace: "render\0frame" },
    ];

    for (const report of invalidDiagnostics) {
      expect(() => validateCrashReport(create(SubmitCrashReportRequestSchema, report))).toThrow(TypeError);
    }
  });

  it("accepts browser reports only without fabricated native revisions", () => {
    const browser = create(SubmitCrashReportRequestSchema, {
      ...safeCrashReport,
      clientBuild: {
        ...clientBuild,
        platform: DiagnosticPlatform.BROWSER,
        osVersion: "browser",
        tauriRevision: "",
        cefRevision: "",
      },
    });
    expect(() => validateCrashReport(browser)).not.toThrow();

    const fabricated = create(SubmitCrashReportRequestSchema, {
      ...browser,
      clientBuild: {
        ...browser.clientBuild!,
        tauriRevision: clientBuild.tauriRevision,
      },
    });
    expect(() => validateCrashReport(fabricated)).toThrow(TypeError);
  });

  it("accepts only the pinned Tauri revision on native reports", () => {
    expect(() => validateCrashReport({
      ...safeCrashReport,
      clientBuild: { ...clientBuild, tauriRevision: "a".repeat(40) },
    })).toThrow(TypeError);
    expect(() => validateCrashReport(safeCrashReport)).not.toThrow();
  });

  it("accepts only the pinned CEF revision on desktop reports", () => {
    expect(() => validateCrashReport({
      ...safeCrashReport,
      clientBuild: { ...clientBuild, cefRevision: "x" },
    })).toThrow(TypeError);
    expect(() => validateCrashReport(safeCrashReport)).not.toThrow();
  });
});
