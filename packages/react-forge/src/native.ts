import { createRequire } from "node:module";
import platforms from "./native-platforms.json" with { type: "json" };
import { ForgeError } from "./errors.js";
import { ErrorCode, Format, Stage, type Diagnostic } from "./types.js";

interface Cancellation { cancel(): void }
export interface NativeOutput { bytes: Buffer; model: string; geometry: string; diagnostics: string }
interface Binding {
  Cancellation: new () => Cancellation;
  processDocument(format: string, operation: string, model: string, source: Buffer, assets: { id: string; bytes: Buffer }[],
    documentId: string, revision: number, cancellation: Cancellation, fontOptions: string): Promise<NativeOutput>;
}
let binding: Binding | undefined;
function load(): Binding {
  if (!binding) {
    const host = platforms.find(host => host.platform === process.platform && host.architecture === process.arch);
    const glibc = process.platform !== "linux" || !!(process.report.getReport() as { header: { glibcVersionRuntime?: string } }).header.glibcVersionRuntime;
    if (!host || !glibc || process.versions.node.split(".")[0] !== "24") {
      throw new ForgeError(ErrorCode.UnsupportedPackage, "React Forge requires Node.js 24 on macOS, Windows or glibc Linux, using x64 or arm64.");
    }
    try { binding = createRequire(import.meta.url)(`../dist/react-forge.${host.id}.node`) as Binding; }
    catch { throw new ForgeError(ErrorCode.Io, "Native binding unavailable for this host. Rebuild here with pnpm --filter @delino/react-forge build."); }
  }
  return binding;
}

const codes: Record<string, ErrorCode> = {
  resource_limit: ErrorCode.ResourceLimit, cancelled: ErrorCode.Cancelled,
  unsupported_package: ErrorCode.UnsupportedPackage, unsupported_edit: ErrorCode.UnsupportedEdit,
  font_unavailable: ErrorCode.MissingFont, text_overflow: ErrorCode.LayoutOverflow,
  invalid_geometry: ErrorCode.LayoutOverflow, revision_conflict: ErrorCode.Conflict,
  source_changed: ErrorCode.Conflict, output_exists: ErrorCode.Conflict,
  invalid_reference: ErrorCode.InvalidTarget, not_found: ErrorCode.InvalidTarget,
  io: ErrorCode.Io, layout_cycle: ErrorCode.LayoutOverflow,
};

// Only model vocabulary and array indices may leave a native diagnostic.
// In particular, a parser path or an arbitrary source node key is not a model
// location and must never become a host path/content leak.
const locationFields = new Set("id version theme font_family font_size fonts embedding text width height color background language direction align style runs paragraphs heading list blocks document sections header footer paragraph table rows row cells cell column columns sheets sheet workbook chart series categories data value values formula cached expression number_format image asset alt geometry frame layout nodes slides children target address range merge validation conditional_format validations conditional_formats prompt error hyperlink shape points".split(" "));
function safeLocation(value: unknown): string | undefined {
  if (typeof value !== "string" || value.length > 256 || !value) return undefined;
  return value.split("/").every(part => part === "" || locationFields.has(part) || /^(0|[1-9][0-9]{0,5})$/.test(part)) ? value : undefined;
}

export async function processDocument(format: Format, operation: "generate" | "inspect" | "update", model: unknown,
  source: Buffer, assets: Map<string, Buffer>, documentId: string, revision: number, signal: AbortSignal, fontOptions: { system: boolean; ids: string[] } = { system: true, ids: [] }, onDiagnostic?: (event: Diagnostic) => void): Promise<NativeOutput> {
  const emit = (events: unknown) => {
    if (!Array.isArray(events)) return;
    for (const event of events) {
      if (!event || typeof event !== "object") continue;
      const diagnostic: Diagnostic = Object.freeze({ source: "native", format, revision,
        stage: operation === "inspect" ? Stage.Import : Stage.Export, operation,
        status: ["started", "completed", "failed"].includes(event.status) ? event.status : undefined,
        durationMs: Number.isFinite(event.duration_ms) ? event.duration_ms : 0,
        code: event.code ? codes[event.code] ?? ErrorCode.MalformedInput : undefined });
      try { onDiagnostic?.(diagnostic); } catch { /* Observers cannot affect native results. */ }
    }
  };
  const native = load();
  const cancellation = new native.Cancellation();
  const cancel = () => cancellation.cancel();
  signal.addEventListener("abort", cancel, { once: true });
  if (signal.aborted) cancel();
  try {
    const result = await native.processDocument(format, operation, JSON.stringify(model), source,
      Array.from(assets, ([id, bytes]) => ({ id, bytes })), documentId, revision, cancellation, JSON.stringify(fontOptions));
    emit(JSON.parse(result.diagnostics));
    return result;
  } catch (error) {
    let code = ErrorCode.MalformedInput;
    let location: string | undefined;
    if (error instanceof Error) {
      try {
        const detail = JSON.parse(error.message) as { code?: string; path?: unknown; native_events?: unknown };
        emit(detail.native_events);
        code = codes[detail.code ?? ""] ?? code;
        location = safeLocation(detail.path);
      } catch { /* Native runtime messages can contain paths; never forward them. */ }
    }
    const message = code === ErrorCode.MissingFont
      ? "Required glyphs, color emoji, or embedding permissions are unavailable. Register a compatible font with registerFont() or install a system fallback, then retry."
      : `Native document processing failed (${code}). Correct the input and retry.`;
    throw new ForgeError(code, message, { format, revision, location, stage: operation === "inspect" ? Stage.Import : Stage.Export });
  } finally { signal.removeEventListener("abort", cancel); }
}

export const processPptx = (...args: Parameters<typeof processDocument> extends [Format, ...infer Rest] ? Rest : never) => processDocument(Format.Pptx, ...args);
