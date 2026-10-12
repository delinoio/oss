// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, fireEvent, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { AgentManagedMCP } from "./agent-managed-mcp";

it("preserves unavailable imported bindings and permits explicit unselection", async () => {
  const change = vi.fn();
  const data = { name: "Agent", harness: "codex", managed_mcp: { selections: [{ machine_id: newRequestId(), worker_device_id: newRequestId(), definition_id: newRequestId(), unresolved: true }] } };
  const transport = createRouterTransport(router => router.service(SystemService, { getStatus: () => ({ capabilities: [] }) }));
  render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient()}><AgentManagedMCP data={data} change={change} active /></QueryClientProvider></TransportProvider>);
  expect(await screen.findByText(/Rebind required/)).toBeTruthy();expect(change).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", {name: "Remove selection"}));expect(change).toHaveBeenCalledWith({ ...data, managed_mcp: { selections: [] } });
});
