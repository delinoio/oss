import { Code, ConnectError } from "@connectrpc/connect";
import type { DescMessage, MessageInitShape } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import {
  ErrorMetadataSchema,
  PaginationFailureReason,
  PaginationFailureSchema,
  QuotaFailureSchema,
  QuotaKind,
} from "../src/gen/devhud/v1/common_pb.js";
import { SettingsRevisionConflictSchema } from "../src/gen/devhud/v1/settings_pb.js";
import { UploadFailureReason, UploadFailureSchema } from "../src/gen/devhud/v1/upload_pb.js";
import { mapDevHudError } from "../src/error.js";

const correlationId = "018f47a2-7b3c-7def-8abc-1234567890ab";

describe("Connect error mapping", () => {
  it("maps revision conflicts with their current snapshot", () => {
    const error = connectError(Code.Aborted, SettingsRevisionConflictSchema, {
      expectedRevision: 2n,
      currentSnapshot: {
        schemaVersion: 1,
        revision: 3n,
        canonicalJson: new TextEncoder().encode('{"theme":"system"}'),
      },
    });
    const mapped = mapDevHudError(error);

    expect(mapped.kind).toBe("revisionConflict");
    if (mapped.kind === "revisionConflict") {
      expect(mapped.detail.currentSnapshot?.revision).toBe(3n);
    }
    expect(mapped.correlationId).toBe(correlationId);
  });

  it("maps quota, upload, and pagination detail types", () => {
    expect(
      mapDevHudError(
        connectError(Code.ResourceExhausted, QuotaFailureSchema, {
          quota: QuotaKind.SUBMISSION_IMAGES,
          limit: 10n,
          observed: 11n,
        }),
      ).kind,
    ).toBe("quotaExceeded");

    expect(
      mapDevHudError(
        connectError(Code.FailedPrecondition, UploadFailureSchema, {
          reason: UploadFailureReason.CHECKSUM_MISMATCH,
        }),
      ).kind,
    ).toBe("uploadPrecondition");

    expect(
      mapDevHudError(
        connectError(Code.InvalidArgument, PaginationFailureSchema, {
          reason: PaginationFailureReason.TOKEN_SCOPE_MISMATCH,
        }),
      ).kind,
    ).toBe("pagination");
  });

  it("uses the exposed response header when typed metadata is unavailable", () => {
    const error = new ConnectError("missing credentials", Code.Unauthenticated, {
      "x-devhud-correlation-id": correlationId,
    });
    expect(mapDevHudError(error)).toMatchObject({
      kind: "unauthenticated",
      correlationId,
    });
  });

  it("falls back safely for unmatched errors", () => {
    expect(mapDevHudError(new Error("network failure"))).toMatchObject({
      kind: "unknown",
      code: Code.Unknown,
    });
  });
});

function connectError<Desc extends DescMessage>(
  code: Code,
  desc: Desc,
  value: MessageInitShape<Desc>,
): ConnectError {
  return new ConnectError("contracted failure", code, undefined, [
    { desc, value },
    { desc: ErrorMetadataSchema, value: { correlationId: { value: correlationId } } },
  ]);
}
