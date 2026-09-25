import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, GetUsageSummaryResponseSchema, ResourceSchema, ResourceService, UsageCostState, UsageCoverage, UsageService, newRequestId, type GetUsageSummaryRequest } from "@delinoio/delidev-api-client";
import { Usage } from "./usage";
import { encode } from "./documents";

function fixture() {
  const ids = { session: newRequestId(), account: newRequestId(), model: newRequestId(), provider: newRequestId(), project: newRequestId() };
  const count = { knownTotal: "18446744073709551614", measuredResponses: 2, unavailableResponses: 1 };
  const totals = { responses: 3, total: count, input: { knownTotal: "0", measuredResponses: 2, unavailableResponses: 1 }, output: count, cachedInput: { knownTotal: "", measuredResponses: 0, unavailableResponses: 3 }, cacheWriteInput: { knownTotal: "", measuredResponses: 0, unavailableResponses: 3 }, reasoningOutput: { knownTotal: "0", measuredResponses: 3, unavailableResponses: 0 } };
  const data = create(GetUsageSummaryResponseSchema, { fromUnixMs: BigInt(Date.UTC(2026, 8, 1)), untilUnixMs: BigInt(Date.UTC(2026, 8, 25)), totals, coverage: UsageCoverage.OBSERVED_ROOT_RESPONSES, actualCost: UsageCostState.UNAVAILABLE, estimatedCost: UsageCostState.UNAVAILABLE, acceptedExecutionsWithoutResponse: 2, groups: [{ sessionId: ids.session, sessionName: "Retained session", accountId: ids.account, accountName: "Original account", modelId: ids.model, modelName: "Original model", providerId: ids.provider, providerName: "Original API", totals }] });
  const read = vi.fn(async (_request: GetUsageSummaryRequest) => data);
  const transport = createRouterTransport((router) => {
    router.service(UsageService, { getUsageSummary: read });
    router.service(ResourceService, { listResources: (request) => ({ resources: request.filter?.kind === EntityKind.ACCOUNT ? [create(ResourceSchema, { id: ids.account, kind: EntityKind.ACCOUNT, revision: 1n, documentJson: encode({ alias: "Original account" }) })] : [] }) });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const open = vi.fn();
  const view = (active = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><Usage active={active} open={open} /></QueryClientProvider></TransportProvider>;
  return { ids, data, read, open, view };
}

it("shows exact known subtotals, missing fields and separate unavailable costs", async () => {
  const f = fixture(); render(f.view());
  await screen.findByText("Known subtotals · incomplete coverage");
  expect(screen.getAllByText(BigInt("18446744073709551614").toLocaleString()).length).toBeGreaterThan(0);
  expect(screen.getAllByText("Unavailable").length).toBeGreaterThan(0);
  expect(screen.getByText(/2 executions accepted/)).toBeTruthy();
  expect(screen.getByText("Actual API cost:").parentElement!.textContent).toContain("Unavailable");
  expect(screen.getByText("Token-price estimate:").parentElement!.textContent).toContain("Unavailable");
  const table = screen.getByRole("table");
  expect(within(table).getByText("General Chat")).toBeTruthy();
  expect(within(table).getByText(f.ids.account)).toBeTruthy();
  expect(within(table).getByText("Original model")).toBeTruthy();
  fireEvent.click(within(table).getByRole("button", { name: "Retained session" }));
  expect(f.open).toHaveBeenCalledWith(f.ids.session);
  expect(f.read.mock.calls[0][0].fromUnixMs).toBe(0n);
});

it("applies filters explicitly and preserves a draft across navigation", async () => {
  const f = fixture(); const view = render(f.view());
  await screen.findByText("Known subtotals · incomplete coverage");
  fireEvent.change(screen.getByRole("combobox", { name: "Usage account" }), { target: { value: f.ids.account } });
  fireEvent.click(screen.getByRole("checkbox", { name: "General Chat only" }));
  expect((screen.getByRole("combobox", { name: "Usage project" }) as HTMLSelectElement).disabled).toBe(true);
  expect(f.read).toHaveBeenCalledTimes(1);
  view.rerender(f.view(false)); view.rerender(f.view());
  expect((screen.getByRole("combobox", { name: "Usage account" }) as HTMLSelectElement).value).toBe(f.ids.account);
  fireEvent.click(screen.getByRole("button", { name: "Apply usage filters" }));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(f.read.mock.calls[1][0]).toMatchObject({ accountId: f.ids.account, generalChat: true, projectId: "" });
  fireEvent.change(screen.getByLabelText("From (local time)"), { target: { value: "2026-09-25T10:00" } });
  fireEvent.change(screen.getByLabelText("Until (local time, exclusive)"), { target: { value: "2026-09-24T10:00" } });
  fireEvent.click(screen.getByRole("button", { name: "Apply usage filters" }));
  await screen.findByRole("alert"); expect(f.read).toHaveBeenCalledTimes(2);
});

it("does not invent zero for empty telemetry and marks retained data stale after a failed refresh", async () => {
  const f = fixture(); f.data.groups = []; f.data.totals = undefined;
  render(f.view());
  await screen.findByText(/No exact response usage is recorded/);
  expect(screen.getAllByText("Unavailable").length).toBe(6);
  f.read.mockRejectedValueOnce(new ConnectError("Fixture unavailable", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Refresh usage" }));
  await screen.findByText(/These are the last successfully retrieved values/);
  expect(f.read).toHaveBeenCalledTimes(2);
});
