import { z } from "zod";
import type { CallToolResult, Tool } from "@modelcontextprotocol/sdk/types.js";
import { ForgeError } from "../errors.js";
import { FigmaPublishError } from "../figma/session.js";
import { ErrorCode, limits } from "../types.js";

export enum Operation {
  Capabilities = "capabilities",
  Execute = "execute",
  Sessions = "sessions",
  Inspect = "inspect",
  Measure = "measure",
  Refresh = "refresh",
  Export = "export",
  Publish = "publish",
  Close = "close",
}
export enum SessionStatus { Idle = "idle", Running = "running", Closing = "closing" }
export enum MessageKind { Call = "call", Cancel = "cancel", Shutdown = "shutdown", Result = "result", Diagnostic = "diagnostic", Ready = "ready" }
export enum InspectionView { Targets = "targets", Receipt = "receipt" }

const path = z.string().min(1).max(4096).refine(value => !value.includes("\0"));
const id = z.string().regex(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
const nodeId = z.string().min(1).max(256);
const session = { sessionId: id };
const page = { offset: z.number().int().min(0).default(0), limit: z.number().int().min(1).max(500).default(100) };
export const schemas = {
  [Operation.Capabilities]: z.strictObject({}),
  [Operation.Execute]: z.strictObject({
    sessionId: id.optional(), code: z.string().min(1).optional(), entry: path.optional(), data: z.unknown().optional(),
  }).refine(input => (input.code !== undefined) !== (input.entry !== undefined)),
  [Operation.Sessions]: z.strictObject(page),
  [Operation.Inspect]: z.strictObject({ ...session, ...page, view: z.enum(InspectionView).default(InspectionView.Targets), nodeId: nodeId.optional(), kind: z.string().min(1).max(128).optional() }),
  [Operation.Measure]: z.strictObject({ ...session, nodeId, revision: z.number().int().min(0) }),
  [Operation.Refresh]: z.strictObject({ ...session, pageId: nodeId.optional(), nodeIds: z.array(nodeId).max(24).optional(), resources: z.boolean().optional() }),
  [Operation.Export]: z.strictObject({ ...session, output: path, overwrite: z.boolean().default(false) }),
  [Operation.Publish]: z.strictObject({ ...session, receiptPath: path.optional(), overwrite: z.boolean().default(false) }),
  [Operation.Close]: z.strictObject(session),
};
export type Input<O extends Operation> = z.output<(typeof schemas)[O]>;
export type ToolValue = Record<string, unknown>;
export type ParentMessage = { kind: MessageKind.Call; id: number; operation: Operation; input: unknown }
  | { kind: MessageKind.Cancel; id: number } | { kind: MessageKind.Shutdown };
export type ChildMessage = { kind: MessageKind.Result; id: number; result: CallToolResult }
  | { kind: MessageKind.Diagnostic; operation: Operation; durationMs: number; code?: ErrorCode }
  | { kind: MessageKind.Ready };

const descriptions: Record<Operation, string> = {
  capabilities: "Describe supported formats, hosts, resource limits and the trusted TSX callback contract. No I/O.",
  execute: "Run trusted TSX code OR a local .tsx entry. Default export receives {session,state,data,signal}. New calls return a session; updates return void or the same session. state is a persistent Map. Code runs with your permissions and may explicitly write files or publish remotely; this is not a sandbox or a transaction. No automatic timeout. Does not automatically export or publish.",
  sessions: "List active in-memory sessions, their format, last revision and execution status. Sessions disappear when the server exits.",
  inspect: "Inspect settled local targets or cached Figma targets. Filter by nodeId/kind; offset/limit paginate. Text previews are capped at 4096 characters. view=receipt returns the last Figma receipt without publishing. Does not refresh Figma remotely.",
  measure: "Measure a document-scoped node at an exact revision (WAV timeline x/width are seconds). For local documents first inspect to settle a revision; Figma measurement requires a completed publication. A changed revision returns conflict.",
  refresh: "Refresh explicitly selected Figma page/nodes or resources using the existing host credential. Preserves external ownership. Figma only.",
  export: "Export Office/PDF/WAV/GLB/FBX or a sprite .sprite.zip bundle to a local output. overwrite=true replaces an existing output, never an imported source or its aliases. Atomic publication pins a revision. Figma uses publish instead.",
  publish: "Explicitly publish a Figma revision remotely. Optionally save a .figma.json receipt; existing receipt requires overwrite=true. Partial/unknown outcomes include a receipt and must be reconciled before retrying. Never automatically retry this tool.",
  close: "Dispose a session after its prior operations finish and release its state Map. Does not delete exported files or undo remote changes.",
};
export const tools: Tool[] = Object.values(Operation).map(operation => {
  const inputSchema = z.toJSONSchema(schemas[operation], { unrepresentable: "any" });
  // JSON Schema cannot express Zod refinements; expose the exclusive source
  // choice to clients as well as enforcing it before dispatch.
  if (operation === Operation.Execute) inputSchema.oneOf = [{ required: ["code"], not: { required: ["entry"] } }, { required: ["entry"], not: { required: ["code"] } }];
  const readOnly = [Operation.Capabilities, Operation.Sessions, Operation.Inspect, Operation.Measure, Operation.Refresh].includes(operation);
  return {
    name: `react_forge_${operation}`, description: descriptions[operation],
    inputSchema: inputSchema as Tool["inputSchema"],
    annotations: { readOnlyHint: readOnly, destructiveHint: !readOnly, idempotentHint: readOnly, openWorldHint: [Operation.Execute, Operation.Refresh, Operation.Publish].includes(operation) },
  };
});

export function parse<O extends Operation>(operation: O, input: unknown): Input<O> {
  const result = schemas[operation].safeParse(input);
  if (!result.success) throw new ForgeError(ErrorCode.MalformedInput, "Invalid tool arguments. Follow the advertised input schema.");
  if (operation === Operation.Execute) {
    const { code, data } = result.data as Input<Operation.Execute>;
    if (Buffer.byteLength(code ?? "") + (data === undefined ? 0 : Buffer.byteLength(JSON.stringify(data))) > limits.treeBytes) {
      throw new ForgeError(ErrorCode.ResourceLimit, "TSX source and JSON data exceed 16 MiB.");
    }
  }
  return result.data as Input<O>;
}
export function success(value: ToolValue): CallToolResult {
  return { content: [{ type: "text", text: JSON.stringify(value) }], structuredContent: value };
}
export function failure(error: unknown): CallToolResult {
  const safe = error instanceof ForgeError ? error : new ForgeError(ErrorCode.Render, "Task execution failed. Correct the task and retry.");
  const value = { error: safe.toJSON(), ...(error instanceof FigmaPublishError ? { receipt: error.receipt } : {}) };
  return { ...success(value), isError: true };
}
