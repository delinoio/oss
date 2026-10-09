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

function fixture(details: unknown = [{ id: "credit_1", reset_type: "codexRateLimits", status: "available" }], lease?: { action: string; machine_id: string }, preferred = "", server = false, supported = true, phase = "", serverCredits = false, creditPhase = "", cleanup = false, workerUncertain = false, inventoryFields: Record<string, unknown> = {}, quotaError = "", automaticConsent = false, automaticPhase = "uncertain") {
  const machine = newRequestId(), connection = newRequestId(), generation = newRequestId(), inventory = newRequestId();
  const data = { alias: "Quota fixture", type: "subscription", subscription_service: "chatgpt", health: "ready", recovery_notifications: false, connection: { id: connection }, subscription: { automatic_credit_consent: automaticConsent ? { connection_id: connection, generation } : undefined, observation: workerUncertain ? { id: newRequestId(), action: "reset-credit", phase: automaticPhase } : undefined, generation, owner_machine_id: server ? "" : machine, server_quota_generation: server ? generation : undefined, server_credit: creditPhase ? { id: newRequestId(), phase: creditPhase, cleanup_confirmed: cleanup, outcome: "" } : undefined, server_quota: phase ? { id: newRequestId(), phase, error_code: quotaError } : undefined, lease, reset_credits: { observation_id: inventory, observed_at: new Date().toISOString(), available_count: "2", credits: details, ...inventoryFields } } };
  let account = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.ACCOUNT, revision: 1n, schemaVersion: 2, documentJson: encode(data) });
  const request = vi.fn(async (value) => ({ account, operationId: value.mutation?.requestId }));
  const reconcile = vi.fn(async (_value: unknown) => ({ account }));
  const consent = vi.fn(async (value) => { account = create(ResourceSchema, { ...account, revision: account.revision + 1n, documentJson: encode({ ...data, subscription: { ...data.subscription, automatic_credit_consent: value.enabled ? { connection_id: value.connectionId, generation: value.generationId } : undefined } }) }); return { account }; });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.AUTOMATIC_RESET_CREDIT_CONSENT_V1, SystemCapability.SUBSCRIPTION_RESET_CREDITS_V1, ...(serverCredits ? [SystemCapability.SERVER_SUBSCRIPTION_RESET_CREDITS_V1] : []), ...(supported ? [SystemCapability.SERVER_SUBSCRIPTION_QUOTA_V2] : [SystemCapability.SUBSCRIPTION_QUOTA_V1])] }) });
    router.service(SubscriptionService, { requestSubscriptionObservation: request, reconcileSubscriptionCredit: reconcile, setAutomaticResetCreditConsent: consent });
  });
  const queryClient=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 function Harness() {
    const [current, setCurrent] = useState(account), [, setBusy] = useState(false);
    return <QueryClientProvider client={queryClient}><TransportProvider transport={transport}><MutationIntents><SubscriptionQuotaControls current={current} machine={preferred} active accepted={setCurrent} busyChanged={setBusy} /><button onClick={() => { account = create(ResourceSchema, { ...account, revision: account.revision + 1n, documentJson: encode({ ...data, subscription: { ...data.subscription, reset_credits: { ...data.subscription.reset_credits, credits: Array.isArray(details) ? [...details].reverse() : details } } }) }); setCurrent(account); }}>Reorder fixture credits</button><button onClick={() => { account = create(ResourceSchema, { ...account, revision: account.revision + 1n }); setCurrent(account); }}>Change fixture account revision</button><button onClick={() => { account = create(ResourceSchema, { ...account, revision: account.revision + 1n, documentJson: encode({ ...data, subscription: { ...data.subscription, reset_credits: { ...data.subscription.reset_credits, available_count: "0" } } }) }); setCurrent(account); }}>Remove fixture credits</button><button onClick={() => { account = create(ResourceSchema, { ...account, revision: account.revision + 1n, documentJson: encode({ ...data, subscription: { ...data.subscription, reset_credits: { ...data.subscription.reset_credits, credits: [{id:"credit_2",reset_type:"codexRateLimits",status:"available"}] } } }) }); setCurrent(account); }}>Replace fixture credit</button></MutationIntents></TransportProvider></QueryClientProvider>;
  }
  return { Harness, request, reconcile, consent, account, machine, connection, generation, inventory };
}

async function selectExactCredit() {
 const use = await screen.findByRole("button", { name: "Use" });
 await waitFor(() => expect((use as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(use);
 fireEvent.click(screen.getByRole("button", { name: "Select Reset credit 1" }));
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
 if (!next) fireEvent.click(screen.getByRole("button", { name: "Select Reset credit 1" }));
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
  const review=await screen.findByRole("button",{name:"리셋권 1 선택"});
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
 expect(screen.queryByRole("button", { name: "Select Reset credit 1" })).toBeNull();
 fireEvent.click(use); expect(globalThis.document.activeElement).toBe(screen.getByRole("heading", { name: "Credit details" }));
 const select = screen.getByRole("button", { name: "Select Reset credit 1" }); fireEvent.click(select);
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
 try {await i18n.changeLanguage("ko");expect(await screen.findByRole("button",{name:"리셋권 사용 확인"})).toBeTruthy();expect(screen.getByText("리셋권 1 사용")).toBeTruthy();expect(value.request).not.toHaveBeenCalled();}
 finally {await i18n.changeLanguage("en")}
 expect(screen.getByRole("button",{name:"Confirm credit consumption"})).toBeTruthy();
});

it("restores the section heading when background inventory disables the original selection opener",async()=>{
 const value=fixture();render(<value.Harness />);await selectExactCredit();
 fireEvent.click(screen.getByRole("button",{name:"Remove fixture credits"}));
 expect((screen.getByRole("button",{name:"Select Reset credit 1"}) as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByRole("button",{name:"Keep credit"}));
 expect(globalThis.document.activeElement).toBe(screen.getByRole("heading",{name:"Reset credits"}));expect(value.request).not.toHaveBeenCalled();
});
it("restores the section heading instead of an inert original selection opener",async()=>{
 const value=fixture();render(<value.Harness />);await selectExactCredit();
 screen.getByRole("button",{name:"Select Reset credit 1"}).closest(".reset-credit-rows")!.setAttribute("inert","");
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

it.each(["2030", "2030-02-30T00:00:00Z", "", true, 0])("rejects malformed expiry %s for selection and confirmation", async expires_at => {
 const value=fixture([{id:"credit_1",reset_type:"codexRateLimits",status:"available",expires_at}]);render(<value.Harness />);
 await screen.findByText(/Last successful observation/);
 expect((screen.getByRole("button",{name:"Use"}) as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByRole("button",{name:"View details"}));
 expect(screen.queryByRole("button",{name:"Select Reset credit 1"})).toBeNull();
 expect(screen.queryByRole("button",{name:"Confirm credit consumption"})).toBeNull();
 expect(value.request).not.toHaveBeenCalled();
});
it.each([undefined,null,"2999-01-01T00:00:00Z"])("preserves omitted and valid future expiry selection (%s)",async expires_at=>{
 const value=fixture([{id:"credit_1",reset_type:"codexRateLimits",status:"available",expires_at}]);render(<value.Harness />);await selectExactCredit();
 expect((screen.getByRole("button",{name:"Confirm credit consumption"}) as HTMLButtonElement).disabled).toBe(false);
 expect(value.request).not.toHaveBeenCalled();
});

it("restores the heading after another credit replaces the selected row", async () => {
 const value=fixture();render(<value.Harness />);await selectExactCredit();
 const original=screen.getByRole("button",{name:"Select Reset credit 1"});
 fireEvent.click(screen.getByRole("button",{name:"Replace fixture credit"}));
 const replacement=screen.getByRole("button",{name:"Select Reset credit 1"});
 expect((replacement as HTMLButtonElement).disabled).toBe(false);
 expect(original.isConnected).toBe(false);
 expect((screen.getByRole("button",{name:"Confirm credit consumption"}) as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByRole("button",{name:"Keep credit"}));
 expect(globalThis.document.activeElement).toBe(screen.getByRole("heading",{name:"Reset credits"}));
 expect(globalThis.document.activeElement).not.toBe(replacement);
 expect(value.request).not.toHaveBeenCalled();
});

const numberedDetails = [
 {id:"RateLimitResetCredit_fixture_unavailable",reset_type:"codexRateLimits",status:"unavailable",title:"Private native title",description:"Private native description"},
 {id:"RateLimitResetCredit_fixture_a",reset_type:"codexRateLimits",status:"available",expires_at:"2099-01-01T00:00:00Z"},
 {id:"RateLimitResetCredit_fixture_b",reset_type:"codexRateLimits",status:"available",expires_at:"2099-01-01T00:00:00Z"}
];
it.each(["en","ko"])("numbers every returned detail in %s while sending only the original selected identity",async language=>{
 await i18n.changeLanguage(language);
 try {
  const value=fixture(numberedDetails);value.request.mockRejectedValueOnce(new ConnectError("lost acknowledgment",Code.Unavailable));render(<value.Harness />);
  const use=await screen.findByRole("button",{name:language==="en"?"Use":"사용"});await waitFor(()=>expect((use as HTMLButtonElement).disabled).toBe(false));fireEvent.click(use);
  for(let ordinal=1;ordinal<=3;ordinal++) expect(screen.getByText(language==="en"?`Reset credit ${ordinal}`:`리셋권 ${ordinal}`,{selector:"strong"})).toBeTruthy();
  for(const credit of numberedDetails) expect(screen.getByText(`${language==="en"?"ID":"식별자"}: ${credit.id}`)).toBeTruthy();
  expect(screen.queryByText("Private native title")).toBeNull();expect(screen.queryByText("Private native description")).toBeNull();
  expect(screen.queryByRole("button",{name:language==="en"?"Select Reset credit 1":"리셋권 1 선택"})).toBeNull();
  expect(globalThis.document.querySelectorAll(".reset-credit-row time").length).toBe(2);
  fireEvent.click(screen.getByRole("button",{name:language==="en"?"Select Reset credit 3":"리셋권 3 선택"}));
  expect(value.request).not.toHaveBeenCalled();expect(screen.getByText(language==="en"?"Consume reset credit 3":"리셋권 3 사용")).toBeTruthy();
  fireEvent.click(screen.getByRole("button",{name:copy("subscription-quota.confirmCreditConsumption_251822")}));
  fireEvent.click(await screen.findByRole("button",{name:copy("subscription-quota.retryOriginalQuotaOrCreditRequest_071535")}));
  await waitFor(()=>expect(value.request).toHaveBeenCalledTimes(2));expect(value.request.mock.calls[1][0]).toEqual(value.request.mock.calls[0][0]);
  expect(value.request.mock.calls[0][0]).toMatchObject({creditId:numberedDetails[2].id,confirmed:true,nextCredit:false,connectionId:value.connection,generationId:value.generation,creditsObservationId:value.inventory,mutation:{id:value.account.id,expectedRevision:1n}});
 } finally {await i18n.changeLanguage("en")}
});
it("retains captured ordinal and full identity through reorder, replacement, locale and focus changes",async()=>{
 const value=fixture(numberedDetails);render(<value.Harness />);const use=await screen.findByRole("button",{name:"Use"});await waitFor(()=>expect((use as HTMLButtonElement).disabled).toBe(false));fireEvent.click(use);
 fireEvent.click(screen.getByRole("button",{name:"Select Reset credit 3"}));fireEvent.click(screen.getByRole("button",{name:"Reorder fixture credits"}));
 const confirmation=globalThis.document.querySelector(".reset-credit-confirmation")!;
 expect(confirmation.textContent).toContain("Consume reset credit 3");expect(confirmation.textContent).toContain(`ID: ${numberedDetails[2].id}`);
 expect((screen.getByRole("button",{name:"Confirm credit consumption"}) as HTMLButtonElement).disabled).toBe(true);
 expect(screen.getByRole("button",{name:"Select Reset credit 1"})).toBeTruthy();
 try {await i18n.changeLanguage("ko");expect(await screen.findByText("리셋권 3 사용")).toBeTruthy();expect(confirmation.textContent).toContain(numberedDetails[2].id);expect((screen.getByRole("button",{name:"리셋권 사용 확인"}) as HTMLButtonElement).disabled).toBe(true);}
 finally {await i18n.changeLanguage("en")}
 fireEvent.click(screen.getByRole("button",{name:"Replace fixture credit"}));expect(confirmation.textContent).toContain("Consume reset credit 3");expect(confirmation.textContent).toContain(numberedDetails[2].id);
 fireEvent.click(screen.getByRole("button",{name:"Keep credit"}));expect(globalThis.document.activeElement).toBe(screen.getByRole("heading",{name:"Reset credits"}));expect(value.request).not.toHaveBeenCalled();expect(value.reconcile).not.toHaveBeenCalled();
});


it("keeps automatic credits off until exact confirmation and disables them explicitly", async () => {
 const value=fixture();render(<value.Harness />);
 const checkbox=await screen.findByRole("checkbox",{name:"Automatically use a reset credit when subscription quota is exhausted"}) as HTMLInputElement;
 await waitFor(()=>expect(checkbox.disabled).toBe(false));expect(checkbox.checked).toBe(false);
 fireEvent.click(checkbox);expect(value.consent).not.toHaveBeenCalled();
 expect(await screen.findByRole("heading",{name:"Enable automatic reset credits"})).toBeTruthy();
 fireEvent.click(screen.getByRole("button",{name:"Enable automatic reset credits"}));
 await waitFor(()=>expect(value.consent).toHaveBeenCalledTimes(1));
 expect(value.consent.mock.calls[0][0]).toMatchObject({connectionId:value.connection,generationId:value.generation,enabled:true,confirmed:true,mutation:{id:value.account.id,expectedRevision:1n}});
 const enabled=await screen.findByRole("checkbox",{name:"Automatically use a reset credit when subscription quota is exhausted"}) as HTMLInputElement;
 await waitFor(()=>expect(enabled.checked).toBe(true));fireEvent.click(enabled);
 await waitFor(()=>expect(value.consent).toHaveBeenCalledTimes(2));expect(value.consent.mock.calls[1][0]).toMatchObject({enabled:false,confirmed:false});expect(value.request).not.toHaveBeenCalled();
});
it("fences a consent confirmation after account revision replacement", async()=>{
 const value=fixture();render(<value.Harness />);const checkbox=await screen.findByRole("checkbox",{name:"Automatically use a reset credit when subscription quota is exhausted"});await waitFor(()=>expect((checkbox as HTMLInputElement).disabled).toBe(false));fireEvent.click(checkbox);
 fireEvent.click(screen.getByRole("button",{name:"Change fixture account revision"}));
 expect((screen.getByRole("button",{name:"Enable automatic reset credits"}) as HTMLButtonElement).disabled).toBe(true);expect(value.consent).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button",{name:"Keep automatic reset credits off"}));expect((await screen.findByRole("checkbox",{name:"Automatically use a reset credit when subscription quota is exhausted"}) as HTMLInputElement).checked).toBe(false);
});

it.each(["light", "dark"])("localizes automatic credit consent in Korean using the %s theme",async theme=>{
 globalThis.document.documentElement.dataset.theme=theme;
 try {await i18n.changeLanguage("ko");const value=fixture();render(<value.Harness />);const checkbox=await screen.findByRole("checkbox",{name:"구독 한도가 소진되면 리셋 크레딧 자동 사용"});await waitFor(()=>expect((checkbox as HTMLInputElement).disabled).toBe(false));fireEvent.click(checkbox);expect(await screen.findByRole("heading",{name:"리셋 크레딧 자동 사용 켜기"})).toBeTruthy();expect(screen.getByText("서버가 실행 중일 때 동작합니다. 다시 로그인하면 재승인이 필요합니다.")).toBeTruthy();fireEvent.click(screen.getByRole("button",{name:"자동 사용 끄기 유지"}));expect(value.consent).not.toHaveBeenCalled();}
 finally {await i18n.changeLanguage("en");delete globalThis.document.documentElement.dataset.theme;}
});


it.each(["queued","sending","uncertain"])("permits consent revocation while the original automatic credit is %s",async phase=>{
 const value=fixture(undefined,undefined,"",false,true,"",false,"",false,true,{},"",true,phase);render(<value.Harness />);
 const checkbox=await screen.findByRole("checkbox",{name:"Automatically use a reset credit when subscription quota is exhausted"}) as HTMLInputElement;await waitFor(()=>expect(checkbox.disabled).toBe(false));expect(checkbox.checked).toBe(true);
 fireEvent.click(checkbox);await waitFor(()=>expect(value.consent).toHaveBeenCalledTimes(1));expect(value.consent.mock.calls[0][0]).toMatchObject({enabled:false,confirmed:false,connectionId:value.connection,generationId:value.generation});expect(value.request).not.toHaveBeenCalled();expect(value.reconcile).not.toHaveBeenCalled();
});
