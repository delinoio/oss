// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { changeCounts, commitResult, orderWorkspace } from "./pr-workspace-model";
import { encode } from "./documents";

it("renders each exact branch component once in seed order without summing counts", () => {
 const nodes = [101,102,103,104].map(number => ({ item: { number: String(number) }, counts: { additions: "24", deletions: "8" } }));
 const edges = [{ parent: "101", child: "102" }, { parent: "102", child: "103" }];
 const rows = orderWorkspace(nodes, edges, ["102","104","103"], false);
 expect(rows.map(row => [row.node.item, row.depth, row.owner])).toEqual([[{number:"101"},0,"102"],[{number:"102"},1,"102"],[{number:"103"},2,"102"],[{number:"104"},0,"104"]]);
 expect(rows[1].node.counts).toEqual({additions:"24",deletions:"8"});
});
it("keeps cycles and competing parents selectable without a fabricated tree", () => {
 const nodes = [1,2,3].map(number => ({item:{number:String(number)}}));
 for (const edges of [[{parent:"1",child:"2"},{parent:"2",child:"1"}],[{parent:"1",child:"3"},{parent:"2",child:"3"}]]) {
  const rows = orderWorkspace(nodes,edges,["1","2","3"],true);expect(rows).toHaveLength(3);expect(rows.every(row => row.depth===0&&!row.stack)).toBe(true);
 }
});
it.each([undefined,{additions:-1,deletions:0},{additions:"1.5",deletions:"0"},{additions:"18446744073709551616",deletions:"0"},{additions:1,deletions:0}])("rejects unavailable or invalid change counts (%j)", value => expect(changeCounts(value)).toBeUndefined());
it("preserves valid zero and exact large independent counts", () => {
 expect(changeCounts({additions:"0",deletions:"0"})).toEqual({additions:"0",deletions:"0"});
 expect(changeCounts({additions:"18446744073709551615",deletions:"8"})).toEqual({additions:"18446744073709551615",deletions:"8"});
});
it("rejects foreign commit scope and stalled continuations while preserving full messages", () => {
 const profile = newRequestId(), repository = create(ResourceSchema,{id:newRequestId(),kind:EntityKind.REPOSITORY,revision:1n,schemaVersion:1,documentJson:encode({integration_id:profile,github_owner:"fixture-owner",github_name:"repo"})});
 const remote = {provider:"github.com",id:"37",node_id:"R_37",owner:"fixture-owner",name:"repo"}, item = {id:"817",number:"17",base_sha:"a".repeat(40),head_sha:"b".repeat(40)};
 const value = {repository_id:repository.id,repository_revision:"1",profile_id:profile,generation_id:newRequestId(),observed_at:"2026-10-11T00:00:00Z",repository:remote,pull_request_id:"817",number:"17",base_sha:item.base_sha,head_sha:item.head_sha,page_token:"",next_page_token:"next",commits:[{sha:"c".repeat(40),message:"Long 한글 headline\n\nFull original body",author:{name:"Original author",date:"2026-10-11T00:00:00Z"},committer:{name:"Original committer",date:"2026-10-11T00:00:00Z"},parents:[item.head_sha],counts:{additions:"8",deletions:"3"}}]};
 expect(commitResult(encode(value),repository,item,remote,"")?.commits).toEqual(value.commits);
 expect(commitResult(encode({...value,head_sha:"d".repeat(40)}),repository,item,remote,"")).toBeUndefined();
 expect(commitResult(encode({...value,page_token:"next",next_page_token:"next"}),repository,item,remote,"next")).toBeUndefined();
});
