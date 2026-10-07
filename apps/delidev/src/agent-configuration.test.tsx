// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ConfigurationService, EntityKind, ResourceService, SystemService } from "@delinoio/delidev-api-client";
import { ConfigurationEditor } from "./settings";
import { MutationIntents } from "./mutation";

it("requires the current Worker wizard through every Agent editor entry", async () => {
  const save = vi.fn();
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ protocolVersion: 2, capabilities: [] }) });
    router.service(ResourceService, { listResources: () => ({ resources: [] }) });
    router.service(ConfigurationService, { saveConfiguration: save });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ConfigurationEditor kind={EntityKind.AGENT} active saved={() => {}} cancel={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  expect(await screen.findByText(/Update the server/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Save Agent Worker" })).toBeNull();
  expect(save).not.toHaveBeenCalled();
});
