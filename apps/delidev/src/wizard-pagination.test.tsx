// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SourceKind } from "./worker-source";
import { useWizardAccounts } from "./wizard-pagination";

it("bounds wizard account payloads and rejects a later foreign-source page atomically", async () => {
  const source = { kind: SourceKind.Api, id: newRequestId() };
  const models = Array.from({ length: 5 }, (_, index) => create(ResourceSchema, { id: newRequestId(), revision: 1n, kind: EntityKind.ACCOUNT, schemaVersion: 1, documentJson: encode({ provider_id: source.id, type:"api", alias: `account-${index}` }) }));
  const read = vi.fn(async (request: { pageToken: string }) => { const index = Number(request.pageToken || 0); return { resources: [index < 4 ? models[index] : { ...models[index], documentJson: encode({ provider_id: newRequestId(),type:"api" }) }], nextPageToken: index < 4 ? String(index + 1) : "" }; });
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: request => read({pageToken:request.filter?.pageToken??""}) }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function View() { const query = useWizardAccounts(source, 0, true); return <><output>{query.rows.length}:{query.payloadPages.length}:{query.error ? "failed" : "ready"}</output><button onClick={query.append}>Append</button><button onClick={() => query.restore("")}>Restore</button><button onClick={query.retry}>Retry</button></>; }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><View /></QueryClientProvider></TransportProvider>);
  await screen.findByText("1:1:ready");
  for (let index = 2; index <= 4; index++) { fireEvent.click(screen.getByText("Append")); await screen.findByText(`${index}:${Math.min(index, 3)}:ready`); }
  fireEvent.click(screen.getByText("Restore")); await waitFor(() => expect(read).toHaveBeenCalledTimes(5));
  expect(read.mock.calls.at(-1)?.[0].pageToken).toBe("");
  fireEvent.click(screen.getByText("Append")); await screen.findByText("4:3:failed");
  fireEvent.click(screen.getByText("Append")); expect(read).toHaveBeenCalledTimes(6);
  fireEvent.click(screen.getByText("Retry")); await waitFor(() => expect(read).toHaveBeenCalledTimes(7));
  expect(read.mock.calls.slice(-2).map(([request]) => request.pageToken)).toEqual(["4", "4"]);
  expect(client.getQueryCache().getAll().every(query => query.state.data === null)).toBe(true);
});
