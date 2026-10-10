// SPDX-License-Identifier: Apache-2.0
import { stripVTControlCharacters } from "node:util";

const maximumLine = 32 * 1024;
const omitted = "[desktop diagnostic omitted: line too long]\n";

export function desktopDiagnosticOutput(args, destinations) {
  // Cargo echoes full application argv, including quoted and escaped values.
  // Drop command echo records and redact literal values in other diagnostics.
  // Keep this filter while a launch child can print private application argv.
  const values = args.flatMap(argument => {
    const value = argument.startsWith("--") ? argument.includes("=") ? argument.slice(argument.indexOf("=") + 1) : "" : argument;
    return [argument, value].filter(Boolean).flatMap(privateValue => [privateValue, stripVTControlCharacters(privateValue), JSON.stringify(privateValue).slice(1, -1), ...privateValue.split(/[\r\n]/u)]);
  });
  const patterns = [...new Set(values.filter(Boolean))].sort((left, right) => right.length - left.length);
  const filter = line => {
    // Strip terminal controls before matching so coloring cannot split a value.
    let safe = stripVTControlCharacters(line);
    if (/^\s*Running\s+`/u.test(safe)) return "[desktop command started]\n";
    if (/process didn't exit successfully:/u.test(safe)) {
      const exit = /\(exit status: (\d+)\)\s*$/u.exec(safe)?.[1];
      const signal = /\(signal: (\d+)(?:, [^)]*)?\)\s*$/u.exec(safe)?.[1];
      return `[desktop command failed${exit ? `: exit status: ${exit}` : signal ? `: signal: ${signal}` : ""}]\n`;
    }
    for (const value of patterns) {
      // Short values must be complete words; replacing a single letter inside
      // every ordinary diagnostic would hide unrelated build failures.
      if (value.length <= 3) {
        const literal = value.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
        safe = safe.replace(new RegExp(`(?<![\\p{L}\\p{N}_])${literal}(?![\\p{L}\\p{N}_])`, "gu"), "[private argument]");
      } else safe = safe.replaceAll(value, "[private argument]");
    }
    return safe;
  };
  return Object.fromEntries(["stdout", "stderr"].map(name => {
    let buffered = "", discarded = false;
    return [name, chunk => {
      if (chunk === null) {
        if (buffered) destinations[name](filter(buffered));
        buffered = ""; discarded = false;
        return;
      }
      for (const part of chunk.split(/(?<=\n)/u)) {
        if (!discarded) {
          // Bound retained diagnostics; never publish a partial oversized line
          // that could split a private value across the output boundary.
          if (buffered.length + part.length > maximumLine) { buffered = ""; discarded = true; destinations[name](omitted); }
          else buffered += part;
        }
        if (part.endsWith("\n")) {
          if (!discarded) destinations[name](filter(buffered));
          buffered = ""; discarded = false;
        }
      }
    }];
  }));
}
