import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
test("static production provides app, docs and privacy headers", async () => {
  const app = await readFile(new URL("../dist/index.html", import.meta.url), "utf8");
  const docs = await readFile(new URL("../dist/docs/index.html", import.meta.url), "utf8");
  const headers = await readFile(new URL("../dist/_headers", import.meta.url), "utf8");
  assert.match(app, /<html lang="en">/);
  assert.match(docs, /Final validation/);
  assert.match(headers, /frame-ancestors 'none'/);
  assert.doesNotMatch(headers, /unsafe-inline|unsafe-eval/);
});

test("published installation and upgrade commands follow release defaults", async () => {
  const docs = await readFile(new URL("../dist/docs/index.html", import.meta.url), "utf8");
  assert.match(docs, /^sh install-ach\.sh$/m);
  assert.match(docs, /^\.\/install-ach\.ps1<\/pre/m);
  assert.match(docs, /^ach self-update$/m);
  assert.doesNotMatch(docs, /^(?:sh install-ach\.sh --version|\.\/install-ach\.ps1 -Version|ach self-update --version) \d/m);
  for (const command of [
    "sh install-ach.sh --version MAJOR.MINOR.PATCH",
    "./install-ach.ps1 -Version MAJOR.MINOR.PATCH",
    "ach self-update --version MAJOR.MINOR.PATCH",
  ]) assert.ok(docs.includes(`<code>${command}</code>`), `Missing explicit version guidance: ${command}`);
});
