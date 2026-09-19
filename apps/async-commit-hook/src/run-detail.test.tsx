import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ExecutionState, LocalService, RunSchema } from "@delinoio/async-commit-hook-api-client";
import { expect, it, vi } from "vitest";
import { RunDetail, RunList } from "./App";

it.each([
  ExecutionState.QUEUED, ExecutionState.PREPARING, ExecutionState.RUNNING,
  ExecutionState.COLLECTING, ExecutionState.UNSPECIFIED, ExecutionState.FAILED,
])("acknowledges only completed results (state %s)", async (state) => {
  const acknowledge = vi.fn(() => ({}));
  const transport = createRouterTransport((router) => router.service(LocalService, {
    getRun: () => ({ run: create(RunSchema, { id: "run", state }) }),
    acknowledge,
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(
    <QueryClientProvider client={client}>
      <TransportProvider transport={transport}>
        <RunDetail id="run" onBack={() => {}} onSelect={() => {}} />
      </TransportProvider>
    </QueryClientProvider>,
  );
  const button = await screen.findByRole("button", { name: "Acknowledge" }) as HTMLButtonElement;
  expect(button.disabled).toBe(state !== ExecutionState.FAILED);
  fireEvent.click(button);
  if (state === ExecutionState.FAILED) await waitFor(() => expect(acknowledge).toHaveBeenCalledOnce());
  else expect(acknowledge).not.toHaveBeenCalled();
  unmount();
  client.clear();
});

it.each([false, true])("distinguishes detached checks from an unfiltered inbox (%s)", async (inbox) => {
  const listRuns = vi.fn((_request: { branch: string; detached: boolean; inbox: boolean }) => ({ runs: [] }));
  const transport = createRouterTransport((router) => router.service(LocalService, { listRuns }));
  const client = new QueryClient();
  const { unmount } = render(
    <QueryClientProvider client={client}><TransportProvider transport={transport}>
      <RunList repository="repo" worktree="worktree" branch="" inbox={inbox} onSelect={() => {}} focusRun="" cursor="" setCursor={() => {}} />
    </TransportProvider></QueryClientProvider>,
  );
  await waitFor(() => expect(listRuns).toHaveBeenCalledOnce());
  expect(listRuns.mock.calls[0]?.[0]).toMatchObject({ branch: "", detached: !inbox, inbox });
  unmount(); client.clear();
});

it("stops detail polling after completion and still supports explicit invalidation", async () => {
  vi.useFakeTimers();
  const getRun = vi.fn()
    .mockReturnValueOnce({ run: create(RunSchema, { id: "run", state: ExecutionState.RUNNING }) })
    .mockReturnValue({ run: create(RunSchema, { id: "run", state: ExecutionState.PASSED }) });
  const transport = createRouterTransport((router) => router.service(LocalService, { getRun }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(
    <QueryClientProvider client={client}><TransportProvider transport={transport}>
      <RunDetail id="run" onBack={() => {}} onSelect={() => {}} />
    </TransportProvider></QueryClientProvider>,
  );
  try {
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(getRun).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(1600); });
    expect(getRun).toHaveBeenCalledTimes(2);
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(getRun).toHaveBeenCalledTimes(2);
    await act(async () => { await client.invalidateQueries(); await vi.advanceTimersByTimeAsync(1); });
    expect(getRun).toHaveBeenCalledTimes(3);
  } finally { unmount(); client.clear(); vi.useRealTimers(); }
});
