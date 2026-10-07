// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { StrictMode } from "react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionService, SystemService, SystemCapability, newRequestId, type Resource, type CreateSessionRequest } from "@delinoio/delidev-api-client";
import { chooseScrollOption, scrollChoiceValue, waitScrollChoices } from "./test-scroll-picker";
import { NewSession, NewSessionKind } from "./new-session";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { parseCreationPreferences, CreationPreferenceProblem, type CreationPreferencePair, type CreationPreferenceSnapshot } from "./session-creation-preferences";

function resource(kind: EntityKind, name: string, extra = {}): Resource { return create(ResourceSchema, { kind, id:newRequestId(), revision:1n, schemaVersion:1, documentJson:encode({name,...extra}) }); }
function fixture(initial = true, includeRemembered = false) {
 const agent=resource(EntityKind.AGENT,"Remembered agent"), machine=resource(EntityKind.MACHINE,"Disconnected runner",{disabled:false});
 const otherAgent=resource(EntityKind.AGENT,"Manual agent"), otherMachine=resource(EntityKind.MACHINE,"Manual runner");
 const projects=[resource(EntityKind.PROJECT,"Allowed project",{repositories:[],agents:{configured:true,ids:[agent.id]}}),resource(EntityKind.PROJECT,"Forbidden project",{repositories:[],agents:{configured:true,ids:[otherAgent.id]}}),resource(EntityKind.PROJECT,"Empty restriction",{repositories:[],agents:{configured:true,ids:[]}})];
 const scope={server_id:newRequestId(),device_id:newRequestId()};let revision=1;
 const memory=new Map<NewSessionKind,CreationPreferencePair>();if(initial)memory.set(NewSessionKind.Session,{agent_id:agent.id,machine_id:machine.id});
 const bridge={read:vi.fn(async(kind:NewSessionKind):Promise<unknown>=>({revision,scope,pair:memory.get(kind)??null,problem:null})),update:vi.fn(async(kind:NewSessionKind,pair:CreationPreferencePair,expected:number):Promise<unknown>=>{expect(expected).toBe(revision);memory.set(kind,pair);return {revision:++revision,scope,pair,problem:null};})};
 const get=vi.fn((request:{kind:EntityKind;id:string})=>({resource:[agent,machine,otherAgent,otherMachine,...projects].find(row=>row.id===request.id&&row.kind===request.kind)}));
 const createSession=vi.fn(async(_request:CreateSessionRequest)=>({change:{session:resource(EntityKind.SESSION,"Accepted")}}));
 const transport=createRouterTransport(router=>{
  router.service(ResourceService,{getResource:get,listResources:request=>({resources:request.filter?.kind===EntityKind.AGENT?[otherAgent,...(includeRemembered?[agent]:[])]:request.filter?.kind===EntityKind.MACHINE?[otherMachine]:request.filter?.kind===EntityKind.PROJECT?projects:[]})});
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.AUTOMATIC_TITLES_V1]})});router.service(SessionService,{createSession});
 });
 const client=new QueryClient({defaultOptions:{queries:{retry:false},mutations:{retry:false}}});
 const view=(kind=NewSessionKind.Session, readLocalWorker?: () => Promise<{machineId:string;token:string}>)=><StrictMode><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><NewSession kind={kind} active ownsActivation activation={1} back={()=>{}} openSettings={()=>{}} open={()=>{}} created={()=>{}} preferenceBridge={bridge} preferenceScope={scope} readLocalWorker={readLocalWorker}/></MutationIntents></QueryClientProvider></TransportProvider></StrictMode>;
 return {agent,machine,otherAgent,otherMachine,projects,scope,bridge,memory,get,createSession,client,view};
}

it("restores exact off-page resources including a disconnected registered runner",async()=>{
 const f=fixture();render(f.view());
 await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id));
 expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id);
 expect(screen.getByRole("combobox",{name:"Agent Worker"}).textContent).toBe("Remembered agent");expect(screen.getByRole("combobox",{name:"Runs on"}).textContent).toBe("Disconnected runner");
 expect(f.get.mock.calls.some(([request])=>request.id===f.agent.id)).toBe(true);expect(f.bridge.update).not.toHaveBeenCalled();expect(f.createSession).not.toHaveBeenCalled();
});
it("fences late preference reads after manual selection and prompt edits",async()=>{
 const f=fixture();let resolve!:(value:unknown)=>void;f.bridge.read.mockImplementation(()=>new Promise(done=>{resolve=done;}));render(f.view());
 await waitScrollChoices(screen.getByRole("combobox",{name:"Agent Worker"}));await chooseScrollOption(screen.getByRole("combobox",{name:"Agent Worker"}),f.otherAgent.id);fireEvent.change(screen.getByRole("textbox",{name:"First message"}),{target:{value:"Edited draft"}});
 await act(async()=>resolve({revision:1,scope:f.scope,pair:{agent_id:f.agent.id,machine_id:f.machine.id},problem:null}));
 expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.otherAgent.id);expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe("");expect(f.bridge.update).not.toHaveBeenCalled();
});
it("restores project-permitted fields independently and keeps configured-empty deny-all",async()=>{
 const f=fixture();render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id));
 for(const [index,expected] of [[0,f.agent.id],[1,""],[2,""]] as const){await chooseScrollOption(screen.getByRole("combobox",{name:"Project"}),f.projects[index].id);await waitFor(()=>expect(f.get.mock.calls.some(([r])=>r.id===f.projects[index].id)).toBe(true));await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(expected));await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id));}
 expect(f.bridge.update).not.toHaveBeenCalled();
});
it("saves only the original accepted request pair and restores it after controller restart",async()=>{
 const f=fixture(false);const view=render(f.view());await waitScrollChoices(screen.getByRole("combobox",{name:"Agent Worker"}));
 await chooseScrollOption(screen.getByRole("combobox",{name:"Agent Worker"}),f.otherAgent.id);await chooseScrollOption(screen.getByRole("combobox",{name:"Runs on"}),f.otherMachine.id);fireEvent.change(screen.getByRole("textbox",{name:"First message"}),{target:{value:"Create once"}});
 expect(f.bridge.update).not.toHaveBeenCalled();fireEvent.click(screen.getByRole("button",{name:"Create session"}));await waitFor(()=>expect(f.bridge.update).toHaveBeenCalledTimes(1));
 expect(f.bridge.update.mock.calls[0][1]).toEqual({agent_id:f.otherAgent.id,machine_id:f.otherMachine.id});expect(f.createSession).toHaveBeenCalledTimes(1);
 view.unmount();render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.otherAgent.id));
});
it.each(["failed","unreadable"])("does not save a %s creation",async outcome=>{
 const f=fixture();f.createSession.mockImplementation(async()=>{if(outcome==="failed")throw new ConnectError("Denied",Code.PermissionDenied);return {change:{session:undefined}} as never;});render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id));
 fireEvent.change(screen.getByRole("textbox",{name:"First message"}),{target:{value:"No preference change"}});fireEvent.click(screen.getByRole("button",{name:"Create session"}));await waitFor(()=>expect(f.createSession).toHaveBeenCalledTimes(1));await screen.findByRole("alert");expect(f.bridge.update).not.toHaveBeenCalled();
});
it("requires explicit reinspection after uncertain preference write without resending creation",async()=>{
 const f=fixture();f.bridge.update.mockRejectedValueOnce(new Error("Lost preference response"));render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id));fireEvent.change(screen.getByRole("textbox",{name:"First message"}),{target:{value:"Accepted once"}});fireEvent.click(screen.getByRole("button",{name:"Create session"}));await screen.findByRole("button",{name:"Inspect saved choices"});expect(screen.queryByRole("button",{name:"Retry saving accepted choices"})).toBeNull();fireEvent.click(screen.getByRole("button",{name:"Inspect saved choices"}));fireEvent.click(await screen.findByRole("button",{name:"Retry saving accepted choices"}));await waitFor(()=>expect(f.bridge.update).toHaveBeenCalledTimes(2));expect(f.createSession).toHaveBeenCalledTimes(1);
});
it("rejects foreign scope and malformed preference responses",()=>{
 const f=fixture();const valid:CreationPreferenceSnapshot={revision:1,scope:f.scope,pair:null,problem:null};expect(parseCreationPreferences(valid)).toEqual(valid);
 for(const invalid of [{...valid,revision:0},{...valid,extra:true},{...valid,scope:{server_id:"invalid",device_id:f.scope.device_id}},{...valid,pair:{agent_id:f.agent.id,machine_id:"invalid"}},{...valid,problem:"other"}])expect(()=>parseCreationPreferences(invalid)).toThrow();
 expect(parseCreationPreferences({...valid,problem:CreationPreferenceProblem.Changed}).problem).toBe(CreationPreferenceProblem.Changed);
});
it("keeps independent screen history and empty defaults when a kind has no record",async()=>{
 const f=fixture();const view=render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id));view.unmount();
 const chat=render(f.view(NewSessionKind.GeneralChat));await waitScrollChoices(screen.getByRole("combobox",{name:"Agent Worker"}));expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe("");await chooseScrollOption(screen.getByRole("combobox",{name:"Agent Worker"}),f.otherAgent.id);await chooseScrollOption(screen.getByRole("combobox",{name:"Runs on"}),f.otherMachine.id);fireEvent.change(screen.getByRole("textbox",{name:"First message"}),{target:{value:"Independent conversation"}});fireEvent.click(screen.getByRole("button",{name:"Start general chat"}));await waitFor(()=>expect(f.bridge.update).toHaveBeenCalledTimes(1));expect(f.memory.get(NewSessionKind.Session)).toEqual({agent_id:f.agent.id,machine_id:f.machine.id});chat.unmount();render(f.view(NewSessionKind.GeneralChat));await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.otherAgent.id));
});
it("rejects disabled or missing exact-ID resources without deleting remembered history",async()=>{
 const f=fixture();f.get.mockImplementation(request=>request.kind===EntityKind.MACHINE?{resource:create(ResourceSchema,{...f.machine,documentJson:encode({name:"Disabled runner",disabled:true})})}:{resource:undefined});render(f.view());await waitFor(()=>expect(f.get).toHaveBeenCalledTimes(2));expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe("");expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe("");expect(f.memory.get(NewSessionKind.Session)).toEqual({agent_id:f.agent.id,machine_id:f.machine.id});expect(f.bridge.update).not.toHaveBeenCalled();
});
it("does not restore or save another server/device scope",async()=>{
 const f=fixture();f.bridge.read.mockResolvedValue({revision:1,scope:{server_id:newRequestId(),device_id:f.scope.device_id},pair:{agent_id:f.agent.id,machine_id:f.machine.id},problem:null});render(f.view());await screen.findByRole("button",{name:"Inspect saved choices"});expect(f.get).not.toHaveBeenCalled();expect(f.bridge.update).not.toHaveBeenCalled();expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe("");
});
it("records the exact original pair only when an uncertain creation retry is acknowledged",async()=>{
 const f=fixture();f.createSession.mockRejectedValueOnce(new ConnectError("Lost creation acknowledgment",Code.Unavailable));render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id));fireEvent.change(screen.getByRole("textbox",{name:"First message"}),{target:{value:"Original request"}});fireEvent.click(screen.getByRole("button",{name:"Create session"}));await screen.findByRole("button",{name:"Retry the same session creation"});expect(f.bridge.update).not.toHaveBeenCalled();fireEvent.click(screen.getByRole("button",{name:"Retry the same session creation"}));await waitFor(()=>expect(f.bridge.update).toHaveBeenCalledTimes(1));expect(f.createSession).toHaveBeenCalledTimes(2);expect(f.createSession.mock.calls[0][0]).toEqual(f.createSession.mock.calls[1][0]);expect(f.bridge.update.mock.calls[0][1]).toEqual({agent_id:f.agent.id,machine_id:f.machine.id});
});
it("keeps Local proof pinned above saved runner choices without additional proof reads",async()=>{
 const f=fixture();const proof=vi.fn(async()=>({machineId:f.otherMachine.id,token:"A".repeat(43)}));render(f.view(NewSessionKind.Session,proof));await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id));await chooseScrollOption(screen.getByRole("combobox",{name:"Project"}),f.projects[0].id);fireEvent.click(screen.getByRole("button",{name:"Options"}));fireEvent.click(screen.getByRole("button",{name:/Local checkouts/}));await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.otherMachine.id));expect((screen.getByRole("combobox",{name:"Runs on"}) as HTMLSelectElement).disabled).toBe(true);expect(proof).toHaveBeenCalledTimes(1);expect(f.bridge.update).not.toHaveBeenCalled();
});
it("does not restore a runner missing the current project's Worktree clone capability",async()=>{
 const f=fixture();f.projects[0].documentJson=encode({name:"Allowed project",repositories:[newRequestId()],agents:{configured:true,ids:[f.agent.id]}});render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id));await chooseScrollOption(screen.getByRole("combobox",{name:"Project"}),f.projects[0].id);await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id));expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe("");expect(f.bridge.update).not.toHaveBeenCalled();
});

it("does not restore cached exact-ID resources after their current read fails",async()=>{
 const f=fixture();const first=render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id));expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id);first.unmount();
 f.get.mockImplementation(()=>{throw new ConnectError("Current exact resource lookup failed",Code.Unavailable);});render(f.view());await waitFor(()=>expect(f.get).toHaveBeenCalledTimes(4));await screen.findByRole("alert");expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe("");expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe("");expect(f.bridge.update).not.toHaveBeenCalled();
});

it("keeps later accepted choices in memory until uncertain preference writes are explicitly reinspected",async()=>{
 const f=fixture();f.bridge.update.mockRejectedValueOnce(new Error("Preference acknowledgment lost"));render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id));fireEvent.change(screen.getByRole("textbox",{name:"First message"}),{target:{value:"First accepted"}});fireEvent.click(screen.getByRole("button",{name:"Create session"}));await screen.findByRole("button",{name:"Inspect saved choices"});expect(f.bridge.update).toHaveBeenCalledTimes(1);
 await chooseScrollOption(screen.getByRole("combobox",{name:"Agent Worker"}),f.otherAgent.id);await chooseScrollOption(screen.getByRole("combobox",{name:"Runs on"}),f.otherMachine.id);fireEvent.change(screen.getByRole("textbox",{name:"First message"}),{target:{value:"Second accepted"}});fireEvent.click(screen.getByRole("button",{name:"Create session"}));await waitFor(()=>expect((screen.getByRole("textbox",{name:"First message"}) as HTMLTextAreaElement).value).toBe(""));expect(f.createSession).toHaveBeenCalledTimes(2);expect(f.bridge.update).toHaveBeenCalledTimes(1);
 fireEvent.click(screen.getByRole("button",{name:"Inspect saved choices"}));fireEvent.click(await screen.findByRole("button",{name:"Retry saving accepted choices"}));await waitFor(()=>expect(f.bridge.update).toHaveBeenCalledTimes(2));expect(f.bridge.update.mock.calls[1][1]).toEqual({agent_id:f.otherAgent.id,machine_id:f.otherMachine.id});expect(f.createSession).toHaveBeenCalledTimes(2);
});

it.each(["disabled", "missing", "unsupported", "forbidden"])("clears auto-restored choices after a settled %s exact read without changing history", async outcome => {
 const f=fixture();render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id));
 fireEvent.change(screen.getByRole("textbox",{name:"First message"}),{target:{value:"Keep this draft"}});
 f.get.mockImplementation(request=>{if(outcome==="forbidden")throw new ConnectError("Denied",Code.PermissionDenied);if(outcome==="missing")return {resource:undefined};const original=request.kind===EntityKind.AGENT?f.agent:f.machine;return {resource:create(ResourceSchema,{...original,...(outcome==="unsupported"?{schemaVersion:999}:{}),documentJson:encode({name:"Changed",disabled:outcome==="disabled"})})};});
 await act(async()=>{await f.client.invalidateQueries();});
 await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(""));expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe("");
 expect((screen.getByRole("textbox",{name:"First message"}) as HTMLTextAreaElement).value).toBe("Keep this draft");expect((screen.getByRole("button",{name:"Create session"}) as HTMLButtonElement).disabled).toBe(true);expect(f.bridge.update).not.toHaveBeenCalled();expect(f.createSession).not.toHaveBeenCalled();expect(f.memory.get(NewSessionKind.Session)).toEqual({agent_id:f.agent.id,machine_id:f.machine.id});
});
it("revalidates the untouched automatic runner while preserving a subsequent manual Agent choice",async()=>{
 const f=fixture();render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id));await chooseScrollOption(screen.getByRole("combobox",{name:"Agent Worker"}),f.otherAgent.id);
 f.machine.documentJson=encode({name:"Disconnected runner",disabled:true});await act(async()=>{await f.client.invalidateQueries();});
 await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(""));expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.otherAgent.id);expect(f.createSession).not.toHaveBeenCalled();expect(f.bridge.update).not.toHaveBeenCalled();
});
it("clears a restored runner when a current Worktree capability is lost",async()=>{
 const f=fixture();f.projects[0].documentJson=encode({name:"Allowed project",repositories:[newRequestId()],agents:{configured:true,ids:[f.agent.id]}});f.machine.documentJson=encode({name:"Runner",worker_capabilities:["remote-workspace-clone-v1"]});render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id));await chooseScrollOption(screen.getByRole("combobox",{name:"Project"}),f.projects[0].id);await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id));
 f.machine.documentJson=encode({name:"Runner",worker_capabilities:[]});await act(async()=>{await f.client.invalidateQueries();});await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(""));expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id);expect(f.createSession).not.toHaveBeenCalled();
});
it("blocks new creation during automatic proof refresh without erasing the draft or replaying creation",async()=>{
 const f=fixture();render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id));fireEvent.change(screen.getByRole("textbox",{name:"First message"}),{target:{value:"Still editing"}});
 let resolve!:(value:{resource:Resource})=>void;const original=f.get.getMockImplementation()!;f.get.mockImplementation(request=>request.id===f.agent.id?new Promise(done=>{resolve=done;}) as never:original(request));
 let refresh!:Promise<void>;act(()=>{refresh=f.client.invalidateQueries();});await waitFor(()=>expect(resolve).toBeTypeOf("function"));expect((screen.getByRole("button",{name:"Create session"}) as HTMLButtonElement).disabled).toBe(true);expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id);
 await act(async()=>{resolve({resource:f.agent});await refresh;});expect((screen.getByRole("textbox",{name:"First message"}) as HTMLTextAreaElement).value).toBe("Still editing");await waitFor(()=>expect((screen.getByRole("button",{name:"Create session"}) as HTMLButtonElement).disabled).toBe(false));expect(f.createSession).not.toHaveBeenCalled();
});
it("does not clear a manual selection of the formerly remembered exact ID",async()=>{
 const f=fixture(true,true);render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id));await chooseScrollOption(screen.getByRole("combobox",{name:"Agent Worker"}),f.agent.id);
 f.agent.documentJson=encode({name:"Remembered agent",disabled:true});await act(async()=>{await f.client.invalidateQueries();});expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id);expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id);expect(f.createSession).not.toHaveBeenCalled();
});
it("clears only the restored Agent after current project permissions change",async()=>{
 const f=fixture();render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id));await chooseScrollOption(screen.getByRole("combobox",{name:"Project"}),f.projects[0].id);await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id));
 f.projects[0].documentJson=encode({name:"Allowed project",repositories:[],agents:{configured:true,ids:[]}});await act(async()=>{await f.client.invalidateQueries();});await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(""));expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id);expect(f.bridge.update).not.toHaveBeenCalled();expect(f.createSession).not.toHaveBeenCalled();
});

it.each(["initial", "current"])("excludes a remembered Agent requiring reconfiguration in its %s exact read",async phase=>{
 const f=fixture();if(phase==="initial")f.agent.documentJson=encode({name:"Remembered agent",reconfiguration_required:true});render(f.view());await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id));
 if(phase==="current"){await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(f.agent.id));f.agent.documentJson=encode({name:"Remembered agent",reconfiguration_required:true});await act(async()=>{await f.client.invalidateQueries();});}
 await waitFor(()=>expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Agent Worker"}))).toBe(""));expect(scrollChoiceValue(screen.getByRole("combobox",{name:"Runs on"}))).toBe(f.machine.id);expect(f.memory.get(NewSessionKind.Session)).toEqual({agent_id:f.agent.id,machine_id:f.machine.id});expect(f.bridge.update).not.toHaveBeenCalled();expect(f.createSession).not.toHaveBeenCalled();
});
