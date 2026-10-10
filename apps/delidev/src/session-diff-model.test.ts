import { expect, it } from "vitest";
import { encode } from "./documents";
import { Comparison, ReferenceType, readDiff, readDiffOptions, referenceKey } from "./session-diff-model";
const repository="repository",base={type:ReferenceType.Local,name:"base-A"};
const options={version:1,repository_id:repository,path:".",choices:[{reference:base,configured:true}],default:base,default_available:false};
const wire=(value:object)=>encode({size:"0",binary:false,truncated:false,...value});
it("retains the missing saved base and rejects incomplete or foreign options",()=>{
 expect(readDiffOptions(wire({diff_options:options}),repository,".").default).toEqual(base);
 for(const changed of [{version:2},{repository_id:"other"},{choices:[...options.choices,...options.choices]},{choices:[]},{default_available:"yes"},{authority:true}])expect(()=>readDiffOptions(wire({diff_options:{...options,...changed}}),repository,".")).toThrow();
});
it("binds branch commit facts and preserves omitted legacy fields",()=>{
 const legacy={comparison:Comparison.WorkingTree,repository_id:repository,path:".",base:"commit",base_object:"a".repeat(40),head_commit:"a".repeat(40),patch:"",untracked:[],revision:"b".repeat(64)};
 expect(readDiff(wire({diff:legacy}),repository,Comparison.WorkingTree,".")).toEqual(legacy);
 const branch={...legacy,comparison:Comparison.Branch,head_commit:"c".repeat(40),base_ref:base,base_commit:"d".repeat(40),merge_base:legacy.base_object};
 expect(readDiff(wire({diff:branch}),repository,Comparison.Branch,".",base)).toEqual(branch);
 expect(()=>readDiff(wire({diff:branch}),repository,Comparison.Branch,".",{...base,name:"base-B"})).toThrow();
 expect(()=>readDiff(wire({diff:{...branch,merge_base:"e".repeat(40)}}),repository,Comparison.Branch,".",base)).toThrow();
 expect(()=>readDiff(wire({diff:{...legacy,base_ref:base}}),repository,Comparison.WorkingTree,".")).toThrow();
 expect(referenceKey({...base})).toBe(referenceKey(base));
});
