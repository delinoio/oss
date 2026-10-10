// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { createRef } from "react";
import { encode } from "./documents";
import { TranscriptItem } from "./session";
import { ToolTurnTranscript } from "./tool-turn-transcript";
import { conversationProjection } from "./tool-turn-projection";
import { toolCommandPreview } from "./tool-command";
import { i18n } from "./localization";
afterEach(async()=>{cleanup();await i18n.changeLanguage('en');});
const sessionId=newRequestId(), execution=newRequestId();
const row=(data:object,id=newRequestId(),revision=1n)=>create(ResourceSchema,{id,sessionId,kind:EntityKind.MESSAGE,schemaVersion:1,revision,documentJson:encode({role:'tool',state:'complete',text:'',execution_id:execution,native_thread_id:'thread',native_turn_id:'turn',...data})});
const codex=(command:string,output:unknown='exact aggregate')=>row({tool:{started:{kind:'command',status:'running',command:{command,cwd:'/original directory'}},completed:{kind:'command',status:'completed',command:{command,cwd:'/original directory',aggregated_output:output,exit_code:0}},output:'independent stream'}});
function claude(command:string,applied?:string) {
 const id=newRequestId();return row({native_id:'original-tool',native_parent_id:'original-message',claude_tool:{reference:{id,native_id:'original-tool',name:'Bash'},message_id:newRequestId(),native_message_id:'original-message',index:0,caller:null,initial_input:JSON.stringify({command}),input_delta:null,proposal:{proposed:JSON.stringify({command}),applied:JSON.stringify({command:applied??command,cwd:'/claude directory'})},result:{native_event_id:'123e4567-e89b-42d3-a456-426614174000',is_error:true,text:null,blocks:[{kind:'text',text:''},{kind:'text',text:'<script>inert()</script>\n**literal**'}],structured:'{"original":true}',non_execution:{id:'original-tool',non_execution_kind:'user-rejected'}}}},id);
}
function shell(command:string){return row({tool:{started:{kind:'opencode-shell',status:'pending',shell:{call_id:'shell-original',input:{},raw:'{"command":"not authoritative"}'}},states:[{sequence:1,snapshot:{kind:'opencode-shell',status:'running',shell:{call_id:'shell-original',input:{command,workdir:'/original'},time:{start:1},metadata:{output:'stream preview',exit:null,exit_observed:false}}}}],completed:{kind:'opencode-shell',status:'completed',shell:{call_id:'shell-original',input:{command,workdir:'/original'},time:{start:1,end:2},title:command,output:'completed literal',metadata:{output:'distinct final preview',exit:7,exit_observed:true,truncated:true,outputPath:'https://untrusted.invalid/path',interrupted:true}}}}});}
function query(rows:Resource[]){return {pages:[{token:'exact-original-page',nextPageToken:'',rows:rows.map(r=>conversationProjection(r,sessionId))}],payloadPages:[{token:'exact-original-page',payload:rows}],nextPageToken:'',restore:vi.fn(),protect:vi.fn(),measure:vi.fn()};}
function props(q:ReturnType<typeof query>){return {sessionId,query:q,live:new Map<string,Resource>(),removed:new Set<string>(),arrivals:[],root:createRef<HTMLDivElement>(),render:(r:Resource)=><TranscriptItem resource={r}/>};}
function expand(node:HTMLDetailsElement){node.open=true;fireEvent(node,new Event('toggle'));}
function openEntry(container:HTMLElement){expand(container.querySelector<HTMLDetailsElement>('.tool-turn')!);const entry=container.querySelector<HTMLDetailsElement>('.tool-entry')!;expand(entry);return entry;}
it.each([
 ['/bin/zsh -lc pwd','pwd'],['/bin/bash -lc pwd','/bin/bash -lc pwd'],['/bin/zsh -l -c pwd','/bin/zsh -l -c pwd'],['echo /bin/zsh -lc pwd','echo /bin/zsh -lc pwd'],['/bin/zsh -lc  "한 🙂"\n  ',' "한 🙂"\n  '],[' /bin/zsh -lc pwd',' /bin/zsh -lc pwd'],['','']
])('removes only the exact Codex literal prefix from %j', (original,preview)=>{
 const resource=codex(original);expect(toolCommandPreview(resource)).toBe(preview);
 const {container}=render(<ToolTurnTranscript {...props(query([resource]))}/>);const entry=openEntry(container);
 expect(entry.querySelector('.tool-command-preview')?.textContent).toBe(`> ${preview}`);
 expect(entry.querySelector('pre code')?.textContent).toBe('exact aggregate');
 expect(entry.querySelector('[data-command-presentation] > details')?.hasAttribute('open')).toBe(false);
 expect(entry.querySelector('[data-command-presentation] > details pre')?.textContent).toBe(original);
 expect(entry.querySelector('summary small')).toBeNull();expect(container.textContent).not.toContain('Native aggregate output');
});
it.each(['',undefined,null])('distinguishes empty aggregate from absent aggregate %j without merging stream',aggregate=>{
 const resource=codex('pwd',aggregate);if(aggregate===undefined){const data=JSON.parse(new TextDecoder().decode(resource.documentJson));delete data.tool.completed.command.aggregated_output;resource.documentJson=encode(data);}
 const {container}=render(<ToolTurnTranscript {...props(query([resource]))}/>);const entry=openEntry(container);
 expect(entry.querySelector('pre code')?.textContent).toBe(aggregate===''?'':'independent stream');
 expect(container.querySelector('a,script')).toBeNull();
});
it('keeps absent output pending without creating an empty success result',()=>{
 const resource=row({state:'streaming',tool:{started:{kind:'command',status:'running',command:{command:'pwd'}}}});
 const {container}=render(<ToolTurnTranscript {...props(query([resource]))}/>);const entry=openEntry(container);
 expect(entry.querySelector('.tool-command-output')).toBeNull();expect(entry.textContent).toContain('No command output has been observed.');expect(entry.querySelector('summary small')?.textContent).toBe('running');
});
it('uses original validated Claude applied input and preserves ordered result/error/non-execution evidence',()=>{
 const resource=claude('initial','/bin/zsh -lc pwd');expect(toolCommandPreview(resource)).toBe('/bin/zsh -lc pwd');
 const {container}=render(<ToolTurnTranscript {...props(query([resource]))}/>);const entry=openEntry(container);
 const output=entry.querySelector('[aria-label="Tool result blocks"]')!;expect([...output.querySelectorAll('pre')].map(n=>n.textContent)).toEqual(['','<script>inert()</script>\n**literal**']);
 expect(entry.textContent).toContain('The original user rejection prevented this tool from executing.');expect(entry.textContent).toContain('Error reported');expect(entry.textContent).toContain('{"original":true}');expect(container.querySelector('script,a')).toBeNull();
});
it('uses Claude initial input while streaming and never parses streamed fragments as a command',()=>{
 const resource=claude('original');const d=JSON.parse(new TextDecoder().decode(resource.documentJson));d.state='streaming';d.claude_tool.proposal=null;d.claude_tool.result=null;d.claude_tool.input_delta='{"command":"invented';resource.documentJson=encode(d);
 expect(toolCommandPreview(resource)).toBe('original');d.claude_tool.initial_input='{}';resource.documentJson=encode(d);expect(toolCommandPreview(resource)).toBeUndefined();
});
it('preserves OpenCode command wrapper, output, independent observations and warnings without reading saved paths',()=>{
 const resource=shell('/bin/zsh -lc pwd');expect(toolCommandPreview(resource)).toBe('/bin/zsh -lc pwd');
 const {container}=render(<ToolTurnTranscript {...props(query([resource]))}/>);const entry=openEntry(container);
 expect(entry.querySelector('pre code')?.textContent).toBe('completed literal');expect(entry.textContent).toContain('stream preview');expect(entry.textContent).toContain('distinct final preview');expect(entry.textContent).toContain('Native exit code: 7');expect(entry.textContent).toContain('truncated');expect(entry.textContent).toContain('Native interruption: Observed');expect(entry.textContent).toContain('https://untrusted.invalid/path');expect(container.querySelector('a,script')).toBeNull();
});
it('shows running OpenCode preview directly and leaves failed error visible',()=>{
 const resource=shell('pwd'),d=JSON.parse(new TextDecoder().decode(resource.documentJson));d.state='streaming';delete d.tool.completed;resource.documentJson=encode(d);
 const q=query([resource]),{container,rerender}=render(<ToolTurnTranscript {...props(q)}/>);const entry=openEntry(container);expect(entry.querySelector('pre code')?.textContent).toBe('stream preview');
 d.state='complete';d.tool.completed={kind:'opencode-shell',status:'failed',shell:{call_id:'shell-original',input:{command:'pwd',workdir:'/original'},time:{start:1,end:2},error:'original error'}};const failed={...resource,revision:2n,documentJson:encode(d)};
 rerender(<ToolTurnTranscript {...props(q)} live={new Map([[resource.id,failed]])}/>);expect(entry.querySelector('pre code')?.textContent).toBe('original error');expect(entry.querySelector('summary small')?.textContent).toBe('failed');
});
it('keeps malformed adapter payloads standalone and non-command names without inferred previews',()=>{
 for(const resource of [claude('secret'),shell('secret')]){const d=JSON.parse(new TextDecoder().decode(resource.documentJson));if(d.claude_tool)d.claude_tool.proposal.applied='invalid';else d.tool.completed.shell.input.command='changed';resource.documentJson=encode(d);expect(toolCommandPreview(resource)).toBeUndefined();expect(conversationProjection(resource,sessionId).tool).toBeUndefined();}
 const d=row({tool:{started:{kind:'read',command:{command:'not a command'}}}});expect(toolCommandPreview(d)).toBeUndefined();
 const grok=row({grok_tool:{method:'session/update',payload:{update:{command:'not a command'}}}});expect(toolCommandPreview(grok)).toBeUndefined();
});
it('preserves manual Details/focus through revisions, locale and exact-page eviction without projecting commands',async()=>{
 const resource=codex('/bin/zsh -lc original'),q=query([resource]),p=props(q);const view=render(<ToolTurnTranscript {...p}/>);const entry=openEntry(view.container);const details=entry.querySelector<HTMLDetailsElement>('[data-command-presentation] > details')!;expand(details);const summary=details.querySelector('summary')!;summary.focus();
 const next={...resource,revision:2n};view.rerender(<ToolTurnTranscript {...p} live={new Map([[next.id,next]])}/>);await act(()=>i18n.changeLanguage('ko'));expect(details.open).toBe(true);expect(document.activeElement).toBe(summary);expect(summary.textContent).toBe('세부 정보');
 view.rerender(<ToolTurnTranscript {...p} query={{...q,payloadPages:[]}}/>);expect(entry.querySelector('.tool-command-preview')).toBeNull();expect(entry.querySelector('summary')?.textContent).toContain('command');fireEvent.click(entry.querySelector('button')!);expect(q.restore).toHaveBeenCalledExactlyOnceWith('exact-original-page');view.rerender(<ToolTurnTranscript {...p}/>);expect(entry.querySelector<HTMLDetailsElement>('[data-command-presentation] > details')?.open).toBe(true);
 expect(JSON.stringify(conversationProjection(resource,sessionId),(_,value)=>typeof value==='bigint'?String(value):value)).not.toContain('/bin/zsh');
});

it('falls back to validated initial Claude command when applied input has no string command',()=>{
 const resource=claude('initial original');const d=JSON.parse(new TextDecoder().decode(resource.documentJson));d.claude_tool.proposal.applied='{}';resource.documentJson=encode(d);expect(toolCommandPreview(resource)).toBe('initial original');
});
it('keeps named nested Claude choices and original focused input across new streamed disclosures',()=>{
 const resource=claude('original'),d=JSON.parse(new TextDecoder().decode(resource.documentJson));d.state='streaming';d.claude_tool.proposal=null;d.claude_tool.result=null;resource.documentJson=encode(d);
 const q=query([resource]),p=props(q),view=render(<ToolTurnTranscript {...p}/>);const entry=openEntry(view.container);expand(entry.querySelector<HTMLDetailsElement>('[data-tool-detail="command-details"]')!);const initial=entry.querySelector<HTMLDetailsElement>('[data-tool-detail="claude-initial"]')!;expand(initial);const summary=initial.querySelector('summary')!;summary.focus();
 d.claude_tool.input_delta='{"command":"updated"}';d.claude_tool.proposal={proposed:d.claude_tool.input_delta,applied:'{"command":"updated"}'};const next={...resource,revision:2n,documentJson:encode(d)};
 view.rerender(<ToolTurnTranscript {...p} live={new Map([[resource.id,next]])}/>);expect(entry.querySelector('[data-tool-detail="claude-initial"]')).toBe(initial);expect(initial.open).toBe(true);expect(document.activeElement).toBe(summary);expect(entry.querySelector<HTMLDetailsElement>('[data-tool-detail="claude-streamed"]')?.open).toBe(false);
});
