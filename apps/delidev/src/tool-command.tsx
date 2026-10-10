// SPDX-License-Identifier: Apache-2.0
import type { Resource } from "@delinoio/delidev-api-client";
import type { ReactNode } from "react";
import { document, object } from "./documents";
import { validatedClaudeTool } from "./native-claude-tool";
import { validatedShellSnapshots } from "./native-shell";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, LocalizedText } from "./localization";

export enum CommandAdapter { Codex = "codex", Claude = "claude", OpenCode = "opencode" }
export interface CommandPresentation {
 adapter: CommandAdapter; command: string; outputs: string[]; error?: boolean;
 nonExecution?: "permission-rule" | "user-rejected"; exit?: number | null;
 truncated?: boolean; interrupted?: boolean;
}
const bounded=(value:unknown,max=256<<10):value is string=>typeof value==="string" && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value) && new TextEncoder().encode(value).byteLength<=max;
const allowed=(value:Record<string,unknown>,keys:string[])=>Object.keys(value).every(key=>keys.includes(key));
function codexSnapshot(value:unknown) {
 const snapshot=object(value),c=object(snapshot.command);
 if(snapshot.kind!=="command" || !["running","completed","failed","declined"].includes(String(snapshot.status)) || snapshot.changes!=null || !allowed(snapshot,["kind","status","command","changes"]) || !allowed(c,["command","cwd","source","actions","process_id","aggregated_output","exit_code","duration_ms","plugin_id","script_path"]) || !bounded(c.command) || !c.command.trim() || !bounded(c.cwd,4096) || !c.cwd.trim() || !["agent","user-shell","exec-startup","exec-input"].includes(String(c.source)) || !Array.isArray(c.actions) || c.actions.length>1024)return;
 for(const [name,max] of [["process_id",1024],["aggregated_output",256<<10],["plugin_id",1024],["script_path",4096]] as const)if(c[name]!=null && !bounded(c[name],max))return;
 if(c.exit_code!=null && (typeof c.exit_code!=="number" || !Number.isInteger(c.exit_code) || c.exit_code<-(2**31) || c.exit_code>=2**31))return;
 if(c.duration_ms!=null && (typeof c.duration_ms!=="number" || !Number.isSafeInteger(c.duration_ms) || c.duration_ms<0))return;
 for(const value of c.actions){const a=object(value);if(!allowed(a,["kind","command","name","path","query"]) || !bounded(a.command) || !a.command.trim() || !["read","list","search","unknown"].includes(String(a.kind)))return;for(const name of ["name","path","query"])if(a[name]!=null&&!bounded(a[name],4096))return;if(a.kind==="read"&&(a.name==null||a.path==null||a.query!=null)||a.kind==="list"&&(a.name!=null||a.query!=null)||a.kind==="search"&&a.name!=null||a.kind==="unknown"&&(a.name!=null||a.path!=null||a.query!=null))return;}
 return c;
}
/** Resident payload only: never place commands or output in display projections. */
export function commandPresentation(resource:Resource):CommandPresentation|undefined {
 const d=document(resource),state=String(d.state);
 if(d.role!=="tool" || d.text!=="" || ["artifact","progress","claude","grok_tool","grok_text","grok_user","claude_progress","claude_interruption"].some(key=>Object.hasOwn(d,key)))return;
 if(d.claude_tool!=null){
  if(d.tool!=null || d.input_id!=null || d.phase!=null)return;
  const c=validatedClaudeTool(d.claude_tool,state);
  if(!c || c.reference.id!==resource.id || c.reference.native_id!==d.native_id || c.native_message_id!==d.native_parent_id || c.reference.name!=="Bash")return;
  const input=object(JSON.parse(c.proposal?.applied ?? c.initial_input));
  if(!bounded(input.command))return;
  return {adapter:CommandAdapter.Claude,command:input.command,outputs:c.result?.text!=null?[c.result.text]:c.result?.blocks?.map(block=>block.text) ?? [],error:c.result?.is_error===true,nonExecution:c.result?.non_execution?.non_execution_kind};
 }
 const t=object(d.tool),first=object(t.started);
 if(first.kind==="opencode-shell"){
  const snapshots=validatedShellSnapshots(t,state),latest=snapshots?.at(-1);
  if(!latest || typeof latest.input.command!=="string" || latest.status==="pending")return;
  return {adapter:CommandAdapter.OpenCode,command:latest.input.command,outputs:latest.error!==undefined?[latest.error]:latest.output!==undefined?[latest.output]:latest.preview!==undefined?[latest.preview]:[],error:latest.status==="failed",exit:latest.exit,truncated:latest.truncated,interrupted:latest.interrupted};
 }
 const start=codexSnapshot(t.started),last=t.completed==null?start:codexSnapshot(t.completed);
 if(!start || !last || !["streaming","complete"].includes(state) || (state==="complete" && (t.completed==null || !["completed","failed","declined"].includes(String(object(t.completed).status)))) || start.command!==last.command || start.cwd!==last.cwd || start.source!==last.source || t.output!=null&&!bounded(t.output))return;
 return {adapter:CommandAdapter.Codex,command:last.command as string,outputs:typeof last.aggregated_output==="string"?[last.aggregated_output]:typeof t.output==="string"?[t.output]:[],exit:last.exit_code as number|null|undefined};
}
export function commandPreview(value:CommandPresentation):string {
 const prefix="/bin/zsh -lc ";
 return value.adapter===CommandAdapter.Codex && value.command.startsWith(prefix)?value.command.slice(prefix.length):value.command;
}
export function CommandOutput({value,children}:{value:CommandPresentation;children:ReactNode}) {
 return <div className="tool-command-content">
  {value.error?<p>{copy("native-claude-tool.errorReported_284ccd")}</p>:null}
  {value.nonExecution?<p>{copy(value.nonExecution==="user-rejected"?"native-claude-tool.theOriginalUserRejectionPreventedThis_97b97e":"native-claude-tool.nativePermissionPolicyPreventedThisTool_8e5f57")}</p>:null}
  {value.exit!==undefined?<p><LocalizedText id="native-shell.nativeExitCode_991101" components={{s0:<>{value.exit===null?copy("native-shell.unavailable_ca1844"):value.exit}</>}}/></p>:null}
  {value.truncated?<p>{copy("native-shell.theNativeShellResultIsTruncated_e8b1dd")}</p>:null}
  {value.interrupted?<p><LocalizedText id="native-shell.nativeInterruption_edde92" components={{s0:<>{copy("native-shell.observed_64fa8a")}</>}}/></p>:null}
  {value.outputs.length?value.outputs.map((output,index)=><pre className="tool-command-output" key={index}><code>{output}</code></pre>):<p>{copy("session.commandOutputUnavailable")}</p>}
  <Disclosure className="tool-command-details"><DisclosureSummary>{copy("session.commandDetails")}</DisclosureSummary><div data-original-tool>{children}</div></Disclosure>
 </div>;
}
