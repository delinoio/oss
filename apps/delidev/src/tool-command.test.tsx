// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { encode } from "./documents";
import { CommandAdapter, CommandOutput, commandPresentation, commandPreview } from "./tool-command";
afterEach(cleanup);
const id=newRequestId(),sessionId=newRequestId();
const row=(data:object)=>create(ResourceSchema,{id,sessionId,kind:EntityKind.MESSAGE,schemaVersion:1,revision:1n,documentJson:encode(data)});
function codex(command="/bin/zsh -lc pwd",aggregate:string|null="/workspace/project") {
 const observation={command,cwd:"/workspace/project",source:"agent",actions:[],aggregated_output:aggregate,exit_code:0,duration_ms:1,process_id:null,plugin_id:null,script_path:null};
 return {role:"tool",text:"",state:"complete",tool:{started:{kind:"command",status:"running",changes:null,command:observation},completed:{kind:"command",status:"completed",changes:null,command:observation},output:"distinct original stream"}};
}
it.each(["/bin/bash -lc pwd","/bin/zsh -l -c pwd","echo /bin/zsh -lc pwd","  /bin/zsh -lc pwd","/bin/zsh -lc  '한글'\n pwd"])("preserves every suffix and non-exact wrapper: %s",command=>{
 const presentation=commandPresentation(row(codex(command)))!;
 expect(commandPreview(presentation)).toBe(command.startsWith("/bin/zsh -lc ")?command.slice(13):command);
 expect(presentation.command).toBe(command);
});
it("uses Codex aggregate output, retains empty output, and never joins the stream",()=>{
 const presentation=commandPresentation(row(codex()))!;
 expect(commandPreview(presentation)).toBe("pwd");expect(presentation.outputs).toEqual(["/workspace/project"]);
 expect(commandPresentation(row(codex("pwd","")))?.outputs).toEqual([""]);
 expect(commandPresentation(row(codex("pwd",null)))?.outputs).toEqual(["distinct original stream"]);
});
it("renders literal direct output and collapsed original Details",()=>{
 const value=commandPresentation(row(codex("pwd","<script>run()</script> [link](https://invalid)")))!;
 const view=render(<CommandOutput value={value}><pre>pwd</pre></CommandOutput>);
 expect(view.container.querySelector(".tool-command-output code")?.textContent).toBe(value.outputs[0]);
 expect(view.container.querySelector("script,a,button,img")).toBeNull();
 expect(view.container.querySelector<HTMLDetailsElement>("details")?.open).toBe(false);
 view.rerender(<CommandOutput value={{...value,outputs:[""]}}>original</CommandOutput>);
 expect(view.container.querySelectorAll(".tool-command-output")).toHaveLength(1);
 view.rerender(<CommandOutput value={{...value,outputs:[]}}>original</CommandOutput>);
 expect(view.container.querySelector(".tool-command-output")).toBeNull();expect(view.container.textContent).toContain("No output has been retained.");
});
function claude(){return {role:"tool",text:"",state:"complete",native_id:"original-tool",native_parent_id:"original-message",claude_tool:{reference:{id,native_id:"original-tool",name:"Bash"},message_id:newRequestId(),native_message_id:"original-message",index:0,caller:null,initial_input:'{"command":"initial"}',input_delta:null,proposal:{proposed:'{"command":"initial"}',applied:'{"command":"/bin/zsh -lc pwd"}'},result:{native_event_id:"123e4567-e89b-42d3-a456-426614174000",is_error:true,text:null,blocks:[{kind:"text",text:""},{kind:"text",text:"original failure"}],structured:null}}};}
it("reuses complete Claude validation and applied input without reconstructing fragments",()=>{
 const data=claude(),value=commandPresentation(row(data))!;
 expect(value.adapter).toBe(CommandAdapter.Claude);expect(commandPreview(value)).toBe("/bin/zsh -lc pwd");expect(value.outputs).toEqual(["","original failure"]);expect(value.error).toBe(true);
 data.claude_tool.proposal.applied="partial";expect(commandPresentation(row(data))).toBeUndefined();
 const streaming={...claude(),state:"streaming",claude_tool:{...claude().claude_tool,proposal:null,result:null,input_delta:'{"command":"invented fragment'}};
 expect(commandPresentation(row(streaming))?.command).toBe("initial");
 expect(commandPresentation(row({...data,native_id:"foreign"}))).toBeUndefined();
});
it("uses only immutable OpenCode shell input and preserves independent warnings",()=>{
 const started={kind:"opencode-shell",status:"pending",shell:{call_id:"original",input:{},raw:""}};
 const running={kind:"opencode-shell",status:"running",shell:{call_id:"original",input:{command:"/bin/zsh -lc pwd"},time:{start:1},metadata:{output:"running preview",exit:null,exit_observed:false}}};
 const completed={kind:"opencode-shell",status:"completed",shell:{call_id:"original",input:{command:"/bin/zsh -lc pwd"},time:{start:1,end:2},title:"original",output:"complete output",metadata:{output:"separate preview",exit:7,exit_observed:true,truncated:true,outputPath:"inert/path",interrupted:true}}};
 const data={role:"tool",text:"",state:"complete",tool:{started,states:[{sequence:1,snapshot:running}],completed}};
 const value=commandPresentation(row(data))!;expect(value.adapter).toBe(CommandAdapter.OpenCode);expect(commandPreview(value)).toBe("/bin/zsh -lc pwd");expect(value.outputs).toEqual(["complete output"]);expect(value.truncated).toBe(true);expect(value.interrupted).toBe(true);expect(value.exit).toBe(7);
 expect(commandPresentation(row({...data,state:"streaming",tool:{started}}))).toBeUndefined();
 completed.shell.input.command="changed";expect(commandPresentation(row(data))).toBeUndefined();
});
it("never infers commands from malformed, mixed, non-command or Grok records",()=>{
 expect(commandPresentation(row({...codex(),grok_tool:{command:"pwd"}}))).toBeUndefined();
 const malformed=codex();malformed.tool.started.command.source="foreign";expect(commandPresentation(row(malformed))).toBeUndefined();
 expect(commandPresentation(row({role:"tool",state:"complete",text:"",grok_tool:{title:"pwd",command:"pwd"}}))).toBeUndefined();
 expect(commandPresentation(row({...claude(),claude_tool:{...claude().claude_tool,reference:{id,native_id:"original-tool",name:"Read"}}}))).toBeUndefined();
});
