// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { expect, it } from "vitest";
import { accountRemovalMutation } from "./account-removal";

const id = newRequestId(), requestId = newRequestId();
const marker = (revision: string) => `{"request_id":"${requestId}","expected_revision":${revision}}`;
const body = (removal: string) => `{"type":"api","health":"disconnected","removal":${removal}}`;
function resource(raw: string) {
  return create(ResourceSchema, { id, kind: EntityKind.ACCOUNT, schemaVersion: 1, revision: 999n, documentJson: new TextEncoder().encode(raw) });
}

it.each(["1", "42", "9007199254740991", "9007199254740992", "9007199254740993", "18446744073709551615"])("reconstructs the original request at revision %s", (revision) => {
  expect(accountRemovalMutation(resource(body(marker(revision))))).toEqual({ id, requestId, expectedRevision: BigInt(revision) });
});

it.each(["0", "-1", "-0", "01", "+1", "1.0", "1e3", "9.007199254740993e15", '"42"', '"9007199254740993"', "null", "true", "[]", "{}", "NaN", "Infinity", "18446744073709551616", "999999999999999999999999999999"])("rejects invalid revision token %s", (revision) => {
  expect(accountRemovalMutation(resource(body(marker(revision))))).toBeUndefined();
});

it.each([
  "null", "[]", "42", "{}", `{"request_id":"${requestId}"}`, '{"expected_revision":42}',
  '{"request_id":"not-a-uuid","expected_revision":42}', '{"request_id":42,"expected_revision":42}',
  `{"request_id":"${requestId}","request_id":"${newRequestId()}","expected_revision":42}`,
  `{"request_id":"${requestId}","expected_revision":42,"expected_revision":43}`,
  `{"request_id":"${requestId}","expected_revision":42,"expected\\u005frevision":43}`,
  `{"request_id":"${requestId}","expected_revision":42,"foreign":true}`,
  `{"nested":${marker("42")}}`,
])("rejects malformed or conflicting marker %s", (removal) => {
  expect(accountRemovalMutation(resource(body(removal)))).toBeUndefined();
});

it("ignores nested and string lookalikes and decodes escaped member names", () => {
  const raw = ` { "alias": "Comma, brace } and escaped quote \\"", "nested": [${marker("8")}, {"removal":${marker("9")}}], "type":"api", "health":"disconnected", "remo\\u0076al": { "expected\\u005frevision": 9007199254740993, "request\\u005fid":"${requestId}" } } `;
  expect(accountRemovalMutation(resource(raw))).toEqual({ id, requestId, expectedRevision: 9007199254740993n });
});

it.each([
  `${body(marker("42")).slice(0, -1)},"removal":${marker("43")}}`,
  `${body(marker("42")).slice(0, -1)},"remo\\u0076al":${marker("43")}}`,
  body(marker("42")).replace('"api"', '"subscription"'),
  body(marker("42")).replace('"disconnected"', '"ready"'),
  `${body(marker("42")).slice(0, -1)},"connection":{"id":"${newRequestId()}"}}`,
  `${body(marker("42"))} trailing`,
  '[{"removal":{}}]',
])("rejects inconsistent or malformed account documents %s", (raw) => {
  expect(accountRemovalMutation(resource(raw))).toBeUndefined();
});

it("rejects unsupported resources and oversized or invalid UTF-8 documents", () => {
  const row = resource(body(marker("42")));
  const invalidUtf8 = new TextEncoder().encode(`{"alias":"x",${body(marker("42")).slice(1)}`);
  invalidUtf8[invalidUtf8.indexOf(0x78)] = 0xff;
  for (const change of [
    { kind: EntityKind.PROVIDER }, { schemaVersion: 2 }, { id: "invalid" },
    { documentJson: new TextEncoder().encode(`{"padding":"${"x".repeat(1 << 20)}",${body(marker("42")).slice(1)}`) },
    { documentJson: invalidUtf8 },
  ]) expect(accountRemovalMutation(create(ResourceSchema, { ...row, ...change }))).toBeUndefined();
});

it("retains exact Go key cleanup markers without admitting native subscription accounts", () => {
  const raw = (service: string) => `{"type":"subscription","subscription_service":"${service}","health":"disconnected","removal":${marker("9007199254740993")}}`;
  const row = create(ResourceSchema, { ...resource(raw("opencode_go")), schemaVersion: 2 });
  expect(accountRemovalMutation(row)).toEqual({ id, requestId, expectedRevision: 9007199254740993n });
  for (const service of ["chatgpt", "claude", "grok", "unknown"]) {
    expect(accountRemovalMutation(create(ResourceSchema, { ...row, documentJson: new TextEncoder().encode(raw(service)) }))).toBeUndefined();
  }
});
