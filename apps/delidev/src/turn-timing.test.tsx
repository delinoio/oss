// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { encode } from "./documents";
import { i18n } from "./localization";
import { conversationProjection } from "./tool-turn-projection";
import { ToolTurnTranscript } from "./tool-turn-transcript";
import { currentTurn, messageTurn, retainedTurnTiming, turnDuration, type CurrentTurn } from "./turn-timing";
const sessionId=newRequestId(),executionId=newRequestId(),inputId=newRequestId(),jobId=newRequestId();
const start="2026-10-09T10:00:00.123Z";
function resource(kind:EntityKind,data:object,id=newRequestId(),revision=1n):Resource{return create(ResourceSchema,{kind,id,sessionId,revision,schemaVersion:1,documentJson:encode(data)});}
function session(extra:object={}){return resource(EntityKind.SESSION,{initial_execution:{id:executionId,input_id:inputId},active_execution_id:executionId,execution:{execution_id:executionId,input_id:inputId,job_id:jobId,last_sequence:2,accepted_inputs:[{input_id:inputId,prompt_digest:"a".repeat(64)}],native_thread_id:"original-thread",native_turn_id:"original-turn",outcome:"running",turn_timing:{accepted_at:start},...extra}},sessionId);}
function message(extra:object={},id=newRequestId(),revision=1n){return resource(EntityKind.MESSAGE,{role:"user",text:"Original native input",state:"complete",execution_id:executionId,input_id:inputId,native_thread_id:"original-thread",native_turn_id:"original-turn",first_sequence:3,last_sequence:4,turn_timing:{accepted_at:start},...extra},id,revision);}
function query(pages:Resource[][]){return {pages:pages.map((rows,index)=>({token:index?`original-${index}`:"",nextPageToken:index<pages.length-1?`original-${index+1}`:"",rows:rows.map(row=>conversationProjection(row,sessionId))})),payloadPages:pages.map((payload,index)=>({token:index?`original-${index}`:"",payload})),nextPageToken:"",restore:vi.fn(),measure:vi.fn(),protect:vi.fn()};}
function view(q= query([]),current:CurrentTurn|undefined=currentTurn(session(),sessionId),extra:object={}){return {sessionId,query:q,current,active:true,confirmed:true,live:new Map<string,Resource>(),removed:new Set<string>(),arrivals:[] as string[],root:createRef<HTMLDivElement>(),render:(row:Resource)=><article><button>{row.id}</button></article>,...extra};}
afterEach(async()=>{cleanup();vi.useRealTimers();await i18n.changeLanguage("en");});

it.each([undefined,null,{}, {accepted_at:"bad"},{accepted_at:"2026-02-30T00:00:00Z"},{accepted_at:"2026-10-09T10:00:00+00:00"},{accepted_at:start,terminal_at:null},{accepted_at:start,terminal_at:"2026-10-09T10:00:00.122Z"},{accepted_at:start,provider_elapsed_ms:12}])("marks missing/malformed/inverted timing unavailable %j",timing=>{expect(retainedTurnTiming(timing)).toBeUndefined();const p=view(query([[message({turn_timing:timing})]]),undefined,{current:undefined});const {container}=render(<ToolTurnTranscript {...p}/>);expect(container.querySelector(".turn-time")?.textContent).toContain("Time unavailable");expect(container.textContent).not.toContain("Elapsed · 0s");});

it("starts before the first message, advances through waits and moves one display to the original primary user",()=>{
 vi.useFakeTimers();vi.setSystemTime(new Date(start));const p=view(),renderer=vi.fn(p.render);const {container,rerender}=render(<ToolTurnTranscript {...p} render={renderer}/>);
 expect(container.querySelectorAll(".turn-time")).toHaveLength(1);expect(container.textContent).toContain("In progress · 0s");expect(vi.getTimerCount()).toBe(1);
 act(()=>vi.advanceTimersByTime(83000));expect(container.textContent).toContain("In progress · 1m 23s");
 const primary=message(),part=message(),steer=message({input_id:newRequestId(),turn_timing:undefined}),assistant=message({role:"assistant",input_id:undefined,turn_timing:undefined});const q=query([[primary,assistant],[part,steer]]);
 rerender(<ToolTurnTranscript {...p} query={q} render={renderer}/>);expect(container.querySelectorAll(".turn-time")).toHaveLength(1);expect(container.querySelector(".turn-time")?.nextElementSibling?.tagName).toBe("ARTICLE");
 const focused=screen.getByRole("button",{name:primary.id});focused.focus();const calls=renderer.mock.calls.length;
 act(()=>vi.advanceTimersByTime(1000));expect(container.textContent).toContain("1m 24s");expect(renderer.mock.calls.length).toBe(calls);expect(document.activeElement).toBe(focused);expect(vi.getTimerCount()).toBe(1);
});

it("freezes disconnected/failed-read estimates and resumes authoritative observations without inventing an end",()=>{
 vi.useFakeTimers();vi.setSystemTime(new Date(start));const p=view();const {container,rerender,unmount}=render(<ToolTurnTranscript {...p}/>);act(()=>vi.advanceTimersByTime(12000));
 rerender(<ToolTurnTranscript {...p} confirmed={false}/>);expect(container.textContent).toContain("Unconfirmed · 12s");expect(vi.getTimerCount()).toBe(0);act(()=>vi.advanceTimersByTime(30000));expect(container.textContent).toContain("Unconfirmed · 12s");
 rerender(<ToolTurnTranscript {...p}/>);expect(container.textContent).toContain("In progress · 42s");expect(vi.getTimerCount()).toBe(1);
 rerender(<ToolTurnTranscript {...p} active={false}/>);act(()=>vi.advanceTimersByTime(60000));expect(vi.getTimerCount()).toBe(0);rerender(<ToolTurnTranscript {...p}/>);expect(container.textContent).toContain("1m 42s");unmount();expect(vi.getTimerCount()).toBe(0);
});

it.each(["succeeded","failed","stopped"])("freezes %s at the immutable terminal and preserves genuine zero",outcome=>{
 vi.useFakeTimers();vi.setSystemTime(new Date(start));const p=view();const {container,rerender}=render(<ToolTurnTranscript {...p}/>);act(()=>vi.advanceTimersByTime(15000));
 const terminal=currentTurn(session({outcome,turn_timing:{accepted_at:start,terminal_at:"2026-10-09T10:00:12.123Z"}}),sessionId);
 rerender(<ToolTurnTranscript {...p} current={terminal}/>);expect(container.textContent).toContain("Elapsed · 12s");expect(vi.getTimerCount()).toBe(0);act(()=>vi.advanceTimersByTime(120000));expect(container.textContent).toContain("Elapsed · 12s");
 rerender(<ToolTurnTranscript {...p} current={currentTurn(session({outcome,turn_timing:{accepted_at:start,terminal_at:start}}),sessionId)}/>);expect(container.textContent).toContain("Elapsed · 0s");
});

it("deduplicates split historical/live parts through bounded eviction/restoration and preserves original tokens/focus",()=>{
 const first=message({turn_timing:{accepted_at:start,terminal_at:"2026-10-09T10:00:12.123Z"}}),second=message({turn_timing:{accepted_at:start,terminal_at:"2026-10-09T10:00:12.123Z"}});const q=query([[first],[message({role:"assistant",turn_timing:undefined})],[second]]);const p=view(q,undefined,{current:undefined});const {container,rerender}=render(<ToolTurnTranscript {...p}/>);expect(container.querySelectorAll(".turn-time")).toHaveLength(1);
 const anchor=container.querySelector('[data-payload-page=""]');rerender(<ToolTurnTranscript {...p} query={{...q,payloadPages:q.payloadPages.slice(1)}}/>);expect(container.querySelectorAll(".turn-time")).toHaveLength(1);expect(container.querySelector('[data-payload-page=""]')).toBe(anchor);fireEvent.click(screen.getByRole("button",{name:/Restore/}));expect(q.restore).toHaveBeenCalledWith("");rerender(<ToolTurnTranscript {...p}/>);expect(container.querySelectorAll(".turn-time")).toHaveLength(1);expect(q.payloadPages).toHaveLength(3);
 const button=screen.getByRole("button",{name:first.id});button.focus();rerender(<ToolTurnTranscript {...p} active={false}/>);rerender(<ToolTurnTranscript {...p}/>);expect(document.activeElement).toBe(button);
});

it("retains completed Fork attribution while independently timing the new child turn",()=>{
 vi.useFakeTimers();vi.setSystemTime(new Date(start));const sourceSession=newRequestId(),sourceExecution=newRequestId(),sourceInput=newRequestId();const inherited=message({execution_id:newRequestId(),input_id:"",first_sequence:0,last_sequence:0,inherited:{session_id:sourceSession,execution_id:sourceExecution,input_id:sourceInput,message_id:newRequestId(),first_sequence:3,last_sequence:4},turn_timing:{accepted_at:"2026-10-09T09:00:00Z",terminal_at:"2026-10-09T09:00:12Z"}});
 const p=view(query([[inherited]]));const {container}=render(<ToolTurnTranscript {...p}/>);expect(container.querySelectorAll(".turn-time")).toHaveLength(2);expect(container.textContent).toContain("Inherited turn · Elapsed · 12s");expect(container.querySelector("[aria-label]")?.getAttribute("aria-label")).toContain(sourceSession);act(()=>vi.advanceTimersByTime(5000));expect(container.textContent).toContain("In progress · 5s");expect(container.textContent).toContain("Inherited turn · Elapsed · 12s");
 expect(messageTurn({...inherited,sessionId:newRequestId()},sessionId)).toBeUndefined();
});

it("keeps long durations localized and readable without per-second announcements",async()=>{
 expect(turnDuration(90061)).toBe("1d 1h 1m 1s");await i18n.changeLanguage("ko");expect(turnDuration(90061)).toBe("1일 1시간 1분 1초");vi.useFakeTimers();vi.setSystemTime(new Date(start));const {container}=render(<ToolTurnTranscript {...view()}/>);expect(container.textContent).toContain("진행 중 · 0초");expect(container.querySelector(".turn-time")?.getAttribute("aria-live")).toBe("off");expect(container.querySelector(".turn-time")?.getAttribute("role")).toBeNull();
});

it("rejects queued/pre-acceptance/foreign selected generations and terminal outcomes without captured end",()=>{
 expect(currentTurn(session({outcome:"not-started",native_turn_id:undefined,turn_timing:undefined}),sessionId)).toBeUndefined();expect(currentTurn(session({execution_id:newRequestId()}),sessionId)).toBeUndefined();expect(currentTurn(session({input_id:newRequestId()}),sessionId)).toBeUndefined();expect(currentTurn(session({outcome:"succeeded"}),sessionId)?.timing).toBeUndefined();
 for (const extra of [{last_sequence:1,accepted_inputs:undefined,turn_timing:{accepted_at:start}},{outcome:"not-started",native_turn_id:undefined,accepted_inputs:undefined,turn_timing:{accepted_at:start}},{native_turn_id:undefined,turn_timing:{accepted_at:start}}]) { const prior=currentTurn(session(extra),sessionId); expect(prior?.timing).toBeUndefined(); }
 const projected=conversationProjection(message({text:"DO NOT RETAIN original input"}),sessionId);expect(JSON.stringify(projected,(_,v)=>typeof v==="bigint"?String(v):v)).not.toContain("DO NOT RETAIN");
});

it("anchors pre-user timing before the first original native row and relocates only to its late primary",()=>{
 vi.useFakeTimers();vi.setSystemTime(new Date(start));
 const prior=message({role:"assistant",input_id:undefined,turn_timing:undefined,execution_id:newRequestId(),text:"Earlier execution"});
 const foreign=message({role:"assistant",input_id:undefined,turn_timing:undefined,native_turn_id:"foreign-turn",text:"Foreign turn"});
 const early=message({role:"assistant",input_id:undefined,turn_timing:undefined,text:"Early native output"});
 const p=view(query([[prior,foreign],[early]])); const {container,rerender}=render(<ToolTurnTranscript {...p}/>);
 expect(container.querySelectorAll(".turn-time")).toHaveLength(1);
 const before=container.querySelector(".turn-time")!;expect(before.nextElementSibling?.textContent).toContain(early.id);expect(before.parentElement?.previousElementSibling).toBeNull();
 act(()=>vi.advanceTimersByTime(12000));expect(before.textContent).toContain("12s");
 const primary=message({first_sequence:8,last_sequence:9});rerender(<ToolTurnTranscript {...p} query={query([[prior,foreign],[early,primary]])}/>);
 expect(container.querySelectorAll(".turn-time")).toHaveLength(1);expect(container.querySelector(".turn-time")?.nextElementSibling?.textContent).toContain(primary.id);expect(container.querySelector(".turn-time")?.textContent).toContain("12s");expect(vi.getTimerCount()).toBe(1);
});
