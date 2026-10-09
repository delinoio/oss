// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, SystemService, SystemCapability, FailureCode, clientFailure, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { SessionTerminals } from "./session-terminals";
import { ReadStage } from "./scroll-pagination";
const pagination=vi.hoisted(()=>({value:{} as Record<string,unknown>}));
vi.mock("./conversation-pagination",()=>({useConversationPages:()=>pagination.value}));
const session=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,schemaVersion:1,revision:1n,documentJson:encode({archive:"active"})});
function fixture(stage:ReadStage|undefined,loaded=true,expired=false,failed=expired){
 const retry=vi.fn(),reload=vi.fn();
 pagination.value={loading:stage,loaded,error:failed?{failure:{...clientFailure(new ConnectError("synthetic",Code.Unavailable)),...(expired?{code:FailureCode.CursorExpired}:{})},stage:ReadStage.Refresh,token:"original"}:undefined,rows:[],pages:[],data:loaded?{resources:[]}:undefined,nextPageToken:"",isFetching:Boolean(stage),refresh:vi.fn(),refetch:vi.fn(),append:vi.fn(),retry,reload};
 const transport=createRouterTransport(router=>router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SESSION_TERMINALS_V1]})}));
 const view=render(<QueryClientProvider client={new QueryClient()}><TransportProvider transport={transport}><MutationIntents><SessionTerminals session={session} close={()=>{}}/></MutationIntents></TransportProvider></QueryClientProvider>);
 return {...view,retry,reload};
}
it.each([ReadStage.Initial,ReadStage.Additional,ReadStage.Refresh,ReadStage.Reload,ReadStage.Restore])("preserves terminal %s guidance except the already-loaded refresh row",async stage=>{
 const view=fixture(stage);await screen.findByRole("button",{name:"Create terminal"});
 const continuation=view.container.querySelector(".sidebar-continuation")!;
 expect(Boolean(continuation.querySelector('[role="status"]'))).toBe(stage!==ReadStage.Refresh);
});
it("keeps initial loading even before a Refresh-tagged observation has loaded",()=>{
 const view=fixture(ReadStage.Refresh,false);expect(view.container.querySelector('.sidebar-continuation [role="status"]')).not.toBeNull();
});
it.each([false,true])("preserves failure guidance and the original retry/reload owner (expired=%s)",async expired=>{
 const view=fixture(undefined,true,expired,true);expect(view.container.querySelector('.sidebar-continuation [role="status"]')).not.toBeNull();
 const action=screen.getByRole("button",{name:expired?/Reload/:/^Retry$/});await waitFor(()=>expect((action as HTMLButtonElement).disabled).toBe(false));fireEvent.click(action);
 expect(expired?view.reload:view.retry).toHaveBeenCalledOnce();expect(expired?view.retry:view.reload).not.toHaveBeenCalled();
});
