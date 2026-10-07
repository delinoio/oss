// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { encode } from "./documents";
import { i18n } from "./localization";
it("rechecks the original exact Runner read without changing its field, and retains focus across language changes", async () => {
  const row = create(ResourceSchema, { kind: EntityKind.MACHINE, schemaVersion: 1, id: newRequestId(), revision: 9007199254740993n, documentJson: encode({ name: "Original Runner", disabled: false }) });
  let unavailable = true;
  const get = vi.fn((request: { id: string }) => { if (unavailable) throw new ConnectError("private executable output", Code.PermissionDenied); return { resource: row }; });
  const list = vi.fn(() => ({ resources: [row] }));
  const change = vi.fn();
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: list, getResource: get }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rendered = render(<TransportProvider transport={transport}><QueryClientProvider client={client}><ResourceChoice label="Chosen Runner" kind={EntityKind.MACHINE} value={row.id} active change={change} /></QueryClientProvider></TransportProvider>);
  const retry = await screen.findByRole("button", { name: "Retry current read" });
  retry.focus(); const reads = get.mock.calls.length;
  try {
    await act(async () => { await i18n.changeLanguage("ko"); });
    expect(window.document.activeElement).toBe(retry);
    expect(get).toHaveBeenCalledTimes(reads);
    expect(screen.queryByText(/private executable output/)).toBeNull();
    unavailable = false; fireEvent.click(retry);
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Chosen Runner" }).textContent).toBe("Original Runner"));
    expect(get.mock.calls.at(-1)?.[0].id).toBe(row.id);
    expect(screen.getByRole("combobox", { name: "Chosen Runner" }).getAttribute("data-value")).toBe(row.id);
    expect(change).not.toHaveBeenCalled();
  } finally { rendered.unmount(); client.clear(); await act(async () => { await i18n.changeLanguage("en"); }); }
});
