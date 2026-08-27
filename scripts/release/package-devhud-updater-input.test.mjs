import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { packageUpdaterInput } from "./package-devhud-updater-input.mjs";

function fixture(root, reverse = false) {
  const files = [
    ["updater/manifests/stable/linux/x86_64/linux-deb.json", "manifest\n"],
    ["updater/signatures/stable/linux/x86_64/linux-deb.manifest.ed25519", "signature\n"],
  ];
  for (const [path, contents] of reverse ? files.toReversed() : files) {
    const destination = join(root, path);
    mkdirSync(join(destination, ".."), { recursive: true });
    writeFileSync(destination, contents);
  }
}

test("updater controller input is deterministic across source metadata", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-updater-input-test-"));
  try {
    const first = join(root, "first");
    const second = join(root, "second");
    fixture(first);
    fixture(second, true);
    const old = new Date("2001-02-03T04:05:06Z");
    const recent = new Date("2025-06-07T08:09:10Z");
    const firstManifest = join(first, "updater/manifests/stable/linux/x86_64/linux-deb.json");
    const secondManifest = join(second, "updater/manifests/stable/linux/x86_64/linux-deb.json");
    chmodSync(firstManifest, 0o600);
    chmodSync(secondManifest, 0o755);
    utimesSync(firstManifest, old, old);
    utimesSync(secondManifest, recent, recent);
    const firstArchive = join(root, "first.tar.gz");
    const secondArchive = join(root, "second.tar.gz");
    packageUpdaterInput({ artifactsDirectory: first, output: firstArchive });
    packageUpdaterInput({ artifactsDirectory: second, output: secondArchive });
    assert.deepEqual(readFileSync(firstArchive), readFileSync(secondArchive));
    assert.deepEqual([...readFileSync(firstArchive).subarray(4, 8)], [0, 0, 0, 0], "gzip timestamp must be suppressed");

    const entries = execFileSync("tar", ["-tzf", firstArchive], { encoding: "utf8" }).trim().split("\n");
    assert.deepEqual(entries, entries.toSorted());
    const verbose = execFileSync("tar", ["--numeric-owner", "-tvzf", firstArchive], { encoding: "utf8" });
    assert.match(verbose, /drwxr-xr-x 0\/0/u);
    assert.match(verbose, /-rw-r--r-- 0\/0/u);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("updater controller input rejects links", () => {
  const root = mkdtempSync(join(tmpdir(), "devhud-updater-input-link-test-"));
  try {
    fixture(root);
    symlinkSync("linux-deb.json", join(root, "updater/manifests/stable/linux/x86_64/linked.json"));
    assert.throws(() => packageUpdaterInput({ artifactsDirectory: root, output: join(root, "output.tar.gz") }), /unsupported entry type/u);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
