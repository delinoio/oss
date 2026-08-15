import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { UuidV7Schema } from "../src/gen/devhud/v1/common_pb.js";
import { CreateUploadRequestSchema } from "../src/gen/devhud/v1/upload_pb.js";

const submissionId = create(UuidV7Schema, {
  value: "0198b8d0-07d0-7c4d-8d61-4f2019a76502",
});
const groupId = create(UuidV7Schema, {
  value: "0198b8d0-07d0-7c4d-8d61-4f2019a76503",
});

describe("CreateUpload group lifecycle shapes", () => {
  it("represents creation of a submission and first group with no IDs", () => {
    const request = create(CreateUploadRequestSchema);

    expect(request.submissionId).toBeUndefined();
    expect(request.uploadGroupId).toBeUndefined();
  });

  it("represents creation of a later group with the submission ID only", () => {
    const request = create(CreateUploadRequestSchema, { submissionId });

    expect(request.submissionId).toEqual(submissionId);
    expect(request.uploadGroupId).toBeUndefined();
  });

  it("represents reuse of an owned group with both IDs", () => {
    const request = create(CreateUploadRequestSchema, {
      submissionId,
      uploadGroupId: groupId,
    });

    expect(request.submissionId).toEqual(submissionId);
    expect(request.uploadGroupId).toEqual(groupId);
  });

  it("keeps group-only requests distinguishable for server rejection", () => {
    const request = create(CreateUploadRequestSchema, { uploadGroupId: groupId });

    expect(request.submissionId).toBeUndefined();
    expect(request.uploadGroupId).toEqual(groupId);
  });
});
