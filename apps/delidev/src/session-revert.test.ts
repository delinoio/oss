// SPDX-License-Identifier: Apache-2.0
import { describe,it,expect } from "vitest";
import { create } from "@bufbuild/protobuf";
import { EntityKind,ResourceSchema,newRequestId } from "@delinoio/delidev-api-client";
import { revertEligible,verifiedRevertDraft } from "./session-revert";
const resource=(kind:EntityKind,id:string,sessionId:string,value:unknown)=>create(ResourceSchema,{kind,id,sessionId,revision:1n,schemaVersion:1,documentJson:new TextEncoder().encode(JSON.stringify(value))});
describe("original conversation revert",()=>{
 const id=newRequestId(),message=newRequestId(),turn=newRequestId(),thread=newRequestId(),input=newRequestId(),action=newRequestId();
 const session={initial_execution:{configuration:{harness:"codex"}},archive:"active",recovery:"none",execution:{cleanup_verified:true,native_thread_id:thread}};
 const target={role:"user",state:"complete",input_id:input,native_thread_id:thread,native_turn_id:turn,text:"earlier prompt"};
 it("accepts only an original settled user turn and original context membership",()=>{
  const row=resource(EntityKind.MESSAGE,message,id,target);
  expect(revertEligible(resource(EntityKind.SESSION,id,id,session),row,true)).toBe(true);
  for(const change of [{active_execution_id:input},{pending_steer_id:input},{compaction_job_id:action},{recovery:"required"},{fork:{}},{context_revision:1}])expect(revertEligible(resource(EntityKind.SESSION,id,id,{...session,...change}),row,true)).toBe(false);
  expect(revertEligible(resource(EntityKind.SESSION,id,id,session),row,false)).toBe(false);
  expect(revertEligible(resource(EntityKind.SESSION,id,id,{...session,context_revision:1,revert:{result:{retained_turn_ids:[turn]}}}),row,true)).toBe(true);
 });
 it("restores only a confirmed exact action/target/context generation as unsent text",()=>{
  const result={...session,context_revision:1,revert:{action_id:action,job_id:newRequestId(),result:{context_revision:1,retained_turn_ids:[],target:{message_id:message,native_turn_id:turn,context_revision:0,prompt:{prompt:"earlier prompt",mode:"execute"}}}}};
  expect(verifiedRevertDraft(resource(EntityKind.SESSION,id,id,result),action,message,0n)?.prompt).toBe("earlier prompt");
  expect(verifiedRevertDraft(resource(EntityKind.SESSION,id,id,result),newRequestId(),message,0n)).toBeUndefined();
  expect(verifiedRevertDraft(resource(EntityKind.SESSION,id,id,{...result,recovery:"required"}),action,message,0n)).toBeUndefined();
  expect(verifiedRevertDraft(resource(EntityKind.SESSION,id,id,{...result,context_revision:2}),action,message,0n)).toBeUndefined();
 });
});
