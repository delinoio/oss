// SPDX-License-Identifier: Apache-2.0
// Shared stylesheet scanner for CSS guard tests. jsdom does not evaluate
// container queries, so guards read every committed stylesheet directly.
import { readdirSync, readFileSync } from "node:fs";
export interface CssRule {
  file: string;
  context: string[];
  selector: string;
  declarations: Map<string, string>;
}

export function parseRules(file: string, css: string): CssRule[] {
  const rules: CssRule[] = [];
  const text = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const walk = (body: string, context: string[]) => {
    let i = 0;
    while (i < body.length) {
      const open = body.indexOf("{", i);
      if (open < 0) break;
      const prelude = body.slice(i, open).replace(/\s+/g, " ").trim();
      let depth = 1;
      let j = open + 1;
      while (j < body.length && depth > 0) {
        if (body[j] === "{") depth += 1;
        else if (body[j] === "}") depth -= 1;
        j += 1;
      }
      const inner = body.slice(open + 1, j - 1);
      if (prelude.startsWith("@")) {
        walk(inner, [...context, prelude]);
      } else {
        const declarations = new Map<string, string>();
        for (const declaration of inner.split(";")) {
          const colon = declaration.indexOf(":");
          if (colon > 0)
            declarations.set(
              declaration.slice(0, colon).trim(),
              declaration.slice(colon + 1).trim(),
            );
        }
        rules.push({ file, context, selector: prelude, declarations });
      }
      i = j;
    }
  };
  walk(text, []);
  return rules;
}

export const RULES = readdirSync("src")
  .filter((name) => name.endsWith(".css"))
  .flatMap((name) => parseRules(name, readFileSync(`src/${name}`, "utf8")));

export const rulesFor = (needle: string) =>
  RULES.filter((rule) => rule.selector.includes(needle));

// Merges the declarations of every rule that has a selector in its selector
// list for which the matcher returns true.
export function cascade(
  match: (selector: string) => boolean,
): Map<string, string> {
  const merged = new Map<string, string>();
  for (const rule of RULES)
    if (rule.selector.split(",").some((selector) => match(selector.trim())))
      for (const [key, value] of rule.declarations) merged.set(key, value);
  return merged;
}
