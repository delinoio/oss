// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { encode } from "./documents";
import { startupOperations, observedStartupOperation, startupWorkerCurrent } from "./session-startup-operations";
import { SessionProgressPhase } from "./session-progress";
import { SessionProgressStatus } from "./session-progress-status";
import { i18n } from "./localization";
const sessionId=newRequestId(),jobId=newRequestId(),executionId=newRequestId(),nativeJob=newRequestId(),repo1=newRequestId(),repo2=newRequestId();
const scope={ assignment_revision:1,machine_id:newRequestId(),instance_id:newRequestId(),device_id:newRequestId(),server_epoch:newRequestId() };
const workspace={...scope,job_id:jobId,last_sequence:5,steps:[{workspace_operation:1,state:2,sequence:2},{workspace_operation:3,repository_id:repo1,repository_ordinal:1,repository_count:2,state:2,sequence:4},{workspace_operation:3,repository_id:repo2,repository_ordinal:2,repository_count:2,state:1,sequence:5},{workspace_operation:6},{workspace_operation:7}]};
const native={...scope,job_id:nativeJob,execution_id:executionId,last_sequence:8,steps:[1,2,3,4,5,6].map(native_phase=>({native_phase,...(native_phase===4?{state:2,sequence:6}:native_phase===5?{state:2,sequence:7}:native_phase===6?{state:2,sequence:8}:{})}))};
function resource(progress:object,extra:object={}){return create(ResourceSchema,{kind:EntityKind.SESSION,id:sessionId,sessionId,revision:1n,schemaVersion:1,documentJson:encode({preparation:{job_id:jobId},initial_execution:{id:executionId},execution:{job_id:nativeJob},startup:{job_id:nativeJob,execution_id:executionId},startup_progress:progress,...extra})});}
it("aggregates only confirmed original repository observations and preserves current ordinal",()=>{
 const operations=startupOperations(resource({workspace}),SessionProgressPhase.Preparing)!;
 expect(operations.workspace.find(row=>row.operation===3)).toEqual({operation:3,state:"running",ordinal:2,count:2});
 expect(operations.workspace.find(row=>row.operation===6)?.state).toBe("pending");
 expect(startupOperations(resource({workspace:{...workspace,steps:workspace.steps.slice(0,2)}}),SessionProgressPhase.Preparing)?.workspace.find(row=>row.operation===3)?.state).toBe("completed");
 expect(startupOperations(resource({workspace}),undefined)).toBeUndefined();
});
it("ignores descriptive input/response completion until authoritative acceptance",()=>{
 const starting=startupOperations(resource({native}),SessionProgressPhase.Starting)!;
 expect(starting.native.find(row=>row.operation===5)?.state).toBe("pending");expect(starting.native.find(row=>row.operation===6)?.state).toBe("pending");
 const response=startupOperations(resource({native}),SessionProgressPhase.Response)!;
 expect(response.native.find(row=>row.operation===5)?.state).toBe("completed");expect(response.native.find(row=>row.operation===6)?.state).toBe("running");
});
it.each([{...workspace,job_id:newRequestId()},{...workspace,last_sequence:0},{...workspace,steps:[...workspace.steps,workspace.steps[0]]},{...workspace,steps:[{workspace_operation:3,repository_id:"https://private.invalid",repository_ordinal:1,repository_count:2}]},{...workspace,steps:[{workspace_operation:1,path:"private"}]},{...workspace,steps:[{workspace_operation:3,repository_id:repo1,repository_ordinal:1,repository_count:101}]}])("rejects foreign malformed duplicate or sensitive summary %j",value=>{expect(startupOperations(resource({workspace:value}),SessionProgressPhase.Preparing)).toBeUndefined();});
it("keeps original failed operation inert beside existing guidance",()=>{
 expect(observedStartupOperation(resource({workspace}))).toEqual({native:false,operation:3,ordinal:2,count:2});
 expect(observedStartupOperation(resource({native},{startup:{job_id:nativeJob,execution_id:executionId,failure:{}}}))).toBeUndefined();
});
it("renders grouped applicable operations with stable polite status and native keyboard disclosure",async()=>{
 const operations=startupOperations(resource({workspace}),SessionProgressPhase.Preparing)!;
 const view=render(<><input aria-label="Draft" defaultValue="Retained draft"/><SessionProgressStatus operations={operations} phase={SessionProgressPhase.Preparing} compact={false}/></>);
 const status=screen.getByRole("status");expect(status.textContent).toBe("Session startup");expect(screen.getByText(/Repository 2\/2/)).toBeTruthy();expect(screen.getByRole("region",{name:"Workspace preparation"})).toBeTruthy();
 const draft=screen.getByRole("textbox");draft.focus();view.rerender(<><input aria-label="Draft" defaultValue="Retained draft"/><SessionProgressStatus operations={operations} phase={SessionProgressPhase.Preparing} compact/></>);
 expect(screen.getByRole("status")).toBe(status);expect(document.activeElement).toBe(draft);expect((draft as HTMLInputElement).value).toBe("Retained draft");
 const disclosure=screen.getByText("Startup details");expect(disclosure.tagName).toBe("SUMMARY");fireEvent.click(disclosure);
 await act(()=>i18n.changeLanguage("ko"));expect(screen.getByRole("status")).toBe(status);expect(screen.getByText("시작 준비 세부 정보")).toBeTruthy();expect(screen.getByText(/저장소 2\/2/)).toBeTruthy();await act(()=>i18n.changeLanguage("en"));
});

it("stops animation for unavailable expired or replaced original Worker presence",()=>{
 const now=Date.now(), instance=scope.instance_id, machineId=scope.machine_id;
 const session=resource({workspace},{machine_id:machineId});
 const machine=(last_seen:unknown,owner=instance)=>create(ResourceSchema,{kind:EntityKind.MACHINE,id:machineId,revision:1n,schemaVersion:1,documentJson:encode({last_seen,network:{instance_id:owner},disabled:false})});
 expect(startupWorkerCurrent(session,machine(new Date(now).toISOString()),now,true)).toBe(true);
 for(const last_seen of [undefined,"invalid",new Date(now-60_000).toISOString(),new Date(now+1000).toISOString()])expect(startupWorkerCurrent(session,machine(last_seen),now,true)).toBe(false);
 expect(startupWorkerCurrent(session,machine(new Date(now).toISOString(),newRequestId()),now,true)).toBe(false);
 expect(startupWorkerCurrent(session,machine(new Date(now).toISOString()),now,false)).toBe(false);
});
