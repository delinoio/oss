import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { ExportUsageReason } from "../../../crates/bundlewhy/adapters/common.mjs";

const repository = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../../..",
);

function read(relative) {
  return fs.readFileSync(path.join(repository, relative), "utf8");
}

const contract = read("docs/crates-bundlewhy-foundation.md");

test("the contract tables list every adapter reason code", () => {
  const rows = contract.split("\n").filter((line) => line.startsWith("|"));
  for (const code of Object.values(ExportUsageReason)) {
    assert.ok(
      rows.filter((row) => row.includes(`\`${code}\``)).length >= 2,
      `${code} must appear in the adapter outcome table and the note table`,
    );
  }
});

// Provenance: Rspack reports edges; bundlewhy derives chains and their order.
const provenanceSurfaces = [
  "docs/crates-bundlewhy-foundation.md",
  "docs/apps-bundlewhy-web-contract.md",
  "docs/project-bundlewhy.md",
  "docs/protos-bundlewhy-v1-contract.md",
  "apps/public-docs/docs/bundlewhy/index.md",
  "apps/public-docs/docs/bundlewhy/interpreting-diagnostics.md",
  "apps/public-docs/docs/bundlewhy/limitations.md",
  "apps/public-docs/docs/bundlewhy/supported-engines.md",
  "apps/bundlewhy-web/src/ExportUsageSection.tsx",
  "crates/bundlewhy/src/rules/export_usage.rs",
];

test("no surface says that Rspack reports paths or chains", () => {
  const forbidden =
    /\b(paths?|chains?)\s+(that|which)\s+(Rspack|the engine)\s+(reports?|reported|provides?|provided|found)\b/iu;
  const reversed =
    /\b(Rspack|the engine)\s+(reports?|reported|provides?|provided)\s+(the\s+)?(export usage )?(paths?|chains?)\b/iu;
  for (const surface of provenanceSurfaces) {
    const text = read(surface);
    assert.ok(!forbidden.test(text), `${surface}: ${forbidden.exec(text)}`);
    assert.ok(!reversed.test(text), `${surface}: ${reversed.exec(text)}`);
  }
});

test("the public overview attributes export usage paths to bundlewhy", () => {
  const overview = read("apps/public-docs/docs/bundlewhy/index.md");
  const line = overview
    .split("\n")
    .find((entry) => entry.includes("export usage paths"));
  assert.ok(line, "the overview mentions export usage paths");
  assert.match(line, /bundlewhy also derives/u);
  assert.match(line, /Rspack reports/u);
});

test("the detailed guide and the UI keep the same two tags", () => {
  const guide = read(
    "apps/public-docs/docs/bundlewhy/interpreting-diagnostics.md",
  );
  const ui = read("apps/bundlewhy-web/src/ExportUsageSection.tsx");
  for (const text of [guide, ui, contract]) {
    assert.ok(text.includes("Observed by Rspack"));
    assert.ok(text.includes("Derived by bundlewhy"));
  }
});

test("the chain end table matches the UI text and lists every end", () => {
  const lines = contract.split("\n");
  const start = lines.findIndex((line) => line.startsWith("Chain end. "));
  assert.ok(start >= 0, "the contract has a chain end table");
  const rows = [];
  for (const line of lines.slice(start + 1)) {
    if (line.startsWith("|")) rows.push(line);
    else if (rows.length > 0) break;
  }
  const cells = rows.slice(2).map((row) =>
    row
      .split("|")
      .slice(1, -1)
      .map((cell) => cell.trim()),
  );
  const ui = read("apps/bundlewhy-web/src/ExportUsageSection.tsx");
  const ends = new Set();
  for (const [, end, , text] of cells) {
    if (end === "none yet") continue;
    ends.add(end);
    assert.ok(ui.includes(text), `the UI has no text: ${text}`);
  }
  assert.deepEqual([...ends].sort(), [
    "`CYCLE`",
    "`INCOMPLETE`",
    "`ROOT`",
    "`TRUNCATED`",
  ]);
  // Every end of the protocol is described in the protocol contract.
  const protocol = read("docs/protos-bundlewhy-v1-contract.md");
  for (const end of ["ROOT", "CYCLE", "TRUNCATED", "INCOMPLETE"]) {
    assert.ok(protocol.includes(`\`${end}\``), end);
  }
});
