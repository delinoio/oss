// SPDX-License-Identifier: Apache-2.0
import { expect,it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { ResourceSchema,EntityKind,SessionDirectoryOperationSchema,ChangeSessionDirectoryRequestSchema,newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { canonicalDirectory,safeDirectoryRoots,verifiedDirectoryOperation,directoryEligible } from "./session-directory-model";
it("rejects ambiguous paths and private root labels",()=>{
 for(const path of ["","/tmp","C:/tmp","../a","a/../b","a//b","a/./b","a/","a\\b","a\n"])expect(canonicalDirectory(path)).toBe(false);
 for(const path of [".","src","a/b","경로"])expect(canonicalDirectory(path)).toBe(true);
 expect(safeDirectoryRoots([{repository_id:"",name:"General Chat",primary:true}])).toHaveLength(1);
 expect(safeDirectoryRoots([{repository_id:"",name:"/private/root",primary:true}])?.[0].name).toBe("… (1)");
 expect(safeDirectoryRoots([{repository_id:newRequestId(),name:newRequestId(),primary:true}])?.[0].name).toBe("…");
 expect(safeDirectoryRoots([{repository_id:"",name:"General Chat",primary:true},{repository_id:newRequestId(),name:"Repo",primary:false}])).toBeUndefined();
});
it("requires original source/request/job/generation and never treats queued admission as success",()=>{
 const id=newRequestId(),requestId=newRequestId(),execution=newRequestId(),job=newRequestId(),repo=newRequestId(),previous=newRequestId();
 const request=create(ChangeSessionDirectoryRequestSchema,{mutation:{id,requestId,expectedRevision:7n},repositoryId:repo,relativePath:"src"});const source={execution,previous,sourceJob:newRequestId()};
 const op=create(SessionDirectoryOperationSchema,{sessionId:id,requestId,job:{id:job,kind:EntityKind.JOB,sessionId:id,revision:1n,schemaVersion:1,documentJson:encode({type:"change-session-directory",state:"queued",parent_id:source.sourceJob})}});
 expect(verifiedDirectoryOperation(op,request,source)).toBe(true);
 op.job!.documentJson=encode({type:"change-session-directory",state:"succeeded",parent_id:source.sourceJob});expect(verifiedDirectoryOperation(op,request,source)).toBe(false);
 op.generation={ $typeName:"delidev.v1.SessionDirectoryGeneration",generationId:newRequestId(),jobId:job,requestId,sourceExecutionId:execution,repositoryId:repo,relativePath:"src",previousGenerationId:previous};
 expect(verifiedDirectoryOperation(op,request,source,job)).toBe(true);
 for(const field of ["requestId","sourceExecutionId","repositoryId","previousGenerationId","jobId"] as const){const original=op.generation[field];op.generation[field]=newRequestId();expect(verifiedDirectoryOperation(op,request,source,job)).toBe(false);op.generation[field]=original;}
 op.generation.relativePath="../escape";expect(verifiedDirectoryOperation(op,request,source,job)).toBe(false);
});
it("requires supported original settled Codex root and independent pending gates",()=>{
 const data={initial_execution:{configuration:{harness:"codex"}},preparation:{state:"ready"},archive:"active",recovery:"none",dispatch:"ready",outcome:"succeeded",execution:{cleanup_verified:true,execution_id:newRequestId(),job_id:newRequestId()}};
 const s=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,schemaVersion:1,revision:1n,documentJson:encode(data)});
 expect(directoryEligible(s,true)).toBe(true);expect(directoryEligible(s,false)).toBe(false);
 for(const execution of [{...data.execution,native_compactions:{item:"started"}},{...data.execution,auto_reviews:{review:{status:"inProgress"}}},{...data.execution,waiting:{approval:true}},{...data.execution,subagents:{child:{state:"running"}}}]){s.documentJson=encode({...data,execution});expect(directoryEligible(s,true)).toBe(false);}
 for(const field of ["directory_job_id","active_execution_id","compaction_job_id","pending_steer_id","execution_recovery_job_id"]){s.documentJson=encode({...data,[field]:newRequestId()});expect(directoryEligible(s,true)).toBe(false);}
});

it("permits an original settled ordinary Fork root while denying Sidechat and malformed ownership",()=>{
 const data={initial_execution:{configuration:{harness:"codex"}},fork:{snapshot:{}},preparation:{state:"ready"},archive:"active",recovery:"none",dispatch:"ready",outcome:"succeeded",execution:{cleanup_verified:true,execution_id:newRequestId(),job_id:newRequestId()}};
 const s=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SESSION,schemaVersion:1,revision:1n,documentJson:encode(data)});
 expect(directoryEligible(s,true)).toBe(true);
 s.documentJson=encode({...data,fork:{...data.fork,sidechat_parent_snapshot:{}}});expect(directoryEligible(s,true)).toBe(false);
 s.documentJson=encode({...data,fork:"malformed"});expect(directoryEligible(s,true)).toBe(false);
});
