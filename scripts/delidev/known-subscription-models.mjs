// SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { execFileSync } from "node:child_process";

export const canonical = value => JSON.stringify(value, (_key, item) => item && typeof item === "object" && !Array.isArray(item) ? Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b, "en"))) : item);
export const catalogPath = "cmds/delidev-cli/internal/knownmodels/catalog.json";
export const services = ["chatgpt", "claude", "grok"];
const digest = value => createHash("sha256").update(value).digest("hex");
const fail = reason => { throw new Error(`Known-model validation failed: ${reason}`); };
const datePattern = /^\d{4}-\d{2}-\d{2}$/;
const validDate = value => datePattern.test(value) && new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value;
const exactKeys = (value, required, optional = []) => {
  if (!value || typeof value !== "object" || Array.isArray(value) || required.some(key => !(key in value)) || Object.keys(value).some(key => ![...required, ...optional].includes(key))) fail("object shape");
};
export function validateCatalog(value) {
  exactKeys(value, ["schema_version", "catalog_version", "updated_at", "sources", "services"]);
  if (value.schema_version !== 1 || !/^sha256:[a-f0-9]{64}$/.test(value.catalog_version) || !validDate(value.updated_at)) fail("version or date");
  if (!Array.isArray(value.sources) || !value.sources.length || value.sources.length > 10) fail("sources");
  const keys = new Set();
  for (const source of value.sources) {
    exactKeys(source, ["key", "url", "revision", "sha256"]);
    if (!/^[a-z-]+$/.test(source.key) || keys.has(source.key) || typeof source.revision !== "string" || !source.revision || source.revision.length > 128 || !/^[a-f0-9]{64}$/.test(source.sha256)) fail("source identity");
    const url = new URL(source.url);
    if (url.protocol !== "https:" || url.username || url.password || !["github.com", "learn.chatgpt.com", "code.claude.com", "platform.claude.com", "docs.x.ai"].includes(url.hostname)) fail("source URL");
    keys.add(source.key);
  }
  if (!Array.isArray(value.services) || value.services.length !== services.length) fail("service inventory");
  for (const [index, entry] of value.services.entries()) {
    exactKeys(entry, ["service", "models"]);
    if (entry.service !== services[index] || !Array.isArray(entry.models) || !entry.models.length || entry.models.length > 200) fail("empty or unsupported service");
    const ids = new Set();
    for (const [order, model] of entry.models.entries()) {
      exactKeys(model, ["native_id", "display_name", "order", "source_keys"], ["minimum_harness_version", "retirement_date"]);
      if (typeof model.native_id !== "string" || !/^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,255}$/.test(model.native_id) || ids.has(model.native_id) || typeof model.display_name !== "string" || !model.display_name.trim() || Buffer.byteLength(model.display_name) > 256 || /[\x00-\x1f\x7f]/.test(model.display_name) || model.order !== order) fail("model identity or order");
      if (!Array.isArray(model.source_keys) || !model.source_keys.length || new Set(model.source_keys).size !== model.source_keys.length || model.source_keys.some(key => !keys.has(key))) fail("model provenance");
      if (model.minimum_harness_version !== undefined && !/^\d+\.\d+\.\d+$/.test(model.minimum_harness_version)) fail("minimum version");
      if (model.retirement_date !== undefined && !validDate(model.retirement_date)) fail("retirement date");
      ids.add(model.native_id);
    }
  }
  if (value.catalog_version !== `sha256:${digest(canonical(value.services))}`) fail("catalog digest");
  return value;
}

function section(markdown, heading) {
  const start = markdown.indexOf(heading);
  if (start < 0) fail("upstream section changed");
  return markdown.slice(start + heading.length).split(/\n## /)[0];
}
const normalize = text => text.toLowerCase().replace(/[^a-z0-9]/g, "");
function retirement(markdown, model, today) {
  const notices = section(markdown, "## Deprecated Codex models").split(/\n\s*\n/);
  for (const paragraph of notices) {
    // Replacement IDs belong to the next sentence, not the retiring subject.
    const sentence = paragraph.replace(/\s+/g, " ").split(/\. (?=[A-Z])/)[0];
    if (![model.native_id, model.display_name].some(value => normalize(sentence).includes(normalize(value)))) continue;
    if (/already deprecated/i.test(sentence)) return { excluded: true };
    const match = sentence.match(/(?:retires?|retired|will retire).*?(January|February|March|April|May|June|July|August|September|October|November|December) (\d{1,2}), (\d{4})/i);
    if (!match) fail("unrecognized retirement notice");
    const date = new Date(`${match[1]} ${match[2]}, ${match[3]} UTC`).toISOString().slice(0, 10);
    return { excluded: date <= today, retirement_date: date };
  }
  return {};
}
export function extractChatGPT(raw, docs, today) {
  const input = JSON.parse(raw);
  if (!Array.isArray(input.models) || !input.models.length || input.models.some(row => !["list", "hide"].includes(row.visibility) || !Array.isArray(row.available_in_plans))) fail("Codex catalog structure");
  section(docs, "## Deprecated Codex models");
  const result = input.models.filter(model => model.visibility === "list" && Array.isArray(model.available_in_plans) && model.available_in_plans.length).sort((a, b) => a.priority - b.priority).flatMap(row => {
    if (typeof row.slug !== "string" || typeof row.display_name !== "string" || !Number.isInteger(row.priority)) fail("Codex model shape");
    const model = { native_id: row.slug, display_name: row.display_name, source_keys: ["codex", "openai-retirement"] };
    if (row.minimal_client_version) model.minimum_harness_version = row.minimal_client_version;
    const ended = retirement(docs, model, today);
    if (ended.excluded) return [];
    if (ended.retirement_date) model.retirement_date = ended.retirement_date;
    return [model];
  });
  return ordered(result);
}
const ordered = rows => rows.map((row, order) => ({ ...row, order }));
export function extractClaude(config, overview) {
  const aliases = section(config, "### Model aliases");
  const table = section(overview, "## Compare models");
  const cells = label => {
    const row = table.split("\n").find(line => line.startsWith("|") && line.split("|")[1].trim() === label);
    if (!row) fail("Claude table structure");
    return row.split("|").slice(2, -1).map(cell => cell.trim());
  };
  const names = cells("Feature"), ids = cells("Claude API ID");
  if (names.length !== ids.length || names.length < 3) fail("Claude table columns");
  const result = names.map((name, i) => {
    const family = name.match(/^Claude (\w+) \d+(?:\.\d+)*$/)?.[1]?.toLowerCase();
    if (!family || !aliases.includes(`**\`${family}\`**`)) fail("API-only Claude family");
    const id = ids[i].match(/^`(claude-[a-z0-9-]+)`$/)?.[1];
    if (!id || !id.startsWith(`claude-${family}-`)) fail("Claude exact ID");
    const version = name.slice("Claude ".length).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    const minimum = config.match(new RegExp(`${version} requires (?:Claude Code )?v(\\d+\\.\\d+\\.\\d+) or later`))?.[1];
    return { native_id: id, display_name: name, source_keys: ["claude-code", "claude-models"], ...(minimum ? { minimum_harness_version: minimum } : {}) };
  });
  // Only explicit native selection instructions qualify; arbitrary examples and
  // the API overview's legacy inventory do not become subscription suggestions.
  for (const match of config.matchAll(/^\* \*\*(Fable \d+(?:\.\d+)*)\*\*: select it by model ID\..*?`\/model (claude-[a-z0-9-]+)`/gm)) {
    if (!result.some(row => row.native_id === match[2])) result.push({ native_id: match[2], display_name: `Claude ${match[1]}`, source_keys: ["claude-code"] });
  }
  return ordered(result);
}
export function extractGrok(markdown) {
  const match = markdown.match(/\[models\]\s*\n\s*default\s*=\s*"([a-z0-9-]+)"\s*# recommended for coding/);
  if (!match) fail("Grok native default structure");
  return ordered([{ native_id: match[1], display_name: "Grok Build", source_keys: ["grok-build"] }]);
}
export function buildCatalog(inputs, sources, today = new Date().toISOString().slice(0, 10)) {
  const entries = [extractChatGPT(inputs.codex, inputs.openai, today), extractClaude(inputs.claude, inputs.overview), extractGrok(inputs.grok)];
  const inventory = services.map((service, i) => ({ service, models: entries[i] }));
  return validateCatalog({ schema_version: 1, catalog_version: `sha256:${digest(canonical(inventory))}`, updated_at: today, sources, services: inventory });
}
export function reconcile(previous, next) {
  validateCatalog(previous); validateCatalog(next);
  // A successful daily read alone does not change the reviewed catalog date.
  return JSON.stringify(previous.services) === JSON.stringify(next.services) ? previous : next;
}
export function changes(previous, next) {
  const rows = catalog => new Map(catalog.services.flatMap(entry => entry.models.map(row => [`${entry.service}/${row.native_id}`, row])));
  const before = rows(previous), after = rows(next);
  return {
    added: [...after.keys()].filter(key => !before.has(key)),
    changed: [...after.keys()].filter(key => before.has(key) && JSON.stringify(before.get(key)) !== JSON.stringify(after.get(key))),
    retired: [...before.keys()].filter(key => !after.has(key)),
  };
}
async function fetchText(url) {
  const response = await fetch(url, { redirect: "error", signal: AbortSignal.timeout(15000) });
  if (!response.ok) fail("official source unavailable");
  const reader = response.body.getReader(); const chunks = []; let size = 0;
  try {
    while (true) { const { done, value } = await reader.read(); if (done) break; size += value.length; if (size > 1024 * 1024) fail("official source exceeds limit"); chunks.push(Buffer.from(value)); }
  } finally { await reader.cancel(); }
  return Buffer.concat(chunks).toString("utf8");
}
export async function collect() {
  // GitHub reads use gh; the immutable revision binds the upstream JSON bytes.
  const sha = execFileSync("gh", ["api", "repos/openai/codex/commits/main", "--jq", ".sha"], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  if (!/^[a-f0-9]{40}$/.test(sha)) fail("Codex revision");
  const definitions = [
    ["codex", `https://github.com/openai/codex/blob/${sha}/codex-rs/models-manager/models.json`, sha],
    ["openai", "https://learn.chatgpt.com/docs/models.md", "content-digest"],
    ["claude", "https://code.claude.com/docs/en/model-config.md", "content-digest"],
    ["overview", "https://platform.claude.com/docs/en/models/overview.md", "content-digest"],
    ["grok", "https://docs.x.ai/build/settings.md", "content-digest"],
  ];
  const inputs = {}; const sources = [];
  for (const [key, url, revision] of definitions) {
    const raw = key === "codex" ? execFileSync("gh", ["api", `repos/openai/codex/contents/codex-rs/models-manager/models.json?ref=${sha}`, "-H", "Accept: application/vnd.github.raw+json"], { encoding: "utf8", maxBuffer: 1024 * 1024, stdio: ["ignore", "pipe", "pipe"] }) : await fetchText(url);
    inputs[key] = raw;
    sources.push({ key: { openai: "openai-retirement", claude: "claude-code", overview: "claude-models", grok: "grok-build" }[key] ?? key, url, revision, sha256: digest(raw) });
  }
  return buildCatalog(inputs, sources);
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const previous = validateCatalog(JSON.parse(await readFile(catalogPath, "utf8")));
    if (process.argv.includes("--validate")) console.log(`Known catalog valid: ${previous.catalog_version}`);
    else {
      const next = reconcile(previous, await collect());
      await writeFile(catalogPath, `${JSON.stringify(next, null, 2)}\n`);
      console.log(JSON.stringify({ catalog_version: next.catalog_version, ...changes(previous, next), validation: "passed", limitation: "Official metadata only; no subscription account or native execution verification." }));
    }
  } catch { console.error("Known subscription model collection failed; retained catalog must not be published."); process.exitCode = 1; }
}
