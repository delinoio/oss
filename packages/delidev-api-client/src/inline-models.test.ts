// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";
import { EntityKind, ResourceSchema } from "./gen/delidev/v1/common_pb.js";
import { configurationSchemaVersion, decodeResourceDocument } from "./configuration-identity.js";
const document = { name:"Inline", harness:"codex", routes:[{model:{subscription_service:"chatgpt",native_id:"exact/native",metadata_source:"unknown"},accounts:[{id:"0195c9c0-7b13-7000-8000-000000000001",weight:1}]}] };
const resource=(schemaVersion:number,data:unknown=document,kind:EntityKind=EntityKind.AGENT)=>create(ResourceSchema,{kind,schemaVersion,documentJson:new TextEncoder().encode(JSON.stringify(data))});
describe("current inline source schema",()=>{
 it("accepts current exact inline routes and chooses schema 4",()=>{expect(decodeResourceDocument(resource(4))).toEqual(document);expect(configurationSchemaVersion(EntityKind.AGENT,document)).toBe(4)});
 it("rejects Model resources, older Agent layouts and mixed UUID routes",()=>{expect(decodeResourceDocument(resource(3))).toBeUndefined();expect(decodeResourceDocument(resource(1,{model_id:"legacy"}))).toBeUndefined();expect(decodeResourceDocument(resource(4,{...document,model_id:"legacy"}))).toBeUndefined();expect(decodeResourceDocument(resource(1,{},EntityKind.MODEL))).toBeUndefined()});
 it("rejects ambiguous sources and unknown service identities",()=>{expect(decodeResourceDocument(resource(4,{...document,routes:[{model:{...document.routes[0].model,provider_id:"provider"},accounts:[{}]}]}))).toBeUndefined();expect(decodeResourceDocument(resource(4,{...document,routes:[{model:{native_id:"exact",subscription_service:"other"},accounts:[{}]}]}))).toBeUndefined()})
});
