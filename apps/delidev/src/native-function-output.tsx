import { Disclosure, DisclosureSummary } from "./disclosure";
import { object } from "./documents";
import { copy, useLocale } from "./localization";

enum OutputVariant { String = "string", Structured = "structured" }
enum PartKind { Text = "input_text", Image = "input_image", Audio = "input_audio", Encrypted = "encrypted_content" }
type OutputPart = { kind: PartKind; text?: string; reference?: string; detail?: string };
type Output = { name: string; namespace: string | null; variant: OutputVariant; text?: string; parts: OutputPart[] | null };
const bytes = (value: string) => new TextEncoder().encode(value).length;
const boundedText = (value: unknown, limit: number): value is string => typeof value === "string" && bytes(value) <= limit && !value.includes("\0") && !/[\uD800-\uDFFF]/u.test(value);
const keys = (value: Record<string, unknown>, allowed: string[]) => Object.keys(value).every(key => allowed.includes(key));

// Validate the safe projection only. A native reference or encrypted value is
// never a display URL, media source, download, tool action or recovered output.
function snapshot(value: unknown): Output | undefined {
  const data = object(value);
  if (!keys(data, ["kind", "text", "summary", "content", "revision", "image_generation", "function_output"]) || data.kind !== "function-call-output" || data.text !== "" || data.summary != null || data.content != null || data.revision != null || data.image_generation != null) return undefined;
  const output = object(data.function_output);
  if (!keys(output, ["name", "namespace", "variant", "text", "parts"]) || !boundedText(output.name, 1024) || !output.name || !(output.namespace === null || boundedText(output.namespace, 1024)) || bytes(JSON.stringify(output)) > 512 * 1024) return undefined;
  if (output.variant === OutputVariant.String) {
    if (!boundedText(output.text, 256 * 1024) || output.parts !== null) return undefined;
  } else if (output.variant === OutputVariant.Structured) {
    if (output.text != null || !Array.isArray(output.parts) || output.parts.length > 1024) return undefined;
    for (const value of output.parts) {
      const part = object(value);
      if (!keys(part, ["kind", "text", "reference", "detail"])) return undefined;
      switch (part.kind) {
        case PartKind.Text:
          if (!boundedText(part.text, 256 * 1024) || part.reference != null || part.detail != null) return undefined;
          break;
        case PartKind.Image:
          if (part.text != null || (typeof part.reference !== "string" || !["image_url", "file_id"].includes(part.reference)) || part.detail != null && (typeof part.detail !== "string" || !["auto", "low", "high", "original"].includes(part.detail))) return undefined;
          break;
        case PartKind.Audio:
          if (part.text != null || part.reference !== "audio_url" || part.detail != null) return undefined;
          break;
        case PartKind.Encrypted:
          if (part.text != null || part.reference != null || part.detail != null) return undefined;
          break;
        default: return undefined;
      }
    }
  } else return undefined;
  return output as unknown as Output;
}

export function NativeFunctionOutput({ artifact, state }: { artifact: Record<string, unknown>; state: string }) {
  useLocale();
  const started = snapshot(artifact.started);
  const completed = artifact.completed == null ? undefined : snapshot(artifact.completed);
  const deltas = artifact.deltas ?? [];
  const valid = started && Array.isArray(deltas) && deltas.length === 0 && (state === "streaming" && artifact.completed == null || state === "complete" && completed && completed.name === started.name && completed.namespace === started.namespace && completed.variant === started.variant);
  const output = valid ? completed ?? started : undefined;
  return <Disclosure open>
    <DisclosureSummary>{copy("native-function-output.title")}{output ? <> · {output.namespace === null ? "" : `${output.namespace} · `}{output.name}</> : null}</DisclosureSummary>
    {!output ? <p role="status">{copy("native-function-output.unavailable")}</p> : output.variant === OutputVariant.String ? <pre>{output.text}</pre> : <ol>{output.parts?.map((part, index) => <li key={index}>{part.kind === PartKind.Text ? <pre>{part.text}</pre> : <span>{copy(part.kind === PartKind.Image ? "native-function-output.image" : part.kind === PartKind.Audio ? "native-function-output.audio" : "native-function-output.protected")}{part.kind === PartKind.Image ? <> · {part.reference}{part.detail ? ` · ${part.detail}` : ""}</> : null}</span>}</li>)}</ol>}
  </Disclosure>;
}
