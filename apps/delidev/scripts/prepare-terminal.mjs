// SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";
import { readFile, mkdir, writeFile, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
const app = fileURLToPath(new URL("..", import.meta.url));
const revision = "cc067a20a616fe62f0893ec94005943c349bb492";
const digest = bytes => createHash("sha256").update(bytes).digest("hex");
const checked = (command, args, options = {}) => { const result = spawnSync(command, args, { stdio: "inherit", ...options }); if (result.error || result.status !== 0) throw new Error("Pinned terminal preparation failed"); };
const packageWasm = fileURLToPath(import.meta.resolve("@wterm/ghostty/ghostty-vt.wasm"));
let bytes = await readFile(packageWasm);
if (digest(bytes) !== "da382d54a9d1115e994802c90411e754fc1f7d090e3a28b34b465b187f41be6d") throw new Error("Pinned terminal WASM digest mismatch");
if (process.argv.includes("--source-build")) {
  if (process.platform !== "linux") throw new Error("Pinned source verification requires the Linux CI job");
  const temporary = await mkdtemp(join(tmpdir(), "delidev-terminal-source-"));
  try {
    const response = await fetch(`https://api.github.com/repos/vercel-labs/wterm/tarball/${revision}`);
    if (!response.ok) throw new Error("Pinned terminal source unavailable");
    const archive = Buffer.from(await response.arrayBuffer());
    if (archive.length > 16 * 1024 * 1024 || digest(archive) !== "f018ce1c050a79d955ed540dc89d900ee0ce9abb6b92c3eb7716e7ab93798a11") throw new Error("Pinned terminal source digest mismatch");
    const tarball = join(temporary, "source.tgz"), source = join(temporary, "source");
    await writeFile(tarball, archive); await mkdir(source);
    checked("tar", ["-xzf", tarball, "--strip-components=1", "-C", source]);
    const scripts = join(source, "packages/@wterm/ghostty/scripts"), zig = join(temporary, "zig");
    checked("bash", [join(scripts, "install-zig.sh"), zig]);
    checked("bash", [join(scripts, "build-wasm.sh")], { env: { ...process.env, WTERM_GHOSTTY_ZIG: join(zig, "zig") } });
    bytes = await readFile(join(source, "packages/@wterm/ghostty/wasm/ghostty-vt.wasm"));
    if (digest(bytes) !== "da382d54a9d1115e994802c90411e754fc1f7d090e3a28b34b465b187f41be6d") throw new Error("Pinned terminal source output mismatch");
  } finally { await rm(temporary, { recursive: true, force: true }); }
}
const output = join(app, ".terminal-assets/ghostty-vt.wasm");
await mkdir(dirname(output), { recursive: true }); await writeFile(output, bytes);
console.log(JSON.stringify({ operation: "terminal-prepare", revision, sourceBuild: process.argv.includes("--source-build"), sha256: digest(bytes) }));
