// SPDX-License-Identifier: Apache-2.0
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
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

it("clears definite rejection without releasing a newer pending or uncertain wire request", async () => {
 const id = newRequestId(), requestId = newRequestId();
 let finish!: () => void;
 const requests: unknown[] = [];
 const transport = createRouterTransport(router => router.service(SessionService, { enqueueInput: async request => {
  requests.push(request);
  if (requests.length === 1) throw new ConnectError("Definite rejection", Code.InvalidArgument);
  if (requests.length === 2) { await new Promise<void>(resolve => { finish = resolve; }); throw new ConnectError("Uncertain response", Code.Unavailable); }
  return { change: { requestId: request.requestId } };
 } }));
 function Sender() {
  const mutation = useRetainedMutation("fixture", SessionQuery.enqueueInput);
  return <><button onClick={() => void mutation.send({ sessionId: id, requestId })}>Submit fixture</button><button onClick={mutation.clearRejected}>Discard rejection</button><button onClick={() => { void mutation.send({ sessionId: id, requestId }); mutation.clearRejected(); }}>Send then stale discard</button><button onClick={mutation.retry}>Retry fixture</button><p>{mutation.busy ? "Pending" : mutation.uncertain ? "Uncertain" : "Settled"}</p>{mutation.error instanceof Error ? <p>{mutation.error.message}</p> : null}</>;
 }
 const client = new QueryClient();
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Sender /></MutationIntents></QueryClientProvider></TransportProvider>);
 fireEvent.click(screen.getByRole("button", { name: "Submit fixture" }));
 await screen.findByText(/Definite rejection/);
 fireEvent.click(screen.getByRole("button", { name: "Discard rejection" }));
 expect(screen.queryByText(/Definite rejection/)).toBeNull();
 fireEvent.click(screen.getByRole("button", { name: "Send then stale discard" }));
 await screen.findByText("Pending");
 await act(async () => finish());
 await screen.findByText("Uncertain");
 fireEvent.click(screen.getByRole("button", { name: "Discard rejection" }));
 expect(screen.getByText(/Uncertain response/)).toBeDefined();
 fireEvent.click(screen.getByRole("button", { name: "Retry fixture" }));
 await screen.findByText("Settled");
 expect(requests).toHaveLength(3); expect(requests[2]).toEqual(requests[1]);
});

it("does not mistake an accepted receipt presentation failure for a rejected request", async () => {
 const id = newRequestId(), requestId = newRequestId();
 let calls = 0;
 const transport = createRouterTransport(router => router.service(SessionService, { enqueueInput: request => { calls += 1; return { change: { requestId: request.requestId } }; } }));
 function Sender() {
  const mutation = useRetainedMutation("accepted-fixture", SessionQuery.enqueueInput, () => { throw new Error("Accepted presentation failed"); }, (result, request) => result.change?.requestId === request.requestId);
  return <><button onClick={() => void mutation.send({ sessionId: id, requestId })}>Submit accepted fixture</button><button onClick={mutation.clearRejected}>Discard rejection</button><button onClick={mutation.retry}>Retry fixture</button>{mutation.error instanceof Error ? <p>{mutation.error.message}</p> : null}</>;
 }
 render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient()}><MutationIntents><Sender /></MutationIntents></QueryClientProvider></TransportProvider>);
 fireEvent.click(screen.getByRole("button", { name: "Submit accepted fixture" }));
 await screen.findByText("Accepted presentation failed");
 fireEvent.click(screen.getByRole("button", { name: "Discard rejection" }));
 expect(screen.getByText("Accepted presentation failed")).toBeDefined();
 fireEvent.click(screen.getByRole("button", { name: "Retry fixture" }));
 expect(calls).toBe(1);
});
