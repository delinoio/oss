// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { InertPRMarkdown, patchSections } from "./pr-workspace-content";
import { changeCounts, orderWorkspace, validWorkspace } from "./pr-workspace-page";
import { validCommitPage } from "./pr-workspace-commits";
import { PRBackgroundQueue } from "./pr-workspace-context";
describe("PR workspace boundaries", () => {
 it("preserves zeros and rejects invalid provider counts", () => { expect(changeCounts({additions:0,deletions:0})).toEqual({additions:0,deletions:0});for(const additions of [-1,.5,"1",null,Number.MAX_SAFE_INTEGER+1])expect(changeCounts({additions,deletions:0})).toBeUndefined();expect(changeCounts({additions:0})).toBeUndefined(); });
 it("never accepts a complete stack with missing source or mismatched branch operands", () => {
  const remote={provider:"github.com",id:"37",node_id:"R_37",owner:"owner",name:"repo",private:true};
  const row=(number:string,base:string,head:string,baseSHA:string,headSHA:string)=>({seed:true,counts:{additions:0,deletions:0},item:{provider:"github.com",kind:"pull-request",identity_source:"pull-request-api",id:number,number,title:"PR",body:"",state:"open",draft:false,merged:false,updated_at:"2026-09-01T00:00:00Z",base_ref:base,head_ref:head,base_sha:baseSHA.repeat(40),head_sha:headSHA.repeat(40),head_repository:{state:"available",repository:remote},url:`https://github.com/owner/repo/pull/${number}`}});
  const first=row("1","main","first","a","b"),second=row("2","first","second","b","c"),value={repository:remote,state:"complete",rows:[first,second],edges:[{parent:"1",child:"2"}]};
  expect(validWorkspace(value,["1","2"])).toBe(true);
  second.item.base_sha="d".repeat(40);expect(validWorkspace(value,["1","2"])).toBe(false);
  value.state="incomplete";expect(validWorkspace(value,["1","2"])).toBe(true);
  value.state="complete";second.item.base_sha="b".repeat(40);first.item.head_repository={state:"unavailable",repository:remote};expect(validWorkspace(value,["1","2"])).toBe(false);
 });
 it("orders accepted components and numeric parent-child relationships", () => { const rows=[3,2,1,8].map(number=>({item:{number:String(number)}}));expect(orderWorkspace(rows,[{parent:"1",child:"3"},{parent:"1",child:"2"}],["3","8"],true).map(value=>[value.row.item,value.depth])).toEqual([[{number:"1"},0],[{number:"2"},1],[{number:"3"},1],[{number:"8"},0]]);expect(orderWorkspace(rows,[],["3"],false)).toHaveLength(4); });
 it("keeps raw HTML, images and unsupported Markdown inert", () => {const body="# Heading\n\n**Strong** and `code`.\n\n![secret](https://evil.example/image)\n\n<script>alert(1)</script>\n\n[remote](https://evil.example/)";const{container}=render(<InertPRMarkdown body={body}/>);expect(screen.getByRole("heading",{name:"Heading"})).toBeTruthy();expect(container.querySelector("strong")?.textContent).toBe("Strong");expect(container.querySelectorAll("img,a,script,iframe")).toHaveLength(0);expect(container.querySelector("details pre")?.textContent).toBe(body); });
 it("tracks signed patch line operands and binary fallbacks", () => {const sections=patchSections("diff --git a/file b/file\n@@ -2,2 +4,2 @@\n context\n-old\n+new\ndiff --git a/bin b/bin\nBinary files a/bin and b/bin differ");expect(sections[0]?.lines.slice(2)).toEqual([{source:" context",old:2,next:4,kind:"context"},{source:"-old",old:3,next:undefined,kind:"deletion"},{source:"+new",old:undefined,next:5,kind:"addition"}]);expect(sections[1]?.fallback).toBe(true); });
 it("rejects continuation from different commit operands", () => {const item={id:"1",number:"2",base_sha:"a".repeat(40),head_sha:"b".repeat(40)};expect(validCommitPage({item,commits:[],next_page_token:""},item)).toBe(true);expect(validCommitPage({item:{...item,head_sha:"c".repeat(40)},commits:[],next_page_token:"opaque"},item)).toBe(false); });
 it("joins cancelled background work before the next queued read", async () => {const queue=new PRBackgroundQueue(),seen:string[]=[];let started!:()=>void;const ready=new Promise<void>(resolve=>started=resolve);const first=queue.run(new AbortController().signal,signal=>new Promise<void>(resolve=>{seen.push("first");started();signal.addEventListener("abort",()=>{seen.push("joined");resolve();});}));await ready;queue.prioritize(true);await first;queue.prioritize(false);await queue.run(new AbortController().signal,async()=>{seen.push("next");});expect(seen).toEqual(["first","joined","next"]); });
});

import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, waitFor } from "@testing-library/react";
import { vi } from "vitest";
import { EntityKind, IntegrationService, ResourceSchema, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { PRWorkspace } from "./pr-workspace";
import { ItemKind, ItemState, QueryOperation } from "./github-query-model";
import { encode } from "./documents";
it("keeps the list mounted and lazily reads manually activated detail tabs", async () => {
 const profile=newRequestId(),generation=newRequestId(),selected=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.REPOSITORY,revision:1n,schemaVersion:1,documentJson:encode({name:"Repository",integration_id:profile,github_owner:"owner",github_name:"repo"})});
 const remote={provider:"github.com",id:"37",node_id:"R_37",owner:"owner",name:"repo",private:true};
 const base={provider:"github.com",kind:"pull-request",identity_source:"pull-request-api",id:"17",node_id:"ITEM_17",number:"17",title:"Comparison fixture",state:"open",created_at:"2026-09-01T00:00:00Z",updated_at:"2026-09-02T00:00:00Z",url:"https://github.com/owner/repo/pull/17",draft:false};
 const read=vi.fn(async(request:{queryJson:Uint8Array})=>{
  const query=JSON.parse(new TextDecoder().decode(request.queryJson)),detail=query.operation!=="list";
  return {schemaVersion:1,documentJson:encode({repository_id:selected.id,repository_revision:"1",profile_id:profile,generation_id:generation,observed_at:"2026-09-02T00:00:00Z",identity:{id:"1",node_id:"U_1",login:"owner"},repository:remote,query,items:[detail?{...base,body:"# Original body",merged:false,mergeable:null,base_ref:"main",head_ref:"feature",base_sha:"a".repeat(40),head_sha:"b".repeat(40),head_repository:{state:"available",repository:remote}}:base],...(query.operation==="diff"?{diff:{patch:"diff --git a/f b/f\n@@ -1 +1 @@\n-old\n+new",digest:"c".repeat(64),base_sha:"a".repeat(40),head_sha:"b".repeat(40)}}:{})})};
 });
 const transport=createRouterTransport(router=>{router.service(SystemService,{getStatus:()=>({capabilities:[]})});router.service(IntegrationService,{queryRepositoryIntegration:read});});
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}}),query={kind:ItemKind.PullRequest,operation:QueryOperation.List,state:ItemState.Open,page:1,page_size:20};
 const view=render(<TransportProvider transport={transport}><QueryClientProvider client={client}><PRWorkspace selected={selected} navigation={{scopeKey:"fixture",query,previous:[]}} active /></QueryClientProvider></TransportProvider>);
 await screen.findByRole("heading",{name:"Comparison fixture"});expect(read).toHaveBeenCalledTimes(1);
 const list=view.container.querySelector<HTMLElement>(".pr-workspace-list")!;list.scrollTop=123;fireEvent.click(list.querySelector("button[data-pr-number='17']")!);
 await screen.findByRole("heading",{name:"Original body"});expect(read).toHaveBeenCalledTimes(2);expect(list.isConnected).toBe(true);expect(list.scrollTop).toBe(123);
 const description=screen.getByRole("tab",{name:"Description"}),diff=screen.getByRole("tab",{name:"Diff"});description.focus();fireEvent.keyDown(description,{key:"ArrowRight"});expect(document.activeElement).toBe(diff);expect(diff.getAttribute("aria-selected")).toBe("false");expect(read).toHaveBeenCalledTimes(2);
 fireEvent.click(diff);await screen.findByText("Snapshot details");expect(read).toHaveBeenCalledTimes(3);fireEvent.click(description);fireEvent.click(diff);await waitFor(()=>expect(diff.getAttribute("aria-selected")).toBe("true"));expect(read).toHaveBeenCalledTimes(3);
 fireEvent.click(screen.getByRole("tab",{name:"Commits"}));expect(screen.getByText("Commit reads are not supported by this connection.")).toBeTruthy();expect(read).toHaveBeenCalledTimes(3);
 fireEvent.click(screen.getByRole("button",{name:"Refresh GitHub results"}));await waitFor(()=>expect(read).toHaveBeenCalledTimes(4));await screen.findByRole("heading",{name:"Original body"});
 fireEvent.click(screen.getByRole("tab",{name:"Diff"}));await screen.findByText("Snapshot details");expect(read).toHaveBeenCalledTimes(5);
 fireEvent.click(screen.getByRole("button",{name:"Back to results"}));await waitFor(()=>expect(document.activeElement).toBe(list.querySelector("button[data-pr-number='17']")));expect(list.scrollTop).toBe(123);expect(read).toHaveBeenCalledTimes(5);view.unmount();client.clear();
});
