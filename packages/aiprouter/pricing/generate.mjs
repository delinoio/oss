// SPDX-License-Identifier: Apache-2.0
// Run: node packages/aiprouter/pricing/generate.mjs /path/to/pinned/opencodex
import { registerHooks, stripTypeScriptTypes } from "node:module";
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { execFileSync } from "node:child_process";
import vm from "node:vm";
const revision = "88aaa754566d119b6b8fc898f8fb47602387d82e";
const root = resolve(process.argv[2]);
if (
  execFileSync("git", ["-C", root, "rev-parse", "HEAD"], {
    encoding: "utf8",
  }).trim() !== revision
)
  throw Error("wrong upstream revision");
if (
  execFileSync(
    "git",
    ["-C", root, "status", "--porcelain", "--untracked-files=no"],
    { encoding: "utf8" },
  ).trim()
)
  throw Error("upstream must be clean");
registerHooks({
  resolve(specifier, context, next) {
    try {
      return next(specifier, context);
    } catch (e) {
      if (specifier.startsWith(".")) return next(specifier + ".ts", context);
      throw e;
    }
  },
});
const expected = await import(
  pathToFileURL(resolve(root, "src/usage/expected-prices.ts"))
);
const metadata = readFileSync(
  resolve(root, "src/generated/model-metadata.ts"),
  "utf8",
);
const aliases = JSON.parse(
  metadata.match(/const PROVIDER_ALIASES[^=]*= ([\s\S]*?) as const;/)[1],
);
const vendors = JSON.parse(
  metadata.match(/export const COST_VENDOR_PRIORITY = (.*);/)[1],
);
const rows = {};
for (const line of metadata
  .match(/const DATA[^=]*= \{([\s\S]*?)\n\};/)[1]
  .trim()
  .split("\n")) {
  const entry = JSON.parse("{" + line.trim().replace(/,$/, "") + "}");
  for (const [provider, models] of Object.entries(entry))
    rows[provider] = Object.fromEntries(
      models
        .filter(
          (row) =>
            row.length >= 10 && row.slice(6, 10).every((x) => x !== null),
        )
        .map((row) => [
          row[0],
          {
            input: row[6],
            output: row[7],
            cacheRead: row[8],
            cacheWrite: row[9],
          },
        ]),
    );
}
const antiSource = readFileSync(
  resolve(root, "src/providers/antigravity-models.ts"),
  "utf8",
);
const antiCode = stripTypeScriptTypes(
  antiSource.replace(/^import .*;$/gm, ""),
).replace(/^export /gm, "");
const antigravity = vm.runInNewContext(
  antiCode + "; ANTIGRAVITY_USAGE_BASE_BY_ID",
);
const out = resolve(dirname(fileURLToPath(import.meta.url)), "snapshot");
mkdirSync(out, { recursive: true });
for (const [provider, models] of Object.entries(rows))
  writeFileSync(
    resolve(out, provider + ".json"),
    JSON.stringify(models, null, 2) + "\n",
  );
writeFileSync(
  resolve(out, "rules.json"),
  JSON.stringify(
    {
      revision,
      aliases,
      vendors,
      antigravity,
      overlays: expected.EXPECTED_PRICE_OVERLAYS,
      overrides: expected.VERIFIED_PRICE_OVERRIDES,
      priority: expected.PRIORITY_PRICING_RULES,
      context: expected.CONTEXT_TIERS,
    },
    null,
    2,
  ) + "\n",
);
writeFileSync(
  resolve(out, "LICENSE-MIT"),
  readFileSync(resolve(root, "LICENSE")),
);
// Differential fixtures execute the pinned estimator with empty deployment state.
// No reference server engine or runtime dependencies are bundled.
const meta = await import(
  pathToFileURL(resolve(root, "src/generated/model-metadata.ts"))
);
const costSource = readFileSync(resolve(root, "src/usage/cost.ts"), "utf8");
const costCode = stripTypeScriptTypes(
  costSource.replace(/^import[\s\S]*?;\n/gm, ""),
).replace(/^export /gm, "");
const sandbox = {
  ...meta,
  ...expected,
  canonicalFastTierMarker: (t) =>
    ["fast", "priority"].includes(t?.trim().toLowerCase())
      ? "priority"
      : t?.trim().toLowerCase() === "ultrafast"
        ? "ultrafast"
        : undefined,
  baseProviderLabel: (p) => p,
  canonicalAntigravityUsageModel: (m) => antigravity[m.trim()] ?? m.trim(),
  activeConfiguredProviders: () => new Set(),
  activeAccountPricingProviders: () => new Map(),
  activeUserCostOverlays: () => [],
  userCostOverlayVersion: () => 0,
};
vm.runInNewContext(
  costCode + "; globalThis.estimate = estimateRequestCost;",
  sandbox,
);
const usage = {
  inputTokens: 1000,
  outputTokens: 200,
  cacheReadInputTokens: 100,
  cacheCreationInputTokens: 50,
  cacheCreation1hInputTokens: 20,
};
const fixtureDir = resolve(dirname(out), "testdata");
mkdirSync(fixtureDir, { recursive: true });
const save = (name, inputs) =>
  writeFileSync(
    resolve(fixtureDir, name + ".json"),
    JSON.stringify(
      inputs.map((input) => ({
        input,
        expected: sandbox.estimate(input, expected.EXPECTED_PRICE_OVERLAYS, []),
      })),
      null,
      2,
    ) + "\n",
  );
const unique = [
  ...new Map(
    expected.EXPECTED_PRICE_OVERLAYS.map((o) => [
      o.provider + "\0" + o.modelId,
      o,
    ]),
  ).values(),
];
for (const provider of new Set(unique.map((o) => o.provider)))
  save(
    "provider-" + provider,
    unique
      .filter((o) => o.provider === provider)
      .map((o) => ({
        provider: o.provider,
        model: o.modelId,
        usage,
        usageStatus: "reported",
      })),
  );
save(
  "context",
  expected.CONTEXT_TIERS.flatMap((r) =>
    [r.thresholdInputTokens, r.thresholdInputTokens + 1].flatMap((n) =>
      [
        {},
        { responseServiceTier: "priority" },
        { requestedServiceTier: "priority" },
      ].map((serviceTier) => ({
        provider: r.provider,
        model: r.modelId,
        usage: { ...usage, inputTokens: n, cacheReadInputTokens: n - 100 },
        usageStatus: "reported",
        serviceTier,
      })),
    ),
  ),
);
save(
  "priority",
  expected.PRIORITY_PRICING_RULES.flatMap((r) =>
    [
      {},
      { responseServiceTier: "priority" },
      { requestedServiceTier: "priority" },
      { responseServiceTier: "default", requestedServiceTier: "priority" },
    ].map((serviceTier) => ({
      provider: r.provider,
      model: r.modelId,
      usage,
      usageStatus: "reported",
      serviceTier,
    })),
  ),
);
save(
  "cursor",
  [
    "claude-opus-5-thinking-max-fast",
    "claude-opus-5-max-fast",
    "claude-opus-5-5-thinking-fast",
    "claude-opus-5-5-high-fast",
    "claude-5.5-opus-high-fast",
    "claude-4.8-opus-high-thinking-fast",
    "claude-sonnet-5-5-fast",
  ].map((model) => ({
    provider: "cursor",
    model,
    usage,
    usageStatus: "reported",
  })),
);
save(
  "antigravity",
  Object.keys(antigravity).map((model) => ({
    provider: "google-antigravity",
    model,
    usage,
    usageStatus: "reported",
  })),
);
