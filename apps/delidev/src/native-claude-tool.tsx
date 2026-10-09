import { Disclosure, DisclosureSummary } from "./disclosure";
import { LocalizedText, copy, useLocale } from "./localization";
import { object } from "./documents";

enum State { Streaming = "streaming", Complete = "complete" }
export type ClaudeToolReference = { id: string; native_id: string; name: string };
const uuid = (v: unknown, native = false) => typeof v === "string" && (native ? /^[0-9a-f]{8}-[0-9a-f]{4}-[47][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/ : /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/).test(v);
function keys(v: Record<string, unknown>, expected: string[]) { return Object.keys(v).length === expected.length && expected.every((k) => Object.hasOwn(v, k)); }
function text(v: unknown, max = 256 * 1024): v is string { return typeof v === "string" && v.length <= max && !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= max; }
function label(v: unknown, max: number): v is string { return text(v, max) && v.trim().length > 0; }
export function claudeToolReference(value: unknown): ClaudeToolReference | undefined {
  const v = object(value);
  return keys(v, ["id", "native_id", "name"]) && uuid(v.id) && label(v.native_id, 1024) && label(v.name, 256) ? v as ClaudeToolReference : undefined;
}
// Parse only to validate the JSON object. Display its original string without
// reserialization, preserving native number, whitespace and escape spellings.
function json(v: unknown, errorString = false): v is string {
  if (!text(v)) return false;
  try { const parsed: unknown = JSON.parse(v); return (errorString && text(parsed)) || (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)); } catch { return false; }
}
type Result = { non_execution?: { id: string; non_execution_kind: "permission-rule" | "user-rejected" }; native_event_id: string; is_error: boolean | null; text: string | null; blocks: { kind: "text"; text: string }[] | null; structured: string | null };
type Tool = { reference: ClaudeToolReference; message_id: string; native_message_id: string; index: number; caller: "direct" | null; initial_input: string; input_delta: string | null; proposal: { proposed: string; applied: string } | null; result: Result | null };
function tool(value: unknown, state: string): Tool | undefined {
  const v = object(value), ref = claudeToolReference(v.reference);
  if (!keys(v, ["reference", "message_id", "native_message_id", "index", "caller", "initial_input", "input_delta", "proposal", "result"]) || !ref || !uuid(v.message_id) || !label(v.native_message_id, 1024) || v.native_message_id === ref.native_id || !Number.isInteger(v.index) || Number(v.index) < 0 || Number(v.index) >= 1024 || (v.caller !== null && v.caller !== "direct") || !json(v.initial_input) || (v.input_delta !== null && !text(v.input_delta)) || !Object.values(State).includes(state as State)) return undefined;
  if (new TextEncoder().encode(JSON.stringify(v)).length > 768 * 1024) return undefined;
  if (v.proposal !== null) {
    const p = object(v.proposal);
    if (!keys(p, ["proposed", "applied"]) || !json(p.proposed) || !json(p.applied) || p.proposed !== (v.input_delta || v.initial_input)) return undefined;
  }
  if ((state === State.Complete) !== (v.result !== null) || (v.result !== null && v.proposal === null)) return undefined;
  if (v.result !== null) {
    const r = object(v.result);
    if (!keys(r, ["native_event_id", "is_error", "text", "blocks", "structured", ...(r.non_execution === undefined ? [] : ["non_execution"])]) || !uuid(r.native_event_id, true) || (r.is_error !== null && typeof r.is_error !== "boolean") || (r.text !== null && !text(r.text)) || (r.structured !== null && !json(r.structured, r.is_error === true)) || (r.text !== null && r.blocks !== null)) return undefined;
    if (r.non_execution !== undefined) { const n = object(r.non_execution); if (!keys(n, ["id", "non_execution_kind"]) || n.id !== ref.native_id || (n.non_execution_kind !== "permission-rule" && n.non_execution_kind !== "user-rejected") || r.is_error !== true) return undefined; }
    if (r.blocks !== null) {
      if (!Array.isArray(r.blocks) || r.blocks.length > 1024) return undefined;
      let bytes = 0;
      for (const block of r.blocks) {
        const b = object(block);
        if (!keys(b, ["kind", "text"]) || b.kind !== "text" || !text(b.text)) return undefined;
        bytes += new TextEncoder().encode(b.text).length;
      }
      if (bytes > 256 * 1024) return undefined;
    }
  }
  return v as Tool;
}

export function NativeClaudeTool({ content, state, id, native, parent }: { content: unknown; state: string; id: string; native: string; parent: string }) {
  useLocale();
  const retained = tool(content, state);
  if (!retained || retained.reference.id !== id || retained.reference.native_id !== native || retained.native_message_id !== parent) return <section aria-label={copy("native-claude-tool.claudeToolUnavailable_fade0a")}><p>{copy("native-claude-tool.theRetainedClaudeToolIsUnavailable_552da4")}</p></section>;
  const result = retained.result;
  return <section aria-label={copy("native-claude-tool.claudeToolObservation_17c86c")}><Disclosure>
    <DisclosureSummary>{retained.reference.name} · {result ? copy("native-claude-tool.resultObserved_447816") : retained.proposal ? copy("native-claude-tool.proposalComplete_1ebb0e") : copy("native-claude-tool.receivingProposal_2dfe67")}</DisclosureSummary>
    <p><LocalizedText id="native-claude-tool.providerBlockCaller_c07946" components={{ s0: <>{retained.index + 1}</>, s1: <>{retained.caller === null ? copy("native-claude-tool.notReported_adadfa") : copy("native-claude-tool.direct_002c7c")}</> }} /></p>
    <Disclosure><DisclosureSummary>{copy("native-claude-tool.initialInput_f6c601")}</DisclosureSummary><pre>{retained.initial_input}</pre></Disclosure>
    {retained.input_delta !== null ? <Disclosure><DisclosureSummary>{copy("native-claude-tool.streamedInput_bafbd1")}</DisclosureSummary><pre>{retained.input_delta}</pre></Disclosure> : null}
    {retained.proposal ? <><Disclosure><DisclosureSummary>{copy("native-claude-tool.originalProposedInput_0ecc84")}</DisclosureSummary><pre>{retained.proposal.proposed}</pre></Disclosure><Disclosure><DisclosureSummary>{copy("native-claude-tool.nativeAppliedInput_6397df")}</DisclosureSummary><pre>{retained.proposal.applied}</pre></Disclosure></> : null}
    {result ? <section aria-label={copy("native-claude-tool.originalToolResult_2b1077")}>
      <p><LocalizedText id="native-claude-tool.nativeErrorFlag_e3d9ff" components={{ s0: <>{result.is_error === null ? copy("native-claude-tool.notReported_adadfa") : result.is_error ? copy("native-claude-tool.errorReported_284ccd") : copy("native-claude-tool.noErrorReported_71376f")}</> }} /></p>
      {result.non_execution ? <p>{result.non_execution.non_execution_kind === "user-rejected" ? copy("native-claude-tool.theOriginalUserRejectionPreventedThis_97b97e") : copy("native-claude-tool.nativePermissionPolicyPreventedThisTool_8e5f57")}</p> : null}
      {result.text !== null ? <pre>{result.text}</pre> : null}
      {result.blocks !== null ? <ol aria-label={copy("native-claude-tool.toolResultBlocks_7800be")}>{result.blocks.map((b, index) => <li key={index}><pre>{b.text}</pre></li>)}</ol> : null}
      {result.structured !== null ? <Disclosure><DisclosureSummary>{copy("native-claude-tool.structuredNativeResult_762fc2")}</DisclosureSummary><pre>{result.structured}</pre></Disclosure> : null}
    </section> : <p>{copy("native-claude-tool.noToolResultHasBeenObserved_39f5c0")}</p>}
    <p>{copy("native-claude-tool.proposalCompletionDoesNotEstablishApproval_0d9759")}</p>
  </Disclosure></section>;
}
