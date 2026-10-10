// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { InstallationService, ResourceSchema, SystemService, SystemCapability, EntityKind, newRequestId } from "@delinoio/delidev-api-client";
import { LocalConnectionPresentation, LocalConnectionPresentationProvider } from "./local-connection-presentation";
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
 expect(within(candidate).queryByText(original.id)).toBeNull();
 const id = original.id;
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

it("keeps original native update uncertainty and drafts while its presentation host moves", async () => {
  const id = newRequestId(), resource = create(ResourceSchema, { id, kind: EntityKind.UPDATE, schemaVersion: 1, revision: 9007199254740993n, documentJson: encode({ state: "OBSERVED", version: "0.2.0", target: "darwin-arm64" }) });
  let reject!: (reason: unknown) => void;
  const control = vi.fn(async (action: NativeUpdateAction) => action === NativeUpdateAction.Install ? await new Promise<never>((_yes, no) => { reject = no; }) : { operation_id: id, release_version: "0.2.0", phase: NativeUpdatePhase.Prepared });
  const controls = { readContext: async () => ({ current_version: "0.1.0", target: "darwin-arm64" }), control };
  const transport = createRouterTransport(router => { router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SIGNED_UPDATES_V1] }) }); router.service(InstallationService, { checkUpdate: () => ({ update: resource }), getUpdate: () => ({ update: resource }) }); });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const target = document.createElement("div"); document.body.append(target);
  const view = (destination?: HTMLElement) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><LocalConnectionPresentationProvider inline target={destination}><LocalConnectionPresentation><Updates active controls={controls} /></LocalConnectionPresentation></LocalConnectionPresentationProvider></MutationIntents></QueryClientProvider></TransportProvider>;
  const mounted = render(view()); fireEvent.click(screen.getByText("Desktop updates"));
  const check = await screen.findByRole<HTMLButtonElement>("button", { name: "Check for desktop update" }); await vi.waitFor(() => expect(check.disabled).toBe(false)); fireEvent.click(check);
  fireEvent.click(await screen.findByRole("button", { name: "Download verified desktop update" }));
  fireEvent.click(await screen.findByRole("button", { name: "Review and install desktop update" }));
  const original = screen.getByLabelText("Original update ID");
  mounted.rerender(view(target)); expect(screen.getByLabelText("Original update ID")).toBe(original);
  await act(async () => reject(new Error("Unconfirmed original install")));
  await screen.findByText(/original installation response is unconfirmed/);
  mounted.rerender(view());
  expect(screen.getByLabelText("Original update ID")).toBe(original);
  expect(screen.queryByRole("button", { name: "Review and install desktop update" })).toBeNull();
  expect(control.mock.calls.filter(([action]) => action === NativeUpdateAction.Install)).toHaveLength(1);
  mounted.unmount(); target.remove(); client.clear();
});

it("keeps direct app updates unchecked until authoritative success and preserves inspect-only recovery across slot moves",async()=>{
 const id=newRequestId(),resource=create(ResourceSchema,{id,kind:EntityKind.UPDATE,schemaVersion:1,revision:9007199254740993n,documentJson:encode({state:'OBSERVED',version:'0.2.0',target:'darwin-arm64'})});
 const check=vi.fn(()=>({update:resource}));const control=vi.fn(async(action:NativeUpdateAction)=>{if(action===NativeUpdateAction.Install)throw new Error('Lost installation reply');return {operation_id:id,release_version:'0.2.0',phase:action===NativeUpdateAction.Inspect?NativeUpdatePhase.Uncertain:NativeUpdatePhase.Prepared};});
 const controls={readContext:async()=>({current_version:'0.1.0',target:'darwin-arm64'}),control};
 const transport=createRouterTransport(router=>{router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SIGNED_UPDATES_V1]})});router.service(InstallationService,{checkUpdate:check,getUpdate:()=>({update:resource})});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}}),slot=document.createElement('div');document.body.append(slot);
 const view=(active=true,destination?:HTMLElement)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><LocalConnectionPresentationProvider inline target={destination}><LocalConnectionPresentation><Updates direct active={active} controls={controls}/></LocalConnectionPresentation></LocalConnectionPresentationProvider></MutationIntents></QueryClientProvider></TransportProvider>;
 const mounted=render(view());await screen.findByText('Updates have not been checked yet.');expect(check).not.toHaveBeenCalled();expect(control).not.toHaveBeenCalled();expect(screen.getByText('Update recovery').closest('details')!.open).toBe(false);
 const button=screen.getByRole<HTMLButtonElement>('button',{name:'Check for updates'});await vi.waitFor(()=>expect(button.disabled).toBe(false));fireEvent.click(button);
 await screen.findByText('An update is available.');fireEvent.click(screen.getByRole('button',{name:'Download verified desktop update'}));fireEvent.click(await screen.findByRole('button',{name:'Review and install desktop update'}));await screen.findByText(/original installation response is unconfirmed/);
 const draft=screen.getByLabelText<HTMLInputElement>('Original update ID');expect(draft.value).toBe(id);expect(draft.type).toBe('password');expect(button.disabled).toBe(true);fireEvent.click(button);expect(check).toHaveBeenCalledTimes(1);expect(draft.disabled).toBe(true);expect(screen.getByText('Update recovery').closest('details')!.open).toBe(true);
 mounted.rerender(view(false,slot));mounted.rerender(view(true));expect(screen.getByLabelText('Original update ID')).toBe(draft);expect(screen.queryByRole('button',{name:'Review and install desktop update'})).toBeNull();
 fireEvent.click(screen.getByRole('button',{name:'Inspect original desktop installation'}));await screen.findByText('Desktop installation: uncertain');expect(control.mock.calls.filter(([action])=>action===NativeUpdateAction.Install)).toHaveLength(1);expect(check).toHaveBeenCalledTimes(1);
 mounted.unmount();client.clear();slot.remove();
});

it("reports no candidate only after a completed check and retries the exact uncertain check",async()=>{
 let fail=true;
 const check=vi.fn((request)=>{if(fail)throw new ConnectError('Unknown original response',Code.Unavailable);return {requestId:request.requestId};});
 const transport=createRouterTransport(router=>{router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SIGNED_UPDATES_V1]})});router.service(InstallationService,{checkUpdate:check});});
 const controls={readContext:async()=>({current_version:'0.1.0',target:'darwin-arm64'}),control:vi.fn()};
 render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><MutationIntents><Updates direct active controls={controls}/></MutationIntents></QueryClientProvider></TransportProvider>);
 const button=screen.getByRole<HTMLButtonElement>('button',{name:'Check for updates'});await vi.waitFor(()=>expect(button.disabled).toBe(false));expect(screen.queryByText('The completed check found no newer signed update.')).toBeNull();
 fireEvent.click(button);await screen.findByRole('button',{name:'Retry the same update check'});expect(button.disabled).toBe(true);
 fail=false;fireEvent.click(screen.getByRole('button',{name:'Retry the same update check'}));await screen.findByText('The completed check found no newer signed update.');expect(check).toHaveBeenCalledTimes(2);expect(check.mock.calls[1][0].requestId).toBe(check.mock.calls[0][0].requestId);expect(controls.control).not.toHaveBeenCalled();
});
