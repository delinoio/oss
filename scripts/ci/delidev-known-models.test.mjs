// SPDX-License-Identifier: Apache-2.0
import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { buildCatalog, validateCatalog, reconcile, changes, extractChatGPT, extractClaude, extractGrok, canonical, catalogPath } from "../delidev/known-subscription-models.mjs";
import { createHash } from "node:crypto";
import { assertBotBranch, describeUpdate, branch } from "../delidev/known-subscription-models-pr.mjs";

const sources = ["codex", "openai-retirement", "claude-code", "claude-models", "grok-build"].map(key => ({ key, url: "https://docs.x.ai/build/settings", revision: "fixture", sha256: "a".repeat(64) }));
const inputs = {
  codex: JSON.stringify({ models: [
    { slug: "gpt-current", display_name: "GPT Current", visibility: "list", available_in_plans: ["plus"], priority: 1, minimal_client_version: "0.153.0" },
    { slug: "gpt-old", display_name: "GPT Old", visibility: "list", available_in_plans: ["plus"], priority: 2 },
    { slug: "gpt-future", display_name: "GPT Future", visibility: "list", available_in_plans: ["plus"], priority: 3 },
    { slug: "gpt-hidden", display_name: "Internal", visibility: "hide", available_in_plans: ["plus"], priority: 4 },
    { slug: "api-only", display_name: "API", visibility: "list", available_in_plans: [], priority: 5 },
  ] }),
  openai: "## Deprecated Codex models\n\nGPT Old retired on September 14, 2026. Replace `gpt-old` with `gpt-current`.\n\nGPT Future retires on October 14, 2026. Replace `gpt-future` with `gpt-current`.",
  claude: "### Model aliases\n| **`opus`** | native |\n| **`sonnet`** | native |\n| **`haiku`** | native |\n\n## Versions\nSonnet 5.5 requires Claude Code v2.1.284 or later, and Opus 5.5 requires v2.1.280 or later.",
  overview: "## Compare models\n| Feature | Claude Opus 5.5 | Claude Sonnet 5.5 | Claude Haiku 4.5 |\n| Claude API ID | `claude-opus-5-5` | `claude-sonnet-5-5` | `claude-haiku-4-5-20251001` |\n| Claude API alias | `claude-opus-5-5` | `claude-sonnet-5-5` | `claude-haiku-4-5` |\n| Retirement | Not sooner than Oct 15 | | |",
  grok: '[models]\ndefault = "grok-build" # recommended for coding / agent sessions\nweb_search = "grok-api-example"\n[model."grok-api-example"]\nmodel = "grok-api-example"',
};
const fixture = () => buildCatalog(inputs, sources, "2026-10-06");
const rehash = catalog => { catalog.catalog_version = `sha256:${createHash("sha256").update(canonical(catalog.services)).digest("hex")}`; return catalog; };
test("official-shaped extraction excludes hidden, API-only, retired and arbitrary examples", () => {
  const value = fixture();
  assert.deepEqual(value.services[0].models.map(row => row.native_id), ["gpt-current", "gpt-future"]);
  assert.equal(value.services[0].models[1].retirement_date, "2026-10-14");
  assert.equal(value.services[1].models[0].minimum_harness_version, "2.1.280");
  assert.equal(value.services[1].models[1].minimum_harness_version, "2.1.284");
  assert.equal(value.services[1].models[2].retirement_date, undefined);
  assert.deepEqual(value.services[2].models.map(row => row.native_id), ["grok-build"]);
  assert.equal(extractChatGPT(inputs.codex, inputs.openai, "2026-10-14").length, 1);
});
test("changed document structure, unknown API family and empty extraction fail closed", () => {
  assert.throws(() => extractChatGPT(inputs.codex, "# no retirement section", "2026-10-06"));
  assert.throws(() => extractClaude(inputs.claude, inputs.overview.replace("Claude Opus", "Claude ApiOnly")));
  assert.throws(() => extractClaude(inputs.claude, inputs.overview.replace("Claude API ID", "Model ID")));
  assert.throws(() => extractGrok(inputs.grok.replace("recommended for coding", "changed")));
  assert.throws(() => buildCatalog({ ...inputs, codex: '{"models":[]}' }, sources));
});
test("duplicate models, invalid dates/versions, unbound sources and over-limit pages fail", () => {
  const mutate = change => { const value = fixture(); change(value); assert.throws(() => validateCatalog(rehash(value))); };
  mutate(value => value.services[0].models.push({ ...value.services[0].models[0], order: 2 }));
  mutate(value => value.services[0].models[0].retirement_date = "2026-02-30");
  mutate(value => value.schema_version = 2);
  mutate(value => value.services[0].models[0].source_keys = ["missing"]);
  mutate(value => value.services[0].models = Array.from({ length: 201 }, (_, order) => ({ ...value.services[0].models[0], native_id: `fixture-${order}`, order })));
});
test("daily date/digest-only reads retain reviewed bytes; model metadata changes report a diff", () => {
  const previous = fixture(); const next = fixture(); next.updated_at = "2026-10-07"; next.sources[0].sha256 = "b".repeat(64);
  assert.equal(reconcile(previous, next), previous);
  next.services[0].models[0].minimum_harness_version = "0.155.0"; rehash(next);
  assert.equal(reconcile(previous, next), next);
  assert.deepEqual(changes(previous, next), { added: [], changed: ["chatgpt/gpt-current"], retired: [] });
});
test("committed catalog validates through the collector contract", async () => {
  validateCatalog(JSON.parse(await readFile(catalogPath, "utf8")));
});
test("existing PR updates require complete bot-only history and catalog-only changes", () => {
  const bot = { login: "delino-release-bot[bot]" };
  const state = { commits: [{ author: bot, committer: bot }], files: [{ filename: catalogPath }], total: 1, pullRequests: [{ number: 123, user: bot, head: { repo: { full_name: "delinoio/oss" } }, base: { ref: "main" } }] };
  assert.doesNotThrow(() => assertBotBranch(state));
  assert.throws(() => assertBotBranch({ ...state, commits: [{ author: { login: "human" }, committer: bot }] }));
  assert.throws(() => assertBotBranch({ ...state, total: 2 }));
  assert.throws(() => assertBotBranch({ ...state, files: [{ filename: "another-file" }] }));
  assert.throws(() => assertBotBranch({ ...state, pullRequests: [...state.pullRequests, ...state.pullRequests] }));
  const body = describeUpdate(fixture(), fixture(), "a".repeat(40));
  assert.match(body, /No automatic merge/); assert.match(body, /SHA-256/); assert.match(body, /No subscription entitlement/);
});
test("daily workflow validates before a narrowly scoped write token and protects review ownership", async () => {
  const workflow = await readFile(".github/workflows/delidev-known-models.yml", "utf8");
  const publisher = await readFile("scripts/delidev/known-subscription-models-pr.mjs", "utf8");
  assert.match(workflow, /cron: '17 19 \* \* \*'/); assert.match(workflow, /workflow_dispatch:/);
  assert.match(workflow, /permissions:\n  contents: read/); assert.match(workflow, /persist-credentials: false/);
  assert.match(workflow, /repositories: oss/); assert.match(workflow, /permission-pull-requests: write/);
  assert.ok(workflow.indexOf("--validate") < workflow.indexOf("actions/create-github-app-token"));
  assert.equal((publisher.match(/const branch =/g) ?? []).length, 1); assert.ok(branch.startsWith("kdy1/"));
  assert.match(publisher, /--force-with-lease=refs\/heads/); assert.match(publisher, /--body-file/);
  assert.match(publisher, /current.catalog_version === candidate.catalog_version && pulls.length/);
  assert.doesNotMatch(publisher, /pr.*merge.*--auto|--admin|--no-verify/);
});
