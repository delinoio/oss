// SPDX-License-Identifier: Apache-2.0
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { McpManagementService, McpServerSchema, type AuthenticateMcpServerRequest, McpAuthentication, McpAuthenticationState, McpTransport } from "@delinoio/delidev-api-client";
import { McpManagementProvider, useMcpManagement } from "./mcp-management";
import { MutationIntents } from "./mutation";
import { OAuthNativeAction, OAuthNativeProvider } from "./account-oauth";
const definition=create(McpServerSchema,{machineId:"019a4781-4240-7000-8000-000000000001",workerDeviceId:"019a4781-4240-7000-8000-000000000002",definition:{id:"019a4781-4240-7000-8000-000000000003",name:"Original",transport:McpTransport.STREAMABLE_HTTP,endpoint:"https://mcp.example.test/api",authentication:McpAuthentication.OAUTH,enabled:true,revision:1n},authenticationState:McpAuthenticationState.REQUIRED});
function Harness(){const owner=useMcpManagement();return <><button onClick={()=>owner.start(definition)}>Authenticate fixture</button><button onClick={owner.retry}>Retry fixture</button><output>{owner.oauth?.state??"none"}</output></>;}
it("preserves an original OAuth task across disposable views, opens once and joins original native disposal",async()=>{
 const starts:unknown[]=[];const start=vi.fn(async (request:AuthenticateMcpServerRequest)=>{starts.push({...request});if(starts.length===1)throw new ConnectError("Lost original response",Code.Unavailable);return {requestId:request.mutation!.requestId,attemptId:request.mutation!.requestId,state:McpAuthenticationState.PENDING,authorizationUrl:"https://issuer.example.test/authorize",replayed:true};});
 const transport=createRouterTransport(router=>router.service(McpManagementService,{authenticateMcpServer:start}));
 const native=vi.fn(async(_opening:string,action:OAuthNativeAction)=>action===OAuthNativeAction.BeginMcp?{generation:"019a4781-4240-7000-8000-000000000004",callback_url:"http://127.0.0.1:54321/oauth/mcp/callback"}:{generation:"019a4781-4240-7000-8000-000000000004"});
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const view=(visible:boolean)=><TransportProvider transport={transport}><QueryClientProvider client={client}><OAuthNativeProvider control={native}><MutationIntents><McpManagementProvider>{visible?<Harness/>:null}</McpManagementProvider></MutationIntents></OAuthNativeProvider></QueryClientProvider></TransportProvider>;
 const mounted=render(view(true));expect(start).not.toHaveBeenCalled();expect(native).not.toHaveBeenCalled();
 fireEvent.click(screen.getByText("Authenticate fixture"));await waitFor(()=>expect(start).toHaveBeenCalledTimes(1));
 mounted.rerender(view(false));mounted.rerender(view(true));fireEvent.click(screen.getByText("Retry fixture"));await waitFor(()=>expect(start).toHaveBeenCalledTimes(2));
 expect(starts[1]).toEqual(starts[0]);expect(native.mock.calls.filter(call=>call[1]===OAuthNativeAction.BeginMcp)).toHaveLength(1);expect(native.mock.calls.filter(call=>call[1]===OAuthNativeAction.BindOpen)).toHaveLength(1);
 mounted.rerender(view(false));expect(native.mock.calls.filter(call=>call[1]===OAuthNativeAction.Dispose)).toHaveLength(0);
 mounted.unmount();await act(async()=>{});expect(native.mock.calls.filter(call=>call[1]===OAuthNativeAction.Dispose)).toHaveLength(1);
});
