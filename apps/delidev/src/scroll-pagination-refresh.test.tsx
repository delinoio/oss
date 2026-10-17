// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ResourceQuery, ResourceService } from "@delinoio/delidev-api-client";
import { usePaginationRefresh } from "./scroll-pagination-query";

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function Owner({ background }: { background?: boolean }) {
  usePaginationRefresh(
    ResourceQuery.listResources,
    {},
    true,
    refresh,
    1000,
    background,
  );
  return null;
}
const refresh = vi.fn();
function mount(background?: boolean) {
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {});
  });
  return render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <Owner background={background} />
      </QueryClientProvider>
    </TransportProvider>,
  );
}
async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

it("pauses its interval in a hidden document by default", async () => {
  refresh.mockClear();
  mount();
  await advance(10000);
  expect(refresh).not.toHaveBeenCalled();
});

it("keeps its interval running in a hidden document only when the caller opts in", async () => {
  refresh.mockClear();
  mount(true);
  await advance(10000);
  expect(refresh.mock.calls.length).toBeGreaterThanOrEqual(5);
});
