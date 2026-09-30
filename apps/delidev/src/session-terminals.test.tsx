// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, TerminalService, TerminalAction, SystemService, SystemCapability, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionTerminals, TerminalText } from "./session-terminals";

it("preserves split multibyte bytes and explicitly resets decoding after an output gap", () => {
  const text = new TerminalText();
  expect(text.append(new Uint8Array([0xe2, 0x82]))).toBe("");
  expect(text.append(new Uint8Array([0xac]))).toBe("€");
  expect(text.append(new Uint8Array([0xe2]))).toBe("€");
  expect(text.append(new TextEncoder().encode("after"), true)).toBe("€\n[Terminal output gap]\nafter");
});

it("retries the exact creation request and reattaches without another shell", async () => {
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ archive: "active" }) });
  const terminal = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, schemaVersion: 1, revision: 4n, documentJson: encode({ state: "running", machine_id: newRequestId(), shell: "/bin/sh", cwd: "/fixture" }) });
  const createTerminal = vi.fn(async (_request: unknown) => ({ terminal }));
  createTerminal.mockRejectedValueOnce(new ConnectError("acknowledgment lost", Code.Unavailable));
  const calls: { epoch: string; afterSequence: bigint }[] = [];
  const epoch = newRequestId();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SESSION_TERMINALS_V1] }) });
    router.service(ResourceService, { listResources: () => ({ resources: [terminal] }) });
    router.service(TerminalService, { createTerminal, watchTerminalOutput: async function* (request) {
      calls.push({ epoch: request.epoch, afterSequence: request.afterSequence });
      if (calls.length === 1) { yield { epoch, sequence: 1n, data: new Uint8Array([...new TextEncoder().encode("original shell output"), 0xe2, 0x82]), terminal }; throw new ConnectError("disconnected", Code.Unavailable); }
      if (calls.length === 2) { yield { epoch, sequence: 1n, heartbeat: true, terminal }; return; }
      const exited = create(ResourceSchema, { ...terminal, revision: 5n, documentJson: encode({ state: "exited", cleanup_verified: true }) });
      yield { epoch, sequence: 2n, data: new Uint8Array([0xac]), terminal: exited };
    } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} close={() => {}} /></MutationIntents></TransportProvider></QueryClientProvider>);
  await waitFor(() => expect((screen.getByRole("button", { name: "Create terminal" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Create terminal" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry the same terminal creation" }));
  await screen.findByText("original shell output");
  fireEvent.click(await screen.findByRole("button", { name: "Reattach original terminal" }));
  await waitFor(() => expect(calls).toHaveLength(2));
  expect(calls[1]).toEqual({ epoch, afterSequence: 1n });
  fireEvent.click(await screen.findByRole("button", { name: "Reattach original terminal" }));
  await screen.findByText("original shell output€");
  expect(calls[2]).toEqual({ epoch, afterSequence: 1n });
  expect(createTerminal).toHaveBeenCalledTimes(2);
  expect(createTerminal.mock.calls[0]![0]).toEqual(createTerminal.mock.calls[1]![0]);
  view.unmount(); client.clear();
});


it("focuses the attached terminal input and sends exact UTF-8 and native resize controls", async () => {
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ archive: "active" }) });
  const terminal = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, schemaVersion: 1, revision: 4n, documentJson: encode({ state: "running", machine_id: newRequestId() }) });
  const controlTerminal = vi.fn(async (_request: unknown) => ({ terminal }));
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SESSION_TERMINALS_V1] }) });
    router.service(ResourceService, { listResources: () => ({ resources: [terminal] }) });
    router.service(TerminalService, { controlTerminal, watchTerminalOutput: async function* () {
      yield { epoch: newRequestId(), sequence: 0n, heartbeat: true, terminal };
      await held;
    } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} close={() => {}} /></MutationIntents></TransportProvider></QueryClientProvider>);
  try {
    fireEvent.click(await screen.findByRole("button", { name: /Terminal 1/ }));
    const input = await screen.findByRole("textbox", { name: "Terminal input" });
    await waitFor(() => expect(document.activeElement).toBe(input));
    fireEvent.change(input, { target: { value: "€" } });
    fireEvent.click(screen.getByRole("button", { name: "Send line" }));
    await waitFor(() => expect(controlTerminal).toHaveBeenCalledTimes(1));
    const sent = controlTerminal.mock.calls[0]![0] as { input: Uint8Array };
    expect(sent).toMatchObject({ mutation: { id: terminal.id, expectedRevision: 4n }, action: TerminalAction.INPUT });
    expect(Array.from(sent.input)).toEqual([0xe2, 0x82, 0xac, 13]);
    await waitFor(() => expect((screen.getByRole("button", { name: "Resize terminal" }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.change(screen.getByRole("spinbutton", { name: "Terminal rows" }), { target: { value: "37" } });
    fireEvent.change(screen.getByRole("spinbutton", { name: "Terminal columns" }), { target: { value: "91" } });
    fireEvent.click(screen.getByRole("button", { name: "Resize terminal" }));
    await waitFor(() => expect(controlTerminal).toHaveBeenCalledTimes(2));
    const resized = controlTerminal.mock.calls[1]![0] as { input: Uint8Array };
    expect(resized).toMatchObject({ action: TerminalAction.RESIZE, rows: 37, columns: 91 });
    expect(Array.from(resized.input)).toEqual([]);
  } finally {
    view.unmount(); release(); client.clear();
  }
});

it("attaches to the accepted creation beyond the first full history page", async () => {
  const session = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.SESSION, schemaVersion: 1, revision: 7n, documentJson: encode({ archive: "active" }) });
  const history = Array.from({ length: 50 }, () => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, schemaVersion: 1, revision: 2n, documentJson: encode({ state: "closed", cleanup_verified: true }) }));
  const terminal = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.TERMINAL, schemaVersion: 1, revision: 4n, documentJson: encode({ state: "running" }) });
  const createTerminal = vi.fn(() => ({ terminal }));
  const controlTerminal = vi.fn((_request: unknown) => ({ terminal }));
  const watched: string[] = [];
  const listResources = vi.fn(() => ({ resources: history, nextPageToken: "older-page" }));
  let release = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SESSION_TERMINALS_V1] }) });
    router.service(ResourceService, { listResources });
    router.service(TerminalService, { createTerminal, controlTerminal, watchTerminalOutput: async function* (request) {
      watched.push(request.terminalId);
      yield { epoch: newRequestId(), sequence: 1n, data: new TextEncoder().encode("new original terminal"), terminal };
      await held;
    } });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} close={() => {}} /></MutationIntents></TransportProvider></QueryClientProvider>);
  try {
    await screen.findByRole("button", { name: /Terminal 50/ });
    await waitFor(() => expect((screen.getByRole("button", { name: "Create terminal" }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "Create terminal" }));
    await screen.findByText("new original terminal");
    expect(watched).toEqual([terminal.id]);
    expect(createTerminal).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(listResources.mock.calls.length).toBeGreaterThan(1));
    fireEvent.change(screen.getByRole("textbox", { name: "Terminal input" }), { target: { value: "original" } });
    fireEvent.click(screen.getByRole("button", { name: "Send line" }));
    await waitFor(() => expect(controlTerminal).toHaveBeenCalledTimes(1));
    expect(controlTerminal.mock.calls[0]![0]).toMatchObject({ mutation: { id: terminal.id, expectedRevision: 4n }, action: TerminalAction.INPUT });
    expect(screen.getByText("new original terminal")).toBeTruthy();
  } finally {
    view.unmount(); release(); client.clear();
  }
});
