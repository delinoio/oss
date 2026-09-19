import assert from "node:assert/strict";
import test from "node:test";
import { routes, validatePage } from "./validate.mjs";
function page(content = "") {
  return `<html lang="en"><main><h1>Runlens</h1>${content}</main>${routes.map((route) => `<a href="${route}">Guide</a>`).join("")}<a href="https://github.com/delinoio/oss">GitHub</a></html>`;
}
test("public validator rejects broken routes and unsafe links", () => {
  assert.deepEqual(validatePage(page(), "/"), []);
  for (const link of ["/commands.html", "javascript:alert(1)", "https://user:password@example.com"]) {
    assert.ok(validatePage(page(`<a href="${link}">link</a>`), "/").length);
  }
  assert.ok(validatePage(page().replace('href="/privacy"', 'href="/missing"'), "/").length);
});
test("public docs retain user configuration and exclude repository contracts", () => {
  assert.deepEqual(validatePage(page("runlens.toml schema_version = 1; runlens run --save report.json"), "/"), []);
  assert.ok(validatePage(page("Implementation lives in crates/runlens"), "/").length);
});

test("development and preview fail at their fixed occupied ports", async () => {
  const { createServer } = await import("node:net");
  const { execFile } = await import("node:child_process");
  const { promisify } = await import("node:util");
  const { fileURLToPath } = await import("node:url");
  const invoke = promisify(execFile);
  const wrapper = fileURLToPath(new URL("../../../scripts/run-rspress-port.mjs", import.meta.url));
  for (const [command, port] of [["dev", 46310], ["preview", 46272]]) {
    const server = createServer();
    await new Promise((resolve, reject) => { server.once("error", reject); server.listen(port, "127.0.0.1", resolve); });
    try {
      await assert.rejects(invoke(process.execPath, [wrapper, "runlens-docs", command, String(port), "-"], { timeout: 5000 }), (error) => {
        assert.equal(error.code, 1);
        assert.ok(error.stderr.includes(`port ${port} is already in use`));
        return true;
      });
    } finally { await new Promise((resolve) => server.close(resolve)); }
  }
});
