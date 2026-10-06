// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, ResourceSchema, ResourceService, SystemCapability, SystemService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { accountPreferencesDocument } from "./account-preferences";
import { MutationIntents } from "./mutation";
import { ConfigurationEditor } from "./settings";
import { SubscriptionQuotaControls } from "./subscription-quota";

const decode = (bytes: Uint8Array) => new TextDecoder().decode(bytes);
const original = (revision = "9007199254740993") => ` { "alias" : "Original", "enabled":true, "exclude_automatic":false, "recovery_notifications":false, "type":"subscription", "subscription_service":"chatgpt", "health":"ready", "subscription":{"lease":{"revision":${revision}}}, "quota":[], "confirmed_exhausted":false } `;
function resource(raw = original()): Resource {
  return create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 2, revision: 9007199254740997n, documentJson: new TextEncoder().encode(raw) });
}

it.each(["1", "9007199254740991", "9007199254740992", "9007199254740993", "18446744073709551615"])("preserves protected uint64 %s while patching all preferences", (revision) => {
  const raw = original(revision), account = resource(raw);
  const saved = decode(accountPreferencesDocument(account, { alias: 'Changed "name" 🌟', enabled: false, exclude_automatic: true, recovery_notifications: true }));
  expect(saved).toBe(raw.replace('"Original"', JSON.stringify('Changed "name" 🌟')).replace('"enabled":true', '"enabled":false').replace('"exclude_automatic":false', '"exclude_automatic":true').replace('"recovery_notifications":false', '"recovery_notifications":true'));
  expect(decode(account.documentJson)).toBe(raw);
});

it("preserves escaped keys, nested strings, arrays and numeric spellings, and adds absent preferences", () => {
  const raw = '{"ali\\u0061s":"Original","unknown":[{"alias":"nested","text":"braces } ], and \\\" quotes","n":-1.2300e+40},null,true],"lease":18446744073709551615}';
  const saved = decode(accountPreferencesDocument(resource(raw), { alias: "Changed", enabled: true, recovery_notifications: false }));
  expect(saved).toBe(raw.replace('"Original"', '"Changed"').slice(0, -1) + ',"enabled":true,"recovery_notifications":false}');
});

it.each([
  "{}", "[]", '{"alias":null}', '{"alias":"a",}', '{"alias":"a"} trailing',
  '{"alias":"a","alias":"b"}', '{"alias":"a","ali\\u0061s":"b"}',
  '{"alias":"a","nested":{"n":1,"n":2}}', '{"alias":"a","n":01}',
  '{"alias":"a","n":NaN}', '{"alias":"a","n":1e}', '{"alias":"a","n":[1}}',
  '{"alias":"bad\\x"}', '{"alias":"a","n":truefalse}', '{"alias":"a","n":"unterminated}',
])("rejects malformed account JSON without returning replacement bytes: %s", (raw) => {
  expect(() => accountPreferencesDocument(resource(raw), { enabled: false })).toThrow();
});

it("rejects invalid UTF-8, oversized input/output and changes outside the preference allowlist", () => {
  expect(() => accountPreferencesDocument(create(ResourceSchema, { documentJson: new Uint8Array([255]) }), { enabled: true })).toThrow();
  expect(() => accountPreferencesDocument(resource(original() + " ".repeat(1 << 20)), { enabled: true })).toThrow();
  expect(() => accountPreferencesDocument(resource(), { alias: "a".repeat(1 << 20) })).toThrow();
  expect(() => accountPreferencesDocument(resource(), { health: "ready" } as never)).toThrow("Invalid account preference");
  expect(() => accountPreferencesDocument(resource(), { enabled: "true" } as never)).toThrow("Invalid account preference");
});

// The server owns numeric schema validation. Do not round or repair invalid
// protected uint64 values into a different, potentially valid observation.
it.each(["0", "-1", "1.5", "18446744073709551616", '"9007199254740993"'])("leaves invalid protected uint64 %s for server rejection", (revision) => {
  const raw = original(revision);
  expect(decode(accountPreferencesDocument(resource(raw), { enabled: false }))).toBe(raw.replace('"enabled":true', '"enabled":false'));
});

function fixture() {
  const account = resource(), saved = vi.fn();
  const save = vi.fn(async (request) => ({ resource: create(ResourceSchema, { ...account, revision: account.revision + 1n, documentJson: request.documentJson }) }));
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { saveConfiguration: save });
    router.service(ResourceService, { getResource: () => ({ resource: account }) });
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBSCRIPTION_QUOTA_V1] }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (quota = false) => render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents>{quota
    ? <SubscriptionQuotaControls current={account} machine="" active accepted={saved} busyChanged={() => {}} />
    : <ConfigurationEditor kind={EntityKind.ACCOUNT} initial={account} active saved={saved} cancel={() => {}} />}</MutationIntents></TransportProvider></QueryClientProvider>);
  return { account, saved, save, view };
}

it("saves general account preferences losslessly and retries the same UUID, bigint revision and bytes", async () => {
  const f = fixture(); f.save.mockRejectedValueOnce(new ConnectError("Lost fixture acknowledgment", Code.Unavailable)); f.view();
  fireEvent.change(screen.getByLabelText("Account alias"), { target: { value: "Edited" } });
  fireEvent.click(screen.getByLabelText("Enable this account"));
  fireEvent.click(screen.getByLabelText("Exclude from automatic account selection"));
  fireEvent.click(screen.getByLabelText("Notify when account quota recovers"));
  fireEvent.click(screen.getByRole("button", { name: "Save AI account" }));
  const retry = await screen.findByRole("button", { name: "Retry the same configuration" });
  const request = f.save.mock.calls[0][0];
  expect(decode(request.documentJson)).toBe(original().replace('"Original"', '"Edited"').replace('"enabled":true', '"enabled":false').replace('"exclude_automatic":false', '"exclude_automatic":true').replace('"recovery_notifications":false', '"recovery_notifications":true'));
  expect(request.mutation).toMatchObject({ id: f.account.id, expectedRevision: 9007199254740997n });
  const bytes = request.documentJson.slice();
  f.account.documentJson.fill(32);
  fireEvent.click(retry);
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(2));
  expect(f.save.mock.calls[1][0]).toEqual(request); expect(f.save.mock.calls[1][0].documentJson).toEqual(bytes);
  await waitFor(() => expect(f.saved).toHaveBeenCalledTimes(1));
});

it("patches only quota notifications and retains their original uncertain request", async () => {
  const f = fixture(); f.save.mockRejectedValueOnce(new ConnectError("Lost fixture acknowledgment", Code.Unavailable)); f.view(true);
  const toggle = screen.getByLabelText("Notify me of observed quota recovery");
  await waitFor(() => expect((toggle as HTMLInputElement).disabled).toBe(false));
  fireEvent.click(toggle);
  const retry = await screen.findByRole("button", { name: "Retry original recovery preference" });
  const request = f.save.mock.calls[0][0];
  expect(decode(request.documentJson)).toBe(original().replace('"recovery_notifications":false', '"recovery_notifications":true'));
  expect(request.mutation).toMatchObject({ id: f.account.id, expectedRevision: 9007199254740997n });
  fireEvent.click(retry);
  await waitFor(() => expect(f.save).toHaveBeenCalledTimes(2));
  expect(f.save.mock.calls[1][0]).toEqual(request);
  await waitFor(() => expect(f.saved).toHaveBeenCalledTimes(1));
});

it("retains a rejected stale draft and does not offer an uncertain retry", async () => {
  const f = fixture(); f.save.mockRejectedValueOnce(new ConnectError("Stale fixture revision", Code.Aborted)); f.view();
  fireEvent.change(screen.getByLabelText("Account alias"), { target: { value: "Retained draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Save AI account" }));
  await screen.findByRole("alert");
  expect((screen.getByLabelText("Account alias") as HTMLInputElement).value).toBe("Retained draft");
  expect(screen.queryByRole("button", { name: "Retry the same configuration" })).toBeNull();
  expect(f.saved).not.toHaveBeenCalled();
});
