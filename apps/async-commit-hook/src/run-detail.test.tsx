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

it.each(["Rerun all checks", "Rerun failed checks"])("retains the accepted receipt when %s cannot start", async (buttonName) => {
  const rerun = vi.fn(() => ({ runId: "accepted-run", startupDiagnostic: { code: "startup-failed", message: "The rerun was accepted, but the runner could not start.", hint: "Inspect ach doctor." } }));
  const onSelect = vi.fn();
  const transport = createRouterTransport((router) => router.service(LocalService, {
    getRun: () => ({ run: create(RunSchema, { id: "original", state: ExecutionState.FAILED }) }), rerun,
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(<QueryClientProvider client={client}><TransportProvider transport={transport}>
    <RunDetail id="original" onBack={() => {}} onSelect={onSelect} />
  </TransportProvider></QueryClientProvider>);
  fireEvent.click(await screen.findByRole("button", { name: buttonName }));
  expect(await screen.findByText("Rerun accepted; startup needs attention")).toBeTruthy();
  expect(screen.getByText("accepted-run")).toBeTruthy();
  expect(screen.getByText("ach status --run accepted-run")).toBeTruthy();
  expect(screen.getByText("Inspect ach doctor.")).toBeTruthy();
  expect(onSelect).not.toHaveBeenCalled();
  for (const name of ["Rerun all checks", "Rerun failed checks"]) {
    const button = screen.getByRole("button", { name }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    fireEvent.click(button);
  }
  expect(rerun).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByRole("button", { name: "Open accepted execution" }));
  expect(onSelect).toHaveBeenCalledExactlyOnceWith("accepted-run");
  unmount(); client.clear();
});

it("opens an accepted rerun directly when startup succeeds", async () => {
  const onSelect = vi.fn();
  const transport = createRouterTransport((router) => router.service(LocalService, {
    getRun: () => ({ run: create(RunSchema, { id: "original", state: ExecutionState.FAILED }) }),
    rerun: () => ({ runId: "accepted-run" }),
  }));
  const client = new QueryClient();
  const { unmount } = render(<QueryClientProvider client={client}><TransportProvider transport={transport}>
    <RunDetail id="original" onBack={() => {}} onSelect={onSelect} />
  </TransportProvider></QueryClientProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "Rerun all checks" }));
  await waitFor(() => expect(onSelect).toHaveBeenCalledExactlyOnceWith("accepted-run"));
  expect(screen.queryByText("Rerun accepted; startup needs attention")).toBeNull();
  unmount(); client.clear();
});
