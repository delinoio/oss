// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale } from "./localization";

type Part =
  | { kind: "input_text"; text: string }
  | {
      kind: "input_image";
      reference_kind: "image_url" | "file_id";
      detail?: "auto" | "low" | "high" | "original";
    }
  | { kind: "input_audio"; reference_kind: "audio_url" }
  | { kind: "encrypted_content" };
type Output = {
  name: string;
  namespace: string | null;
  structured: boolean;
  text?: string;
  content?: Part[];
};
const record = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v);
const keys = (v: Record<string, unknown>, allowed: string[]) =>
  Object.keys(v).every((k) => allowed.includes(k));
const bounded = (v: unknown, limit: number): v is string =>
  typeof v === "string" &&
  new TextEncoder().encode(v).length <= limit &&
  !/[\u0000\uD800-\uDFFF]/u.test(v);

/** Admit only inert text and reference kinds; reject locators and encrypted bytes. */
export function functionOutput(value: unknown): Output | undefined {
  if (
    !record(value) ||
    !keys(value, ["name", "namespace", "structured", "text", "content"]) ||
    !bounded(value.name, 256) ||
    !value.name.trim() ||
    !Object.hasOwn(value, "namespace") ||
    (value.namespace !== null && !bounded(value.namespace, 256)) ||
    typeof value.structured !== "boolean"
  )
    return;
  if (value.structured) {
    if (
      Object.hasOwn(value, "text") ||
      (value.content !== undefined && !Array.isArray(value.content))
    )
      return;
    const parts = value.content ?? [];
    if (!Array.isArray(parts) || parts.length > 1024) return;
    for (const part of parts) {
      if (!record(part)) return;
      switch (part.kind) {
        case "input_text":
          if (!keys(part, ["kind", "text"]) || !bounded(part.text, 256 << 10))
            return;
          break;
        case "input_image":
          if (
            !keys(part, ["kind", "reference_kind", "detail"]) ||
            !["image_url", "file_id"].includes(String(part.reference_kind)) ||
            (Object.hasOwn(part, "detail") &&
              !["auto", "low", "high", "original"].includes(String(part.detail)))
          )
            return;
          break;
        case "input_audio":
          if (
            !keys(part, ["kind", "reference_kind"]) ||
            part.reference_kind !== "audio_url"
          )
            return;
          break;
        case "encrypted_content":
          if (!keys(part, ["kind"])) return;
          break;
        default:
          return;
      }
    }
  } else if (!bounded(value.text, 256 << 10) || Object.hasOwn(value, "content"))
    return;
  if (new TextEncoder().encode(JSON.stringify(value)).length > (512 << 10))
    return;
  return value as Output;
}

export function NativeFunctionOutput({ content }: { content: unknown }) {
  useLocale();
  const output = functionOutput(content);
  if (!output) return <p>{copy("session.functionOutputUnavailable")}</p>;
  const parts = output.structured
    ? (output.content ?? [])
    : [{ kind: "input_text" as const, text: output.text ?? "" }];
  return (
    <article className="message" aria-label={copy("session.functionOutput")}>
      <header>
        <strong>
          {output.namespace === null
            ? output.name
            : `${output.namespace}.${output.name}`}
        </strong>
        <small>{copy("session.functionOutput")}</small>
      </header>
      {parts.map((part, index) =>
        part.kind === "input_text" ? (
          <pre key={index}>{part.text}</pre>
        ) : (
          <p key={index}>
            {copy(
              part.kind === "input_image"
                ? "session.functionImageReference"
                : part.kind === "input_audio"
                  ? "session.functionAudioReference"
                  : "session.functionEncryptedContent",
            )}
            {part.kind === "input_image"
              ? ` · ${part.reference_kind}${part.detail ? ` · ${part.detail}` : ""}`
              : null}
          </p>
        ),
      )}
    </article>
  );
}
