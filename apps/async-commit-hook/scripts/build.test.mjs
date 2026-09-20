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
