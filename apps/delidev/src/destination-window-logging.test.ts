// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// Destination window code paths log only a phase, a closed failure code and a
// closed reason. This scan fails when one of those paths gains a log field that
// can carry a route, an opaque ID, a title or connection data.
const allowedRendererFields = new Set(["phase", "code", "reason"]);
const allowedNativeFields = new Set(["operation", "phase", "code"]);

/** Returns the text between the parentheses that open at `open`. */
function balanced(source: string, open: number): string {
  let depth = 0;
  for (let index = open; index < source.length; index++) {
    const char = source[index];
    if (char === "(") depth++;
    else if (char === ")" && --depth === 0)
      return source.slice(open + 1, index);
  }
  throw new Error("unbalanced call");
}

/** Every console call in `source` as its argument text. */
function consoleCalls(source: string): string[] {
  return [
    ...source.matchAll(/console\.(?:log|info|warn|error|debug|trace)\(/g),
  ].map((match) => balanced(source, match.index + match[0].length - 1));
}

/** Top-level field names of the first object literal in the arguments. */
function objectKeys(args: string): string[] {
  const start = args.indexOf("{");
  if (start < 0) return [];
  let depth = 0;
  let token = "";
  let value = false;
  const keys: string[] = [];
  const finish = () => {
    // A shorthand field (`{ id }`) has no colon, so its token is the key.
    if (!value && token.trim()) keys.push(token.trim());
    token = "";
    value = false;
  };
  for (let index = start; index < args.length; index++) {
    const char = args[index];
    if (char === "{" || char === "(" || char === "[") {
      depth++;
      if (depth === 1) continue;
    }
    if (char === "}" || char === ")" || char === "]") {
      depth--;
      if (depth === 0) break;
    }
    if (depth !== 1) continue;
    if (char === ",") finish();
    else if (char === ":" && !value) {
      keys.push(token.trim());
      token = "";
      value = true;
    } else if (!value) token += char;
  }
  finish();
  return keys.filter(Boolean);
}

const rendererFiles = [
  "src/destination-window.tsx",
  "src/navigation-disposition.ts",
];

describe("destination window logging", () => {
  it("logs only closed fields from the renderer controller and classifier", () => {
    for (const file of rendererFiles) {
      for (const call of consoleCalls(readFileSync(file, "utf8")))
        for (const key of objectKeys(call))
          expect(allowedRendererFields, `${file}: ${key}`).toContain(key);
    }
    // The scan itself must see the controller's phases.
    expect(
      rendererFiles.flatMap((file) => consoleCalls(readFileSync(file, "utf8")))
        .length,
    ).toBeGreaterThanOrEqual(4);
  });

  it("opens a Search result destination through anonymous resolution without logging", () => {
    const app = readFileSync("src/App.tsx", "utf8");
    const start = app.indexOf("const openSearchResultInWindow");
    const end = app.indexOf("const openSearchResult = ", start);
    expect(start).toBeGreaterThan(0);
    expect(end).toBeGreaterThan(start);
    const body = app.slice(start, end);
    expect(consoleCalls(body)).toEqual([]);
    const resolve = body.indexOf("resolveSearchSession(");
    expect(resolve).toBeGreaterThan(-1);
    expect(balanced(body, resolve + "resolveSearchSession".length)).toContain(
      "SearchResolutionLogging.Anonymous",
    );
  });

  it("keeps the Search resolution identifier behind the identified mode only", () => {
    const source = readFileSync("src/search-session-routing.ts", "utf8");
    const calls = consoleCalls(source);
    expect(calls).toHaveLength(1);
    const call = calls[0];
    // The only log with an identifier is guarded by the identified mode.
    expect(call).toMatch(
      /SearchResolutionLogging\.Identified\s*\?\s*\{\s*sessionId: id, reason\s*\}\s*:\s*\{\s*reason\s*\}/,
    );
  });

  it("logs only the operation, phase and failure code from the native command", () => {
    for (const file of [
      "src-tauri/src/window_host.rs",
      "src-tauri/src/destination_route.rs",
    ]) {
      const source = readFileSync(file, "utf8");
      const macros = [
        ...source.matchAll(
          /tracing::(?:trace|debug|info|warn|error)!\(([^;]*?)\);/gs,
        ),
      ].map((match) => match[1]);
      const destination = macros.filter(
        (args) =>
          file.endsWith("destination_route.rs") ||
          args.includes('"destination_window"'),
      );
      for (const args of destination)
        for (const field of args.split(",")) {
          const name = field
            .trim()
            .replace(/^[?%]/, "")
            .split(/\s*=/)[0]
            .trim();
          if (name)
            expect(allowedNativeFields, `${file}: ${name}`).toContain(name);
        }
      if (file.endsWith("window_host.rs"))
        expect(destination.length).toBeGreaterThanOrEqual(3);
    }
  });
});
