import { readFile } from "node:fs/promises";

import {
  equals,
  fromBinary,
  fromJson,
  toBinary,
  toJson,
  type DescMessage,
  type JsonValue,
} from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { AdminServiceListAuditEventsResponseSchema } from "../src/gen/devhud/v1/admin_pb.js";
import { GetBootstrapResponseSchema } from "../src/gen/devhud/v1/bootstrap_pb.js";
import { SubmitCrashReportRequestSchema } from "../src/gen/devhud/v1/diagnostics_pb.js";
import { GetSettingsResponseSchema } from "../src/gen/devhud/v1/settings_pb.js";
import { FinalizeUploadResponseSchema } from "../src/gen/devhud/v1/upload_pb.js";

async function readFixture(name: string): Promise<JsonValue> {
  const url = new URL(`../../../protos/devhud/v1/testdata/${name}.json`, import.meta.url);
  return JSON.parse(await readFile(url, "utf8")) as JsonValue;
}

async function expectRoundTrip(schema: DescMessage, fixtureName: string): Promise<void> {
  const fixture = await readFixture(fixtureName);
  const message = fromJson(schema, fixture);
  const decoded = fromBinary(schema, toBinary(schema, message));

  expect(equals(schema, message, decoded)).toBe(true);
  expect(toJson(schema, decoded)).toEqual(fixture);
}

describe("representative serialization fixtures", () => {
  it("round-trips bootstrap", async () => {
    await expectRoundTrip(GetBootstrapResponseSchema, "bootstrap");
  });

  it("round-trips settings with a Struct body and bigint revision", async () => {
    await expectRoundTrip(GetSettingsResponseSchema, "settings");
  });

  it("round-trips upload checksum bytes and UUID v7 identifiers", async () => {
    const fixture = await readFixture("upload");
    const response = fromJson(FinalizeUploadResponseSchema, fixture);

    expect(response.upload?.sha256).toHaveLength(32);
    expect(response.upload?.submissionId?.value).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
    await expectRoundTrip(FinalizeUploadResponseSchema, "upload");
  });

  it("round-trips only structured redacted diagnostics", async () => {
    await expectRoundTrip(SubmitCrashReportRequestSchema, "diagnostics");
  });

  it("round-trips typed audit targets", async () => {
    await expectRoundTrip(AdminServiceListAuditEventsResponseSchema, "audit");
  });
});
