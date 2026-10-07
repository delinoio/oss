// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport, Code, ConnectError } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, WorkerService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { RunnerRemediationProvider, useRunnerRemediation } from "./runner-remediation";
const machine = (name: string) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, schemaVersion: 1, revision: 9007199254740993n, documentJson: encode({ name, disabled: false, installations: [{ harness: "claude-code", state: "missing", explicit_path: "", problem: { message: "/private/native secret", guidance: "secret native instruction" } }] }) });
it("retains one inspection draft and original uncertain request across presentation close, without replacing its Runner", async () => {
  const first = machine("First Runner"), second = machine("Second Runner");
  const discover = vi.fn((_request: unknown) => { throw new ConnectError("unavailable", Code.Unavailable); });
  const transport = createRouterTransport(router => { router.service(ResourceService, { getResource: request => ({ resource: request.id === first.id ? first : second }) }); router.service(WorkerService, { discoverHarnesses: discover }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Surface() { const open = useRunnerRemediation(); return <><button onClick={() => open?.(first)}>Inspect first</button><button onClick={() => open?.(second)}>Inspect second</button><button disabled={open?.pendingFor(first.id)}>Start original workflow</button>{open?.body}</>; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active><Surface /></RunnerRemediationProvider></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByText("Inspect first"));
  await screen.findByText("First Runner");
  expect(screen.queryByText(/private\/native/)).toBeNull();
  expect(discover).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Edit executable paths" }));
  const path = screen.getByLabelText("claude-code executable path"); fireEvent.change(path, { target: { value: "/chosen/claude" } });
  fireEvent.click(screen.getByRole("button", { name: "Close Inspect installed harnesses" }));
  expect((screen.getByRole("button", { name: "Start original workflow" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByText("Inspect second")); expect(screen.queryByText("Second Runner")).toBeNull();
  fireEvent.click(screen.getByText("Inspect first"));
  await waitFor(() => expect((screen.getByLabelText("claude-code executable path") as HTMLInputElement).value).toBe("/chosen/claude"));
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(screen.getByRole("button", { name: "Run optional diagnostics" }));
  await screen.findByRole("button", { name: "Retry the same harness check" });
  expect(discover).toHaveBeenCalledOnce(); const request = discover.mock.calls[0][0] as { mutation: { expectedRevision: bigint } };
  fireEvent.click(screen.getByRole("button", { name: "Close Inspect installed harnesses" }));
  fireEvent.click(screen.getByText("Inspect first"));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same harness check" }));
  await waitFor(() => expect(discover).toHaveBeenCalledTimes(2));
  expect(discover.mock.calls[1][0]).toEqual(request);
  expect(request.mutation.expectedRevision).toBe(9007199254740993n);
  client.clear();
});
it("preserves Worker updates and Network settings in the original full Runner inspection", async () => {
  const row = machine("Settings Runner");
  const transport = createRouterTransport(router => router.service(ResourceService, { getResource: () => ({ resource: row }) }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Surface() { const open = useRunnerRemediation({ compact: false }); return <><button onClick={() => open?.(row)}>Inspect saved Runner</button>{open?.body}</>; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active><Surface /></RunnerRemediationProvider></QueryClientProvider></TransportProvider>);
  fireEvent.click(screen.getByText("Inspect saved Runner"));
  expect(await screen.findByText("Worker updates")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Network settings" })).toBeTruthy();
  client.clear();
});
it("uses one presenter and preserves the original draft after its calling task disappears", async () => {
  const row = machine("Borrowed Runner");
  const reads = vi.fn(() => ({ resource: row }));
  const transport = createRouterTransport(router => router.service(ResourceService, { getResource: reads }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Surface({ label, active = true }: { label: string; active?: boolean }) { const open = useRunnerRemediation({ active }); return <><button onClick={() => open?.(row)}>{label}</button>{open?.body}</>; }
  const view = (first: boolean, secondActive = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><RunnerRemediationProvider active>{first ? <Surface key="first" label="First caller" /> : null}<Surface key="second" label="Second caller" active={secondActive} /></RunnerRemediationProvider></QueryClientProvider></TransportProvider>;
  const rendered = render(view(true)); fireEvent.click(screen.getByText("First caller"));
  await screen.findByText("Borrowed Runner"); fireEvent.click(screen.getByRole("button", { name: "Edit executable paths" }));
  fireEvent.change(screen.getByLabelText("claude-code executable path"), { target: { value: "/original/draft" } });
  fireEvent.click(screen.getByText("Second caller"));
  await waitFor(() => expect(screen.getAllByRole("dialog")).toHaveLength(1));
  rendered.rerender(view(false));
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  expect((screen.getByLabelText("claude-code executable path") as HTMLInputElement).value).toBe("/original/draft");
  rendered.rerender(view(false, false)); expect(screen.queryByRole("dialog")).toBeNull();
  rendered.rerender(view(false, true)); expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByText("Second caller"));
  await waitFor(() => expect((screen.getByLabelText("claude-code executable path") as HTMLInputElement).value).toBe("/original/draft"));
  fireEvent.click(screen.getByRole("button", { name: "Close Inspect installed harnesses" }));
  const before = reads.mock.calls.length;
  await new Promise(resolve => setTimeout(resolve, 10)); expect(reads.mock.calls.length).toBe(before);
  fireEvent.click(screen.getByText("Second caller"));
  await waitFor(() => expect((screen.getByLabelText("claude-code executable path") as HTMLInputElement).value).toBe("/original/draft"));
  rendered.unmount(); client.clear();
});
