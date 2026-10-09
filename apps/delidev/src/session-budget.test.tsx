import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { BudgetState, BudgetEvidenceSchema, EstimatedCostBudgetSchema, EntityKind, ResourceSchema, SessionBudgetViewSchema, SessionService, UsageCoverage, newRequestId, type SetSessionBudgetRequest } from "@delinoio/delidev-api-client";
import { SessionBudget } from "./session-budget";
import { MutationIntents } from "./mutation";
import { encode } from "./documents";
function fixture(){
 const resource=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,revision:1n,documentJson:encode({name:"Budget session",archive:"archived",dispatch:"paused"})});
 let view=create(SessionBudgetViewSchema,{session:resource,state:BudgetState.DISABLED,coverage:UsageCoverage.OBSERVED_ROOT_RESPONSES});
 const read=vi.fn(()=>({view}));
 const write=vi.fn(async (request:SetSessionBudgetRequest)=>{
  view=create(SessionBudgetViewSchema,{...view,session:{...resource,revision:2n},budget:request.change.case==="budget"?request.change.value:undefined,state:request.change.case==="budget"?BudgetState.ALLOW_INCOMPLETE:BudgetState.DISABLED});return {view};
 });
 const transport=createRouterTransport(router=>router.service(SessionService,{getSessionBudget:read,setSessionBudget:write}));
 const client=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity}}});
 const changed=vi.fn(),blocked=vi.fn();
 const renderView=()=> <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionBudget resource={resource} changed={changed} blocked={blocked}/></MutationIntents></QueryClientProvider></TransportProvider>;
 return {client,resource,read,write,changed,blocked,renderView,get view(){return view;},set view(value){view=value;}};
}
it("sets an exact optional budget and exposes unavailable evidence without claiming zero or compliance",async()=>{
 const f=fixture();render(f.renderView());await screen.findByText("No estimated-cost budget is configured.");
 fireEvent.click(screen.getByRole("button",{name:"Edit session budget"}));fireEvent.click(screen.getByRole("checkbox",{name:"Enable estimated-cost budget"}));
 fireEvent.change(screen.getByLabelText("Budget currency"),{target:{value:"USD"}});fireEvent.change(screen.getByLabelText("Estimated-cost threshold"),{target:{value:"0.000000000000001"}});
 fireEvent.click(screen.getByRole("button",{name:"Save session budget"}));await screen.findByText(/Known lifetime subtotal: Unavailable/);
 expect(f.write.mock.calls[0][0]).toMatchObject({mutation:{id:f.resource.id,expectedRevision:1n},change:{case:"budget",value:{currency:"USD",threshold:"0.000000000000001"}}});
 expect(screen.getByText(/does not verify budget compliance/)).toBeTruthy();expect(f.changed).toHaveBeenCalledTimes(1);expect(f.blocked).toHaveBeenLastCalledWith(false);
});
it("announces a reached threshold and preserves the original uncertain mutation across a changed session",async()=>{
 const f=fixture();f.view=create(SessionBudgetViewSchema,{...f.view,budget:create(EstimatedCostBudgetSchema,{currency:"USD",threshold:"1"}),state:BudgetState.THRESHOLD_REACHED,selectedCurrency:create(BudgetEvidenceSchema,{currency:"USD",knownAmount:"1",partialResponses:1n}),unpricedResponses:2n,otherCurrencyResponses:3n});render(f.renderView());
 await screen.findByText(/Budget threshold reached/);expect(f.blocked).toHaveBeenLastCalledWith(true);
 fireEvent.click(screen.getByRole("button",{name:"Edit session budget"}));fireEvent.change(screen.getByLabelText("Estimated-cost threshold"),{target:{value:"2"}});
 f.write.mockRejectedValueOnce(new ConnectError("Lost result",Code.Unavailable));fireEvent.click(screen.getByRole("button",{name:"Save session budget"}));await screen.findByRole("button",{name:"Retry the same budget"});
 const original=f.write.mock.calls[0][0];f.view=create(SessionBudgetViewSchema,{...f.view,session:{...f.resource,revision:5n}});
 void f.client.invalidateQueries({refetchType:"active"});await screen.findByText(/The session changed/);
 expect((screen.getByLabelText("Estimated-cost threshold") as HTMLInputElement).value).toBe("2");expect(screen.queryByRole("button",{name:"Use latest revision with this draft"})).toBeNull();
 fireEvent.click(screen.getByRole("button",{name:"Retry the same budget"}));await waitFor(()=>expect(f.write).toHaveBeenCalledTimes(2));expect(f.write.mock.calls[1][0]).toEqual(original);
});
it("requires explicit rebasing of a retained stale draft and sends an explicit removal",async()=>{
 const f=fixture();f.view=create(SessionBudgetViewSchema,{...f.view,budget:create(EstimatedCostBudgetSchema,{currency:"USD",threshold:"1"}),state:BudgetState.ALLOW_INCOMPLETE});render(f.renderView());
 await screen.findByText(/Known lifetime subtotal: Unavailable/);fireEvent.click(screen.getByRole("button",{name:"Edit session budget"}));fireEvent.click(screen.getByRole("checkbox",{name:"Enable estimated-cost budget"}));
 f.view=create(SessionBudgetViewSchema,{...f.view,session:{...f.resource,revision:3n}});void f.client.invalidateQueries({refetchType:"active"});await screen.findByText(/The session changed/);
 expect((screen.getByRole("button",{name:"Save session budget"}) as HTMLButtonElement).disabled).toBe(true);expect(f.write).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole("button",{name:"Use latest revision with this draft"}));fireEvent.click(screen.getByRole("button",{name:"Save session budget"}));await screen.findByText("No estimated-cost budget is configured.");
 expect(f.write.mock.calls[0][0]).toMatchObject({mutation:{expectedRevision:3n},change:{case:"remove",value:true}});
});

it.each([UsageCoverage.UNSPECIFIED, UsageCoverage.OBSERVED_ROOT_RESPONSES, 99])("keeps native budget evidence unavailable for unsupported coverage %s", async (coverage) => {
 const f=fixture();f.view=create(SessionBudgetViewSchema,{...f.view,coverage:coverage as UsageCoverage,budget:create(EstimatedCostBudgetSchema,{currency:"USD",threshold:"1"}),state:BudgetState.ALLOW_INCOMPLETE});render(f.renderView());
 await screen.findByText("Native budget evidence is unavailable from this server version. Update the server to view it.");
 expect(screen.queryByText(/native units with complete categories/)).toBeNull();
});

it("renders measured zero native units only with native accounting coverage", async () => {
 const f=fixture();f.view=create(SessionBudgetViewSchema,{...f.view,coverage:UsageCoverage.OBSERVED_ROOT_ACCOUNTING_UNITS,budget:create(EstimatedCostBudgetSchema,{currency:"USD",threshold:"1"}),state:BudgetState.ALLOW_INCOMPLETE});render(f.renderView());
 await screen.findByText(/0 native units with complete categories/);
 expect(screen.queryByText(/Native budget evidence is unavailable/)).toBeNull();
});
