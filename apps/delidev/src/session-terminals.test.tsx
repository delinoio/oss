import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, TerminalService, SystemService, SystemCapability, newRequestId } from "@delinoio/delidev-api-client";
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
