import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { NativeClaudeUsage } from "./native-claude-usage";

function fixture(): Record<string, unknown> {
 return { source: "input-result", native_event_id: "01960dcb-e1fa-7000-8000-000000000001", result: { main_loop_turn: { input_tokens: "9007199254740993", output_tokens: "0", cache_read_input_tokens: null, output_tokens_details: { thinking_tokens: "0" }, iterations: [{ type: "advisor_message", model: "Original advisor", output_tokens: "7" }] }, native_cumulative_models: { "<script>inert</script>": { inputTokens: "9223372036854775807", costUSD: "0.0006994999999999999" } }, native_cumulative_cost_usd: "0.0006994999999999999" } };
}
it("preserves exact counters, unavailable data and native cumulative scope without billing", () => {
 render(<NativeClaudeUsage value={fixture()} />);
 expect(screen.getByText("Input excluding cache").nextElementSibling?.textContent).toBe("9007199254740993");
 expect(screen.getByText("Output including thinking").nextElementSibling?.textContent).toBe("0");
 expect(screen.getByText("Cache read input").nextElementSibling?.textContent).toBe("Unavailable");
 expect(screen.getByText("Native cumulative USD estimate").nextElementSibling?.textContent).toBe("0.0006994999999999999");
 expect(screen.getByText("<script>inert</script>")).toBeTruthy();
 expect(document.querySelector("script")).toBeNull();
 expect(screen.queryByRole("link")).toBeNull();
 expect(screen.queryByRole("button")).toBeNull();
});
it.each(["provider-message-start", "block-complete", "provider-message-metadata"])("preserves original %s observations separately", (source) => {
 const value = { source, native_event_id: "01960dcb-e1fa-7000-8000-000000000001", message_id: "01960dcb-e1fa-7000-8000-000000000002", native_message_id: "msg_original", model: "fixture", provider: {}, ...(source === "block-complete" ? { index: 0 } : {}) };
 render(<NativeClaudeUsage value={value} />);
 expect(screen.getByLabelText("Claude native usage")).toBeTruthy();
 expect(screen.getByText("Input excluding cache").nextElementSibling?.textContent).toBe("Unavailable");
});
it.each(["-1", "01", "9223372036854775808", "1.5", 1, "1e2"])("rejects malformed public counter %s", (input_tokens) => {
 const value=fixture(); value.result={main_loop_turn:{input_tokens}};
 render(<NativeClaudeUsage value={value} />);
 expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
 expect(screen.queryByText("Input excluding cache")).toBeNull();
});
it.each([
 {source:"unknown"}, {native_event_id:"msg_other"}, {provider:{}}, {index:0},
 {result:{main_loop_turn:{unknown:"1"}}}, {result:{main_loop_turn:{iterations:[{type:"compaction",model:"wrong"}]}}},
 {result:{native_cumulative_cost_usd:"-0"}}, {result:{native_cumulative_cost_usd:"1e309"}}, {result:{native_cumulative_models:{m:{contextWindow:"0"}}}},
 {result:{main_loop_turn:{fallback_credit:{status:{type:"not_applied",reason:"variant_fields_present",remove_to_redeem:[]}}}}},
])("rejects mixed or malformed usage before partial rendering", (change) => {
 render(<NativeClaudeUsage value={{...fixture(),...change}} />);
 expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
 expect(screen.queryByText("Input excluding cache")).toBeNull();
});
