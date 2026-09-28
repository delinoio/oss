import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { NativeClaudeMessage } from "./native-claude-message";
import { validClaudeCitation, validClaudeCitationHistory } from "./native-claude-citations";
import { object, type Document } from "./documents";

afterEach(cleanup);
const citation = (): Document => ({kind:"web_search_result_location",text:"<script>original citation</script>",title:null,web:{url:"javascript:original-reference()"}});
const history = (): Document => ({initial:{null:false,entries:[]},deltas:[citation()],completed:{null:false,entries:[]},completion:"omitted-by-native"});
const message = (citations=history()) => ({model:"fixture-model",blocks:[{index:0,block:{kind:"text",text:"Original answer"},state:"stopped",citations}],stop_reason:"end_turn",stop_sequence:null});

test("keeps original streamed citation and completed omission inert",()=>{
 const {container}=render(<NativeClaudeMessage content={message()} state="complete"/>);
 expect(screen.getByText("Original answer")).toBeTruthy();
 expect(screen.getByText("<script>original citation</script>")).toBeTruthy();
 expect(screen.getByText("javascript:original-reference()")).toBeTruthy();
 expect(screen.getByText(/Claude omitted streamed citations/)).toBeTruthy();
 expect(container.querySelectorAll("script,a,button,input,iframe,img")).toHaveLength(0);
});

test.each(["char_location","page_location","content_block_location","search_result_location"])("preserves exact native %s indices and missing title",kind=>{
 const v:Document={kind,text:"Original quote",title:null};
 const positions={index:"18446744073709551615",start:"9007199254740993",end:"9007199254740994"};
 if(kind==="search_result_location")v.search={...positions,source:"Original native source"};else v.document={...positions,file:{value:null}};
 expect(validClaudeCitation(v)).toBe(true);
 render(<NativeClaudeMessage content={message({initial:{null:true,entries:null},deltas:[v],completed:{null:false,entries:[]},completion:"omitted-by-native"})} state="complete"/>);
 expect(screen.getByText("18446744073709551615")).toBeTruthy();
 expect(screen.getByText("9007199254740993")).toBeTruthy();
 expect(screen.getByText("9007199254740994")).toBeTruthy();
});

test("matches completed citations independently of JSON member order",()=>{
 const v=citation();
 const h={initial:null,deltas:[v],completed:{null:false,entries:[{web:v.web,title:v.title,text:v.text,kind:v.kind}]},completion:"matched"};
 expect(validClaudeCitationHistory(h,"stopped")).toBe(true);
 expect(validClaudeCitationHistory({...h,completed:null,completion:undefined},"streaming")).toBe(false);
 expect(validClaudeCitationHistory({initial:null,deltas:[v],completed:null},"streaming")).toBe(true);
});

test.each(["changed","omitted-null","omitted-absent","initial-omitted","missing-completed","missing-deltas","opaque","unknown","wrong-kind","overflow","huge","early","wrong-block"])("rejects inconsistent citation history: %s",change=>{
 const h=history(),v=(h.deltas as Document[])[0]!,data=message(h);
 if(change==="changed"){h.completion="matched";h.completed={null:false,entries:[{...v,text:"changed"}]};}
 if(change==="omitted-null")h.completed={null:true,entries:null};
 if(change==="omitted-absent")h.completed=null;
 if(change==="initial-omitted")h.initial={null:false,entries:[citation()]};
 if(change==="missing-completed")delete h.completed;
 if(change==="missing-deltas")delete h.deltas;
 if(change==="opaque")v.encrypted_index="private";
 if(change==="unknown")h.future=true;
 if(change==="wrong-kind")v.document={index:"0",start:"0",end:"1"};
 if(change==="overflow"){v.kind="char_location";delete v.web;v.document={index:"18446744073709551616",start:"0",end:"1"};}
 if(change==="huge")v.text="x".repeat((256<<10)+1);
 if(change==="early")data.blocks[0]!.state="streaming";
 if(change==="wrong-block")data.blocks[0]!.block.kind="thinking";
 render(<NativeClaudeMessage content={data} state="complete"/>);
 expect(screen.getByLabelText("Claude message unavailable")).toBeTruthy();
 expect(screen.queryByText("Original answer")).toBeNull();
});

test.each(["missing-title","null-text","empty-url","null-file","missing-file-value","zero-page","reversed","number"])("rejects malformed citation: %s",change=>{
 const v=citation();
 if(change==="missing-title")delete v.title;
 if(change==="null-text")v.text=null;
 if(change==="empty-url")object(v.web).url="";
 if(["null-file","missing-file-value","zero-page","reversed","number"].includes(change)){
  v.kind="page_location";delete v.web;v.document={index:"0",start:"1",end:"2"};const d=object(v.document);
  if(change==="null-file")d.file=null;
  if(change==="missing-file-value")d.file={};
  if(change==="zero-page")d.start="0";
  if(change==="reversed")d.end="1";
  if(change==="number")d.index=9007199254740992;
 }
 expect(validClaudeCitation(v)).toBe(false);
});
