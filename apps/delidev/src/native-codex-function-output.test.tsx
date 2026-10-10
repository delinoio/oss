// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { NativeCodexFunctionOutput, codexFunctionOutput } from "./native-codex-function-output";
import { object, type Document } from "./documents";
const id = "01960dcb-e1fa-7000-8000-000000000001";
function fixture(): Document {
 return { execution_id: id, native_thread_id: id, native_turn_id: "526452fa-1956-42dd-b5f4-60e2b23dfe92", native_id: "original-native-output", role: "tool", state: "complete", first_sequence: 3, last_sequence: 4, text: "<script>inert()</script>last", codex_function_output: { version: 1, id, native_id: "original-native-output", name: "exact_name", namespace: null, stage: "completed", output: { variant: "contents", contents: [{ type: "input_text", text: "<script>inert()</script>" }, { type: "input_image", reference_kind: "image_url", reference_present: true, detail: "original" }, { type: "input_audio", reference_kind: "audio_url", reference_present: true }, { type: "encrypted_content", present: true }, { type: "input_text", text: "last" }] } } };
}
it("retains inert ordered output without media retrieval or protected bytes", () => {
 const { container } = render(<NativeCodexFunctionOutput data={fixture()} id={id} />);
 expect(screen.getByText("<script>inert()</script>")).toBeTruthy();
 expect(screen.getByText("last")).toBeTruthy();
 expect(container.querySelectorAll("pre,p")).toHaveLength(5);
 expect(container.querySelector("pre,p")?.textContent).toBe("<script>inert()</script>");
 expect(container.querySelector("script,a,img,audio,video,iframe,button")).toBeNull();
 expect(container.textContent).not.toContain("null");
});
it.each(["string", "empty-string", "empty-array", "empty-namespace"])("preserves original output variant: %s", kind => {
 const data = fixture(), v = object(data.codex_function_output);
 if (kind === "empty-array") { v.output = { variant: "contents", contents: [] }; data.text = ""; }
 else { v.output = { variant: "string", text: kind === "empty-string" ? "" : "original" }; data.text = object(v.output).text; }
 if (kind === "empty-namespace") v.namespace = "";
 expect(codexFunctionOutput(data, id)).toBeTruthy();
 render(<NativeCodexFunctionOutput data={data} id={id} />);
 expect(screen.queryByText("Function output unavailable")).toBeNull();
});
it.each(["foreign-id", "mixed", "phase", "stage", "missing-namespace", "private-url", "encrypted-bytes", "unknown", "oversize", "text-mismatch", "detail", "wrong-order-sequence"])("rejects invalid projection without rendering its content: %s", fault => {
 const data = fixture(), v = object(data.codex_function_output), output = object(v.output), contents = output.contents as Document[];
 switch (fault) {
  case "foreign-id": v.id = "01960dcb-e1fa-7000-8000-000000000002"; break;
  case "mixed": data.tool = {}; break;
  case "phase": data.phase = "final"; break;
  case "stage": v.stage = "started"; break;
  case "missing-namespace": delete v.namespace; break;
  case "private-url": contents[1]!.image_url = "private-sentinel"; break;
  case "encrypted-bytes": contents[3]!.encrypted_content = "private-sentinel"; break;
  case "unknown": contents[1]!.type = "future"; break;
  case "oversize": output.variant = "string"; output.text = "x".repeat((256 << 10) + 1); delete output.contents; data.text = output.text; break;
  case "text-mismatch": data.text = "changed"; break;
  case "detail": contents[1]!.detail = null; break;
  case "wrong-order-sequence": data.last_sequence = 2; break;
 }
 const { container } = render(<NativeCodexFunctionOutput data={data} id={id} />);
 expect(screen.getByText("Function output unavailable")).toBeTruthy();
 expect(container.textContent).not.toContain("private-sentinel");
 expect(container.querySelector("pre,a,img,audio")).toBeNull();
});
