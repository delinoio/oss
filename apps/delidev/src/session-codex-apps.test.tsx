// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SessionCodexApps, sessionCodexAppsAccountId } from "./session";

vi.mock("./codex-apps", () => ({ CodexAppsPanel: ({ session, account, onClose }: { session: Resource; account: Resource; onClose: () => void }) => <div role="dialog" aria-label="Original Apps panel"><span>{session.revision.toString()}:{account.revision.toString()}</span><button onClick={onClose}>Close Apps fixture</button></div> }));

function fixture() {
  const accountId = newRequestId(), foreignId = newRequestId();
  const configuration = { harness: "codex", subscription: true, subscription_service: "chatgpt" };
  const document = { initial_execution: { initial_account_id: foreignId, configuration }, current_execution: { account_id: accountId } };
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 5n, documentJson: encode(document) });
  let account = create(ResourceSchema, { id: accountId, kind: EntityKind.ACCOUNT, schemaVersion: 2, revision: 7n, documentJson: encode({ type: "subscription", subscription_service: "chatgpt", enabled: false, removal: { state: "pending" } }) });
  const read = vi.fn((_request: { id: string }) => ({ resource: account }));
  const transport = createRouterTransport(router => router.service(ResourceService, { getResource: read }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = (source = session, supported = true, active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><SessionCodexApps session={source} supported={supported} active={active} /></QueryClientProvider></TransportProvider>;
  return { accountId, foreignId, configuration, document, session, read, client, view, replace: (value: Resource) => { account = value; }, account };
}

it("reads only on explicit opening and retains the original disabled account cleanup scope", async () => {
  const f = fixture(), rendered = render(f.view());
  expect(f.read).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Codex Apps" }));
  await screen.findByRole("dialog", { name: "Original Apps panel" });
  expect(f.read.mock.calls[0]?.[0]).toMatchObject({ id: f.accountId });
  expect(screen.getByText("5:7")).not.toBeNull();
  rendered.rerender(f.view({ ...f.session, revision: 6n }));
  expect(screen.getByText("6:7")).not.toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Close Apps fixture" }));
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("rejects a foreign account envelope and retries only the original account read", async () => {
  const f = fixture(); f.replace({ ...f.account, id: f.foreignId }); render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Codex Apps" }));
  await screen.findByText("The original Apps scope or returned metadata is unavailable. No new action is authorized.");
  expect(screen.queryByRole("dialog", { name: "Original Apps panel" })).toBeNull();
  f.replace(f.account); fireEvent.click(screen.getByRole("button", { name: "Reload saved Apps state" }));
  await screen.findByRole("dialog", { name: "Original Apps panel" });
  for (const call of f.read.mock.calls) expect(call[0]).toMatchObject({ id: f.accountId });
});

it.each(["account", "session", "capability", "activity"] as const)("disposes an opened panel after original %s changes", async change => {
  const f = fixture(), rendered = render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Codex Apps" })); await screen.findByRole("dialog", { name: "Original Apps panel" });
  const source = change === "account" ? { ...f.session, documentJson: encode({ ...f.document, current_execution: { account_id: f.foreignId } }) } : change === "session" ? { ...f.session, id: newRequestId() } : f.session;
  rendered.rerender(f.view(source, change !== "capability", change !== "activity"));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  rendered.rerender(f.view());
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(f.read).toHaveBeenCalledTimes(1);
});

it.each([
  { harness: "claude", subscription: true, subscription_service: "chatgpt" },
  { harness: "codex", subscription: false, subscription_service: "chatgpt" },
  { harness: "codex", subscription: true },
  { harness: "codex", subscription: true, subscription_service: "claude" },
])("does not infer managed Apps support from an unsupported retained profile (%j)", configuration => {
  const f = fixture(), source = { ...f.session, documentJson: encode({ ...f.document, initial_execution: { initial_account_id: f.accountId, configuration } }) };
  render(f.view(source)); expect(screen.queryByRole("button", { name: "Codex Apps" })).toBeNull(); expect(f.read).not.toHaveBeenCalled();
});

it("never borrows routing, changed accounts, malformed current references or Sidechat/Fork profiles", () => {
  const f = fixture();
  expect(sessionCodexAppsAccountId({ ...f.session, documentJson: encode({ initial_execution: { initial_account_id: f.accountId, configuration: f.configuration }, account_changes: [{ account_id: f.foreignId }] }) })).toBe(f.accountId);
  expect(sessionCodexAppsAccountId({ ...f.session, documentJson: encode({ ...f.document, current_execution: { account_id: "" } }) })).toBe("");
  expect(sessionCodexAppsAccountId({ ...f.session, documentJson: encode({ fork: { snapshot: { configuration: f.configuration } }, current_execution: { account_id: f.accountId } }) })).toBe("");
  expect(sessionCodexAppsAccountId({ ...f.session, documentJson: encode({ ...f.document, fork: { sidechat_parent_snapshot: {} } }) })).toBe("");
});
