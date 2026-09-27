import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { object, type Document } from "./documents";
import { NativeGrokText, NativeGrokUsage } from "./native-grok";
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
