// SPDX-License-Identifier: Apache-2.0
import { Code, ConnectError, createClient, createRouterTransport } from "@connectrpc/connect";
import { waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { SystemService, UsageService } from "@delinoio/delidev-api-client";
import { observeUsageReads, usageReadSucceeded, UsageReadState } from "./test-usage-read";

it("keeps the matching filtered read pending until its original response is delivered", async () => {
  let completeResponse!: () => void, releaseDelivery!: () => void;
  const response = new Promise<void>(resolve => { completeResponse = resolve; });
  const delivery = new Promise<void>(resolve => { releaseDelivery = resolve; });
  const original = createRouterTransport(router => router.service(UsageService, { getUsageSummary: async () => { await response; return { fromUnixMs: 1n, untilUnixMs: 2n }; } }));
  const observed = observeUsageReads(original, () => delivery);
  const request = createClient(UsageService, observed.transport).getUsageSummary({ generalChat: true });
  await waitFor(() => expect(observed.reads).toHaveLength(1));
  expect(observed.reads[0]).toMatchObject({ generalChat: true, responseReceived: false, state: UsageReadState.Pending });
  expect(usageReadSucceeded(observed.reads[0])).toBe(false);
  completeResponse();
  await waitFor(() => expect(observed.reads[0].responseReceived).toBe(true));
  expect(usageReadSucceeded(observed.reads[0])).toBe(false);
  releaseDelivery();
  expect(await request).toMatchObject({ fromUnixMs: 1n, untilUnixMs: 2n });
  expect(usageReadSucceeded(observed.reads[0])).toBe(true);
});

it("rejects failed current reads instead of treating stopped loading as success", async () => {
  const failure = new ConnectError("Synthetic unavailable read", Code.Unavailable);
  const original = createRouterTransport(router => router.service(UsageService, { getUsageSummary: () => { throw failure; } }));
  const observed = observeUsageReads(original);
  await expect(createClient(UsageService, observed.transport).getUsageSummary({ generalChat: true })).rejects.toMatchObject({ code: Code.Unavailable });
  expect(observed.reads[0]).toMatchObject({ generalChat: true, responseReceived: false, state: UsageReadState.Failed });
  expect(usageReadSucceeded(observed.reads[0])).toBe(false);
  expect(usageReadSucceeded(undefined)).toBe(false);
});

it("separates ordinary successful scope and passes unrelated original reads through", async () => {
  const original = createRouterTransport(router => {
    router.service(UsageService, { getUsageSummary: () => ({ fromUnixMs: 1n, untilUnixMs: 2n }) });
    router.service(SystemService, { getStatus: () => ({ protocolVersion: 2 }) });
  });
  const observed = observeUsageReads(original);
  expect(await createClient(SystemService, observed.transport).getStatus({})).toMatchObject({ protocolVersion: 2 });
  expect(observed.reads).toHaveLength(0);
  await createClient(UsageService, observed.transport).getUsageSummary({});
  expect(observed.reads[0].generalChat).toBe(false);
  expect(usageReadSucceeded(observed.reads[0])).toBe(true);
});
