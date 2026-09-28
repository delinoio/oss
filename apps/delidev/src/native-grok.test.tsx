import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { object, type Document } from "./documents";
import { NativeGrokText, NativeGrokUser, NativeGrokUsage, NativeGrokTerminal, NativeGrokStop } from "./native-grok";
const thread = "01960dcb-e1fa-7000-8000-000000000001";
function textFixture(): Document {
 const meta = { event_id: `${thread}-10`, chunk_id: "1", context_tokens: "18446744073709551615", timestamp_ms: "1", stream_start_ms: "0", turn_start_ms: "0" };
 return { execution_id: thread, native_thread_id: thread, native_turn_id: "526452fa-1956-42dd-b5f4-60e2b23dfe92", native_id: meta.event_id, role: "assistant", state: "complete", text: "Original <script>private()</script> 한글", first_sequence: 3, last_sequence: 4, grok_text: { response_ordinal: 1, chunks: [{ ...meta }] } };
}
function usageFixture(): Document { return { ordinal: 1, counts: { input_tokens: "18446744073709551615", output_tokens: "5", cache_read_input_tokens: "0", cache_creation_input_tokens: "0", reasoning_tokens: "0" } }; }

function userFixture(): Document {
 const data = textFixture(); delete data.grok_text;
 return { ...data, role: "user", input_id: "01960dcb-e1fa-7000-8000-000000000002", native_id: `${thread}-2`, first_sequence: 5, last_sequence: 5, grok_user: { source: "closed-first-text", native_event_id: `${thread}-2`, timestamp_ms: "253402300799999", prompt_index: "0", model: "Original model", input_digest: "ab".repeat(32) } };
}
it("shows the original user input with its closed-history provenance", () => {
 const { container } = render(<NativeGrokUser data={userFixture()} />);
 expect(screen.getByLabelText("Grok user input")).toBeTruthy();
 expect(screen.getByText("Verified from closed native history")).toBeTruthy();
 expect(screen.getByText("Original <script>private()</script> 한글")).toBeTruthy();
 expect(screen.getByText("Native timestamp (ms)").nextElementSibling?.textContent).toBe("253402300799999");
 expect(container.querySelector("script")).toBeNull();
});
it.each(["role", "state", "input", "foreign", "source", "index", "timestamp", "rounded", "digest", "mixed", "unknown", "surrogate", "sequence"])("rejects inconsistent closed Grok user input: %s", change => {
 const data = userFixture(), user = object(data.grok_user);
 if(change === "role") data.role = "assistant";
 if(change === "state") data.state = "streaming";
 if(change === "input") delete data.input_id;
 if(change === "foreign") user.native_event_id = "foreign-2";
 if(change === "source") user.source = "live";
 if(change === "index") user.prompt_index = "1";
 if(change === "timestamp") user.timestamp_ms = "253402300800000";
 if(change === "rounded") user.timestamp_ms = 1;
 if(change === "digest") user.input_digest = "";
 if(change === "mixed") data.grok_text = textFixture().grok_text;
 if(change === "unknown") user.extra = true;
 if(change === "surrogate") data.text = "\ud800";
 if(change === "sequence") data.last_sequence = 6;
 render(<NativeGrokUser data={data} />);
 expect(screen.getByLabelText("Grok user input unavailable")).toBeTruthy();
 expect(screen.queryByText("Verified from closed native history")).toBeNull();
});
it("preserves inert original text, exact context and distinct response completion", () => {
 const { container } = render(<NativeGrokText data={textFixture()} />);
 expect(screen.getByText("Original <script>private()</script> 한글")).toBeTruthy();
 expect(container.querySelector("script")).toBeNull();
 expect(screen.getByText("Latest reported context tokens").nextElementSibling?.textContent).toBe("18446744073709551615");
 expect(screen.getByText(/does not mean the execution has finished/)).toBeTruthy();
});
it.each(["foreign-event", "foreign-prompt", "replaced-anchor", "mixed", "missing", "rounded", "regression", "duplicate-chunks", "unknown", "bad-text"])("rejects inconsistent Grok text: %s", (change) => {
 const data=textFixture(),v=object(data.grok_text),chunks=v.chunks as Document[],last=chunks[0]!;
 if(change==="foreign-event") last.event_id="another-session-10";
 if(change==="foreign-prompt") data.native_turn_id=thread;
 if(change==="replaced-anchor") data.native_id=`${thread}-11`;
 if(change==="mixed") data.claude={};
 if(change==="missing") delete last.context_tokens;
 if(change==="rounded") last.context_tokens=18446744073709551615;
 if(change==="regression") { chunks.push({...last,event_id:`${thread}-9`,chunk_id:"2"}); data.last_sequence=5; }
 if(change==="duplicate-chunks") { chunks.push({...last}); data.last_sequence=5; }
 if(change==="unknown") last.extra="not original";
 if(change==="bad-text") data.text="\ud800";
 render(<NativeGrokText data={data} />);
 expect(screen.getByLabelText("Grok text unavailable")).toBeTruthy();
 expect(screen.queryByText(data.text as string)).toBeNull();
});
it("retains response-only counters without synthesizing totals or costs", () => {
 render(<NativeGrokUsage value={usageFixture()} />);
 expect(screen.getByText("Input tokens").nextElementSibling?.textContent).toBe("18446744073709551615");
 expect(screen.getByText("Cache read input tokens").nextElementSibling?.textContent).toBe("0");
 expect(screen.getByText("Response total").nextElementSibling?.textContent).toBe("Not reported");
 expect(screen.getByText("Actual cost").nextElementSibling?.textContent).toBe("Not reported");
});
it.each(["", "01", "-1", "1e3", "18446744073709551616", 11, null])("rejects inexact or absent Grok usage %s", (count) => {
 const value=usageFixture();object(value.counts).input_tokens=count;
 render(<NativeGrokUsage value={value} />);
 expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
 expect(screen.queryByText("Input tokens")).toBeNull();
});
it.each([0,129,1.5])("rejects invalid response order %s", (ordinal) => {
 render(<NativeGrokUsage value={{...usageFixture(),ordinal}} />);
 expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
});

function terminalFixture(): Document {
 return {execution_id:thread,native_thread_id:thread,native_turn_id:"526452fa-1956-42dd-b5f4-60e2b23dfe92",outcome:"succeeded",observed:{model:"Original model",grok_mode:"default"},grok_content:{responses:1,message_bytes:0,message_chunks:0,text_bytes:8,last_event:`${thread}-10`,last_chunk:"1"},grok_terminal:{kind:"closed-first-text",native_event_id:`${thread}-12`,timestamp_ms:"1",elapsed_ms:"18446744073709551615",model:"Original model",counts:usageFixture().counts,total_tokens:"18446744073709551615",model_calls:"1",api_duration_ms:"3",turns:"1",closure_id:thread,history_digest:"ab".repeat(32)}};
}
it("displays original closed input totals without implying continuation or additional usage",()=>{
 render(<NativeGrokTerminal progress={terminalFixture()} />);
 expect(screen.getByText("Reported input total tokens").nextElementSibling?.textContent).toBe("18446744073709551615");
 expect(screen.getByText(/Input totals overlap/)).toBeTruthy();
 expect(screen.queryByRole("button")).toBeNull();
});
it.each(["valid", "missing-reservation", "missing-proof", "foreign-model", "event-order", "null-proof"])("validates closed user provenance alongside the original terminal: %s", change => {
 const progress=terminalFixture(), terminal=object(progress.grok_terminal), user=object(userFixture().grok_user);
 progress.grok_user_message_id="01960dcb-e1fa-7000-8000-000000000002";
 terminal.user=user;
 if(change==="missing-reservation") delete progress.grok_user_message_id;
 if(change==="missing-proof") delete terminal.user;
 if(change==="foreign-model") user.model="other";
 if(change==="event-order") user.native_event_id=`${thread}-10`;
 if(change==="null-proof") terminal.user=null;
 render(<NativeGrokTerminal progress={progress}/>);
 if(change==="valid") expect(screen.getByText("Original Grok input completion")).toBeTruthy();
 else expect(screen.getByText(/completion is unavailable or inconsistent/)).toBeTruthy();
});
it.each(["mixed","mode","model","prompt","outcome","open-text","missing-total","calls","event","history","event-order","missing-content"])("rejects inconsistent original Grok completion: %s",change=>{
 const data=terminalFixture(),v=object(data.grok_terminal);
 if(change==="mixed")data.claude_terminal={};
 if(change==="mode")object(data.observed).grok_mode="plan";
 if(change==="model")v.model="Other model";
 if(change==="prompt")data.native_turn_id=thread;
 if(change==="outcome")data.outcome="running";
 if(change==="open-text")object(data.grok_content).message_id=thread;
 if(change==="missing-total")delete v.total_tokens;
 if(change==="calls")v.model_calls="2";
 if(change==="event")v.native_event_id=`${thread}-01`;
 if(change==="history")v.history_digest="";
 if(change==="event-order")object(data.grok_content).last_event=v.native_event_id;
 if(change==="missing-content")delete object(data.grok_content).last_chunk;
 render(<NativeGrokTerminal progress={data} />);
 expect(screen.getByText(/completion is unavailable or inconsistent/)).toBeTruthy();
 expect(screen.queryByText("Reported input total tokens")).toBeNull();
});

const stopId="01960dcb-e1fa-7000-8000-000000000002", inputId="01960dcb-e1fa-7000-8000-000000000003", operationId="01960dcb-e1fa-7000-8000-000000000004", messageId="01960dcb-e1fa-7000-8000-000000000005";
function stoppedFixture(completed=false): Document {
 const progress=terminalFixture();delete progress.grok_terminal;
 progress.input_id=inputId;progress.outcome=completed?"succeeded":"stopped";
 if(!completed) progress.grok_content={responses:0,message_id:messageId,message_bytes:8,message_chunks:1,text_bytes:8,last_event:`${thread}-10`,last_chunk:"1"};
 progress.grok_stop={kind:completed?"completed-during-stop":"interrupted-text",request_id:stopId,input_id:inputId,input_request_id:operationId,message_id:messageId,native_event_id:`${thread}-12`,timestamp_ms:"1",elapsed_ms:"18446744073709551615",model:"Original model",output_digest:"ab".repeat(32),text_chunks:1,delivered:true,idle:true,cleanup_joined:true,...(completed?{completed:{counts:usageFixture().counts,total_tokens:"18446744073709551615",model_calls:"1",api_duration_ms:"2",turns:"1"}}:{category:"MidTurnAbort",context_tokens:"18446744073709551615",retries:[{native_event_id:`${thread}-11`,timestamp_ms:"1",kind:"retrying",error:"http",attempt:"1",max_retries:"3"}]})};
 return progress;
}
it("shows interrupted output without asserting a completed response or missing usage",()=>{
 const data=textFixture();object(data.grok_text).interruption={request_id:stopId,native_event_id:`${thread}-12`};
 render(<NativeGrokText data={data}/>);
 expect(screen.getByText("Partial response stopped")).toBeTruthy();
 expect(screen.queryByText("Response text complete")).toBeNull();
 expect(screen.getByText(/Its usage was not reported/)).toBeTruthy();
});
it.each([null,{request_id:stopId,native_event_id:`${thread}-10`},{request_id:stopId,native_event_id:"foreign-12"},{request_id:stopId,native_event_id:`${thread}-12`,extra:true}])("rejects a changed interruption marker %s",interruption=>{
 const data=textFixture();object(data.grok_text).interruption=interruption;
 render(<NativeGrokText data={data}/>);
 expect(screen.getByLabelText("Grok text unavailable")).toBeTruthy();
});
it.each([false,true])("keeps Stop native outcome, usage and workspace cleanup independent (%s)",completed=>{
 const data=stoppedFixture(completed);render(<NativeGrokStop progress={data}/>);
 expect(screen.getByText("Native outcome").nextElementSibling?.textContent).toBe(completed?"Completed while Stop was requested":"Interrupted");
 expect(screen.getByText("Native elapsed time (ms)").nextElementSibling?.textContent).toBe("18446744073709551615");
 expect(screen.getByText("Workspace cleanup report").nextElementSibling?.textContent).toBe("Not yet verified");
 if(!completed) {expect(screen.getByText("Input usage").nextElementSibling?.textContent).toBe("Not reported");expect(screen.getByText("Reported context tokens").nextElementSibling?.textContent).toBe("18446744073709551615");}
 expect(screen.queryByRole("button")).toBeNull();
});
function beforeTextFixture(): Document {
 const data=stoppedFixture(),v=object(data.grok_stop);
 delete data.grok_content;delete v.message_id;
 v.kind="interrupted-before-text";v.text_chunks=0;v.output_digest="e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";
 return data;
}
it("shows original pre-text Stop without inventing a message, usage or workspace cleanup",()=>{
 render(<NativeGrokStop progress={beforeTextFixture()}/>);
 expect(screen.getByText("Native outcome").nextElementSibling?.textContent).toBe("Interrupted before first text");
 expect(screen.getByText(/No assistant text was observed/)).toBeTruthy();
 expect(screen.getByText("Input usage").nextElementSibling?.textContent).toBe("Not reported");
 expect(screen.getByText("Reported context tokens").nextElementSibling?.textContent).toBe("18446744073709551615");
 expect(screen.getByText("Workspace cleanup report").nextElementSibling?.textContent).toBe("Not yet verified");
 expect(screen.queryByText(/Partial output remains visible/)).toBeNull();
 expect(screen.queryByRole("button")).toBeNull();
});
it.each(["message","empty-message","null-message","chunks","digest","content","null-content","usage","context","completed","input","mixed","event","outcome"])("rejects invented or mixed pre-text Stop: %s",change=>{
 const data=beforeTextFixture(),v=object(data.grok_stop);
 if(change==="message")v.message_id=messageId;
 if(change==="empty-message")v.message_id="";
 if(change==="null-message")v.message_id=null;
 if(change==="chunks")v.text_chunks=1;
 if(change==="digest")v.output_digest="ab".repeat(32);
 if(change==="content")data.grok_content=stoppedFixture().grok_content;
 if(change==="null-content")data.grok_content=null;
 if(change==="usage")data.latest_usage_id=messageId;
 if(change==="context")delete v.context_tokens;
 if(change==="completed")v.completed={};
 if(change==="input")v.input_id=messageId;
 if(change==="mixed")data.claude_stop={};
 if(change==="event")v.native_event_id="foreign-12";
 if(change==="outcome")data.outcome="succeeded";
 render(<NativeGrokStop progress={data}/>);
 expect(screen.getByText(/Stop is unavailable or inconsistent/)).toBeTruthy();
 expect(screen.queryByText("Reported context tokens")).toBeNull();
});
it.each(["input","mode","model","mixed","partial","chunks","context","usage","cleanup","retry","event","rounded","scope","outcome"])("rejects changed Grok Stop: %s",change=>{
 const data=stoppedFixture(),v=object(data.grok_stop);
 if(change==="input")v.input_id=messageId;
 if(change==="mode")object(data.observed).grok_mode="plan";
 if(change==="model")v.model="foreign";
 if(change==="mixed")data.grok_terminal={};
 if(change==="partial")object(data.grok_content).responses=1;
 if(change==="chunks")v.text_chunks=2;
 if(change==="context")delete v.context_tokens;
 if(change==="usage")v.completed={};
 if(change==="cleanup")v.cleanup_joined=false;
 if(change==="retry")object((v.retries as Document[])[0]).attempt="01";
 if(change==="event")v.native_event_id=`${thread}-10`;
 if(change==="rounded")v.context_tokens=18446744073709551615;
 if(change==="scope")v.input_request_id=inputId;
 if(change==="outcome")data.outcome="succeeded";
 render(<NativeGrokStop progress={data}/>);
 expect(screen.getByText(/Stop is unavailable or inconsistent/)).toBeTruthy();
 expect(screen.queryByText("Reported context tokens")).toBeNull();
});
