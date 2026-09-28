import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { NativeClaudeCompactionBoundary, NativeClaudeCompactionSummary, validClaudeCompactionBoundary, validClaudeCompactionSummary } from "./native-claude-compaction";
import { NativeClaudePermissionProgress, NativeClaudeProgress } from "./native-claude-progress";
import { newRequestId } from "@delinoio/delidev-api-client";
import { type Document, object } from "./documents";

afterEach(cleanup);
const boundary = (): Document => ({ trigger: "auto", pre_tokens: "18446744073709551615", post_tokens: "0", preserved_messages: { anchor_uuid: newRequestId(), uuids: [newRequestId()] } });
const summary = (): Document => ({ boundary_native_id: newRequestId(), boundary_message_id: newRequestId(), text: null, blocks: [{ kind: "text", text: "<script>original context</script>" }] });

test("preserves exact original context counts separately from usage", () => {
 const value=boundary();
 expect(validClaudeCompactionBoundary(value)).toBe(true);
 render(<NativeClaudeCompactionBoundary value={value} />);
 expect(screen.getByText("18446744073709551615")).toBeTruthy();
 expect(screen.getByText("0")).toBeTruthy();
 expect(screen.getByText(/separate from provider usage/)).toBeTruthy();
 expect(screen.queryByRole("button")).toBeNull();
});

test("discloses original native summary as inert context", () => {
 const value=summary();
 expect(validClaudeCompactionSummary(value)).toBe(true);
 const {container}=render(<NativeClaudeCompactionSummary value={value} />);
 expect(screen.getByText("<script>original context</script>")).toBeTruthy();
 expect(container.querySelector("script")).toBeNull();
 expect(screen.queryByRole("button")).toBeNull();
});

test.each(["number","negative","overflow","null","duplicate","anchor","empty-all","null-all","parent","segment","unknown"])("rejects malformed original compaction boundary: %s", (change) => {
 const v=boundary(), m=object(v.preserved_messages);
 if(change==="number")v.pre_tokens=1;
 if(change==="negative")v.pre_tokens="-1";
 if(change==="overflow")v.post_tokens="18446744073709551616";
 if(change==="null")v.duration_ms=null;
 if(change==="duplicate")m.uuids=[...(m.uuids as string[]),...(m.uuids as string[])];
 if(change==="anchor")m.uuids=[m.anchor_uuid];
 if(change==="empty-all")m.all_uuids=[];
 if(change==="null-all")m.all_uuids=null;
 if(change==="parent")v.logical_parent_uuid="foreign";
 if(change==="segment")v.preserved_segment={anchor_uuid:newRequestId(),head_uuid:newRequestId(),tail_uuid:newRequestId()};
 if(change==="unknown")v.completion=true;
 expect(validClaudeCompactionBoundary(v)).toBe(false);
});

test.each(["missing-text","missing-blocks","mixed","empty","rich","overflow","foreign","unknown"])("rejects malformed original summary: %s", (change) => {
 const v=summary();
 if(change==="missing-text")delete v.text;
 if(change==="missing-blocks"){v.text="Original";delete v.blocks;}
 if(change==="mixed")v.text="Original";
 if(change==="empty")v.blocks=[];
 if(change==="rich")v.blocks=[{kind:"thinking",text:"Original"}];
 if(change==="overflow")v.blocks=[{kind:"text",text:"x".repeat((256<<10)+1)}];
 if(change==="foreign")v.boundary_message_id="foreign";
 if(change==="unknown")v.input_id=newRequestId();
 expect(validClaudeCompactionSummary(v)).toBe(false);
});

test.each(["latest_compaction_id","latest_compaction_summary_id"])("context reference does not invent permission: %s",(key)=>{
 render(<NativeClaudePermissionProgress progress={{claude_progress:{native_turn_id:newRequestId(),[key]:newRequestId()}}}/>);
 expect(screen.getByText("Not reported")).toBeTruthy();
});

function envelope(isSummary: boolean, accepted = false): Document {
 const native = newRequestId();
 return {
  execution_id: newRequestId(), native_thread_id: newRequestId(), native_turn_id: newRequestId(), native_id: native,
  role: "progress", state: "complete", text: "", first_sequence: 2, last_sequence: 2,
  claude_progress: {
   native_event_id: native, kind: isSummary ? "compaction-summary" : "compaction-boundary", input_accepted: accepted,
   status: null, thinking: null, [isSummary ? "compaction_summary" : "compaction"]: isSummary ? summary() : boundary(),
  },
 };
}

test.each([[false, false], [false, true], [true, false], [true, true]])("renders original compaction envelope with independent acceptance: summary=%s accepted=%s", (isSummary, accepted) => {
 render(<NativeClaudeProgress data={envelope(isSummary, accepted)} />);
 expect(screen.getByRole("article", {name: "Claude progress observation"})).toBeTruthy();
 expect(screen.getByText(accepted ? "After input acceptance" : "Before input acceptance")).toBeTruthy();
 expect(screen.getByText(isSummary ? "<script>original context</script>" : "Automatic")).toBeTruthy();
 expect(screen.queryByRole("button")).toBeNull();
});

test.each(["status", "thinking", "task", "tool", "retry", "both", "self-boundary", "input"])("rejects mixed compaction envelope: %s", (change) => {
 const data=envelope(true), v=object(data.claude_progress);
 if(change==="status")v.status={status:"compacting",permission:null,compact_result:null,compact_error:null};
 if(change==="thinking")v.thinking={estimated_tokens:"1",estimated_tokens_delta:"1"};
 if(change==="task")v.task={};
 if(change==="tool")v.tool={};
 if(change==="retry")v.api_retry={};
 if(change==="both")v.compaction=boundary();
 if(change==="self-boundary")object(v.compaction_summary).boundary_native_id=v.native_event_id;
 if(change==="input")data.input_id=newRequestId();
 render(<NativeClaudeProgress data={data} />);
 expect(screen.getByRole("article", {name:"Claude progress unavailable"})).toBeTruthy();
});
