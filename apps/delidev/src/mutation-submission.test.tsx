// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { SessionQuery, SessionService, newRequestId } from "@delinoio/delidev-api-client";
import { MutationIntents, useRetainedMutation, useRetainedMutationAccepted, useRetainedMutationIntents, useRetainedMutationNotifications } from "./mutation";

it("publishes the verified original result before clearing intent, then settles the unmounted sender", async () => {
  const id = newRequestId(), requestId = newRequestId(), key = `enqueue:${id}`, order: string[] = [];
  let finish!: () => void;
  const transport = createRouterTransport(router => router.service(SessionService, { enqueueInput: async request => {
    await new Promise<void>(resolve => { finish = resolve; }); return { change: { requestId: request.requestId } };
  } }));
  function Observers() {
    const pending = useRetainedMutationIntents("enqueue:");
    useRetainedMutationNotifications((receivedKey, original, result) => {
      expect(receivedKey).toBe(key); expect(original).toMatchObject({ requestId }); expect(result).toMatchObject({ change: { requestId } });
      expect(pending).toMatchObject([{ key, busy: true }]); order.push("mapping");
    });
    useRetainedMutationAccepted(key, () => order.push("draft"));
    return <p>{pending.length ? "Original pending" : "Original settled"}</p>;
  }
  function Sender() {
    const send = useRetainedMutation(key, SessionQuery.enqueueInput, () => order.push("mounted"), (result, request) => { order.push("verify"); return result.change?.requestId === request.requestId; });
    return <button onClick={() => void send.send({ requestId, sessionId: id })}>Submit fixture</button>;
  }
  const client = new QueryClient();
  const view = (sender: boolean) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Observers />{sender ? <Sender /> : null}</MutationIntents></QueryClientProvider></TransportProvider>;
  const mounted = render(view(true)); fireEvent.click(screen.getByRole("button", { name: "Submit fixture" })); await screen.findByText("Original pending");
  mounted.rerender(view(false)); await act(async () => finish()); await waitFor(() => expect(screen.getByText("Original settled")).toBeDefined());
  expect(order).toEqual(["verify", "mapping", "draft"]);
});
