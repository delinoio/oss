import { test } from "node:test";
import assert from "node:assert/strict";
import { validateBundle } from "./embed.mjs";
test("local production bundle contains only self-contained UI assets", async () => {
  const names = await validateBundle();
  assert.ok(!names.some((name) => /docs|install|_headers|_redirects/.test(name)));
});
