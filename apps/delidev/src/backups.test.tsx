import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { Backups } from "./backups";
import { MutationIntents } from "./mutation";

function fixture() {
  const id = newRequestId();
  const backup = { id, sizeBytes: 9007199254740993n, modifiedAt: "2026-09-29T00:00:00Z" };
  const list = vi.fn(async () => ({ backups: [backup] }));
  const inspect = vi.fn(async () => ({ backup, sha256: "a".repeat(64), schemaVersion: 20, serverId: newRequestId() }));
  const create = vi.fn(async (_input: unknown) => ({ id, requestId: newRequestId(), replayed: false }));
  const transport = createRouterTransport(router => router.service(SystemService, { listBackups: list, inspectBackup: inspect, createBackup: create }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Backups active={active} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { id, list, inspect, create, view };
}

it("lists metadata without inspecting automatically and preserves exact byte counts", async () => {
  const f = fixture();
  const view = render(f.view(false));
  expect(f.list).not.toHaveBeenCalled();
  view.rerender(f.view());
  await screen.findByText(/9007199254740993 bytes/);
  expect(f.inspect).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: `Inspect backup ${f.id}` }));
  await screen.findByText("Database integrity and original server identity verified.");
  f.inspect.mockRejectedValueOnce(new ConnectError("Backup changed", Code.FailedPrecondition));
  fireEvent.click(screen.getByRole("button", { name: "Recheck selected backup" }));
  await screen.findByRole("alert");
  expect(f.inspect).toHaveBeenCalledTimes(2);
  expect(screen.queryByText("Database integrity and original server identity verified.")).toBeNull();
});

it("retries an uncertain creation with its original request after hiding settings", async () => {
  const f = fixture();
  f.create.mockRejectedValueOnce(new ConnectError("Acknowledgement lost", Code.Unavailable));
  const view = render(f.view());
  fireEvent.click(screen.getByRole("button", { name: "Create database backup" }));
  await screen.findByRole("button", { name: "Retry the same backup creation" });
  view.rerender(f.view(false));
  view.rerender(f.view());
  expect((screen.getByRole("button", { name: "Create database backup" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Retry the same backup creation" }));
  await waitFor(() => expect(f.create).toHaveBeenCalledTimes(2));
  expect(f.create.mock.calls[0]![0]).toEqual(f.create.mock.calls[1]![0]);
  await screen.findByText(`Backup created: ${f.id}`);
});
