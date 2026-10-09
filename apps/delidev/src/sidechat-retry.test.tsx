// SPDX-License-Identifier: Apache-2.0
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { ResourceSchema, EntityKind, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { parseSidechatRetryView, SidechatRetryPopover, sidechatAnswerFilter } from "./sidechat-retry";
import { i18n } from "./localization";
afterEach(()=>{cleanup();vi.useRealTimers();});

test("hover waits 300ms; the tooltip remains within the trigger owner and closes on departure",()=>{
 vi.useFakeTimers();render(<SidechatRetryPopover disabled={false} onRetry={vi.fn()}/>);
 const button=screen.getByRole("button"),owner=button.parentElement!,tooltip=screen.getByRole("tooltip",{hidden:true});
 fireEvent.pointerEnter(owner);act(()=>vi.advanceTimersByTime(299));expect(tooltip.hidden).toBe(true);
 act(()=>vi.advanceTimersByTime(1));expect(tooltip.hidden).toBe(false);
 fireEvent.pointerEnter(tooltip);expect(tooltip.hidden).toBe(false);fireEvent.pointerLeave(owner);expect(tooltip.hidden).toBe(true);
});
test("disabled keyboard focus explains the action immediately without sending or moving focus",()=>{
 const retry=vi.fn();const {rerender,unmount}=render(<SidechatRetryPopover disabled onRetry={retry}/>);
 const button=screen.getByRole("button"),tooltip=screen.getByRole("tooltip",{hidden:true});
 act(()=>button.focus());expect(tooltip.hidden).toBe(false);expect(button.getAttribute("aria-describedby")).toBe(tooltip.id);
 fireEvent.click(button);expect(retry).not.toHaveBeenCalled();fireEvent.keyDown(button,{key:"Escape"});expect(tooltip.hidden).toBe(true);expect(document.activeElement).toBe(button);
 fireEvent.focus(button);rerender(<SidechatRetryPopover active={false} disabled onRetry={retry}/>);expect(tooltip.hidden).toBe(true);expect(document.activeElement).toBe(button);unmount();
});
test("navigation and unmount clear pending hover timers and Korean explanation uses the approved copy",async()=>{
 await i18n.changeLanguage("ko");vi.useFakeTimers();const {rerender,unmount}=render(<SidechatRetryPopover disabled={false} onRetry={vi.fn()}/>);
 const button=screen.getByRole("button",{name:"재시도"}),tooltip=screen.getByRole("tooltip",{hidden:true});
 expect(tooltip.textContent).toBe("부모 세션의 최신 완료 실행을 기준으로 같은 질문에 다시 답변합니다. 같은 사이드챗을 유지하며, 완료되면 표시된 답변을 교체합니다. 이전 답변은 기록에 보존됩니다.");
 fireEvent.pointerEnter(button.parentElement!);rerender(<SidechatRetryPopover active={false} disabled onRetry={vi.fn()}/>);act(()=>vi.advanceTimersByTime(300));expect(tooltip.hidden).toBe(true);unmount();act(()=>vi.advanceTimersByTime(300));vi.useRealTimers();await i18n.changeLanguage("en");
});
test("strict observations reject oversized revisions, unknown fields, and foreign generation receipts",()=>{
 const base={candidate:false,eligible:false,child_revision:"1",question_revision:"0",parent_revision:"0",generations:[]};
 expect(parseSidechatRetryView(encode(base))).toBeDefined();
 for(const patch of [{child_revision:"9223372036854775808"},{secret:"x"},{observed_generation:newRequestId()}])expect(parseSidechatRetryView(encode({...base,...patch}))).toBeUndefined();
});
test("current answer selection preserves inherited context and original question; previous attempts remain history",()=>{
 const question=newRequestId(),baseline=newRequestId(),current=newRequestId(),otherQuestion=newRequestId();
 const session=create(ResourceSchema,{kind:EntityKind.SESSION,schemaVersion:1,revision:1n,documentJson:encode({fork:{sidechat_parent_snapshot:{}},sidechat_current_answer:current,sidechat_retries:[{question_id:question,previous_execution_id:baseline,execution_id:current}]})});
 const main=sidechatAnswerFilter(session),history=sidechatAnswerFilter(session,true);
 const rows=[{id:"inherited",revision:1n,role:"user",inherited:true,executionId:baseline,inputId:otherQuestion},{id:"question",revision:1n,role:"user",executionId:baseline,inputId:question},{id:"generated",revision:1n,role:"user",executionId:current,inputId:otherQuestion},{id:"old",revision:1n,role:"assistant",executionId:baseline},{id:"new",revision:1n,role:"assistant",executionId:current}];
 expect(rows.filter(main).map(r=>r.id)).toEqual(["inherited","question","new"]);expect(rows.filter(history).map(r=>r.id)).toEqual(["old"]);
 const ordinary=newRequestId();rows.push({id:"second question",revision:1n,role:"user",executionId:ordinary,inputId:otherQuestion},{id:"ordinary answer",revision:1n,role:"assistant",executionId:ordinary});
 expect(rows.filter(main).map(r=>r.id)).toEqual(["inherited","question","new","second question","ordinary answer"]);expect(rows.filter(history).map(r=>r.id)).toEqual(["old"]);
});

test("lost acceptance retains the exact request across presentation departure and observes its original receipt",async()=>{
 const {createRouterTransport,ConnectError,Code}=await import("@connectrpc/connect");
 const {TransportProvider}=await import("@connectrpc/connect-query");
 const {QueryClient,QueryClientProvider}=await import("@tanstack/react-query");
 const {SessionService,SystemService,SystemCapability}=await import("@delinoio/delidev-api-client");
 const {MutationIntents}=await import("./mutation");
 const {useSidechatQuestionRetry,SidechatRetryAction}=await import("./sidechat-retry");
 const child=newRequestId(),question=newRequestId(),parent=newRequestId(),turn=newRequestId(),worker=newRequestId(),instance=newRequestId();
 const session=create(ResourceSchema,{id:child,kind:EntityKind.SESSION,schemaVersion:1,revision:1n,documentJson:encode({fork:{sidechat_parent_snapshot:{}}})});
 const calls:unknown[]=[],observations:string[]=[];
 let accepted:any;
 const initial={candidate:true,eligible:true,child_revision:"1",question_id:question,question_revision:"3",parent_id:parent,parent_revision:"5",parent_turn_id:turn,generations:[]};
 const received=()=>({...initial,eligible:false,child_revision:"2",observed_generation:accepted.mutation.requestId,phase:"claimed",generations:[{id:accepted.mutation.requestId,previous_job_id:newRequestId(),previous_execution_id:newRequestId(),fork_job_id:newRequestId(),runtime_id:newRequestId(),question_id:question,question_revision:"3",parent_revision:"5",parent_execution_id:newRequestId(),parent_turn_id:turn,worker_device_id:worker,worker_instance_id:instance}]});
 const transport=createRouterTransport(router=>{
  router.service(SystemService,{getStatus:()=>({capabilities:[SystemCapability.SIDECHAT_QUESTION_RETRY_V1]})});
  router.service(SessionService,{getSidechatQuestionRetry:r=>{observations.push(r.requestId);return {documentJson:encode(accepted&&r.requestId?received():initial)};},retrySidechatQuestion:r=>{calls.push(r);if(!accepted){accepted=r;throw new ConnectError("fixture lost acknowledgement",Code.Unavailable);}return {requestId:r.mutation!.requestId,replayed:true,documentJson:encode(received())};}});
 });
 function Sender(){const c=useSidechatQuestionRetry(session,true);return <SidechatRetryAction controller={c} inputId={question}/>;}
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const view=(visible:boolean)=><TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{visible?<Sender/>:null}</MutationIntents></QueryClientProvider></TransportProvider>;
 const mounted=render(view(true));
 const {waitFor}=await import("@testing-library/react");
 await waitFor(()=>expect(screen.getByRole("button",{name:"Retry"}).getAttribute("aria-disabled")).toBe("false"));
 fireEvent.click(screen.getByRole("button",{name:"Retry"}));await screen.findByRole("button",{name:"Check original retry request"});
 mounted.rerender(view(false));mounted.rerender(view(true));
 fireEvent.click(await screen.findByRole("button",{name:"Check original retry request"}));
 await waitFor(()=>expect(calls).toHaveLength(2));expect(calls[1]).toEqual(calls[0]);
 await waitFor(()=>expect(observations).toContain(accepted.mutation.requestId));mounted.unmount();client.clear();
});
