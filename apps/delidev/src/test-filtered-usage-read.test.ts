// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createClient, createRouterTransport } from "@connectrpc/connect";
import { expect, it, vi } from "vitest";
import { GetUsageSummaryResponseSchema, UsageService } from "@delinoio/delidev-api-client";
import { filteredUsageRead } from "./test-filtered-usage-read";

it("requires the new filtered successful response rather than a previous summary", async () => {
  const summary = create(GetUsageSummaryResponseSchema, { fromUnixMs: 1n, untilUnixMs: 2n });
  const handler = vi.fn(async () => summary);
  const base = createRouterTransport(router => router.service(UsageService, { getUsageSummary: handler }));
  const read = filteredUsageRead(base);
  const client = createClient(UsageService, read.transport);
  await client.getUsageSummary({});
  let completed = false;
  void read.completed.then(() => { completed = true; });
  expect(completed).toBe(false);
  const result = client.getUsageSummary({ generalChat: true });
  expect((await read.started).generalChat).toBe(true);
  await vi.waitFor(() => expect(handler).toHaveBeenCalledTimes(2));
  expect(completed).toBe(false);
  read.release();
  const current = await read.completed;
  expect(current.request.generalChat).toBe(true); expect(current.request.projectId).toBe("");
  expect(current.response).toEqual(summary); expect(await result).toEqual(summary);
});

it("rejects filtered error completion instead of treating settled loading as success", async () => {
  const base = createRouterTransport(router => router.service(UsageService, { getUsageSummary: async () => { throw new ConnectError("Synthetic filtered read failed", Code.Unavailable); } }));
  const read = filteredUsageRead(base);
  const result = createClient(UsageService, read.transport).getUsageSummary({ generalChat: true });
  await Promise.all([
    expect(read.completed).rejects.toMatchObject({ code: Code.Unavailable }),
    expect(result).rejects.toMatchObject({ code: Code.Unavailable }),
  ]);
});
