// SPDX-License-Identifier: Apache-2.0
import {create} from "@bufbuild/protobuf";
import {Code,ConnectError,createRouterTransport} from "@connectrpc/connect";
import {TransportProvider} from "@connectrpc/connect-query";
import {QueryClient,QueryClientProvider} from "@tanstack/react-query";
import {fireEvent,render,screen,waitFor} from "@testing-library/react";
import {expect,test,vi} from "vitest";
import {ConfigurationService,ResourceService,ResourceSchema,SystemService,SystemCapability,EntityKind,newRequestId,inheritedHarnessValues,type SaveConfigurationRequest} from "@delinoio/delidev-api-client";
import {ConfigurationEditor,ConfigurationEditorPresentation,Settings} from "./settings";
import {newConfiguration,ServerPreferenceSection} from "./configuration-fields";
import {encode} from "./documents";
import {MutationIntents} from "./mutation";

test.each([{harness:"codex",section:ServerPreferenceSection.Codex,title:"Codex CLI",target:"codex"},{harness:"claude-code",section:ServerPreferenceSection.Claude,title:"Claude Code CLI",target:"claude"}])("$title keeps the exact authoritative draft after a rejected revision save",async({harness,section})=>{
 const body={...newConfiguration(EntityKind.SETTINGS),harness_defaults_version:1,harness_defaults:[{harness,values:inheritedHarnessValues()}]};
 const row=create(ResourceSchema,{kind:EntityKind.SETTINGS,id:newRequestId(),revision:7n,schemaVersion:4,documentJson:encode(body)});
 const save=vi.fn((_request:SaveConfigurationRequest)=>{throw new ConnectError("Revision conflict",Code.Aborted)});
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.HARNESS_DEFAULTS_V1,SystemCapability.PROJECT_BEHAVIOR_SETTINGS_V1,SystemCapability.SESSION_DEFAULTS_V1]})});
  router.service(ResourceService,{getResource:()=>({resource:row}),listResources:()=>({resources:[],nextPageToken:""})});
  router.service(ConfigurationService,{saveConfiguration:save});
 });
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const saved=vi.fn();render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><ConfigurationEditor kind={EntityKind.SETTINGS} initial={row} serverPreferenceSection={section} active saved={saved} cancel={()=>{}} presentation={ConfigurationEditorPresentation.InlineServerPreferences} preferencesObservation={{complete:true,resource:row,fetching:false}}/></MutationIntents></QueryClientProvider></TransportProvider>);
 const selection=await screen.findByLabelText("Reasoning effort: Setting source");await waitFor(()=>expect(selection).toHaveProperty("disabled",false));fireEvent.change(selection,{target:{value:"override"}});
 const input=screen.getByRole("textbox",{name:"Reasoning effort"});fireEvent.change(input,{target:{value:"high"}});
 const button=screen.getByRole("button",{name:"Save changes"});await waitFor(()=>expect(button).toHaveProperty("disabled",false));fireEvent.click(button);
 await waitFor(()=>expect(save).toHaveBeenCalledTimes(1));expect(input).toHaveProperty("value","high");expect(saved).toHaveBeenCalledTimes(1);await waitFor(()=>expect(screen.getAllByRole("alert").map(node=>node.textContent).join(" ")).toContain("draft is retained"));expect(button).toHaveProperty("disabled",true);
 const request=save.mock.calls[0][0];expect(request.schemaVersion).toBe(4);expect(request.mutation?.expectedRevision).toBe(7n);
 const sent=JSON.parse(new TextDecoder().decode(request.documentJson));expect(sent.harness_defaults[0].values.effort).toEqual({state:"override",value:"high"});expect(sent.default_routing).toBe(body.default_routing);
});

test.each([{harness:"codex",title:"Codex CLI",target:"codex"},{harness:"claude-code",title:"Claude Code CLI",target:"claude"}])("Settings exposes $title under Harnesses and search reaches original controls",async({harness,title,target})=>{
 const body={...newConfiguration(EntityKind.SETTINGS),harness_defaults_version:1,harness_defaults:[{harness,values:inheritedHarnessValues()}]};
 const row=create(ResourceSchema,{kind:EntityKind.SETTINGS,id:newRequestId(),revision:1n,schemaVersion:4,documentJson:encode(body)});
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.HARNESS_DEFAULTS_V1,SystemCapability.PROJECT_BEHAVIOR_SETTINGS_V1,SystemCapability.SESSION_DEFAULTS_V1]})});
  router.service(ResourceService,{listResources:request=>({resources:request.filter?.kind===EntityKind.SETTINGS?[row]:[]}),getResource:()=>({resource:row})});
 });
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Settings/></MutationIntents></QueryClientProvider></TransportProvider>);
 expect(screen.getByRole("heading",{name:"Harnesses"})).toBeTruthy();fireEvent.click(screen.getByRole("button",{name:title}));expect(await screen.findByLabelText("Reasoning effort: Setting source")).toBeTruthy();
 const search=screen.getByRole("searchbox");fireEvent.change(search,{target:{value:"Reasoning effort"}});const result=screen.getByRole("button",{name:`${title} › Reasoning effort`});result.focus();fireEvent.click(result);
 await waitFor(()=>expect(document.activeElement?.closest(`[data-settings-search-target="${target}-effort"]`)).toBeTruthy());
});
