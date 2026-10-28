// SPDX-License-Identifier: Apache-2.0
// Extract only data from the pinned source. No upstream runtime is bundled.
import { registerHooks } from "node:module";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { pathToFileURL, fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";
const root = resolve(process.argv[2]);
const revision = "88aaa754566d119b6b8fc898f8fb47602387d82e";
if (
  execFileSync("git", ["-C", root, "rev-parse", "HEAD"], {
    encoding: "utf8",
  }).trim() !== revision
)
  throw Error("wrong revision");
if (
  execFileSync(
    "git",
    ["-C", root, "status", "--porcelain", "--untracked-files=no"],
    { encoding: "utf8" },
  ).trim()
)
  throw Error("upstream must be clean");
registerHooks({
  load(u, c, next) {
    const r = next(u, c);
    if (
      u.endsWith("/adapters/devin/live-models.ts") ||
      u.endsWith("/providers/antigravity-models.ts")
    )
      return { ...r, source: String(r.source).replace(/^import .*;$/gm, "") };
    return r;
  },
  resolve(s, c, next) {
    try {
      return next(s, c);
    } catch (e) {
      if (s.startsWith(".")) return next(s + ".ts", c);
      throw e;
    }
  },
});
const { PROVIDER_REGISTRY_CORE: core } = await import(
  pathToFileURL(resolve(root, "src/providers/registry/entries-core.ts"))
);
const { PROVIDER_REGISTRY_EXTENDED: extended } = await import(
  pathToFileURL(resolve(root, "src/providers/registry/entries-extended.ts"))
);
const rows = [...core, ...extended];
const out = resolve(dirname(fileURLToPath(import.meta.url)), "snapshot");
writeFileSync(
  resolve(out, "catalog.json"),
  JSON.stringify(
    rows,
    (k, v) => {
      if (typeof v !== "function") return v;
      if (
        k !== "modelServiceTierCapabilityBaseUrlGuard" ||
        v.name !== "isCanonicalOpenRouterTarget"
      )
        throw Error("unmapped catalog function");
      return { function: v.name };
    },
    2,
  ) + "\n",
);
writeFileSync(
  resolve(out, "LICENSE-MIT"),
  readFileSync(resolve(root, "LICENSE")),
);
console.log(
  JSON.stringify({
    revision,
    presets: rows.length,
    wires: [...new Set(rows.map((row) => row.adapter))].sort(),
  }),
);
