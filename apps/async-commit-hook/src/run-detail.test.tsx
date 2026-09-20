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

it.each([false, true])("displays bounded run summaries and opens details (inbox=%s)", async (inbox) => {
  const onSelect = vi.fn();
  const transport = createRouterTransport((router) => router.service(LocalService, {
    listRuns: () => ({ runs: [
      create(RunSchema, { id: "large", commit: "abcdef", state: ExecutionState.FAILED, checkCount: 4000 }),
      create(RunSchema, { id: "empty", commit: "empty", state: ExecutionState.FAILED, checkCount: 0 }),
      create(RunSchema, { id: "legacy", commit: "legacy", state: ExecutionState.FAILED, checks: [{ id: "old-check" }] }),
    ] }),
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(<QueryClientProvider client={client}><TransportProvider transport={transport}>
    <RunList repository="repo" worktree="worktree" branch="main" inbox={inbox} onSelect={onSelect} focusRun="" cursor="" setCursor={() => {}} />
  </TransportProvider></QueryClientProvider>);
  try {
    const row = await screen.findByRole("button", { name: /abcdef.*4000 checks/ });
    expect(screen.getByRole("button", { name: /empty.*0 checks/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /legacy.*1 checks/ })).toBeTruthy();
    fireEvent.click(row);
    expect(onSelect).toHaveBeenCalledExactlyOnceWith("large");
  } finally { unmount(); client.clear(); }
});

it.each([
  ExecutionState.PASSED, ExecutionState.FAILED, ExecutionState.CANCELLED,
  ExecutionState.INTERRUPTED, ExecutionState.REPLACED, ExecutionState.BLOCKED,
  ExecutionState.SKIPPED, ExecutionState.EXPIRED,
])("finishes log polling once check %s completes while other checks remain active", async (terminal) => {
  vi.useFakeTimers();
  let state = ExecutionState.RUNNING;
  const getLogs = vi.fn((request: { offset: bigint }) => ({
    text: state === ExecutionState.RUNNING ? "live output" : `final page ${request.offset}`,
    nextOffset: request.offset + 65536n, complete: request.offset > 0n,
  }));
  const transport = createRouterTransport((router) => router.service(LocalService, {
    getRun: () => ({ run: create(RunSchema, {
      id: "run", state: ExecutionState.RUNNING,
      checks: [{ id: "check", name: "Example check", state }],
    }) }), getLogs,
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount } = render(<QueryClientProvider client={client}><TransportProvider transport={transport}>
    <RunDetail id="run" onBack={() => {}} onSelect={() => {}} />
  </TransportProvider></QueryClientProvider>);
  try {
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    fireEvent.click(screen.getByRole("button", { name: "Example check" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(getLogs).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(2200); });
    expect(getLogs).toHaveBeenCalledTimes(2);
    state = terminal;
    await act(async () => { await vi.advanceTimersByTimeAsync(1600); });
    expect(getLogs).toHaveBeenCalledTimes(3);
    expect(screen.getByLabelText("Check log output").textContent).toBe("final page 0");
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(getLogs).toHaveBeenCalledTimes(3);
    fireEvent.click(screen.getByRole("button", { name: "Refresh log" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(getLogs).toHaveBeenCalledTimes(4);
    fireEvent.click(screen.getByRole("button", { name: "Next log page" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(screen.getByLabelText("Check log output").textContent).toBe("final page 65536");
    expect(getLogs).toHaveBeenCalledTimes(5);
    await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
    expect(getLogs).toHaveBeenCalledTimes(5);
  } finally { unmount(); client.clear(); vi.useRealTimers(); }
});
