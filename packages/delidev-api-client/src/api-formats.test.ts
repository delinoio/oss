// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import { EntityKind, ResourceSchema } from "./gen/delidev/v1/delidev_pb.js";
import { APIFormatId, APIAuthenticationId, accountAPIProfile, apiFormatMatchesHarness } from "./api-formats.js";
import { configurationSchemaVersion, supportsResourceSchema } from "./configuration-identity.js";

const responses = { protocol: APIFormatId.Responses, endpoint: "https://openrouter.ai/api/v1", authentication: APIAuthenticationId.Bearer };
const chat = { ...responses, protocol: APIFormatId.ChatCompletions };
const provider = { ...chat, api_formats: [responses, chat] };
const resource = (kind: EntityKind, data: Record<string, unknown>, version = configurationSchemaVersion(kind, data)) => create(ResourceSchema, { kind, schemaVersion: version, documentJson: new TextEncoder().encode(JSON.stringify(data)) });

it("uses each key's selected format and preserves a legacy account's original default", () => {
  for (const format of [APIFormatId.Responses, APIFormatId.ChatCompletions]) {
    const selected = accountAPIProfile(provider, { type: "api", api_protocol: format });
    expect(selected?.protocol).toBe(format);
    expect(apiFormatMatchesHarness(selected?.protocol, "codex")).toBe(format === APIFormatId.Responses);
  }
  expect(accountAPIProfile(provider, { type: "api" })).toEqual(chat);
  expect(accountAPIProfile(provider, { type: "api", api_protocol: APIFormatId.Messages })).toBeUndefined();
  expect(accountAPIProfile(provider, { type: "subscription" })).toBeUndefined();
});

it("protects explicit API families with schema 3 and rejects unknown or duplicate profiles", () => {
  const account = { type: "api", provider_id: "provider", api_protocol: APIFormatId.Responses };
  for (const [kind, data] of [[EntityKind.PROVIDER, provider], [EntityKind.ACCOUNT, account]] as const) {
    expect(configurationSchemaVersion(kind, data)).toBe(3);
    expect(supportsResourceSchema(resource(kind, data))).toBe(true);
    expect(supportsResourceSchema(resource(kind, data, 1))).toBe(false);
    expect(supportsResourceSchema(resource(kind, data, 2))).toBe(false);
  }
  for (const formats of [[], [responses, responses], [{ ...responses, protocol: "other" }], [{ ...responses, authentication: "other" }]]) {
    expect(supportsResourceSchema(resource(EntityKind.PROVIDER, { ...chat, api_formats: formats }, 3))).toBe(false);
  }
  expect(supportsResourceSchema(resource(EntityKind.ACCOUNT, { ...account, api_protocol: "other" }, 3))).toBe(false);
  expect(supportsResourceSchema(resource(EntityKind.PROVIDER, chat, 1))).toBe(true);
});
