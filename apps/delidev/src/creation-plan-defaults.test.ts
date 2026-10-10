// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { expect, it, vi } from "vitest";
import { encode } from "./documents";
import { readCreationPlanDefault } from "./creation-plan-defaults";
const settings=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.SETTINGS,revision:2n,schemaVersion:3,documentJson:encode({plan_mode_default:true})});
const project=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.PROJECT,revision:2n,schemaVersion:3,documentJson:encode({settings:{plan_mode_default:"inherit"}})});
const args=()=>({readSettings:vi.fn(async()=>({resources:[settings]})),readProject:vi.fn(async()=>({resource:project})),projectId:project.id,agentId:newRequestId()});
it("reads both exact current sources and preserves Project override precedence",async()=>{
 const options=args();expect(await readCreationPlanDefault(options)).toBe("plan");expect(options.readSettings).toHaveBeenCalledExactlyOnceWith();expect(options.readProject).toHaveBeenCalledExactlyOnceWith(project.id);
 const disabled=create(ResourceSchema,{...project,documentJson:encode({settings:{plan_mode_default:"disabled"}})});
 expect(await readCreationPlanDefault({...args(),readProject:async()=>({resource:disabled})})).toBe("execute");
});
it("does not read an unrelated Project for projectless creation",async()=>{
 const options={...args(),projectId:""};expect(await readCreationPlanDefault(options)).toBe("plan");expect(options.readProject).not.toHaveBeenCalled();
});
it.each(["revision","contradiction","foreign","unsupported","override","restriction","pagination"])("refuses incomplete or stale creation evidence: %s",async change=>{
 const options=args();
 if(change==="revision")Object.assign(options,{priorSettings:create(ResourceSchema,{...settings,revision:3n})});
 if(change==="contradiction")Object.assign(options,{priorSettings:create(ResourceSchema,{...settings,documentJson:encode({plan_mode_default:false})})});
 if(change==="foreign")options.readProject.mockResolvedValue({resource:create(ResourceSchema,{...project,id:newRequestId()})});
 if(change==="unsupported")options.readProject.mockResolvedValue({resource:create(ResourceSchema,{...project,schemaVersion:99})});
 if(change==="override")options.readProject.mockResolvedValue({resource:create(ResourceSchema,{...project,documentJson:encode({settings:{plan_mode_default:null}})})});
 if(change==="restriction")options.readProject.mockResolvedValue({resource:create(ResourceSchema,{...project,documentJson:encode({agents:{configured:true,ids:[]}})})});
 if(change==="pagination")Object.assign(options,{readSettings:async()=>({resources:[settings],nextPageToken:"unread"})});
 await expect(readCreationPlanDefault(options)).rejects.toThrow();
});
