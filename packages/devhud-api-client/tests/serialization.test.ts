import { create, fromBinary, fromJsonString, toBinary, toJsonString } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import {
  CreateUploadRequestSchema,
  FinalizeUploadRequestSchema,
} from "../src/gen/devhud/v1/upload_pb.js";
import { UploadContentType } from "../src/gen/devhud/v1/common_pb.js";
import { ReplaceSettingsRequestSchema } from "../src/gen/devhud/v1/settings_pb.js";
import { assertSha256, validateCanonicalSettingsJson } from "../src/validation.js";

const uuid = "018f47a2-7b3c-7def-8abc-1234567890ab";

describe("protobuf serialization", () => {
  it("round-trips immutable upload finalization fields in binary and JSON", () => {
    const checksum = Uint8Array.from({ length: 32 }, (_, index) => index);
    const message = create(FinalizeUploadRequestSchema, {
      uploadId: { value: uuid },
      submissionId: { value: uuid },
      uploadGroupId: { value: uuid },
      reservationId: { value: uuid },
      stagingGeneration: 42n,
      expectedSizeBytes: 50_000_000n,
      expectedSha256: checksum,
      observedEtag: '"immutable-etag"',
    });

    const binary = toBinary(FinalizeUploadRequestSchema, message);
    expect(fromBinary(FinalizeUploadRequestSchema, binary)).toEqual(message);

    const json = toJsonString(FinalizeUploadRequestSchema, message);
    expect(json).toContain('"stagingGeneration":"42"');
    expect(json).toContain('"expectedSha256":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="');
    expect(fromJsonString(FinalizeUploadRequestSchema, json)).toEqual(message);
  });

  it("preserves explicit upload lifecycle variants", () => {
    const checksum = new Uint8Array(32);
    const message = create(CreateUploadRequestSchema, {
      target: { target: { case: "newSubmission", value: {} } },
      expectedSizeBytes: 123n,
      expectedSha256: checksum,
      contentType: UploadContentType.PNG,
    });

    const decoded = fromBinary(
      CreateUploadRequestSchema,
      toBinary(CreateUploadRequestSchema, message),
    );
    expect(decoded.target?.target.case).toBe("newSubmission");
    assertSha256(decoded.expectedSha256);
  });

  it("preserves canonical settings bytes and uint64 revisions", () => {
    const canonicalJson = new TextEncoder().encode('{"language":"en","theme":"system"}');
    const message = create(ReplaceSettingsRequestSchema, {
      schemaVersion: 3,
      canonicalJson,
      expectedRevision: 9_007_199_254_740_993n,
    });

    const decoded = fromBinary(
      ReplaceSettingsRequestSchema,
      toBinary(ReplaceSettingsRequestSchema, message),
    );
    expect(decoded.expectedRevision).toBe(9_007_199_254_740_993n);
    expect(validateCanonicalSettingsJson(decoded.canonicalJson)).toEqual({
      language: "en",
      theme: "system",
    });
  });
});
