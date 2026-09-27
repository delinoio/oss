import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { object, type Document } from "./documents";
import { NativeGrokText, NativeGrokUsage, NativeGrokTerminal } from "./native-grok";
const thread = "01960dcb-e1fa-7000-8000-000000000001";
function textFixture(): Document {
 const meta = { event_id: `${thread}-10`, chunk_id: "1", context_tokens: "18446744073709551615", timestamp_ms: "1", stream_start_ms: "0", turn_start_ms: "0" };
 return { execution_id: thread, native_thread_id: thread, native_turn_id: "526452fa-1956-42dd-b5f4-60e2b23dfe92", native_id: meta.event_id, role: "assistant", state: "complete", text: "Original <script>private()</script> 한글", first_sequence: 3, last_sequence: 4, grok_text: { response_ordinal: 1, chunks: [{ ...meta }] } };
}
function usageFixture(): Document { return { ordinal: 1, counts: { input_tokens: "18446744073709551615", output_tokens: "5", cache_read_input_tokens: "0", cache_creation_input_tokens: "0", reasoning_tokens: "0" } }; }
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
