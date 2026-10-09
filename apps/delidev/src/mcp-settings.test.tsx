// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { McpServerSchema, McpAuthentication, McpAuthenticationState, McpTransport, SystemCapability } from "@delinoio/delidev-api-client";
import { McpSettings } from "./mcp-settings";
import { McpSelectionFields, mcpSelections } from "./mcp-selection";
import { i18n } from "./localization";
const mocks=vi.hoisted(()=>({send:vi.fn(),start:vi.fn(),cancel:vi.fn(),refetch:vi.fn(),rows:[] as unknown[],uncertain:false,catalog:vi.fn()}));
vi.mock("@connectrpc/connect-query",()=>({useQuery:()=>({data:{capabilities:[64]},error:undefined})}));
vi.mock("./configuration-fields",()=>({ResourceChoice:({label,value,change}:{label:string;value:string;change:(value:string)=>void})=><label>{label}<select value={value} onChange={event=>change(event.target.value)}><option value=""/><option value="worker-one">One</option><option value="worker-two">Two</option></select></label>}));
vi.mock("./mcp-management",()=>({McpPending:()=>null,useMcpCatalog:(machine:string)=>{mocks.catalog(machine);return {data:machine?{servers:mocks.rows}:undefined,error:undefined,isLoading:false,isFetching:false,refetch:mocks.refetch}},useMcpManagement:()=>({mutation:{busy:false,uncertain:mocks.uncertain,send:mocks.send},nativeAvailable:true,start:mocks.start,cancelServer:mocks.cancel})}));
beforeEach(()=>{mocks.send.mockReset();mocks.start.mockReset();mocks.rows=[];mocks.uncertain=false;mocks.catalog.mockClear();});
function fixture(name="Selected",refs:string[]=[]){return create(McpServerSchema,{machineId:"worker-one",workerDeviceId:"device-one",definition:{id:"definition-one",name,transport:McpTransport.STREAMABLE_HTTP,endpoint:"https://mcp.example.test/api",authentication:McpAuthentication.MANUAL,enabled:true,revision:7n},authenticationState:McpAuthenticationState.READY,agentIds:refs});}
it("opens as read-only, switches exact Runner query scope and identifies deletion references",()=>{
 mocks.rows=[fixture("Selected",["agent-reference"])];render(<McpSettings active/>);
 expect(mocks.send).not.toHaveBeenCalled();expect(mocks.start).not.toHaveBeenCalled();
 fireEvent.change(screen.getByLabelText("Runner device"),{target:{value:"worker-one"}});
 expect(screen.getByText("agent-reference")).toBeTruthy();expect(screen.getByRole("button",{name:"Delete MCP server"}).hasAttribute("disabled")).toBe(true);
 expect(screen.getByText("Codex: Unsupported")).toBeTruthy();expect(screen.queryByDisplayValue("private-fixture-secret")).toBeNull();
 fireEvent.change(screen.getByLabelText("Runner device"),{target:{value:"worker-two"}});expect(mocks.catalog).toHaveBeenLastCalledWith("worker-two");expect(mocks.send).not.toHaveBeenCalled();
});
it("binds enablement to the original Worker revision and freezes replacement after uncertainty",()=>{
 mocks.rows=[fixture()];const view=render(<McpSettings active/>);fireEvent.change(screen.getByLabelText("Runner device"),{target:{value:"worker-one"}});
 fireEvent.click(screen.getByRole("button",{name:"Disable"}));const request=mocks.send.mock.calls[0][0];expect(request.machineId).toBe("worker-one");expect(request.mutation.id).toBe("definition-one");expect(request.mutation.expectedRevision).toBe(7n);expect(request.enabled).toBe(false);
 mocks.uncertain=true;view.rerender(<McpSettings active/>);expect(screen.getByRole("button",{name:"Disable"}).hasAttribute("disabled")).toBe(true);
});
it("retains imported references without reads or writes and explicitly clears selection",async()=>{
 const change=vi.fn();const data={mcp_selections:{rebinding_required:true,selections:[{machine_id:"worker-one",device_id:"device-one",server_id:"definition-one",revision:"7"}]}};
 expect(mcpSelections(data)?.[0].expectedRevision).toBe(7n);expect(mcpSelections({})).toBeUndefined();render(<McpSelectionFields data={data} change={change} active disabled={false}/>);
 expect(change).not.toHaveBeenCalled();fireEvent.click(screen.getByRole("button",{name:"Remove selection"}));expect(change.mock.calls[0][0].mcp_selections).toEqual({selections:[]});
 await i18n.changeLanguage("ko");expect(screen.getByRole("button",{name:"선택 제거"})).toBeTruthy();
});
