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
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  GetUsageSummaryResponseSchema,
  ResourceSchema,
  UsageAccountingProfile,
  UsageCoverage,
  UsageService,
  newRequestId,
  type GetUsageSummaryRequest,
} from "@delinoio/delidev-api-client";
import { SessionLifetimeUsage } from "./session-lifetime-usage";
import {
  readLifetimeUsage,
  sessionCreatedAt,
  usageWindowMs,
  type UsageRange,
} from "./session-lifetime-usage-model";
import { SessionActivityProvider } from "./session-activity";
import { i18n } from "./localization";

const until = BigInt(Date.parse("2026-10-11T00:00:00Z"));
function summary(
  range = { fromUnixMs: until - usageWindowMs, untilUnixMs: until },
  input = "9007199254740993",
  output = "0",
) {
  return create(GetUsageSummaryResponseSchema, {
    ...range,
    accountingProfile: UsageAccountingProfile.UNSPECIFIED,
    coverage: UsageCoverage.OBSERVED_ROOT_RESPONSES,
    acceptedExecutionsWithoutResponse: 3,
    acceptedCompactionsWithoutResponse: 2,
    totals: {
      responses: 2,
      input: {
        knownTotal: input,
        measuredResponses: 1,
        unavailableResponses: 1,
      },
      output: {
        knownTotal: output,
        measuredResponses: 2,
        unavailableResponses: 0,
      },
      cachedInput: { knownTotal: "100000", measuredResponses: 2 },
      reasoningOutput: { knownTotal: "100000", measuredResponses: 2 },
    },
  });
}
it("pins the server upper bound and reads sequential disjoint ranges back to creation with exact arithmetic", async () => {
  const createdAt = until - usageWindowMs * 2n - 10n;
  let inFlight = 0;
  const read = vi.fn(async (range?: UsageRange) => {
    expect(inFlight++).toBe(0);
    await Promise.resolve();
    inFlight--;
    return summary(range);
  });
  const result = await readLifetimeUsage(
    createdAt,
    read,
    new AbortController().signal,
  );
  expect(read.mock.calls.map(([range]) => range)).toEqual([
    undefined,
    {
      fromUnixMs: until - usageWindowMs * 2n,
      untilUnixMs: until - usageWindowMs,
    },
    { fromUnixMs: createdAt, untilUnixMs: until - usageWindowMs * 2n },
  ]);
  expect(result.input).toEqual({
    known: 27021597764222979n,
    measured: 3n,
    unavailable: 3n,
  });
  expect(result.output).toEqual({ known: 0n, measured: 6n, unavailable: 0n });
  expect(result.ranges.map((range) => range.missingExecutions)).toEqual([
    3, 3, 3,
  ]);
  expect(result).not.toHaveProperty("missingExecutions");
});
it("does not publish partial work after a later failure or cancellation", async () => {
  const controller = new AbortController();
  const read = vi
    .fn()
    .mockResolvedValueOnce(summary())
    .mockRejectedValueOnce(new Error("range unavailable"));
  await expect(
    readLifetimeUsage(until - usageWindowMs * 2n, read, controller.signal),
  ).rejects.toThrow("range unavailable");
  const cancel = vi.fn(async () => {
    controller.abort();
    return summary();
  });
  await expect(
    readLifetimeUsage(until - usageWindowMs, cancel, controller.signal),
  ).rejects.toThrow();
  expect(cancel).toHaveBeenCalledTimes(1);
});
it.each([
  "profile",
  "bounds",
  "counts",
  "negative",
  "leading-zero",
  "missing",
  "coverage",
])("rejects inconsistent %s evidence", async (fault) => {
  const reply = summary();
  if (fault === "profile")
    reply.accountingProfile = UsageAccountingProfile.NATIVE_UNITS_V1;
  if (fault === "bounds") reply.untilUnixMs = reply.fromUnixMs;
  if (fault === "counts") reply.totals!.input!.measuredResponses = 2;
  if (fault === "negative") reply.totals!.input!.knownTotal = "-1";
  if (fault === "leading-zero") reply.totals!.input!.knownTotal = "01";
  if (fault === "missing") reply.totals!.input = undefined;
  if (fault === "coverage")
    reply.coverage = UsageCoverage.OBSERVED_ROOT_ACCOUNTING_UNITS;
  await expect(
    readLifetimeUsage(
      until - usageWindowMs,
      async () => reply,
      new AbortController().signal,
    ),
  ).rejects.toThrow();
});
it("keeps empty measures unavailable and rejects changed echoed older bounds", async () => {
  const empty = summary();
  empty.totals = {
    ...empty.totals!,
    responses: 0,
    input: {
      ...empty.totals!.input!,
      knownTotal: "",
      measuredResponses: 0,
      unavailableResponses: 0,
    },
    output: {
      ...empty.totals!.output!,
      knownTotal: "",
      measuredResponses: 0,
      unavailableResponses: 0,
    },
  };
  expect(
    (
      await readLifetimeUsage(
        until - usageWindowMs,
        async () => empty,
        new AbortController().signal,
      )
    ).input.known,
  ).toBeUndefined();
  const read = vi
    .fn()
    .mockResolvedValueOnce(summary())
    .mockResolvedValueOnce(summary());
  await expect(
    readLifetimeUsage(
      until - usageWindowMs * 2n,
      read,
      new AbortController().signal,
    ),
  ).rejects.toThrow();
});
it("requires original valid creation metadata", () => {
  const resource = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    createdAt: "2024-02-29T00:00:00Z",
  });
  expect(sessionCreatedAt(resource)).toBe(
    BigInt(Date.parse(resource.createdAt)),
  );
  for (const createdAt of [
    "",
    "2025-02-29T00:00:00Z",
    "2024-02-29T00:00:00",
    "not a date",
  ])
    expect(sessionCreatedAt({ ...resource, createdAt })).toBeUndefined();
  expect(sessionCreatedAt({ ...resource, schemaVersion: 2 })).toBeUndefined();
});
it("retains only the previous complete summary on failed invalidation and retries without a locale read", async () => {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    createdAt: "2026-10-01T00:00:00Z",
  });
  const read = vi.fn(async (request: GetUsageSummaryRequest) => {
    expect(request.sessionId).toBe(session.id);
    expect(request.accountingProfile).toBe(UsageAccountingProfile.UNSPECIFIED);
    expect(request.projectId).toBe("");
    return summary();
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const transport = createRouterTransport((router) =>
    router.service(UsageService, { getUsageSummary: read }),
  );
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <SessionLifetimeUsage session={session} diagnosticsTarget={null} />
      </QueryClientProvider>
    </TransportProvider>,
  );
  await screen.findByText("9,007,199,254,740,993");
  read.mockRejectedValueOnce(new Error("fixture refresh unavailable"));
  await act(async () => {
    await client.invalidateQueries();
  });
  expect(screen.getByText("9,007,199,254,740,993")).toBeTruthy();
  await screen.findByText(/Previous complete usage is stale/);
  fireEvent.click(screen.getByRole("button", { name: "Retry read" }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(3));
  await waitFor(() =>
    expect(screen.queryByText(/Previous complete usage is stale/)).toBeNull(),
  );
  await act(async () => {
    await i18n.changeLanguage("ko");
  });
  expect(screen.getByText("입력")).toBeTruthy();
  expect(read).toHaveBeenCalledTimes(3);
  await act(async () => {
    await i18n.changeLanguage("en");
  });
});
it("fences a delayed old-session result and pauses inactive readers", async () => {
  const oldSession = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    createdAt: "2026-10-01T00:00:00Z",
  });
  const nextSession = { ...oldSession, id: newRequestId() };
  let release!: () => void;
  const read = vi.fn(async (request: GetUsageSummaryRequest) => {
    if (request.sessionId === oldSession.id)
      await new Promise<void>((resolve) => {
        release = resolve;
      });
    return summary(
      undefined,
      request.sessionId === oldSession.id ? "111" : "222",
    );
  });
  const client = new QueryClient();
  const transport = createRouterTransport((router) =>
    router.service(UsageService, { getUsageSummary: read }),
  );
  const view = (session: typeof oldSession, active: boolean) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <SessionActivityProvider active={active}>
          <SessionLifetimeUsage session={session} diagnosticsTarget={null} />
        </SessionActivityProvider>
      </QueryClientProvider>
    </TransportProvider>
  );
  const mounted = render(view(oldSession, true));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  mounted.rerender(view(nextSession, true));
  await screen.findByText("222");
  await act(async () => {
    release();
  });
  expect(screen.queryByText("111")).toBeNull();
  mounted.rerender(view(nextSession, false));
  await act(async () => {
    await client.invalidateQueries();
  });
  expect(read).toHaveBeenCalledTimes(2);
  mounted.rerender(view(nextSession, true));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(3));
});
