// SPDX-License-Identifier: Apache-2.0
import {useState} from "react";
import {act,fireEvent,render,screen,within} from "@testing-library/react";
import {expect,test,vi} from "vitest";
import {SystemCapability,inheritedHarnessValues,HarnessInheritance} from "@delinoio/delidev-api-client";
import {HarnessDefaultFields,AgentHarnessFields,HarnessSettingsScope} from "./harness-settings";
import {i18n} from "./localization";
import type {Document} from "./documents";
vi.mock("@connectrpc/connect-query",()=>({useQuery:(_query:unknown,input:{filter?:{kind?:number}})=>input.filter?.kind?{data:{resources:[],nextPageToken:""}}:{data:{capabilities:[SystemCapability.HARNESS_DEFAULTS_V1]}}}));
vi.mock("./configuration-fields",()=>({ServiceTierField:()=>null,ResourceChoice:({label,value,change}:{label:string;value:string;change:(s:string)=>void})=><label>{label}<input aria-label={label} value={value} onChange={e=>change(e.target.value)}/></label>}));
function View(){const [data,setData]=useState<Document>({harness_defaults_version:1,harness_defaults:[{harness:"codex",values:inheritedHarnessValues()}],untouched:"original"});return <><HarnessDefaultFields data={data} change={setData} scope={HarnessSettingsScope.Server} active/><output>{JSON.stringify(data)}</output></>}
test("override/reset edits authoritative values and language changes retain draft and focus",async()=>{
 render(<View/>);const source=screen.getByLabelText("Reasoning effort: Setting source");fireEvent.change(source,{target:{value:"override"}});
 const input=screen.getByRole("textbox",{name:"Reasoning effort"});fireEvent.change(input,{target:{value:"high"}});input.focus();
 expect(screen.getAllByText(/Unavailable until verified/).length).toBeGreaterThan(0);
 expect(screen.getByText(/Effective value: high/)).toBeTruthy();
 await act(()=>i18n.changeLanguage("ko"));expect(screen.getByRole("textbox",{name:"추론 노력"})).toBe(input);expect(document.activeElement).toBe(input);expect(input).toHaveProperty("value","high");
 fireEvent.click(screen.getByRole("button",{name:"추론 노력 상속으로 초기화"}));expect(screen.queryByRole("textbox",{name:"추론 노력"})).toBeNull();
 expect(screen.getByText(/"untouched":"original"/)).toBeTruthy();await act(()=>i18n.changeLanguage("en"));
});
test("Agent inherits models in route order and unsupported native functions stay disabled",()=>{
 const change=vi.fn();render(<AgentHarnessFields data={{harness_settings:{version:1,models:[{state:HarnessInheritance.Inherit},{state:HarnessInheritance.Override,value:"model-two"}],values:inheritedHarnessValues()}}} routeCount={2} change={change} active/>);
 const second=screen.getByRole("group",{name:"Source 2 model"});expect(within(second).getByRole("textbox",{name:"Model"})).toHaveProperty("value","model-two");
 fireEvent.click(within(second).getByRole("button",{name:"Reset Model to inherit"}));expect(change.mock.lastCall?.[0].harness_settings.models).toEqual([{state:"inherit"},{state:"inherit"}]);
 expect(screen.queryByLabelText("Subagent model: Setting source")).toBeNull();
});

test("Project reset preserves other defaults and returns to the exact server value",()=>{
 const inherited=inheritedHarnessValues();const change=vi.fn();render(<HarnessDefaultFields scope={HarnessSettingsScope.Project} active change={change} data={{harness_defaults_version:1,untouched:{revision:7},harness_defaults:[{harness:"claude-code",values:inherited},{harness:"codex",values:{...inherited,effort:{state:HarnessInheritance.Override,value:"low"}}}]}}/>);
 fireEvent.click(screen.getByRole("button",{name:"Reset Reasoning effort to inherit"}));const next=change.mock.lastCall?.[0];expect(next.harness_defaults[1].values.effort).toEqual({state:"inherit"});expect(next.harness_defaults[0].harness).toBe("claude-code");expect(next.untouched).toEqual({revision:7});
});
