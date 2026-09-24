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
  for (const [platform, arch] of [["freebsd", "x64"], ["win32", "ia32"], ["darwin", "riscv64"]]) {
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


test("capabilities enumerate the six native targets and the matching artifact loads", async () => {
  const { capabilities, createSession, Format } = await import("../src/index.js");
  assert.deepEqual(capabilities.runtime.hosts.map(h => h.id), [
    "darwin-x64", "darwin-arm64", "linux-x64-gnu", "linux-arm64-gnu", "win32-x64-msvc", "win32-arm64-msvc",
  ]);
  assert.ok(capabilities.runtime.hosts.some(h => h.platform === process.platform && h.architecture === process.arch));
  const session = createSession(Format.Pptx);
  try {
    // Empty models reach native validation, proving a host-matching addon loaded.
    const { processPptx } = await import("../src/native.js");
    await assert.rejects(processPptx("generate", {}, Buffer.alloc(0), new Map(), session.documentId, 0, new AbortController().signal), { code: "malformed_input" });
  } finally { await session.dispose(); }
});
