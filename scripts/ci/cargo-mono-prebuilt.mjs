import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { appendFileSync, chmodSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { unpackArchive } from "../archive.mjs";

// This first-party CLI release is independent of the Tauri producer recipe lock.
// Updating it requires verifying the public archive, executable and CLI fixtures.
export const cargoMonoRelease = Object.freeze({
  version: "0.6.9",
  revision: "46d60acb5554d5a877115dadca16cbb1836078c2",
  url: "https://github.com/delinoio/oss/releases/download/cargo-mono%40v0.6.9/cargo-mono-linux-amd64.tar.gz",
  size: 2075297,
  sha256: "4e2ce89c83aa04a9bd3ca038c9790df03f69ca4ad52171d176c666ac2f20be96",
  binarySize: 4328112,
  binarySha256: "6f42b8138f6d93c2a01d16299f8947570c1806fa332a4bd06b8ea265d191b439",
  verificationOutput: "cargo mono 0.6.9",
});
const hash = bytes => createHash("sha256").update(bytes).digest("hex");

export async function prepareCargoMono({
  platform = process.platform, arch = process.arch, directory = tmpdir(),
  fetchAsset = globalThis.fetch, release = cargoMonoRelease,
  run = (file, args) => execFileSync(file, args, { encoding: "utf8", maxBuffer: 16 * 1024, stdio: ["ignore", "pipe", "inherit"] }).trim(),
} = {}) {
  if (platform !== "linux" || arch !== "x64") throw new Error(`Unsupported CI cargo-mono host: ${platform}/${arch}`);
  const root = mkdtempSync(join(directory, "ci-cargo-mono-"));
  try {
    const response = await fetchAsset(release.url, { signal: AbortSignal.timeout(120_000) });
    if (!response.ok) throw new Error(`cargo-mono download failed: HTTP ${response.status}`);
    const declared = response.headers.get("content-length");
    if (declared !== null && Number(declared) !== release.size) throw new Error("cargo-mono archive size mismatch");
    if (!response.body) throw new Error("cargo-mono download has no body");
    const chunks = [];
    let length = 0;
    for await (const chunk of response.body) {
      length += chunk.length;
      if (length > release.size) throw new Error("cargo-mono archive exceeds its pinned size");
      chunks.push(Buffer.from(chunk));
    }
    const bytes = Buffer.concat(chunks);
    if (length !== release.size || hash(bytes) !== release.sha256) throw new Error("cargo-mono archive SHA-256 or size mismatch");
    const entries = unpackArchive(bytes);
    if (entries.length !== 1 || entries[0].name !== "cargo-mono" || entries[0].data.length !== release.binarySize || hash(entries[0].data) !== release.binarySha256) throw new Error("cargo-mono executable SHA-256 or archive layout mismatch");
    const executable = join(root, "cargo-mono");
    writeFileSync(executable, entries[0].data, { flag: "wx", mode: 0o700 });
    chmodSync(executable, 0o700);
    if (run(executable, ["--version"]) !== release.verificationOutput) throw new Error("cargo-mono executable version mismatch");
    console.log(JSON.stringify({ event: "ci_cargo_mono", version: release.version, sourceRevision: release.revision, archiveSha256: release.sha256, binarySha256: release.binarySha256, result: "verified" }));
    return { root, executable };
  } catch (error) {
    rmSync(root, { recursive: true, force: true });
    throw error;
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const installation = await prepareCargoMono({ directory: process.env.RUNNER_TEMP ?? tmpdir() });
  appendFileSync(process.env.GITHUB_OUTPUT, `binary=${installation.executable}\n`);
}
