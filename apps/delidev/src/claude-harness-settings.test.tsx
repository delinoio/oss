// SPDX-License-Identifier: Apache-2.0
import {useState} from "react";
import {act,fireEvent,render,screen} from "@testing-library/react";
import {create} from "@bufbuild/protobuf";
import {expect,test,vi} from "vitest";
import {EntityKind,ResourceSchema,SystemCapability,inheritedHarnessValues,HarnessInheritance,newRequestId,effectiveHarnessSelection,type Resource} from "@delinoio/delidev-api-client";
import {HarnessDefaultFields,AgentHarnessFields,HarnessSettingsScope,HarnessSettingsHarness} from "./harness-settings";
import {encode,type Document} from "./documents";
import {i18n} from "./localization";
const read=vi.hoisted(()=>({rows:[] as Resource[],failed:false}));
vi.mock("@connectrpc/connect-query",()=>({useQuery:(_query:unknown,input:{filter?:unknown})=>input.filter?{data:{resources:read.rows,nextPageToken:""},error:read.failed?new Error("Connection unavailable"):undefined}:{data:{capabilities:[SystemCapability.HARNESS_DEFAULTS_V1,SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1,SystemCapability.CODEX_APPROVAL_REVIEW_V1]}}}));
vi.mock("./configuration-fields",()=>({ClaudePermission:{Default:"default",Plan:"plan",AcceptEdits:"acceptEdits",DontAsk:"dontAsk",Bypass:"bypassPermissions",Auto:"auto"},ServiceTierField:()=>null,ResourceChoice:({label,value,change}:{label:string;value:string;change:(s:string)=>void})=><label>{label}<input aria-label={label} value={value} onChange={e=>change(e.target.value)}/></label>}));
const values=inheritedHarnessValues();
function server(effort:string){return create(ResourceSchema,{kind:EntityKind.SETTINGS,id:newRequestId(),revision:2n,schemaVersion:4,documentJson:encode({harness_defaults_version:1,harness_defaults:[{harness:"claude-code",values:{...values,effort:{state:HarnessInheritance.Override,value:effort}}}]})});}
function Agent({active=true}:{active?:boolean}){const [data,change]=useState<Document>({harness:"claude-code",managed_skills:{original:"unchanged"},harness_settings:{version:1,models:[{state:"inherit"}],values:{...values,effort:{state:"override",value:"low"},service_tier:{state:"override",value:"foreign-tier"}}}});return <><AgentHarnessFields harness={HarnessSettingsHarness.Claude} active={active} data={data} change={change} sources={[{subscription_service:"claude"}]}/><output>{JSON.stringify(data)}</output></>}
test("Claude hierarchy uses original harness/source and exact project precedence",()=>{
 const global=[{harness:"claude-code",values:{...values,effort:{state:HarnessInheritance.Override,value:"low"}}},{harness:"claude-code",provider_id:"original",api_protocol:"anthropic-messages",values:{...values,effort:{state:HarnessInheritance.Override,value:"high"}}}];
 const project=[{harness:"claude-code",values:{...values,effort:{state:HarnessInheritance.Override,value:"medium"}}}];
 expect(effectiveHarnessSelection("effort","claude-code",{provider_id:"original",api_protocol:"anthropic-messages"},global,project)).toEqual({selection:{state:"override",value:"medium"},source:"project"});
 expect(effectiveHarnessSelection("effort","claude-code",{},global,project,{...values,effort:{state:HarnessInheritance.Override,value:"max"}})).toEqual({selection:{state:"override",value:"max"},source:"agent"});
 expect(effectiveHarnessSelection("effort","codex",{},global,project).selection.state).toBe("inherit");
});
test("Claude reset uses current inherited server value and retains draft across reconnect and locale",async()=>{
 read.rows=[server("high")];read.failed=false;const view=render(<Agent/>);
 expect(screen.getByText(/Effective value: low · Source: Agent/)).toBeTruthy();
 expect(screen.queryByLabelText("Subagent model: Setting source")).toBeNull();expect(screen.queryByLabelText("Approval reviewer: Setting source")).toBeNull();
 expect(screen.getByLabelText("Service tier: Setting source")).toHaveProperty("disabled",true);
 read.failed=true;view.rerender(<Agent active={false}/>);expect(screen.getByText("Inherited server values are unavailable. Your draft is retained.")).toBeTruthy();
 expect(screen.getByRole("textbox",{name:"Reasoning effort"})).toHaveProperty("value","low");
 read.rows=[server("medium")];read.failed=false;view.rerender(<Agent/>);fireEvent.click(screen.getByRole("button",{name:"Reset Reasoning effort to inherit"}));
 expect(screen.getByText(/Effective value: medium · Source: Server/)).toBeTruthy();
 fireEvent.change(screen.getByLabelText("Claude permission mode: Setting source"),{target:{value:"override"}});const permission=screen.getByRole("combobox",{name:"Claude permission mode"});fireEvent.change(permission,{target:{value:"acceptEdits"}});permission.focus();
 await act(()=>i18n.changeLanguage("ko"));expect(document.activeElement).toBe(permission);expect(permission).toHaveProperty("value","acceptEdits");
 expect(screen.getAllByRole("status").map(node=>node.textContent).join(" ")).toContain("사용");expect(screen.getByText(/"managed_skills":\{"original":"unchanged"\}/)).toBeTruthy();await act(()=>i18n.changeLanguage("en"));
});
test("Claude Project overrides preserve hidden Codex defaults and explicit unrelated fields",()=>{
 read.rows=[server("high")];read.failed=false;const change=vi.fn();render(<HarnessDefaultFields harness={HarnessSettingsHarness.Claude} scope={HarnessSettingsScope.Project} active change={change} data={{harness_defaults_version:1,hidden:{owner:7},harness_defaults:[{harness:"codex",values},{harness:"claude-code",values:{...values,effort:{state:"override",value:"low"}}}]}}/>);
 fireEvent.click(screen.getByRole("button",{name:"Reset Reasoning effort to inherit"}));const next=change.mock.lastCall?.[0];expect(next.harness_defaults[0]).toEqual({harness:"codex",values});expect(next.hidden).toEqual({owner:7});expect(next.harness_defaults[1].values.effort).toEqual({state:"inherit"});
});
