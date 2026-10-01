import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { BrowserService, ConfigurationService, EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { ConfigurationDeletion } from "./configuration-actions";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";

function fixture(type: "subscription" | "api") {
  const initial = create(ResourceSchema, {
    id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 3n, schemaVersion: 1,
    documentJson: encode({ alias: "Original fixture", type }),
  });
  const remove = vi.fn(async (_request: unknown) => ({}));
  const cleanup = vi.fn(async (_request: unknown) => ({ pending: 2, removed: 1 }));
  const deleted = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, { deleteConfiguration: remove });
    router.service(BrowserService, { getAccountBrowserCleanup: cleanup });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function View() {
    return <TransportProvider transport={transport}><QueryClientProvider client={client}>
      <MutationIntents><ConfigurationDeletion initial={initial} deleted={deleted} close={() => {}} /></MutationIntents>
    </QueryClientProvider></TransportProvider>;
  }
  return { initial, remove, cleanup, deleted, View };
}

it("keeps offline cleanup progress separate from the accepted configuration deletion", async () => {
  const f = fixture("subscription");
  render(<f.View />);
  expect(f.cleanup).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm configuration deletion" }));
  await screen.findByText("2 profile cleanup obligations pending · 1 confirmed removed");
  expect(f.remove).toHaveBeenCalledTimes(1);
  expect(f.remove.mock.calls[0][0]).toMatchObject({ kind: EntityKind.ACCOUNT, mutation: { id: f.initial.id, expectedRevision: 3n } });
  expect(f.cleanup.mock.calls[0][0]).toMatchObject({ accountId: f.initial.id });
  expect(f.deleted).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Refresh cleanup status" }));
  await waitFor(() => expect(f.cleanup).toHaveBeenCalledTimes(2));
  expect(f.remove).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Return to accounts" }));
  expect(f.deleted).toHaveBeenCalledTimes(1);
});

it("reports unavailable cleanup without replaying deletion and preserves API entry terminology", async () => {
  const f = fixture("api");
  f.cleanup.mockRejectedValue(new ConnectError("Cleanup observation unavailable", Code.Unavailable));
  render(<f.View />);
  expect(screen.getByRole("heading", { name: "Delete entry?", level: 1 })).toBeTruthy();
  expect(screen.getByText(/Disconnect the entry/)).toBeTruthy();
  expect(screen.getByText(/Browser profile cleanup remains pending/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Confirm configuration deletion" }));
  await screen.findByRole("heading", { name: "API key entry deleted", level: 1 });
  await screen.findByText("Cleanup status is unavailable until the owning server can be read.");
  expect(screen.getByRole("button", { name: "Return to AI API Keys" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Refresh cleanup status" }));
  await waitFor(() => expect(f.cleanup).toHaveBeenCalledTimes(2));
  expect(f.remove).toHaveBeenCalledTimes(1);
  expect(f.deleted).not.toHaveBeenCalled();
});
