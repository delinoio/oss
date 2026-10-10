// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import type { MessageInitShape } from "@bufbuild/protobuf";
import type { Transport } from "@connectrpc/connect";
import { GetUsageSummaryRequestSchema, UsageService, type GetUsageSummaryRequest, type GetUsageSummaryResponse } from "@delinoio/delidev-api-client";

// Observe one actual General Chat read, then hold its response at the transport
// boundary. Old DOM notices cannot resolve this original request's proof.
export function filteredUsageRead(base: Transport) {
  let begin!: (request: GetUsageSummaryRequest) => void;
  let succeed!: (value: { request: GetUsageSummaryRequest; response: GetUsageSummaryResponse }) => void;
  let fail!: (error: unknown) => void;
  let release!: () => void;
  const started = new Promise<GetUsageSummaryRequest>(resolve => { begin = resolve; });
  const completed = new Promise<{ request: GetUsageSummaryRequest; response: GetUsageSummaryResponse }>((resolve, reject) => { succeed = resolve; fail = reject; });
  // A failed real read may arrive before the test awaits it. Retain that same
  // rejection for the caller without a temporary unhandled-rejection failure.
  void completed.catch(() => {});
  const held = new Promise<void>(resolve => { release = resolve; });
  let observed = false;
  const transport: Transport = { ...base, async unary(method, signal, timeout, headers, input, context) {
    if (observed || method.parent.typeName !== UsageService.typeName || method.name !== "GetUsageSummary" || !(input as { generalChat?: boolean }).generalChat) return base.unary(method, signal, timeout, headers, input, context);
    observed = true;
    const request = create(GetUsageSummaryRequestSchema, input as MessageInitShape<typeof GetUsageSummaryRequestSchema>);
    begin(request);
    try {
      const response = await base.unary(method, signal, timeout, headers, input, context);
      await held;
      if (signal?.aborted) throw signal.reason;
      succeed({ request, response: response.message as unknown as GetUsageSummaryResponse });
      return response;
    } catch (error) { fail(error); throw error; }
  } };
  return { transport, started, completed, release };
}
