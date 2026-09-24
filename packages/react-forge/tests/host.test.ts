import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { readFile } from "node:fs/promises";
import test from "node:test";
const exec = promisify(execFile);

test("workspace metadata stays host-neutral while native loading rejects unsupported hosts", async () => {
  const manifest = JSON.parse(await readFile(new URL("../package.json", import.meta.url), "utf8"));
  assert.equal(manifest.os, undefined, "pnpm must not prepend workspace platform warnings to protocol stdout");
  assert.equal(manifest.cpu, undefined);
  const native = new URL("../dist/native.js", import.meta.url).href;
  for (const [platform, arch] of [["linux", "x64"], ["win32", "arm64"], ["darwin", "x64"]]) {
    const script = `
      Object.defineProperty(process, "platform", { value: ${JSON.stringify(platform)} });
      Object.defineProperty(process, "arch", { value: ${JSON.stringify(arch)} });
      const { processDocument } = await import(${JSON.stringify(native)});
      try { await processDocument("pptx", "generate", {}, Buffer.alloc(0), new Map(), "unused", 0, new AbortController().signal); }
      catch (error) { console.log(JSON.stringify(error)); }
    `;
    const { stdout, stderr } = await exec(process.execPath, ["--input-type=module", "--eval", script]);
    assert.equal(JSON.parse(stdout).code, "unsupported_package");
    assert.equal(stderr, "");
  }
});
