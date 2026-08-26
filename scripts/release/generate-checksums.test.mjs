import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const script = fileURLToPath(new URL("./generate-checksums.sh", import.meta.url));

function run(artifacts, sigstore) {
  execFileSync("bash", [script, "--artifacts-dir", artifacts, "--sigstore-dir", sigstore], {
    env: { ...process.env, REQUIRE_COSIGN: "0" },
    stdio: "pipe",
  });
}

test("checksums are recursive, sorted, deterministic, and preserve non-Sigstore signatures", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-checksums-"));
  const artifacts = join(root, "artifacts");
  const sigstore = join(root, "sigstore");
  mkdirSync(join(artifacts, "updater/signatures"), { recursive: true });
  writeFileSync(join(artifacts, "z.bin"), "z");
  writeFileSync(join(artifacts, "a.bin"), "a");
  writeFileSync(join(artifacts, "updater/signatures/update.sig"), "updater-signature");
  writeFileSync(join(artifacts, "platform.pem"), "platform-certificate");

  run(artifacts, sigstore);
  const first = readFileSync(join(artifacts, "SHA256SUMS"), "utf8");
  run(artifacts, sigstore);
  const second = readFileSync(join(artifacts, "SHA256SUMS"), "utf8");

  assert.equal(first, second);
  const paths = first.trim().split("\n").map((line) => line.slice(66));
  assert.deepEqual(paths, ["a.bin", "platform.pem", "updater/signatures/update.sig", "z.bin"]);
  assert.equal(readFileSync(join(artifacts, "platform.pem"), "utf8"), "platform-certificate");
  assert.equal(readFileSync(join(artifacts, "updater/signatures/update.sig"), "utf8"), "updater-signature");
});

test("a nested Sigstore destination is never included in SHA256SUMS", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-checksums-nested-"));
  const artifacts = join(root, "artifacts");
  const sigstore = join(artifacts, "sigstore");
  mkdirSync(sigstore, { recursive: true });
  writeFileSync(join(artifacts, "artifact.bin"), "artifact");
  writeFileSync(join(sigstore, "old.bundle"), "old");
  run(artifacts, sigstore);
  assert.match(readFileSync(join(artifacts, "SHA256SUMS"), "utf8"), /  artifact\.bin\n$/u);
});
