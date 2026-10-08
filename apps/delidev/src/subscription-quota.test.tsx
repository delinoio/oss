// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ResourceSchema, EntityKind, SubscriptionService, SystemService, SystemCapability, newRequestId } from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { MutationIntents } from "./mutation";
 import { SubscriptionQuotaControls } from "./subscription-quota";

function fixture(details: unknown = [{ id: "credit_1", reset_type: "codexRateLimits", status: "available" }], lease?: { action: string; machine_id: string }, preferred = "", server = false, supported = true, phase = "") {
  const machine = newRequestId(), connection = newRequestId(), generation = newRequestId(), inventory = newRequestId();
  const data = { alias: "Quota fixture", type: "subscription", subscription_service: "chatgpt", health: "ready", recovery_notifications: false, connection: { id: connection }, subscription: { generation, owner_machine_id: server ? "" : machine, server_quota_generation: server ? generation : undefined, server_quota: phase ? { id: newRequestId(), phase } : undefined, lease, reset_credits: { observation_id: inventory, observed_at: new Date().toISOString(), available_count: "2", credits: details } } };
  let account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 2, documentJson: encode(data) });
  const request = vi.fn(async (value) => ({ account, operationId: value.mutation?.requestId }));
  const reconcile = vi.fn(async () => ({ account }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBSCRIPTION_RESET_CREDITS_V1, ...(server && supported ? [SystemCapability.SERVER_SUBSCRIPTION_QUOTA_V1] : [SystemCapability.SUBSCRIPTION_QUOTA_V1])] }) });
    router.service(SubscriptionService, { requestSubscriptionObservation: request, reconcileSubscriptionCredit: reconcile });
  });
  const queryClient=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 function Harness() {
    const [current, setCurrent] = useState(account), [, setBusy] = useState(false);
    return <QueryClientProvider client={queryClient}><TransportProvider transport={transport}><MutationIntents><SubscriptionQuotaControls current={current} machine={preferred} active accepted={setCurrent} busyChanged={setBusy} /><button onClick={() => { account = create(ResourceSchema, { ...account, revision: account.revision + 1n }); setCurrent(account); }}>Change fixture account revision</button></MutationIntents></TransportProvider></QueryClientProvider>;
  }
  return { Harness, request, reconcile, account, machine, connection, generation, inventory };
}

it("preserves authoritative count, confirms the selected credit and retains the exact lost request", async () => {
  const value = fixture(); value.request.mockRejectedValueOnce(new ConnectError("lost acknowledgment", Code.Unavailable)); render(<value.Harness />);
  fireEvent.click(await screen.findByRole("button", { name: "Review reset credit credit_1" }));
  expect(screen.getByText(/2 available reset credits; 1 returned details/)).toBeTruthy(); expect(value.request).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm credit consumption" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry original quota or credit request" }));
  await waitFor(() => expect(value.request).toHaveBeenCalledTimes(2));
  expect(value.request.mock.calls[1][0]).toEqual(value.request.mock.calls[0][0]);
  expect(value.request.mock.calls[0][0]).toMatchObject({ creditId: "credit_1", nextCredit: false, confirmed: true, connectionId: value.connection, generationId: value.generation, creditsObservationId: value.inventory });
});

it("requires an explicit count-only next-credit confirmation and fences stale revisions", async () => {
  const value = fixture(null); render(<value.Harness />);
  fireEvent.click(await screen.findByRole("button", { name: "Review native next-credit selection" }));
  fireEvent.click(screen.getByRole("button", { name: "Change fixture account revision" }));
  expect((screen.getByRole("button", { name: "Confirm credit consumption" }) as HTMLButtonElement).disabled).toBe(true);
  expect(value.request).not.toHaveBeenCalled(); expect(document(value.account).recovery_notifications).toBe(false);
});

it("explicit quota refresh does not consume credits or refresh authentication", async () => {
  const value = fixture(); render(<value.Harness />);
  await waitFor(() => expect((screen.getByRole("button", { name: "Refresh quota" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Refresh quota" }));
  await waitFor(() => expect(value.request).toHaveBeenCalledTimes(1));
  expect(value.request.mock.calls[0][0]).toMatchObject({ creditId: "", nextCredit: false, confirmed: false });
  expect(value.reconcile).not.toHaveBeenCalled();
});


it("keeps detail quota refresh bound to the active execution owner despite a different selected device",async()=>{
 const runner=newRequestId(),value=fixture(undefined,{action:"execute",machine_id:runner},newRequestId());
 render(<value.Harness />);
 await waitFor(()=>expect((screen.getByRole("button",{name:"Refresh quota"}) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button",{name:"Refresh quota"}));
 await waitFor(()=>expect(value.request).toHaveBeenCalledTimes(1));
 expect(value.request.mock.calls[0]?.[0]).toMatchObject({machineId:runner});
});
it("blocks detail quota refresh while a different native lease kind owns the account",async()=>{
 const value=fixture(undefined,{action:"refresh",machine_id:newRequestId()},newRequestId());
 render(<value.Harness />);
 await screen.findByText(/Last successful observation/);
 expect((screen.getByRole("button",{name:"Refresh quota"}) as HTMLButtonElement).disabled).toBe(true);
 expect(value.request).not.toHaveBeenCalled();
});


it.each([true, false])("negotiates server quota without a Runner Device (supported: %s)", async supported => {
 const value=fixture(undefined,undefined,"",true,supported);render(<value.Harness />);
 await screen.findByText(/Last successful observation/);
 const button=screen.getByRole("button",{name:"Refresh quota"}) as HTMLButtonElement;
 await waitFor(()=>expect(button.disabled).toBe(!supported));
 fireEvent.click(button);
 if(supported){await waitFor(()=>expect(value.request).toHaveBeenCalledTimes(1));expect(value.request.mock.calls[0][0]).toMatchObject({machineId:"",connectionId:value.connection,generationId:value.generation,mutation:{id:value.account.id,expectedRevision:1n}})}else{expect(value.request).not.toHaveBeenCalled()}
 expect((screen.getByRole("button",{name:"Review reset credit credit_1"}) as HTMLButtonElement).disabled).toBe(true);
});
it.each(["queued","sending","uncertain"])("retains the server quota %s fence in detail controls",async phase=>{
 const value=fixture(undefined,undefined,"",true,true,phase);render(<value.Harness />);await screen.findByText(/Last successful observation/);
 expect((screen.getByRole("button",{name:"Refresh quota"}) as HTMLButtonElement).disabled).toBe(true);expect(value.request).not.toHaveBeenCalled();
});
