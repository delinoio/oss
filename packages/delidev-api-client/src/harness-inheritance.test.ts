// SPDX-License-Identifier: Apache-2.0
import {expect,test} from "vitest";
import {create} from "@bufbuild/protobuf";
import {EntityKind,ResourceSchema} from "./gen/delidev/v1/delidev_pb.js";
import {HarnessInheritance,inheritedHarnessValues,effectiveHarnessSelection} from "./harness-inheritance.js";
import {configurationSchemaVersion,supportsResourceSchema} from "./configuration-identity.js";
test("all inherited scopes use fixed source/profile precedence and preserve explicit empty",()=>{
 const values=inheritedHarnessValues();const server=[{harness:"codex",values:{...values,effort:{state:HarnessInheritance.Override,value:"high"}}},{harness:"codex",provider_id:"source",api_protocol:"openai-responses",values:{...values,effort:{state:HarnessInheritance.Override,value:"medium"}}}];
 expect(effectiveHarnessSelection("effort","codex",{provider_id:"source",api_protocol:"openai-responses"},server,[])).toEqual({selection:{state:"override",value:"medium"},source:"server"});
 const agent={...values,effort:{state:HarnessInheritance.Override,value:""}};
 expect(effectiveHarnessSelection("effort","codex",{},server,[],agent)).toEqual({selection:{state:"override",value:""},source:"agent"});
 expect(effectiveHarnessSelection("model","codex",{},[],[]).source).toBe("native");
});
test("schema4 owners cannot masquerade as legacy or erase empty defaults",()=>{
 const data={harness_defaults_version:1,harness_defaults:[]};expect(configurationSchemaVersion(EntityKind.SETTINGS,data)).toBe(4);
 const resource=(schemaVersion:number,body:unknown)=>create(ResourceSchema,{kind:EntityKind.SETTINGS,schemaVersion,documentJson:new TextEncoder().encode(JSON.stringify(body))});
 expect(supportsResourceSchema(resource(4,data))).toBe(true);expect(supportsResourceSchema(resource(3,data))).toBe(false);expect(supportsResourceSchema(resource(4,{...data,harness_defaults:null}))).toBe(false);
});
