// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { codexAppTranscript } from "./native-codex-app";
const identity = () => ({ account_id: "01900000-0000-7000-8000-000000000001", configuration_generation: "01900000-0000-7000-8000-000000000002", app_id: "original-app", link_id: null, tool: "original-tool", arguments: { original: true }, app_name: "Original", action_name: null, read_only_hint: null });
const snapshot = (status = "running") => ({ kind: "codex-app", status, codex_app: { identity: identity(), result: status === "completed" ? { content: [{ type: "text", text: "<script>never execute</script>" }], structured_content: null } : null, error_present: false, duration_ms: null, progress: null } });
describe("original Codex app transcript", () => {
 it("retains typed original input/result as data", () => expect(codexAppTranscript({ started: snapshot(), completed: snapshot("completed"), output: null }, "complete")).toHaveLength(2));
 it.each(["account_id", "configuration_generation", "app_id", "tool", "arguments"])("rejects substituted %s", key => { const last = snapshot("completed"); Object.assign(last.codex_app.identity, { [key]: key === "arguments" ? { original: false } : "foreign" }); expect(codexAppTranscript({ started: snapshot(), completed: last }, "complete")).toBeUndefined(); });
 it("rejects native presentation and transport authority", () => { const last = snapshot("completed"); Object.assign(last.codex_app.result!, { _meta: { resourceUri: "javascript:alert(1)" } }); expect(codexAppTranscript({ started: snapshot(), completed: last }, "complete")).toBeUndefined(); });
 it("does not infer completion from a missing result", () => expect(codexAppTranscript({ started: snapshot() }, "complete")).toBeUndefined());
});
