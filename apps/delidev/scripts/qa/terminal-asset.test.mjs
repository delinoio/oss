// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createHost } from "./host.mjs";

test("fixture serves same-origin WASM with a narrow script policy and external styles", async () => {
  const assets = await realpath(await mkdtemp(join(tmpdir(), "delidev-terminal-asset-")));
  const binary = new Uint8Array([0, 97, 115, 109, 1, 0, 0, 0]);
  await writeFile(join(assets, "ghostty.fixture.wasm"), binary);
  const host = await createHost(assets, { endpoint: "http://127.0.0.1:12345" });
  try {
    const response = await fetch(`${host.origin}/ghostty.fixture.wasm`);
    assert.equal(response.status, 200); assert.equal(response.headers.get("content-type"), "application/wasm");
    assert.deepEqual(new Uint8Array(await response.arrayBuffer()), binary);
    const policy = response.headers.get("content-security-policy");
    assert.match(policy, /script-src 'self' 'wasm-unsafe-eval';/);
    assert.match(policy, /style-src 'self';/);
    assert.match(policy, /connect-src 'self' http:\/\/127\.0\.0\.1:12345;/);
    assert(!policy.includes("'unsafe-eval'") && !policy.includes("'unsafe-inline'"));
  } finally { await host.close(); await rm(assets, { recursive: true }); }
});
