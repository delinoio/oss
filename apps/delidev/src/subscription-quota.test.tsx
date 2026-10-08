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
import { i18n, copy } from "./localization";
import { MutationIntents } from "./mutation";
 import { SubscriptionQuotaControls } from "./subscription-quota";

function fixture(details: unknown = [{ id: "credit_1", reset_type: "codexRateLimits", status: "available" }], lease?: { action: string; machine_id: string }, preferred = "", server = false, supported = true, phase = "", serverCredits = false, creditPhase = "", cleanup = false, workerUncertain = false, inventoryFields: Record<string, unknown> = {}, quotaError = "") {
  const machine = newRequestId(), connection = newRequestId(), generation = newRequestId(), inventory = newRequestId();
  const data = { alias: "Quota fixture", type: "subscription", subscription_service: "chatgpt", health: "ready", recovery_notifications: false, connection: { id: connection }, subscription: { observation: workerUncertain ? { id: newRequestId(), action: "reset-credit", phase: "uncertain" } : undefined, generation, owner_machine_id: server ? "" : machine, server_quota_generation: server ? generation : undefined, server_credit: creditPhase ? { id: newRequestId(), phase: creditPhase, cleanup_confirmed: cleanup, outcome: "" } : undefined, server_quota: phase ? { id: newRequestId(), phase, error_code: quotaError } : undefined, lease, reset_credits: { observation_id: inventory, observed_at: new Date().toISOString(), available_count: "2", credits: details, ...inventoryFields } } };
  let account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 2, documentJson: encode(data) });
  const request = vi.fn(async (value) => ({ account, operationId: value.mutation?.requestId }));
  const reconcile = vi.fn(async (_value: unknown) => ({ account }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBSCRIPTION_RESET_CREDITS_V1, ...(serverCredits ? [SystemCapability.SERVER_SUBSCRIPTION_RESET_CREDITS_V1] : []), ...(supported ? [SystemCapability.SERVER_SUBSCRIPTION_QUOTA_V2] : [SystemCapability.SUBSCRIPTION_QUOTA_V1])] }) });
    router.service(SubscriptionService, { requestSubscriptionObservation: request, reconcileSubscriptionCredit: reconcile });
  });
  const queryClient=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 function Harness() {
    const [current, setCurrent] = useState(account), [, setBusy] = useState(false);
    return <QueryClientProvider client={queryClient}><TransportProvider transport={transport}><MutationIntents><SubscriptionQuotaControls current={current} machine={preferred} active accepted={setCurrent} busyChanged={setBusy} /><button onClick={() => { account = create(ResourceSchema, { ...account, revision: account.revision + 1n }); setCurrent(account); }}>Change fixture account revision</button><button onClick={() => { account = create(ResourceSchema, { ...account, revision: account.revision + 1n, documentJson: encode({ ...data, subscription: { ...data.subscription, reset_credits: { ...data.subscription.reset_credits, available_count: "0" } } }) }); setCurrent(account); }}>Remove fixture credits</button></MutationIntents></TransportProvider></QueryClientProvider>;
  }
  return { Harness, request, reconcile, account, machine, connection, generation, inventory };
}

async function selectExactCredit() {
 const use = await screen.findByRole("button", { name: "Use" });
 await waitFor(() => expect((use as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(use);
 fireEvent.click(screen.getByRole("button", { name: "Select credit credit_1" }));
}

it("preserves authoritative count, confirms the selected credit and retains the exact lost request", async () => {
  const value = fixture(); value.request.mockRejectedValueOnce(new ConnectError("lost acknowledgment", Code.Unavailable)); render(<value.Harness />);
  await selectExactCredit();
  expect(screen.getByText("2 available reset credits")).toBeTruthy(); expect(screen.getByText("1 returned details")).toBeTruthy(); expect(value.request).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm credit consumption" }));
  fireEvent.click(await screen.findByRole("button", { name: "Retry original quota or credit request" }));
  await waitFor(() => expect(value.request).toHaveBeenCalledTimes(2));
  expect(value.request.mock.calls[1][0]).toEqual(value.request.mock.calls[0][0]);
  expect(value.request.mock.calls[0][0]).toMatchObject({ creditId: "credit_1", nextCredit: false, confirmed: true, connectionId: value.connection, generationId: value.generation, creditsObservationId: value.inventory });
});

it("requires an explicit count-only next-credit confirmation and fences stale revisions", async () => {
  const value = fixture(null); render(<value.Harness />);
  const use = await screen.findByRole("button", { name: "Use" });
  await waitFor(() => expect((use as HTMLButtonElement).disabled).toBe(false));fireEvent.click(use);
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


it("refreshes detail quota on the server during execution despite a different selected device",async()=>{
 const runner=newRequestId(),value=fixture(undefined,{action:"execute",machine_id:runner},newRequestId());
 render(<value.Harness />);
 await waitFor(()=>expect((screen.getByRole("button",{name:"Refresh quota"}) as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(screen.getByRole("button",{name:"Refresh quota"}));
 await waitFor(()=>expect(value.request).toHaveBeenCalledTimes(1));
 expect(value.request.mock.calls[0]?.[0]).toMatchObject({machineId:""});
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
 expect((screen.getByRole("button",{name:"Use"}) as HTMLButtonElement).disabled).toBe(true);
});
it.each(["queued","sending","uncertain"])("retains the server quota %s fence in detail controls",async phase=>{
 const value=fixture(undefined,undefined,"",true,true,phase);render(<value.Harness />);await screen.findByText(/Last successful observation/);
 expect((screen.getByRole("button",{name:"Refresh quota"}) as HTMLButtonElement).disabled).toBe(true);expect(value.request).not.toHaveBeenCalled();
});

it.each([undefined, null])("confirms server reset credits with the original omitted-machine selector", async details => {
 const value=fixture(details,undefined,"",true,true,"",true);render(<value.Harness />);
 const next=details===null;
 const review=await screen.findByRole("button",{name:"Use"});
 await waitFor(()=>expect((review as HTMLButtonElement).disabled).toBe(false));fireEvent.click(review);
 if (!next) fireEvent.click(screen.getByRole("button", { name: "Select credit credit_1" }));
 fireEvent.click(screen.getByRole("button",{name:"Confirm credit consumption"}));
 await waitFor(()=>expect(value.request).toHaveBeenCalledTimes(1));
 expect(value.request.mock.calls[0][0]).toMatchObject({machineId:"",creditId:next?"":"credit_1",nextCredit:next,confirmed:true,connectionId:value.connection,generationId:value.generation,creditsObservationId:value.inventory});
});
it.each(["queued","sending","uncertain"])("server credit %s blocks competing refresh and consumption",async phase=>{
 const value=fixture(undefined,undefined,"",true,true,"",true,phase);render(<value.Harness />);
 await screen.findByText(/Last successful observation/);
 expect((screen.getByRole("button",{name:"Refresh quota"}) as HTMLButtonElement).disabled).toBe(true);
 expect((screen.getByRole("button",{name:"Use"}) as HTMLButtonElement).disabled).toBe(true);
 expect(value.request).not.toHaveBeenCalled();
});
it.each([false,true])("server credit reconciliation requires independently confirmed cleanup (%s)",async cleanup=>{
 const value=fixture(undefined,undefined,"",true,true,"",true,"uncertain",cleanup);render(<value.Harness />);
 const reconcile=await screen.findByRole("button",{name:"Reconcile original credit operation"});
 await waitFor(()=>expect((reconcile as HTMLButtonElement).disabled).toBe(!cleanup));fireEvent.click(reconcile);
 if(cleanup) {await waitFor(()=>expect(value.reconcile).toHaveBeenCalledTimes(1));expect(value.reconcile.mock.calls[0][0]).toMatchObject({connectionId:value.connection,generationId:value.generation});} else {expect(value.reconcile).not.toHaveBeenCalled()}
});

it("preserves the server confirmation selectors in Korean",async()=>{
 await i18n.changeLanguage("ko");
 try {
  const value=fixture(undefined,undefined,"",true,true,"",true);render(<value.Harness />);
  const use=await screen.findByRole("button",{name:"사용"});
  await waitFor(()=>expect((use as HTMLButtonElement).disabled).toBe(false));fireEvent.click(use);
  const review=await screen.findByRole("button",{name:/credit_1/});
  await waitFor(()=>expect((review as HTMLButtonElement).disabled).toBe(false));fireEvent.click(review);
  fireEvent.click(screen.getByRole("button",{name:copy("subscription-quota.confirmCreditConsumption_251822")}));
  await waitFor(()=>expect(value.request).toHaveBeenCalledTimes(1));
  expect(value.request.mock.calls[0][0]).toMatchObject({machineId:"",creditId:"credit_1",confirmed:true,connectionId:value.connection,generationId:value.generation,creditsObservationId:value.inventory});
 } finally {await i18n.changeLanguage("en")}
});

it("retains original Worker reconciliation after a terminal server-credit history",async()=>{
 const value=fixture(undefined,undefined,"",false,true,"",true,"succeeded",true,true);render(<value.Harness />);
 const button=await screen.findByRole("button",{name:"Reconcile original credit operation"});
 await waitFor(()=>expect((button as HTMLButtonElement).disabled).toBe(false));fireEvent.click(button);
 await waitFor(()=>expect(value.reconcile).toHaveBeenCalledTimes(1));
 const state=document(value.account).subscription as Record<string,unknown>;
 expect(value.reconcile.mock.calls[0][0]).toMatchObject({operationId:(state.observation as Record<string,unknown>).id});
});


it("starts collapsed, uses authoritative count, and restores selection focus without sending", async () => {
 const value = fixture(); render(<value.Harness />);
 const use = await screen.findByRole("button", { name: "Use" });
 await waitFor(() => expect((use as HTMLButtonElement).disabled).toBe(false));
 expect(screen.getByRole("button", { name: "View details" }).getAttribute("aria-expanded")).toBe("false");
 expect(screen.queryByRole("button", { name: "Select credit credit_1" })).toBeNull();
 fireEvent.click(use); expect(globalThis.document.activeElement).toBe(screen.getByRole("heading", { name: "Credit details" }));
 const select = screen.getByRole("button", { name: "Select credit credit_1" }); fireEvent.click(select);
 expect(globalThis.document.activeElement).toBe(screen.getByRole("heading", { name: "Confirm reset credit consumption" }));
 expect(screen.getByText("Account: Quota fixture")).toBeTruthy();
 fireEvent.click(screen.getByRole("button", { name: "Keep credit" })); expect(globalThis.document.activeElement).toBe(select);
 expect(value.request).not.toHaveBeenCalled();
});
it("retains original confirmation outside collapsed details and restores section fallback focus", async () => {
 const value=fixture();render(<value.Harness />);await selectExactCredit();
 fireEvent.click(screen.getByRole("button",{name:"Hide details"}));
 expect(screen.getByRole("button",{name:"Confirm credit consumption"})).toBeTruthy();
 fireEvent.click(screen.getByRole("button",{name:"Keep credit"}));
 expect(globalThis.document.activeElement).toBe(screen.getByRole("heading",{name:"Reset credits"}));
 expect(value.request).not.toHaveBeenCalled();
});
it.each([{}, "invalid", [{id:"expired",reset_type:"codexRateLimits",status:"available",expires_at:"2020-01-01T00:00:00Z"}]])("never invents next-credit selection for malformed or ineligible details",async details=>{
 const value=fixture(details);render(<value.Harness />);await screen.findByText(/Last successful observation/);
 expect((screen.getByRole("button",{name:"Use"}) as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByRole("button",{name:"View details"}));expect(screen.queryByRole("button",{name:"Confirm credit consumption"})).toBeNull();expect(value.request).not.toHaveBeenCalled();
});

it.each([
 {available_count:"0"}, {available_count:"not-a-count"},
 {observed_at:"2020-01-01T00:00:00Z"}, {observed_at:"2999-01-01T00:00:00Z"}
])("keeps unknown, zero and stale inventory disabled with visible guidance",async fields=>{
 const value=fixture(undefined,undefined,"",false,true,"",false,"",false,false,fields);render(<value.Harness />);
 await screen.findByText(/Last successful observation/);
 expect((screen.getByRole("button",{name:"Use"}) as HTMLButtonElement).disabled).toBe(true);
 expect(screen.getByText(fields.available_count==="0"?"No reset credits are available.":fields.available_count?"Available credit count unknown":"Refresh the account to review a current credit inventory.",{selector:"p"})).toBeTruthy();
 expect(value.request).not.toHaveBeenCalled();
});
it("locale changes and collapsed details retain the original exact selection without sending",async()=>{
 const value=fixture();render(<value.Harness />);await selectExactCredit();fireEvent.click(screen.getByRole("button",{name:"Hide details"}));
 try {await i18n.changeLanguage("ko");expect(await screen.findByRole("button",{name:"리셋권 사용 확인"})).toBeTruthy();expect(screen.getByText("리셋권 credit_1 사용")).toBeTruthy();expect(value.request).not.toHaveBeenCalled();}
 finally {await i18n.changeLanguage("en")}
 expect(screen.getByRole("button",{name:"Confirm credit consumption"})).toBeTruthy();
});

it("restores the section heading when background inventory disables the original selection opener",async()=>{
 const value=fixture();render(<value.Harness />);await selectExactCredit();
 fireEvent.click(screen.getByRole("button",{name:"Remove fixture credits"}));
 expect((screen.getByRole("button",{name:"Select credit credit_1"}) as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByRole("button",{name:"Keep credit"}));
 expect(globalThis.document.activeElement).toBe(screen.getByRole("heading",{name:"Reset credits"}));expect(value.request).not.toHaveBeenCalled();
});
it("restores the section heading instead of an inert original selection opener",async()=>{
 const value=fixture();render(<value.Harness />);await selectExactCredit();
 screen.getByRole("button",{name:"Select credit credit_1"}).closest(".reset-credit-rows")!.setAttribute("inert","");
 fireEvent.click(screen.getByRole("button",{name:"Keep credit"}));
 expect(globalThis.document.activeElement).toBe(screen.getByRole("heading",{name:"Reset credits"}));expect(value.request).not.toHaveBeenCalled();
});

it("renders expiry only for a valid supplied RFC3339 timestamp",async()=>{
 const value=fixture([
  {id:"credit-bad-date",reset_type:"codexRateLimits",status:"available",expires_at:"2030"},
  {id:"credit-valid-date",reset_type:"codexRateLimits",status:"available",expires_at:"2030-10-31T00:00:00Z"}
 ]);render(<value.Harness />);const use=await screen.findByRole("button",{name:"Use"});await waitFor(()=>expect((use as HTMLButtonElement).disabled).toBe(false));fireEvent.click(use);
 expect(globalThis.document.querySelectorAll(".reset-credit-row time").length).toBe(1);
 expect(globalThis.document.querySelector(".reset-credit-row time")?.getAttribute("datetime")).toBe("2030-10-31T00:00:00Z");expect(value.request).not.toHaveBeenCalled();
});
it.each(["unsupported", "unavailable"])("shows selected-server quota troubleshooting for %s without Worker fallback", async code => {
 const value = fixture(undefined, undefined, "", false, true, "failed", false, "", false, false, {}, code);
 render(<value.Harness />);
 await screen.findByText(code === "unsupported" ? /Check the selected server’s Codex installation/ : /Check the selected server connection/);
 expect(value.request).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button", { name: "Refresh quota" }));
 await waitFor(() => expect(value.request).toHaveBeenCalledTimes(1));
 expect(value.request.mock.calls[0][0]).toMatchObject({ machineId: "" });
});
