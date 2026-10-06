// SPDX-License-Identifier: Apache-2.0
import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { buildCatalog, validateCatalog, reconcile, changes, extractChatGPT, extractClaude, extractGrok, canonical, catalogPath } from "../delidev/known-subscription-models.mjs";
import { createHash } from "node:crypto";
import { assertBotBranch, describeUpdate, findClosedCandidateReview, branch } from "../delidev/known-subscription-models-pr.mjs";

const sourceHosts = { codex: "github.com", "openai-retirement": "learn.chatgpt.com", "claude-code": "code.claude.com", "claude-models": "platform.claude.com", "grok-build": "docs.x.ai" };
const sources = Object.entries(sourceHosts).map(([key, host]) => ({ key, url: `https://${host}/fixture`, revision: "fixture", sha256: "a".repeat(64) }));
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
  overview: "## Compare models\n| Feature | Claude Opus 5.5 | Claude Sonnet 5.5 | Claude Haiku 4.5 |\n| Claude API ID | `claude-opus-5-5` | `claude-sonnet-5-5` | `claude-haiku-4-5-20251001` |\n| Claude API alias | `claude-opus-5-5` | `claude-sonnet-5-5` | `claude-haiku-4-5` |\n| Retirement | Not sooner than Oct 15 | October 20, 2026 | |",
  grok: '[models]\ndefault = "grok-build" # recommended for coding / agent sessions\nweb_search = "grok-api-example"\n[model."grok-api-example"]\nmodel = "grok-api-example"',
};
const fixture = () => buildCatalog(inputs, sources.map(source => ({ ...source })), "2026-10-06");
const rehash = catalog => { catalog.catalog_version = `sha256:${createHash("sha256").update(canonical(catalog.services)).digest("hex")}`; return catalog; };
test("official-shaped extraction excludes hidden, API-only, retired and arbitrary examples", () => {
  const value = fixture();
  assert.deepEqual(value.services[0].models.map(row => row.native_id), ["gpt-current", "gpt-future"]);
  assert.equal(value.services[0].models[1].retirement_date, "2026-10-14");
  assert.equal(value.services[1].models[0].minimum_harness_version, "2.1.280");
  assert.equal(value.services[1].models[1].minimum_harness_version, "2.1.284");
  assert.equal(value.services[1].models[1].retirement_date, "2026-10-20");
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
test("retirement subjects require an exact model name or ID, not a family prefix", () => {
  const input = JSON.parse(inputs.codex);
  input.models.push({ slug: "gpt", display_name: "GPT", visibility: "list", available_in_plans: ["plus"], priority: 6 });
  assert.ok(extractChatGPT(JSON.stringify(input), inputs.openai, "2026-10-14").some(row => row.native_id === "gpt"));
  assert.throws(() => extractChatGPT(inputs.codex, inputs.openai.replace("September 14", "February 30"), "2026-10-06"));
  assert.throws(() => extractClaude(inputs.claude, inputs.overview.replace("October 20, 2026", "February 30, 2026")));
  assert.equal(canonical({ display_name: "<&>\u2028\u2029" }), '{"display_name":"\\u003c\\u0026\\u003e\\u2028\\u2029"}');
});
test("duplicate models, invalid dates/versions, unbound sources and over-limit pages fail", () => {
  const mutate = change => { const value = fixture(); change(value); assert.throws(() => validateCatalog(rehash(value))); };
  mutate(value => value.services[0].models.push({ ...value.services[0].models[0], order: 2 }));
  mutate(value => value.services[0].models[0].retirement_date = "2026-02-30");
  mutate(value => value.schema_version = 2);
  mutate(value => value.services[0].models[0].source_keys = ["missing"]);
  mutate(value => value.services[0].models = Array.from({ length: 201 }, (_, order) => ({ ...value.services[0].models[0], native_id: `fixture-${order}`, order })));
});
test("catalog provenance requires every fixed source key and its host", () => {
  const incomplete = fixture();
  incomplete.sources = [incomplete.sources.find(source => source.key === "grok-build")];
  for (const entry of incomplete.services) for (const model of entry.models) model.source_keys = ["grok-build"];
  assert.throws(() => validateCatalog(rehash(incomplete)));
  const wrongHost = fixture();
  wrongHost.sources.find(source => source.key === "codex").url = "https://docs.x.ai/build/settings.md";
  assert.throws(() => validateCatalog(rehash(wrongHost)));
});
test("minimum harness versions must be scalar strings", () => {
  const value = JSON.parse(inputs.codex);
  value.models[0].minimal_client_version = ["0.153.0"];
  assert.throws(() => buildCatalog({ ...inputs, codex: JSON.stringify(value) }, sources));
});
test("daily date/digest-only reads retain reviewed bytes; model metadata changes report a diff", () => {
  const previous = fixture(); const next = fixture(); next.updated_at = "2026-10-07"; next.sources[0].sha256 = "b".repeat(64);
  assert.equal(reconcile(previous, next), previous);
  next.services[0].models[0].minimum_harness_version = "0.155.0"; rehash(next);
  assert.equal(reconcile(previous, next), next);
  assert.deepEqual(changes(previous, next), { added: [], changed: ["chatgpt/gpt-current"], retired: [] });
});
test("committed catalog validates through the collector contract", async () => {
  const catalog = JSON.parse(await readFile(catalogPath, "utf8"));
  validateCatalog(catalog);
  assert.equal(catalog.sources.find(source => source.key === "openai-retirement").url, "https://learn.chatgpt.com/docs/models.md");
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
test("a manually closed review suppresses the same candidate until metadata changes", () => {
  const candidate = fixture();
  const review = { html_url: "https://github.com/delinoio/oss/pull/456", user: { login: "delino-release-bot[bot]" }, head: { sha: "candidate", repo: { full_name: "delinoio/oss" } }, base: { ref: "main" } };
  assert.equal(findClosedCandidateReview([review], candidate.catalog_version, () => candidate), review);
  assert.equal(findClosedCandidateReview([review], "sha256:" + "b".repeat(64), () => candidate), undefined);
  assert.equal(findClosedCandidateReview([{ ...review, user: { login: "human" } }], candidate.catalog_version, () => candidate), undefined);
  assert.equal(findClosedCandidateReview([{ ...review, merged_at: "2026-10-06T00:00:00Z" }], candidate.catalog_version, () => candidate), undefined);
});
test("daily workflow validates before a narrowly scoped write token and protects review ownership", async () => {
  const workflow = await readFile(".github/workflows/delidev-known-models.yml", "utf8");
  const publisher = await readFile("scripts/delidev/known-subscription-models-pr.mjs", "utf8");
  const collector = await readFile("scripts/delidev/known-subscription-models.mjs", "utf8");
  assert.match(workflow, /cron: '17 19 \* \* \*'/); assert.match(workflow, /workflow_dispatch:/);
  assert.match(workflow, /permissions:\n  contents: read/); assert.match(workflow, /persist-credentials: false/);
  assert.match(workflow, /repositories: oss/); assert.match(workflow, /permission-pull-requests: write/);
  assert.ok(workflow.indexOf("--validate") < workflow.indexOf("actions/create-github-app-token"));
  assert.doesNotMatch(workflow.slice(workflow.indexOf("- name: Collect and validate public metadata"), workflow.indexOf("- name: Create repository-scoped write token")), /GH_TOKEN/);
  assert.doesNotMatch(collector, /execFileSync|GH_TOKEN/);
  assert.equal((publisher.match(/const branch =/g) ?? []).length, 1); assert.ok(branch.startsWith("kdy1/"));
  assert.match(publisher, /--force-with-lease=refs\/heads/); assert.match(publisher, /--body-file/);
  assert.match(publisher, /current.catalog_version === candidate.catalog_version && pulls.length/);
  assert.match(publisher, /sameSourceProvenance/); assert.match(publisher, /collect\(\)/); assert.match(publisher, /pr", "close/);
  assert.doesNotMatch(workflow, /if: steps\.collect\.outputs\.changed == 'true'/);
  assert.doesNotMatch(publisher, /pr.*merge.*--auto|--admin|--no-verify/);
});
