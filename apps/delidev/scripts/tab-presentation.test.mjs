// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { test } from "node:test";
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
const source=readFileSync(new URL("../src/tab-presentation.ts",import.meta.url),"utf8");
const {revealTabItem,revealSelectedTab,revealTabFocus}=await import(`data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(source)).toString("base64")}`);
const item=(left,right)=>({getBoundingClientRect:()=>({left,right})});
test("reveal changes only owning strip scroll, leaves reachable items unchanged",()=>{
 const root={scrollLeft:17,clientWidth:100,getBoundingClientRect:()=>({left:10})};
 revealTabItem(root,item(20,80));assert.equal(root.scrollLeft,17);
 revealTabItem(root,item(80,140));assert.equal(root.scrollLeft,47);
 revealTabItem(root,item(-10,20));assert.equal(root.scrollLeft,27);
});
test("manual focused item wins over selected item without activation or resource callbacks",()=>{
 const selected=item(10,40),focused=item(100,160);let changes=0;
 const root={scrollLeft:0,clientWidth:100,getBoundingClientRect:()=>({left:0}),ownerDocument:{activeElement:{closest:()=>focused}},contains:()=>true,querySelector:()=>{changes++;return selected}};
 revealSelectedTab(root);assert.equal(root.scrollLeft,60);assert.equal(changes,0);
 root.contains=()=>false;revealSelectedTab(root);assert.equal(root.scrollLeft,60);assert.equal(changes,1);
 revealSelectedTab(null);
});
test("focus reveal ignores other strip controls",()=>{
 const root={scrollLeft:0,clientWidth:100,getBoundingClientRect:()=>({left:0})};
 revealTabFocus({currentTarget:root,target:{closest:()=>null}});assert.equal(root.scrollLeft,0);
 revealTabFocus({currentTarget:root,target:{closest:()=>item(80,130)}});assert.equal(root.scrollLeft,30);
});
