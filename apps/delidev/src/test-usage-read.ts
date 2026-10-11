// SPDX-License-Identifier: Apache-2.0
import type { Transport } from "@connectrpc/connect";
import { UsageService } from "@delinoio/delidev-api-client";

export enum UsageReadState { Pending = "pending", Succeeded = "succeeded", Failed = "failed" }
export interface UsageReadObservation {
  readonly generalChat: boolean;
  state: UsageReadState;
  responseReceived: boolean;
}

// Observe the original transport without changing its request, authority or error.
// A delivery gate exposes the interval after the real response but before React
// can consume it, so preexisting coverage text cannot settle the current read.
export function observeUsageReads(original: Transport, beforeDelivery?: (read: UsageReadObservation) => Promise<void>) {
  const reads: UsageReadObservation[] = [];
  const transport: Transport = {
    ...original,
    async unary(method, signal, timeout, header, input, context) {
      if (method.parent.typeName !== UsageService.typeName || method.localName !== UsageService.method.getUsageSummary.localName) return original.unary(method, signal, timeout, header, input, context);
      const read: UsageReadObservation = { generalChat: (input as { generalChat?: boolean }).generalChat === true, state: UsageReadState.Pending, responseReceived: false };
      reads.push(read);
      try {
        const response = await original.unary(method, signal, timeout, header, input, context);
        read.responseReceived = true;
        await beforeDelivery?.(read);
        read.state = UsageReadState.Succeeded;
        return response;
      } catch (error) {
        read.state = UsageReadState.Failed;
        throw error;
      }
    },
  };
  return { transport, reads };
}

export function usageReadSucceeded(read: UsageReadObservation | undefined): boolean {
  return read?.responseReceived === true && read.state === UsageReadState.Succeeded;
}
