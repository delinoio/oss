// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { gzipSync, brotliCompressSync, constants } from "node:zlib";
// Vite appends an 8-character content hash to chunk names. Content changes
// alter the hash, so asset sets are compared by name without it.
const CONTENT_HASH = /-[A-Za-z0-9_-]{8}(?=\.(?:js|mjs|cjs)$)/;
const logical = (file) => file.replace(CONTENT_HASH, "");
function assetSetDifference(from, to) {
  const counts = new Map();
  for (const file of from.files)
    counts.set(logical(file.path), (counts.get(logical(file.path)) ?? 0) + 1);
  for (const file of to.files)
    counts.set(logical(file.path), (counts.get(logical(file.path)) ?? 0) - 1);
  const removed = [];
  const added = [];
  for (const [name, count] of [...counts].sort(([a], [b]) =>
    a.localeCompare(b, "en"),
  )) {
    for (let i = 0; i < count; i++) removed.push(name);
    for (let i = 0; i < -count; i++) added.push(name);
  }
  return { removed, added };
}
async function measure(root) {
  const result = { files: [], raw: 0, gzip: 0, brotli: 0, texts: new Map() };
  async function walk(directory) {
    for (const entry of (
      await readdir(directory, { withFileTypes: true })
    ).sort((a, b) => a.name.localeCompare(b.name, "en"))) {
      const full = path.join(directory, entry.name);
      if (entry.isDirectory()) await walk(full);
      else if (entry.isFile() && /\.(?:js|mjs|cjs|html)$/.test(entry.name)) {
        const content = await readFile(full);
        const relative = path.relative(root, full).split(path.sep).join("/");
        result.texts.set(relative, content.toString("utf8"));
        if (entry.name.endsWith(".html")) continue;
        const file = {
          path: relative,
          raw: content.length,
          gzip: gzipSync(content, { level: 9 }).length,
          brotli: brotliCompressSync(content, {
            params: { [constants.BROTLI_PARAM_QUALITY]: 11 },
          }).length,
        };
        result.files.push(file);
        for (const key of ["raw", "gzip", "brotli"]) result[key] += file[key];
      }
    }
  }
  await walk(root);
  if (!result.files.length)
    throw new Error("No production JavaScript assets found");
  return result;
}
// Loading references are the local module edges a browser follows: module
// scripts and modulepreload links in HTML, and static `import`/`export ...
// from` specifiers and literal `import()` arguments (quoted strings or
// templates without substitutions) in JavaScript. Each one
// must resolve to a file of its own build, whether or not any other build
// emits an asset with the same logical name. Other quoted `.js` strings,
// remote URLs and bare specifiers are not loading references.
const SCRIPT_FILE = /\.(?:js|mjs|cjs)$/;
const URL_SCHEME = /^(?:[A-Za-z][A-Za-z0-9+.-]*:|\/\/)/;
function isIdentifierCharacter(character) {
  return character !== undefined && /[\w$]/.test(character);
}
const SIMPLE_ESCAPES = { n: "\n", t: "\t", r: "\r", b: "\b", f: "\f", v: "\v" };
// Decode the escape sequence that starts at the backslash `text[at]`. Returns
// the runtime characters and the index after the sequence. A line
// continuation contributes nothing; a malformed escape keeps its character.
function readEscape(text, at) {
  const next = text[at + 1];
  if (next === undefined) return ["", at + 1];
  if (next in SIMPLE_ESCAPES) return [SIMPLE_ESCAPES[next], at + 2];
  if (next === "\n" || next === "\u2028" || next === "\u2029")
    return ["", at + 2];
  if (next === "\r") return ["", text[at + 2] === "\n" ? at + 3 : at + 2];
  const code = (digits, end) =>
    /^[0-9A-Fa-f]+$/.test(digits) && Number.parseInt(digits, 16) <= 0x10ffff
      ? [String.fromCodePoint(Number.parseInt(digits, 16)), end]
      : null;
  if (next === "x") {
    const decoded = code(text.slice(at + 2, at + 4), at + 4);
    if (decoded && text.length >= at + 4) return decoded;
  } else if (next === "u") {
    if (text[at + 2] === "{") {
      const close = text.indexOf("}", at + 3);
      const decoded =
        close === -1 ? null : code(text.slice(at + 3, close), close + 1);
      if (decoded) return decoded;
    } else {
      const decoded = code(text.slice(at + 2, at + 6), at + 6);
      if (decoded && text.length >= at + 6) return decoded;
    }
  } else if (/[0-7]/.test(next)) {
    // Legacy octal escapes take up to three digits, at most \377.
    const limit = next <= "3" ? 3 : 2;
    let end = at + 1;
    while (end < at + 1 + limit && /[0-7]/.test(text[end] ?? "")) end++;
    return [
      String.fromCharCode(Number.parseInt(text.slice(at + 1, end), 8)),
      end,
    ];
  }
  return [next, at + 2];
}
// Scan JavaScript for module specifiers. Comments, string literals and
// template literals are skipped so quoted text never counts as an import.
function scanModuleSpecifiers(text) {
  const specifiers = [];
  // The two most recent significant tokens, most recent last.
  let previous = ["", ""];
  const remember = (token) => {
    previous = [previous[1], token];
  };
  let i = 0;
  while (i < text.length) {
    const character = text[i];
    if (/\s/.test(character)) {
      i++;
    } else if (character === "/" && text[i + 1] === "/") {
      while (i < text.length && text[i] !== "\n") i++;
    } else if (character === "/" && text[i + 1] === "*") {
      const end = text.indexOf("*/", i + 2);
      i = end === -1 ? text.length : end + 2;
    } else if (character === '"' || character === "'") {
      let value = "";
      i++;
      while (i < text.length && text[i] !== character && text[i] !== "\n") {
        if (text[i] === "\\") {
          const [decoded, end] = readEscape(text, i);
          value += decoded;
          i = end;
        } else value += text[i++];
      }
      i++;
      const [before, last] = previous;
      if (last === "from" || last === "import") specifiers.push(value);
      else if (last === "(" && before === "import") specifiers.push(value);
      remember('"');
    } else if (character === "`") {
      // Skip the template, including nested `${...}` expressions. A template
      // without substitutions is a literal, so keep its text for `import()`.
      let depth = 0;
      let substituted = false;
      let value = "";
      i++;
      while (i < text.length && !(text[i] === "`" && depth === 0)) {
        if (text[i] === "\\") {
          const [decoded, end] = readEscape(text, i);
          if (depth === 0) value += decoded;
          i = end;
          continue;
        } else if (text[i] === "$" && text[i + 1] === "{") {
          depth++;
          substituted = true;
          i++;
        } else if (text[i] === "}" && depth > 0) depth--;
        else if (depth === 0) value += text[i];
        i++;
      }
      i++;
      const [before, last] = previous;
      if (!substituted && last === "(" && before === "import")
        specifiers.push(value);
      remember("`");
    } else if (isIdentifierCharacter(character)) {
      let end = i;
      while (isIdentifierCharacter(text[end])) end++;
      // A property access such as `module.import(` is not a module edge.
      const word =
        text[i - 1] === "." ? "." + text.slice(i, end) : text.slice(i, end);
      remember(word);
      i = end;
    } else {
      remember(character);
      i++;
    }
  }
  return specifiers;
}
function htmlAttributes(tag) {
  const attributes = new Map();
  for (const match of tag.matchAll(
    /([^\s"'<>\/=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?/g,
  ))
    attributes.set(
      match[1].toLowerCase(),
      match[2] ?? match[3] ?? match[4] ?? "",
    );
  return attributes;
}
// Loading references of a file, in order, with inline module scripts
// included. HTML attributes hold URLs, which resolve against the document
// even without a `./` prefix; JavaScript specifiers without a prefix are bare.
function loadingReferences(file, text) {
  const asSpecifiers = (list) =>
    list.map((specifier) => ({ specifier, url: false }));
  if (!file.endsWith(".html")) return asSpecifiers(scanModuleSpecifiers(text));
  const references = [];
  const markup = text.replace(/<!--[\s\S]*?-->/g, "");
  for (const match of markup.matchAll(
    /<script\b([^>]*)>([\s\S]*?)<\/script\s*>|<link\b([^>]*)>/gi,
  )) {
    if (match[3] !== undefined) {
      const attributes = htmlAttributes(match[3]);
      const relations = (attributes.get("rel") ?? "")
        .toLowerCase()
        .split(/\s+/);
      if (relations.includes("modulepreload") && attributes.has("href"))
        references.push({ specifier: attributes.get("href"), url: true });
      continue;
    }
    const attributes = htmlAttributes(match[1]);
    if ((attributes.get("type") ?? "").toLowerCase() !== "module") continue;
    if (attributes.has("src"))
      references.push({ specifier: attributes.get("src"), url: true });
    else references.push(...asSpecifiers(scanModuleSpecifiers(match[2])));
  }
  return references;
}
// Resolve a reference against the importing file. Returns null for
// references that are not local script files (remote URLs, bare specifiers,
// non-script assets). A URL (HTML attribute) never is a bare specifier.
function localTarget(file, specifier, url) {
  const bare = specifier.trim().split(/[?#]/)[0];
  if (!bare || URL_SCHEME.test(bare)) return null;
  if (
    !url &&
    !bare.startsWith("/") &&
    !bare.startsWith("./") &&
    !bare.startsWith("../")
  )
    return null;
  if (!SCRIPT_FILE.test(bare)) return null;
  return bare.startsWith("/")
    ? path.posix.normalize(bare.slice(1))
    : path.posix.normalize(path.posix.join(path.posix.dirname(file), bare));
}
function brokenReferences(build) {
  const present = new Set(build.files.map((f) => f.path));
  const broken = new Set();
  for (const [file, text] of build.texts)
    for (const { specifier, url } of loadingReferences(file, text)) {
      const target = localTarget(file, specifier, url);
      if (target !== null && !present.has(target))
        broken.add(`${file} -> ${specifier}`);
    }
  return [...broken].sort();
}
const usage =
  "Usage: node measure.mjs <original-build> <optimized-build> | <original-build> <existing-typeopt-build> <extended-typeopt-build>";
const roots = process.argv.slice(2);
if (roots.length !== 2 && roots.length !== 3) throw new Error(usage);
// Size totals are a valid comparison only when all builds come from the
// same verified source and build settings.
const PROVENANCE = [
  "schema_version",
  "revision",
  "source_tree",
  "yarn_lock_sha256",
  "node",
  "vite",
  "mode",
  "sourcemap",
  "environment",
];
async function provenance(root) {
  try {
    return JSON.parse(
      await readFile(path.join(root, "typeopt-build.json"), "utf8"),
    );
  } catch (error) {
    throw new Error(
      `${root} lacks typeopt-build.json; build it with benchmark/build.mjs`,
      { cause: error },
    );
  }
}
// gzip and Brotli ship with Node, so the pinned Node fixes the compressors.
const pin = JSON.parse(
  await readFile(new URL("./pin.json", import.meta.url), "utf8"),
);
if (process.versions.node !== pin.node)
  throw new Error(`Measure with pinned Node ${pin.node}`);
const builds = await Promise.all(roots.map(provenance));
const names =
  roots.length === 2
    ? ["original", "optimized"]
    : ["original", "existing", "extended"];
const mismatches = [];
for (let i = 1; i < builds.length; i++)
  for (const key of PROVENANCE)
    if (JSON.stringify(builds[0][key]) !== JSON.stringify(builds[i][key]))
      mismatches.push(`${names[i]}.${key}`);
if (builds[0].optimized !== false) mismatches.push("original.optimized");
for (let i = 1; i < builds.length; i++)
  if (builds[i].optimized !== true) mismatches.push(`${names[i]}.optimized`);
for (let i = 1; i < builds.length; i++) {
  // The optimizer binary must come from a recorded, clean source revision.
  const optimizer = builds[i].optimizer;
  if (!/^[0-9a-f]{40}$/.test(optimizer?.source_revision ?? ""))
    mismatches.push(`${names[i]}.optimizer.source_revision`);
  if (optimizer?.source_dirty !== false)
    mismatches.push(`${names[i]}.optimizer.source_dirty`);
}
if (builds.length === 3) {
  // The existing optimizer is the pinned revision and writes report v0; the
  // extended one writes v1.
  if (builds[1].optimizer?.report_schema_version !== 0)
    mismatches.push("existing.optimizer");
  if (builds[1].optimizer?.source_revision !== pin.existing_typeopt_revision)
    mismatches.push("existing.optimizer.revision");
  if (!(builds[2].optimizer?.report_schema_version >= 1))
    mismatches.push("extended.optimizer");
  if (builds[2].optimizer?.source_revision === pin.existing_typeopt_revision)
    mismatches.push("extended.optimizer.revision");
}
const measured = await Promise.all(roots.map(measure));
const broken = Object.fromEntries(
  measured.map((m, i) => [names[i], brokenReferences(m)]),
);
const smaller = (a, b) =>
  ["raw", "gzip", "brotli"].every((key) => a[key] < b[key]);
const last = measured.at(-1);
const comparisons = measured.slice(0, -1).map((baseline, i) => ({
  baseline: names[i],
  ...assetSetDifference(baseline, last),
  strictly_smaller: smaller(last, baseline),
}));
const loadingOk = Object.values(broken).every((list) => !list.length);
const passed =
  !mismatches.length &&
  loadingOk &&
  comparisons.every((c) => c.strictly_smaller);
const summary = (m) => ({
  raw: m.raw,
  gzip: m.gzip,
  brotli: m.brotli,
  files: m.files,
});
console.log(
  JSON.stringify(
    {
      schema_version: 1,
      node: process.version,
      zlib: process.versions.zlib,
      brotli: process.versions.brotli,
      revision: builds[0].revision,
      source_tree: builds[0].source_tree,
      optimizers: Object.fromEntries(
        builds.map((b, i) => [names[i], b.optimizer ?? null]),
      ),
      provenance_mismatches: mismatches,
      builds: Object.fromEntries(
        measured.map((m, i) => [names[i], summary(m)]),
      ),
      // Removed chunks are legitimate when nothing still loads them.
      comparisons,
      broken_chunk_references: broken,
      passed,
    },
    null,
    2,
  ),
);
if (!passed) process.exitCode = 1;
