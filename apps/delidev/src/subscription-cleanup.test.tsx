// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create, toBinary } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  CleanupFailedSubscriptionsRequestSchema, FailedSubscriptionCleanupJobSchema, FailedSubscriptionCleanupState as State,
  FailedSubscriptionCleanupOutcome as Outcome, FailedSubscriptionCleanupReason as Reason, SystemCapability, SystemService,
  SubscriptionService, ResourceService, ResourceSchema, EntityKind, newRequestId, type CleanupFailedSubscriptionsRequest,
} from "@delinoio/delidev-api-client";
import { MutationIntents } from "./mutation";
import { SettingsLifetime } from "./settings-lifetime";
import { SubscriptionAccounts } from "./subscription-accounts";
import { encode } from "./documents";

function fixture({ capable = true, lost = false, total = 3 }: { capable?: boolean; lost?: boolean; total?: number } = {}) {
  let job = create(FailedSubscriptionCleanupJobSchema, { id: newRequestId(), revision: 1n, state: total === 0 ? State.COMPLETED : State.PENDING, total });
  let unavailable = false;
  let pendingAdmission: (() => void) | undefined;
  let delayed = false;
  const row = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, schemaVersion: 2, revision: 1n, documentJson: encode({ alias: "Existing subscription", type: "subscription", subscription_service: "chatgpt", enabled: true, health: "disconnected", quota: [] }) });
  const list = vi.fn(() => ({ resources: [row] }));
  const start = vi.fn(async request => {
    if (delayed) await new Promise<void>(resolve => { pendingAdmission = resolve; });
    if (lost && start.mock.calls.length === 1) throw new ConnectError("Fixture response lost", Code.Unavailable);
    return { requestId: request.requestId, job, replayed: start.mock.calls.length > 1 };
  });
  const status = vi.fn(request => {
    if (unavailable) throw new ConnectError("Fixture status unavailable", Code.Unavailable);
    return { job, results: job.state === State.PENDING || job.total === 0 ? [] : [{ accountId: row.id, alias: "Retained subscription", outcome: Outcome.RETAINED, reason: Reason.REFERENCED }], nextPageToken: request.pageToken ? "" : "fixture-next-page" };
  });
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1, ...(capable ? [SystemCapability.FAILED_SUBSCRIPTION_CLEANUP_V1] : [])] }) });
    router.service(ResourceService, { listResources: list });
    router.service(SubscriptionService, { cleanupFailedSubscriptions: start, getFailedSubscriptionCleanup: status });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false, gcTime: 0 } } });
  const edit = vi.fn(), remove = vi.fn();
  function Harness() {
    const [visible, setVisible] = useState(true);
    return <TransportProvider transport={transport}><QueryClientProvider client={client}>
      <button onClick={() => setVisible(false)}>Leave category fixture</button><button onClick={() => setVisible(true)}>Open category fixture</button>
      {visible ? <SettingsLifetime>{() => <MutationIntents><SubscriptionAccounts active editAccount={edit} deleteAccount={remove} /></MutationIntents>}</SettingsLifetime> : null}
    </QueryClientProvider></TransportProvider>;
  }
  return { Harness, start, status, list, client, edit, remove, failStatus: (value: boolean) => { unavailable = value; }, delay: () => { delayed = true; }, release: () => pendingAdmission?.(), complete: (total = 3) => { job = create(FailedSubscriptionCleanupJobSchema, { ...job, revision: 2n, state: State.COMPLETED, total, processed: total, deleted: total === 0 ? 0 : total - 1, retained: total === 0 ? 0 : 1 }); } };
}

it("starts once without confirmation, blocks account changes and presents partial results", async () => {
  const f = fixture(); render(<f.Harness />);
  const cleanup = await screen.findByRole("button", { name: "Auto cleanup" });
  await waitFor(() => expect(cleanup.hasAttribute("disabled")).toBe(false));
  fireEvent.click(cleanup); fireEvent.click(cleanup);
  await screen.findByText("Processed 0 of 3 accounts");
  expect(f.start).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByRole("button", { name: "Cleaning up…" }).getAttribute("aria-disabled") === "true").toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "More actions for Existing subscription" }));
  expect(screen.getByRole("button", { name: "Edit preferences" }).hasAttribute("disabled")).toBe(true);
  expect(screen.getByRole("button", { name: "Delete account" }).hasAttribute("disabled")).toBe(true);
  f.complete(); await act(async () => { await f.client.invalidateQueries({ refetchType: "active" }); });
  await screen.findByText("2 deleted · 1 retained");
  fireEvent.click(screen.getByText("Retained accounts (1)"));
  expect(await screen.findByText(/The account is still referenced by saved work/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Load more Cleanup result pages" }));
  await waitFor(() => expect(f.status.mock.calls.at(-1)?.[0].pageToken).toBe("fixture-next-page"));
  expect(f.list.mock.calls.length).toBeGreaterThan(1);
  expect(f.start).toHaveBeenCalledTimes(1);
});

it("retries only identical original admission after a lost response", async () => {
  const f = fixture({ lost: true }); render(<f.Harness />);
  const cleanup = await screen.findByRole("button", { name: "Auto cleanup" });
  await waitFor(() => expect(cleanup.hasAttribute("disabled")).toBe(false));
  fireEvent.click(cleanup);
  await screen.findByRole("button", { name: "Retry original cleanup request" });
  expect(f.status).not.toHaveBeenCalled();
  expect(cleanup.getAttribute("aria-disabled")).toBe("true");
  fireEvent.click(screen.getByRole("button", { name: "Retry original cleanup request" }));
  await screen.findByText("Processed 0 of 3 accounts");
  const bytes = (index: number) => toBinary(CleanupFailedSubscriptionsRequestSchema, f.start.mock.calls[index]![0] as CleanupFailedSubscriptionsRequest);
  expect(bytes(0)).toEqual(bytes(1)); expect(f.start).toHaveBeenCalledTimes(2);
});

it("offers read-only retry after admitted status failure and resumes the original job", async () => {
  const f = fixture(); f.failStatus(true); render(<f.Harness />);
  const cleanup = await screen.findByRole("button", { name: "Auto cleanup" });
  await waitFor(() => expect(cleanup.hasAttribute("disabled")).toBe(false)); fireEvent.click(cleanup);
  await screen.findByRole("button", { name: "Retry cleanup status" });
  expect(screen.queryByRole("button", { name: "Retry original cleanup request" })).toBeNull();
  f.failStatus(false); f.complete(0); // Invalid changed totals cannot acknowledge the original job.
  fireEvent.click(screen.getByRole("button", { name: "Retry cleanup status" }));
  await screen.findByText("Cleanup status is unavailable. The accepted job continues on the server.");
  expect(screen.getByRole("button", { name: "Cleaning up…" }).getAttribute("aria-disabled") === "true").toBe(true);
  f.complete(); fireEvent.click(screen.getByRole("button", { name: "Retry cleanup status" }));
  await screen.findByText("2 deleted · 1 retained");
  expect(f.start).toHaveBeenCalledTimes(1);
});

it("disables unsupported cleanup with an explicit reason", async () => {
  const f = fixture({ capable: false }); render(<f.Harness />);
  await screen.findByText(/Update the selected server to use automatic cleanup/);
  expect(screen.getByRole("button", { name: "Auto cleanup" }).hasAttribute("disabled")).toBe(true);
  expect(f.start).not.toHaveBeenCalled(); expect(f.status).not.toHaveBeenCalled();
});

it("disposes late admission presentation on category departure without replay", async () => {
  const f = fixture(); f.delay(); render(<f.Harness />);
  const cleanup = await screen.findByRole("button", { name: "Auto cleanup" });
  await waitFor(() => expect(cleanup.hasAttribute("disabled")).toBe(false)); fireEvent.click(cleanup);
  await waitFor(() => expect(f.start).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Leave category fixture" }));
  await act(async () => { f.release(); });
  expect(f.status).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Open category fixture" }));
  await screen.findByRole("button", { name: "Auto cleanup" });
  expect(f.start).toHaveBeenCalledTimes(1); expect(screen.queryByText(/Processed .* accounts/)).toBeNull();
});

it("reports an empty accepted batch and refreshes inventory without a result disclosure", async () => {
 const f = fixture({ total: 0 }); render(<f.Harness />);
 const button = await screen.findByRole("button", { name: "Auto cleanup" });
 await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false)); fireEvent.click(button);
 await screen.findByText("No accounts to clean up.");
 expect(screen.queryByText(/Retained accounts/)).toBeNull();
 await waitFor(() => expect(f.list.mock.calls.length).toBeGreaterThan(1));
 expect(f.start).toHaveBeenCalledTimes(1);
});
