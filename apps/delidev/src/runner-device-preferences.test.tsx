// SPDX-License-Identifier: Apache-2.0
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useEffect, useRef, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { EntityKind, ResourceSchema, ResourceService } from "@delinoio/delidev-api-client";
import { CreationPreferenceProblem } from "./session-creation-preferences";
import { RunnerPreferenceProvider, RunnerWorkflow, parseRunnerPreference, useRunnerPreference, type RunnerPreferenceSnapshot } from "./runner-device-preferences";
const server = "0199cc44-1111-7111-8111-111111111111", device = "0199cc44-2222-7222-8222-222222222222", remote = "0199cc44-3333-7333-8333-333333333333", local = "0199cc44-4444-7444-8444-444444444444";
const resource = (id: string, enabled = true) => create(ResourceSchema, { id, kind: EntityKind.MACHINE, revision: 1n, schemaVersion: 1, documentJson: new TextEncoder().encode(JSON.stringify({ name: id, enabled })) });
function Field({ kind = RunnerWorkflow.Schedule, excludeLocal = false }: { kind?: RunnerWorkflow; excludeLocal?: boolean }) {
 const runner = useRunnerPreference(kind, true, undefined, excludeLocal), [machine,setMachine] = useState(""), touched = useRef(false);
 useEffect(() => { if (!touched.current && !machine && runner.suggestion) setMachine(runner.suggestion.id); }, [runner.suggestion, machine]);
 return <><label>Runner<input value={machine} onChange={event => { touched.current = true; runner.touch(); setMachine(event.target.value); }}/></label><button onClick={() => runner.remember(machine)}>Accept</button>{runner.guidance}</>;
}
function fixture(machine: string | null = remote) {
 let revision = 1;
 const memory = new Map([[RunnerWorkflow.Schedule,machine]]);
 const read = vi.fn(async (kind: RunnerWorkflow): Promise<unknown> => ({revision,scope:{server_id:server,device_id:device},machine_id:memory.get(kind)??null,problem:null}));
 const update = vi.fn(async (kind: RunnerWorkflow, id: string, expected: number): Promise<unknown> => { expect(expected).toBe(revision); memory.set(kind,id); return {revision:++revision,scope:{server_id:server,device_id:device},machine_id:id,problem:null}; });
 const get = vi.fn(async (request: {id: string}) => ({resource:resource(request.id)}));
 const native = vi.fn(async () => ({machineId:local,token:"secret-proof"}));
 const transport = createRouterTransport(({service}) => {service(ResourceService,{getResource:get});});
 const view = (kind = RunnerWorkflow.Schedule, excludeLocal = false) => <TransportProvider transport={transport}><RunnerPreferenceProvider bridge={{read,update}} readLocalWorker={native}><Field kind={kind} excludeLocal={excludeLocal}/></RunnerPreferenceProvider></TransportProvider>;
 return {read,update,get,native,view,memory};
}
afterEach(cleanup);
describe("workflow Runner preferences",()=>{
 it("restores exact off-page history and records accepted selections only",async()=>{
  const f=fixture();render(f.view());await waitFor(()=>expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe(remote));expect(f.get).toHaveBeenCalledWith(expect.objectContaining({id:remote}),expect.anything());
  fireEvent.change(screen.getByRole("textbox"),{target:{value:local}});expect(f.update).not.toHaveBeenCalled();fireEvent.click(screen.getByText("Accept"));await waitFor(()=>expect(f.update).toHaveBeenCalledTimes(1));expect(f.memory.get(RunnerWorkflow.Schedule)).toBe(local);
 });
 it("falls back for definite absence but retains uncertainty without choosing local",async()=>{
  const f=fixture();f.get.mockRejectedValueOnce(new ConnectError("gone",Code.NotFound));render(f.view());await waitFor(()=>expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe(local));cleanup();
  const uncertain=fixture();uncertain.get.mockRejectedValueOnce(new ConnectError("offline",Code.Unavailable));render(uncertain.view());await screen.findByRole("alert");expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe("");expect(uncertain.native).not.toHaveBeenCalled();
 });
 it("keeps disabled history out and never crosses workflow scope",async()=>{
  const f=fixture();f.get.mockImplementation(async request=>({resource:resource(request.id,request.id!==remote)}));render(f.view());await waitFor(()=>expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe(local));cleanup();render(f.view(RunnerWorkflow.Checkout));await waitFor(()=>expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe(local));expect(f.read).toHaveBeenCalledWith(RunnerWorkflow.Checkout);
 });
 it("fences delayed reads after manual edits",async()=>{
  const f=fixture();let release!: (value: unknown)=>void;f.read.mockImplementationOnce(()=>new Promise(resolve=>{release=resolve;}));render(f.view());fireEvent.change(screen.getByRole("textbox"),{target:{value:local}});release({revision:1,scope:{server_id:server,device_id:device},machine_id:remote,problem:null});await waitFor(()=>expect(f.read).toHaveBeenCalledTimes(1));expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe(local);expect(f.get).not.toHaveBeenCalled();
 });
 it("requires explicit inspection before retrying an uncertain preference write",async()=>{
  const f=fixture();f.update.mockRejectedValueOnce(new Error("lost reply"));render(f.view());await waitFor(()=>expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe(remote));fireEvent.click(screen.getByText("Accept"));await screen.findByRole("alert");expect(screen.queryByText("Retry saving accepted choices")).toBeNull();fireEvent.click(screen.getByText("Inspect saved choices"));await screen.findByText("Retry saving accepted choices");fireEvent.click(screen.getByText("Retry saving accepted choices"));await waitFor(()=>expect(f.update).toHaveBeenCalledTimes(2));
 });
 it("finishes admitted preference publication after its field is disposed",async()=>{
  const f=fixture();let release!: (value: unknown)=>void;let closes!: ()=>void;
  function Screen(){const [visible,setVisible]=useState(true);closes=()=>setVisible(false);return visible?<Field/>:<p>Closed</p>;}
  const transport=createRouterTransport(({service})=>service(ResourceService,{getResource:f.get}));
  render(<TransportProvider transport={transport}><RunnerPreferenceProvider bridge={{read:f.read,update:f.update}} readLocalWorker={f.native}><Screen/></RunnerPreferenceProvider></TransportProvider>);
  await waitFor(()=>expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe(remote));
  f.read.mockImplementationOnce(()=>new Promise(resolve=>{release=resolve;}));fireEvent.click(screen.getByText("Accept"));await waitFor(()=>expect(f.read).toHaveBeenCalledTimes(2));closes();release({revision:1,scope:{server_id:server,device_id:device},machine_id:remote,problem:null});await waitFor(()=>expect(f.update).toHaveBeenCalledTimes(1));
 });
 it("never selects this computer in explicit Another computer mode",async()=>{
  const f=fixture(local);render(f.view(RunnerWorkflow.Schedule,true));await waitFor(()=>expect(f.get).toHaveBeenCalledTimes(1));expect(f.native).toHaveBeenCalledTimes(1);expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe("");expect(f.update).not.toHaveBeenCalled();
 });
 it("rejects malformed preference authority",()=>{
  const valid:RunnerPreferenceSnapshot={revision:1,scope:{server_id:server,device_id:device},machine_id:remote,problem:null};expect(parseRunnerPreference(valid)).toEqual(valid);
  for(const value of [{...valid,revision:0},{...valid,machine_id:"bad"},{...valid,scope:{server_id:server,device_id:"bad"}},{...valid,extra:true},{...valid,problem:"bad"}])expect(()=>parseRunnerPreference(value)).toThrow();expect(parseRunnerPreference({...valid,problem:CreationPreferenceProblem.Changed}).problem).toBe(CreationPreferenceProblem.Changed);
 });
});
