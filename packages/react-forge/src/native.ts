import { createRequire } from "node:module";
import { ForgeError } from "./errors.js";
import { ErrorCode, Format } from "./types.js";

interface Cancellation { cancel(): void }
export interface NativeOutput { bytes: Buffer; model: string; geometry: string }
interface Binding {
  Cancellation: new () => Cancellation;
  processDocument(format: string, operation: string, model: string, source: Buffer, assets: { id: string; bytes: Buffer }[],
    documentId: string, revision: number, cancellation: Cancellation): Promise<NativeOutput>;
}
let binding: Binding | undefined;
function load(): Binding {
  if (!binding) {
    if (process.platform !== "darwin" || process.arch !== "arm64" || process.versions.node.split(".")[0] !== "24") {
      throw new ForgeError(ErrorCode.UnsupportedPackage, "React Forge requires Node.js 24 on macOS arm64.");
    }
    try { binding = createRequire(import.meta.url)("../dist/react-forge.node") as Binding; }
    catch { throw new ForgeError(ErrorCode.Io, "Native binding unavailable. Run pnpm --filter react-forge build."); }
  }
  return binding;
}

const codes: Record<string, ErrorCode> = {
  resource_limit: ErrorCode.ResourceLimit, cancelled: ErrorCode.Cancelled,
  unsupported_package: ErrorCode.UnsupportedPackage, unsupported_edit: ErrorCode.UnsupportedEdit,
  font_unavailable: ErrorCode.MissingFont, text_overflow: ErrorCode.LayoutOverflow,
  invalid_geometry: ErrorCode.LayoutOverflow, revision_conflict: ErrorCode.Conflict,
  source_changed: ErrorCode.Conflict, invalid_reference: ErrorCode.InvalidTarget,
};

export async function processDocument(format: Format, operation: "generate" | "inspect" | "update", model: unknown,
  source: Buffer, assets: Map<string, Buffer>, documentId: string, revision: number, signal: AbortSignal): Promise<NativeOutput> {
  const native = load();
  const cancellation = new native.Cancellation();
  const cancel = () => cancellation.cancel();
  signal.addEventListener("abort", cancel, { once: true });
  if (signal.aborted) cancel();
  try {
    return await native.processDocument(format, operation, JSON.stringify(model), source,
      Array.from(assets, ([id, bytes]) => ({ id, bytes })), documentId, revision, cancellation);
  } catch (error) {
    let code = ErrorCode.MalformedInput;
    if (error instanceof Error) {
      try {
        const detail = JSON.parse(error.message) as { code?: string };
        code = codes[detail.code ?? ""] ?? code;
      } catch { /* Native runtime messages can contain paths; never forward them. */ }
    }
    throw new ForgeError(code, `Native document processing failed (${code}). Correct the input and retry.`);
  } finally { signal.removeEventListener("abort", cancel); }
}

export const processPptx = (...args: Parameters<typeof processDocument> extends [Format, ...infer Rest] ? Rest : never) => processDocument(Format.Pptx, ...args);
