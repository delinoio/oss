// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { InstallationService, ResourceSchema, SystemService, SystemCapability, EntityKind, newRequestId } from "@delinoio/delidev-api-client";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";
import { Updates, NativeUpdateAction, NativeUpdatePhase } from "./updates";
function fixture(supported=true, revision=1n, id=newRequestId()){const resource=create(ResourceSchema,{id,kind:EntityKind.UPDATE,schemaVersion:1,revision,documentJson:encode({state:"OBSERVED",version:"0.2.0",target:"darwin-arm64"})});const control=vi.fn(async(action:NativeUpdateAction, _id: string, _revision: bigint)=>{if(action===NativeUpdateAction.Install)throw new Error("Lost native outcome");return {operation_id:id,release_version:"0.2.0",phase:action===NativeUpdateAction.Inspect?NativeUpdatePhase.Uncertain:NativeUpdatePhase.Prepared}});const check=vi.fn(()=>({update:resource}));const transport=createRouterTransport(r=>{r.service(SystemService,{getStatus:()=>({capabilities:supported?[SystemCapability.SIGNED_UPDATES_V1]:[]})});r.service(InstallationService,{checkUpdate:check,getUpdate:()=>({update:resource})})});render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}})}><MutationIntents><Updates active controls={{readContext:async()=>({current_version:"0.1.0",target:"darwin-arm64"}),control}} /></MutationIntents></QueryClientProvider></TransportProvider>);fireEvent.click(screen.getByText("Desktop updates"));return {control,check,id};}
it("gates all update effects on negotiated support",async()=>{const f=fixture(false);await screen.findByText("This server or Worker requires an update to support signed updates.");expect(f.control).not.toHaveBeenCalled();expect(f.check).not.toHaveBeenCalled()});
it("keeps a lost install outcome inspect-only without repeating installation",async()=>{const f=fixture();const check=await screen.findByRole("button",{name:"Check for desktop update"});await vi.waitFor(()=>expect((check as HTMLButtonElement).disabled).toBe(false));fireEvent.click(check);fireEvent.click(await screen.findByRole("button",{name:"Download verified desktop update"}));fireEvent.click(await screen.findByRole("button",{name:"Review and install desktop update"}));await screen.findByText(/original installation response is unconfirmed/);expect(screen.queryByRole("button",{name:"Review and install desktop update"})).toBeNull();fireEvent.click(screen.getByRole("button",{name:"Inspect original desktop installation"}));await screen.findByText("Desktop installation: uncertain");expect(f.control.mock.calls.filter(([action])=>action===NativeUpdateAction.Install)).toHaveLength(1);expect(screen.queryByRole("button",{name:"Review and install desktop update"})).toBeNull()});

it("inspects the exact retained local installation without live negotiated support", async () => {
  const f = fixture(false);
  await screen.findByText("This server or Worker requires an update to support signed updates.");
  fireEvent.change(screen.getByLabelText("Retained desktop update ID"), { target: { value: f.id } });
  fireEvent.change(screen.getByLabelText("Retained desktop update revision"), { target: { value: "9007199254740993" } });
  fireEvent.click(screen.getByRole("button", { name: "Inspect retained local installation" }));
  await screen.findByText("Retained desktop 0.2.0: uncertain");
  expect(f.control).toHaveBeenCalledExactlyOnceWith(NativeUpdateAction.Inspect, f.id, 9007199254740993n);
  expect(f.check).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Review and install desktop update" })).toBeNull();
});


it("preserves the exact desktop recovery pair for a later unsupported-server visit", async () => {
 const original = fixture(true, 9007199254740993n);
 const check = await screen.findByRole("button", { name: "Check for desktop update" });
 await vi.waitFor(() => expect((check as HTMLButtonElement).disabled).toBe(false));
 fireEvent.click(check);
 const candidate = await screen.findByRole("region", { name: "Original update" });
 const id = within(candidate).getByText(original.id).textContent!;
 const revision = within(candidate).getByText("9007199254740993").textContent!;
 fireEvent.click(within(candidate).getByRole("button", { name: "Download verified desktop update" }));
 fireEvent.click(await within(candidate).findByRole("button", { name: "Review and install desktop update" }));
 await screen.findByText(/original installation response is unconfirmed/);
 cleanup();
 const recovery = fixture(false, 9007199254740993n, id);
 await screen.findByText("This server or Worker requires an update to support signed updates.");
 fireEvent.change(screen.getByLabelText("Retained desktop update ID"), { target: { value: id } });
 fireEvent.change(screen.getByLabelText("Retained desktop update revision"), { target: { value: revision } });
 fireEvent.click(screen.getByRole("button", { name: "Inspect retained local installation" }));
 await screen.findByText("Retained desktop 0.2.0: uncertain");
 expect(recovery.control).toHaveBeenCalledExactlyOnceWith(NativeUpdateAction.Inspect, id, 9007199254740993n);
 expect(recovery.check).not.toHaveBeenCalled();
 expect(original.control.mock.calls.filter(([action]) => action === NativeUpdateAction.Install)).toHaveLength(1);
});
