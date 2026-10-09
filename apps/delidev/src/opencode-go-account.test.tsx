// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountService, ConfigurationService, SystemService, SystemCapability, ResourceSchema, EntityKind, newRequestId } from "@delinoio/delidev-api-client";
import { SettingsLifetime } from "./settings-lifetime";
import { MutationIntents } from "./mutation";
import { OpenCodeGoAccount, OpenCodeGoManagement } from "./opencode-go-account";
import { document, encode } from "./documents";

function fixture(supported = true, management = false) {
 let current = create(ResourceSchema, {id:newRequestId(),kind:EntityKind.ACCOUNT,schemaVersion:2,revision:1n,documentJson:encode({alias:"OpenCode Go",type:"subscription",subscription_service:"opencode_go"})});
 const save=vi.fn(async request=> { current=create(ResourceSchema,{...current,documentJson:request.documentJson});return {requestId:request.mutation.requestId,resource:current}; });
 const connect=vi.fn(async request=> {current=create(ResourceSchema,{...current,revision:2n,documentJson:encode({...document(current),connection:{id:request.mutation.requestId},health:"ready"})});return {requestId:request.mutation.requestId,account:current};});
 const read=vi.fn(async ()=>({account:current}));const close=vi.fn();
 const transport=createRouterTransport(router=>{router.service(SystemService,{getStatus:()=>({capabilities:supported?[SystemCapability.OPENCODE_GO_SUBSCRIPTIONS_V1]:[]})});router.service(ConfigurationService,{saveConfiguration:save});router.service(AccountService,{connectAccount:connect,getAccountStatus:read});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 function Body(){const [visible,setVisible]=useState(true);return <><button onClick={()=>setVisible(true)}>Show original</button>{management ? <OpenCodeGoManagement initial={current} active visible={visible} close={()=>{close();setVisible(false);}}/> : <OpenCodeGoAccount active visible={visible} changed={()=>{}} close={()=>{close();setVisible(false);}}/>}</>;}
 const view=<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SettingsLifetime>{()=> <Body/>}</SettingsLifetime></MutationIntents></QueryClientProvider></TransportProvider>;
 return {view,save,connect,read,close};
}
it("connects one Go/Go Plus identity with a masked key and no native login or quota actions",async()=>{
 const f=fixture();render(f.view);const key=await screen.findByLabelText("API key");await waitFor(()=>expect((key as HTMLInputElement).disabled).toBe(false));
 await waitFor(()=>expect(window.document.activeElement).toBe(key));
 expect((key as HTMLInputElement).type).toBe("password");expect(screen.getByLabelText("Account name").getAttribute("value")).toBe("OpenCode Go");
 fireEvent.click(screen.getByRole("button",{name:"Reveal API key"}));expect((key as HTMLInputElement).type).toBe("text");
 fireEvent.change(key,{target:{value:"synthetic-go-key"}});fireEvent.click(screen.getByRole("button",{name:"Connect account"}));
 await waitFor(()=>expect(f.close).toHaveBeenCalledTimes(1));expect(f.save).toHaveBeenCalledTimes(1);expect(f.connect).toHaveBeenCalledTimes(1);
 expect(document((await f.save.mock.results[0].value).resource).subscription_service).toBe("opencode_go");
 expect(new TextDecoder().decode(f.connect.mock.calls[0][0].apiKey)).toBe("synthetic-go-key");
 expect(screen.queryByRole("button",{name:/Refresh quota|Reset credits|Open browser/})).toBeNull();
});
it("retains an uncertain original create across dismissal without a replacement account",async()=>{
 const f=fixture(),original=f.save.getMockImplementation()!;f.save.mockImplementationOnce(async request=>{await original(request);throw new ConnectError("Lost fixture acknowledgment",Code.Unavailable);});
 render(f.view);const key=await screen.findByLabelText("API key");await waitFor(()=>expect((key as HTMLInputElement).disabled).toBe(false));fireEvent.change(key,{target:{value:"synthetic-go-key"}});fireEvent.click(screen.getByRole("button",{name:"Connect account"}));
 const retry=await screen.findByRole("button",{name:"Retry the original request"});expect(f.connect).not.toHaveBeenCalled();const originalID=f.save.mock.calls[0][0].mutation.requestId;
 fireEvent.keyDown(screen.getByRole("dialog"),{key:"Escape"});fireEvent.click(screen.getByRole("button",{name:"Show original"}));
 fireEvent.click(await screen.findByRole("button",{name:"Retry the original request"}));await waitFor(()=>expect(f.connect).toHaveBeenCalledTimes(1));expect(f.save.mock.calls[1][0].mutation.requestId).toBe(originalID);expect(f.save).toHaveBeenCalledTimes(2);
});
it("disables connection on an older server without submitting a key",async()=>{const f=fixture(false);render(f.view);await screen.findByText("This server does not support OpenCode Go connections.");expect((screen.getByRole("button",{name:"Connect account"}) as HTMLButtonElement).disabled).toBe(true);expect(f.save).not.toHaveBeenCalled();expect(f.connect).not.toHaveBeenCalled();});

it("retains dismissal on failed management reads without granting account authority",async()=>{
 const f=fixture(true,true);f.read.mockImplementation(async()=>{throw new ConnectError("Fixture denied",Code.PermissionDenied);});render(f.view);
 const dialog=await screen.findByRole("dialog",{name:"Manage OpenCode Go connection"});
 await screen.findByRole("alert");fireEvent.click(screen.getByRole("button",{name:"Close Manage OpenCode Go connection"}));
 expect(f.close).toHaveBeenCalledTimes(1);expect(f.connect).not.toHaveBeenCalled();expect(f.save).not.toHaveBeenCalled();
});
