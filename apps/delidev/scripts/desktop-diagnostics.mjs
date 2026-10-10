import { StringDecoder } from "node:string_decoder";

const maximumLineLength = 64 * 1024;
const ansiSequence = /\x1b\[[0-?]*[ -/]*[@-~]/gu;

// Cargo prints its application argv on a Running line. Other commands and the
// application can repeat values in diagnostics, including quoted/escaped forms.
// Keep safe text, but never forward a known application argument value.
export function createDiagnosticFilter(args, emit) {
  const variants = new Set();
  for (const arg of args) {
    for (const value of [arg, ...(arg.startsWith("--") && arg.includes("=") ? [arg.slice(arg.indexOf("=") + 1)] : [])]) {
      if (!value) continue;
      for (const variant of [value, JSON.stringify(value).slice(1, -1), value.replace(/([\\ '"`$])/gu, "\\$1"), value.replaceAll("'", "'\\''")]) {
        for (const fragment of variant.split(/[\r\n]/u)) if (fragment) variants.add(fragment);
      }
    }
  }
  const privateValues = [...variants].sort((a, b) => b.length - a.length);
  const patterns = privateValues.map(value => {
    const escaped = value.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
    // A short positional value must not erase unrelated words in safe logs.
    return /^[\p{L}\p{N}_]+$/u.test(value)
      ? `(?<![\\p{L}\\p{N}_])${escaped}(?![\\p{L}\\p{N}_])`
      : escaped;
  });
  const privatePattern = patterns.length ? new RegExp(patterns.join("|"), "gu") : null;
  const decoder = new StringDecoder("utf8");
  let line = "";
  let omitted = false;
  const finishLine = newline => {
    if (omitted) {
      emit(`[desktop diagnostic omitted: oversized line]${newline}`);
    } else if (!/^\s*Running\s/u.test(line.replace(ansiSequence, ""))) {
      const safe = privatePattern ? line.replace(privatePattern, "[redacted]") : line;
      emit(safe + newline);
    }
    line = "";
    omitted = false;
  };
  const consume = text => {
    for (const fragment of text.split(/(?<=\n)/u)) {
      const newline = fragment.endsWith("\n");
      if (!omitted) {
        line += newline ? fragment.slice(0, -1) : fragment;
        if (line.length > maximumLineLength) { line = ""; omitted = true; }
      }
      if (newline) finishLine("\n");
    }
  };
  return {
    write: chunk => consume(decoder.write(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk))),
    end: () => { consume(decoder.end()); if (line || omitted) finishLine(""); },
  };
}
