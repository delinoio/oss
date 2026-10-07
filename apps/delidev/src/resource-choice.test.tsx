// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { expect, it } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, newRequestId } from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { encode } from "./documents";

it("applies a delayed exact selection to the current sibling form draft", async () => {
  const row = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, revision: 1n, schemaVersion: 1, documentJson: encode({ name: "Runner" }) });
  let release!: () => void;
  const gate = new Promise<void>(resolve => { release = resolve; });
  let reads = 0;
  const transport = createRouterTransport(router => router.service(ResourceService, {
    listResources: () => ({ resources: [row] }), getResource: async () => { reads++; await gate; return { resource: row }; },
  }));
  function Form() {
    const [draft, setDraft] = useState({ alias: "Original", machine: "" });
    return <><input aria-label="Alias" value={draft.alias} onChange={event => setDraft({ ...draft, alias: event.target.value })} />
      <ResourceChoice label="Runner" kind={EntityKind.MACHINE} value={draft.machine} active change={machine => setDraft({ ...draft, machine })} />
      <output>{draft.alias}:{draft.machine}</output></>;
  }
  render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Form /></QueryClientProvider></TransportProvider>);
  const control = screen.getByRole("combobox", { name: "Runner" }); fireEvent.click(control);
  fireEvent.click(await screen.findByRole("option", { name: "Runner" }));
  await waitFor(() => expect(reads).toBe(1));
  fireEvent.change(screen.getByLabelText("Alias"), { target: { value: "Edited during read" } });
  await act(async () => release());
  await waitFor(() => expect(screen.getByRole("status").textContent).toBe(`Edited during read:${row.id}`));
});
