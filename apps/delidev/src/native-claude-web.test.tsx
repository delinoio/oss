import { goJsonBytes } from "./go-json-bytes";
import timestampVectors from "../../../cmds/delidev-cli/internal/domain/testdata/claude-web-timestamps.json";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { NativeClaudeMessage, validNativeClaudeMessage } from "./native-claude-message";

afterEach(cleanup);
function fixture(name = "web_search", problem = "") {
  return { model: "fixture-model", blocks: [
    { index: 0, block: { kind: "server_tool_use", text: "", web: { native_id: "srvtool_original", name, caller: null, call: { initial_input: name === "web_search" ? '{"query":"Original query"}' : '{"url":"https://fixture.invalid"}', input_delta: null } } }, state: "stopped" },
    { index: 1, block: { kind: name === "web_search" ? "web_search_tool_result" : "web_fetch_tool_result", text: "", web: { native_id: "srvtool_original", name, caller: "direct", result: { problem, search: !problem && name === "web_search" ? [{ url: "https://fixture.invalid/<script>inert()</script>", title: "Original source <img src=x>", page_age: "Yesterday" }] : null, fetch: !problem && name === "web_fetch" ? { url: "https://fixture.invalid", retrieved_at: "2026-10-09T00:00:00Z", document: { source: "text", media: "text/plain", title: "Original document", context: null, citations: true, text: "Original native content <script>inert()</script>" } } : null } } }, state: "stopped" },
  ], stop_reason: "end_turn", stop_sequence: null };
}
test.each(["web_search", "web_fetch"])("renders original %s provenance without fetching or executing", (name) => {
  const data = fixture(name);
  expect(validNativeClaudeMessage(data, "complete")).toBe(true);
  const { container } = render(<NativeClaudeMessage content={data} state="complete" />);
  expect(container.textContent).toContain(name);
  expect(container.textContent).toContain("https://fixture.invalid");
  expect(container.textContent).toContain(name === "web_search" ? "Original source <img src=x>" : "Original native content <script>inert()</script>");
  expect(container.querySelector("script, img, a, button")).toBeNull();
});
test.each(["unavailable", "url_not_allowed", "url_not_in_prior_context"])("keeps native fetch error %s independent of completion", (problem) => {
  const data = fixture("web_fetch", problem);
  render(<NativeClaudeMessage content={data} state="complete" />);
  expect(screen.getByText(problem)).toBeTruthy();
  expect(screen.queryByText(/message unavailable/i)).toBeNull();
});
test.each(["foreign", "family", "mixed", "encrypted", "input", "duplicate", "unknown", "unfinished"])("rejects invalid retained web tool %s", (change) => {
  const data = fixture();
  const result = data.blocks[1]!.block.web;
  switch (change) {
    case "foreign": result.native_id = "foreign"; break;
    case "family": result.name = "code_execution"; break;
    case "mixed": result.result!.problem = "unavailable"; break;
    case "encrypted": Object.assign(result.result!, { encrypted_content: "private" }); break;
    case "input": data.blocks[0]!.block.web.call!.initial_input = '{"query":"original","code":"unsupported"}'; break;
    case "duplicate": data.blocks.push({ ...data.blocks[1]!, index: 2 }); break;
    case "unknown": Object.assign(result, { arbitrary: true }); break;
    case "unfinished": data.blocks = data.blocks.slice(0, 1); break;
  }
  expect(validNativeClaudeMessage(data, "complete")).toBe(false);
  render(<NativeClaudeMessage content={data} state="complete" />);
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
});

test("rejects normalized impossible native retrieval dates", () => {
 const data=fixture("web_fetch");data.blocks[1]!.block.web.result!.fetch!.retrieved_at="2026-02-30T00:00:00Z";
 expect(validNativeClaudeMessage(data,"complete")).toBe(false);
});

// Use the backend admission vectors against complete transcript rendering.
test.each(timestampVectors)("preserves timestamp readability parity: $name", ({ value, valid }) => {
  const data = fixture("web_fetch");
  const fetch = data.blocks[1]!.block.web.result!.fetch!;
  Object.assign(fetch, { retrieved_at: value });
  expect(validNativeClaudeMessage(data, "complete")).toBe(valid);
  const { container } = render(<NativeClaudeMessage content={data} state="complete" />);
  if (valid) {
    expect(container.textContent).toContain("Original native content <script>inert()</script>");
    if (value !== null) expect(container.textContent).toContain(value);
    expect(fetch.retrieved_at).toBe(value);
  } else {
    expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
  }
});


test.each(["<", ">", "&", "\u2028", "\u2029"])("rejects Go-escaped overbound web document text %s before rendering", character => {
 const data=fixture("web_fetch");
 data.blocks[1]!.block.web.result!.fetch!.document.text=character.repeat(50000);
 expect(validNativeClaudeMessage(data,"complete")).toBe(false);
 const {container}=render(<NativeClaudeMessage content={data} state="complete"/>);
 expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
 expect(container.textContent).not.toContain(character.repeat(100));
});

test.each([-1,0,1])("enforces the exact mixed escaped web aggregate boundary (%i bytes)", delta => {
 const data=fixture("web_fetch");
 const document=data.blocks[1]!.block.web.result!.fetch!.document;
 document.text="<>&\u2028\u2029".repeat(8000);
 const bytes=data.blocks.reduce((sum,entry)=>sum+goJsonBytes(entry.block.web),0);
 document.text+="a".repeat((256<<10)-bytes+delta);
 expect(data.blocks.reduce((sum,entry)=>sum+goJsonBytes(entry.block.web),0)).toBe((256<<10)+delta);
 expect(validNativeClaudeMessage(data,"complete")).toBe(delta<=0);
});

test("includes ordinary text and every web call in the shared aggregate",()=>{
 const data=fixture("web_fetch");
 const document=data.blocks[1]!.block.web.result!.fetch!.document;
 document.text="&".repeat(40000);
 const remaining=(256<<10)-data.blocks.reduce((sum,entry)=>sum+goJsonBytes(entry.block.web),0);
 const mixed={...data,blocks:[...data.blocks,{index:2,block:{kind:"text",text:"a".repeat(remaining)},state:"stopped"}]};
 expect(validNativeClaudeMessage(mixed,"complete")).toBe(true);
 mixed.blocks[2]!.block.text+="a";
 expect(validNativeClaudeMessage(mixed,"complete")).toBe(false);
});
