import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const script = fileURLToPath(new URL("./generate-checksums.sh", import.meta.url));

function run(artifacts, sigstore) {
  const bin = mkdtempSync(join(tmpdir(), "checksum-signing-stub-"));
  writeFileSync(join(bin, "cosign"), '#!/bin/sh\nwhile [ "$#" -gt 1 ]; do shift; done\ntest -f "$1"\n', { mode: 0o755 });
  try {
    execFileSync("bash", [script, "--artifacts-dir", artifacts, "--sigstore-dir", sigstore], {
      env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, REQUIRE_COSIGN: "1" },
      stdio: "pipe",
    });
  } finally {
    rmSync(bin, { recursive: true, force: true });
  }
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

test("checksum bytes match existing tools for spaces, backslashes, and option-like filenames", (t) => {
  const root = mkdtempSync(join(tmpdir(), "checksum-spellings-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const names = ["-leading.bin", "a file.bin", "back\\slash.bin"];
  for (const name of names) writeFileSync(join(root, name), Buffer.from([0, 255, 13, 10]));
  const [command, ...args] = process.platform === "darwin" ? ["shasum", "-a", "256"] : ["sha256sum"];
  const expected = execFileSync(command, [...args, "--", ...names], { cwd: root, encoding: "utf8" });
  run(root, join(root, "sigstore"));
  const manifest = join(root, "SHA256SUMS");
  assert.equal(readFileSync(manifest, "utf8"), expected);
  execFileSync("pnpm", ["exec", "clibox", "hash", "verify", "--check", manifest, "--quiet"]);
  writeFileSync(join(root, names[0]), "tampered");
  assert.throws(() => execFileSync("pnpm", ["exec", "clibox", "hash", "verify", "--check", manifest, "--quiet"], { stdio: "pipe" }));
});

test("a literal dash is hashed as a file and newline-bearing filenames fail before manifest replacement", (t) => {
  const root = mkdtempSync(join(tmpdir(), "checksum-dash-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writeFileSync(join(root, "-"), "abc");
  run(root, join(root, "sigstore"));
  const manifest = join(root, "SHA256SUMS");
  const expected = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad  -\n";
  assert.equal(readFileSync(manifest, "utf8"), expected);
  writeFileSync(join(root, "invalid\nname"), "fixture");
  assert.throws(() => run(root, join(root, "sigstore")), /must not contain newlines/u);
  assert.equal(readFileSync(manifest, "utf8"), expected);
});
