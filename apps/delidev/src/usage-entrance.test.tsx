// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  GetUsageSummaryResponseSchema,
  ResourceService,
  SystemService,
  TokenPricingMode,
  UsageCostState,
  UsageCoverage,
  UsageService,
  newRequestId,
  type GetUsageSummaryRequest,
} from "@delinoio/delidev-api-client";
import {
  dispatchAnimation,
  reducedMotion,
  setAnimation,
} from "./entrance-test-support";
import { Usage } from "./usage";

function fixture() {
  const model = newRequestId();
  const provider = newRequestId();
  const count = {
    knownTotal: "10",
    measuredResponses: 1,
    unavailableResponses: 0,
  };
  const totals = {
    responses: 1,
    total: count,
    input: count,
    output: count,
    cachedInput: count,
    cacheWriteInput: count,
    reasoningOutput: count,
  };
  const data = create(GetUsageSummaryResponseSchema, {
    fromUnixMs: BigInt(Date.UTC(2026, 8, 1)),
    untilUnixMs: BigInt(Date.UTC(2026, 8, 25)),
    totals,
    coverage: UsageCoverage.OBSERVED_ROOT_RESPONSES,
    actualCost: UsageCostState.UNAVAILABLE,
    estimatedCost: UsageCostState.UNAVAILABLE,
    groups: [
      {
        sessionId: newRequestId(),
        sessionName: "Entrance session",
        model: { nativeId: model, providerId: provider },
        modelName: "Entrance model",
        providerId: provider,
        providerName: "Entrance API",
        totals,
      },
    ],
  });
  const read = vi.fn(async (_request: GetUsageSummaryRequest) => data);
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({ version: "0.1.0", protocolVersion: 1 }),
    });
    router.service(UsageService, {
      getUsageSummary: read,
      getTokenPricing: () => ({
        providerRevision: 0n,
        policy: { mode: TokenPricingMode.AUTOMATIC },
        reference: { state: "unavailable" },
      }),
      listTokenPricing: () => ({ models: [] }),
    });
    router.service(ResourceService, {
      getResource: () => ({}),
      listResources: (request) => ({
        resources: request.filter?.kind === EntityKind.ACCOUNT ? [] : [],
      }),
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  const view = (active = true) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <Usage active={active} open={vi.fn()} />
      </QueryClientProvider>
    </TransportProvider>
  );
  return { read, view, client };
}

const page = () => document.querySelector<HTMLElement>("section.usage-page")!;
const content = () =>
  document.querySelector<HTMLElement>(".usage-page > .usage-content")!;
const tabStrip = () => screen.getByRole("tablist");
const pageEntering = () => page().getAttribute("data-entrance");
const contentEntering = () => content().getAttribute("data-entrance");

beforeEach(() => {
  reducedMotion(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("plays one Y4 entrance per destination activation and never for refresh, polling or data updates", async () => {
  const f = fixture();
  const view = render(f.view(false));
  expect(pageEntering()).toBeNull();
  view.rerender(f.view(true));
  await screen.findByText("Incomplete coverage");
  expect(pageEntering()).toBe("y4");
  expect(content().hasAttribute("data-entrance")).toBe(false);
  expect(page().hasAttribute("style")).toBe(false);
  dispatchAnimation(page());
  expect(pageEntering()).toBeNull();

  // Refresh, refetch and range/source edits render immediately with no entrance.
  const reads = f.read.mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(f.read.mock.calls.length).toBeGreaterThan(reads));
  await act(async () => {
    await f.client.invalidateQueries();
  });
  expect(pageEntering()).toBeNull();
  expect(contentEntering()).toBeNull();

  // Leaving and returning replays the page entrance exactly once.
  view.rerender(f.view(false));
  expect(pageEntering()).toBeNull();
  view.rerender(f.view(true));
  expect(pageEntering()).toBe("y4");
  dispatchAnimation(page());
  expect(pageEntering()).toBeNull();
});

it("plays one Y4 entrance on the content for explicit tab selection and never moves the tab strip", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByText("Incomplete coverage");
  dispatchAnimation(page());
  const strip = tabStrip();

  // Click, arrow, Home and End each select a different tab and play once.
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  expect(contentEntering()).toBe("y4");
  dispatchAnimation(content());
  expect(contentEntering()).toBeNull();
  const history = screen.getByRole("tab", { name: "Usage history" });
  fireEvent.keyDown(history, { key: "ArrowRight" });
  expect(screen.getByRole("tab", { name: "Model prices" })).toBeTruthy();
  expect(contentEntering()).toBe("y4");
  dispatchAnimation(content());
  fireEvent.keyDown(screen.getByRole("tab", { name: "Model prices" }), {
    key: "Home",
  });
  expect(contentEntering()).toBe("y4");
  dispatchAnimation(content());
  fireEvent.keyDown(screen.getByRole("tab", { name: "Overview" }), {
    key: "End",
  });
  expect(contentEntering()).toBe("y4");
  dispatchAnimation(content());

  // Reselecting the current tab, or refreshing, never replays.
  fireEvent.click(screen.getByRole("tab", { name: "Model prices" }));
  dispatchAnimation(content());
  fireEvent.click(screen.getByRole("tab", { name: "Model prices" }));
  expect(contentEntering()).toBeNull();
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  dispatchAnimation(content());
  await act(async () => {
    await f.client.invalidateQueries();
  });
  expect(contentEntering()).toBeNull();

  // The strip and the page itself carry no transform for a tab selection.
  expect(tabStrip()).toBe(strip);
  expect(strip.closest("[data-entrance]")).toBeNull();
  expect(pageEntering()).toBeNull();
});

it("plays the price-detail navigation to Prices once and keeps one entrance active at a time", async () => {
  const f = fixture();
  render(f.view());
  await screen.findByText("Incomplete coverage");
  // The page entrance is still running when the explicit tab selection happens.
  expect(pageEntering()).toBe("y4");
  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  expect(pageEntering()).toBeNull();
  expect(contentEntering()).toBe("y4");
  dispatchAnimation(content());
  const table = screen.getByRole("table");
  fireEvent.click(within(table).getByRole("button", { name: "Entrance model" }));
  expect(
    screen.getByRole("tab", { name: "Model prices" }).getAttribute("aria-selected"),
  ).toBe("true");
  expect(contentEntering()).toBe("y4");
  expect(pageEntering()).toBeNull();
  dispatchAnimation(content());
  expect(contentEntering()).toBeNull();
});

it("suppresses Usage motion under Animation Off and reduced motion and never replays on re-enable", async () => {
  const setReduced = reducedMotion(false);
  const f = fixture();
  const view = render(f.view());
  await screen.findByText("Incomplete coverage");
  expect(pageEntering()).toBe("y4");
  await setAnimation("false");
  expect(pageEntering()).toBeNull();
  await setAnimation(undefined);
  expect(pageEntering()).toBeNull();

  fireEvent.click(screen.getByRole("tab", { name: "Usage history" }));
  expect(contentEntering()).toBe("y4");
  setReduced(true);
  expect(contentEntering()).toBeNull();
  setReduced(false);
  expect(contentEntering()).toBeNull();

  setReduced(true);
  fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
  view.rerender(f.view(false));
  view.rerender(f.view(true));
  expect(pageEntering()).toBeNull();
  expect(contentEntering()).toBeNull();
});
