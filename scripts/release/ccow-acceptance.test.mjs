// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import {
  accept,
  cargoProbeEnv,
  checks,
  darwinFilesystem,
  gate,
  lanes,
  mountPointOf,
  phases,
  probeCargo,
  releaseIdentity,
  validateMeasurements,
} from "./ccow-acceptance.mjs";

const revision = "a".repeat(40);
const fixture = () => ({
  kind: "native",
  unperformed: [],
  tools: { cargo: "1.99.1", rspack: "2.3.0", rsbuild: "2.3.0" },
  checks: Object.fromEntries(checks.map((id) => [id, true])),
  measurements: ["cargo", "rspack", "rsbuild"].flatMap((preset) =>
    phases.map((phase) => ({
      preset,
      phase,
      physicalBytes: 42,
      preparationMs: 1,
      buildMs: 2,
      peakMemoryBytes: 1024,
    })),
  ),
});

test("release identity rejects arbitrary refs, versions and revisions", () => {
  assert.equal(
    releaseIdentity("0.1.0", revision, "refs/heads/main").tag,
    "ccow@v0.1.0",
  );
  for (const args of [
    ["0.1.1", revision, "refs/heads/main"],
    ["0.1.0", revision, "refs/heads/topic"],
    ["0.1.0", "0".repeat(40), "refs/heads/main"],
  ])
    assert.throws(() => releaseIdentity(...args));
});
test("native measurement schema requires every scenario and minimum tool", () => {
  assert.equal(validateMeasurements(fixture()).measurements.length, 12);
  for (const mutate of [
    (r) => {
      r.kind = "fixture";
    },
    (r) => {
      r.unperformed.push("rollback");
    },
    (r) => {
      r.checks["physical-sharing"] = false;
    },
    (r) => {
      delete r.checks["process-tree"];
    },
    (r) => {
      r.tools.cargo = "1.100.0";
    },
    (r) => {
      r.measurements.pop();
    },
    (r) => {
      r.measurements[0].physicalBytes = -1;
    },
    (r) => {
      r.measurements[0].peakMemoryBytes = null;
    },
    (r) => {
      r.measurements[0] = r.measurements[1];
    },
  ]) {
    const result = fixture();
    mutate(result);
    assert.throws(() => validateMeasurements(result));
  }
});
test("public evidence projects out raw content and arbitrary commands", () => {
  const result = fixture();
  result.secret = "must not persist";
  result.measurements[0].command = "private arguments";
  assert(!JSON.stringify(validateMeasurements(result)).includes("private"));
  assert(!JSON.stringify(validateMeasurements(result)).includes("secret"));
});
test("missing evidence fails closed and blocked entrypoint retains unperformed checks", async () => {
  const directory = mkdtempSync(path.join(os.tmpdir(), "ccow-gate-"));
  try {
    assert.throws(() => gate(directory, directory, revision));
    const output = path.join(directory, "blocked.json");
    await assert.rejects(
      accept({
        lane: Object.keys(lanes)[0],
        artifacts: directory,
        workspace: directory,
        output,
        revision,
      }),
    );
    const record = JSON.parse(readFileSync(output, "utf8"));
    assert.equal(record.status, "blocked");
    assert.deepEqual(record.unperformed, checks);
    assert(!("measurements" in record));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

const dfHeader = "Filesystem 1024-blocks Used Available Capacity Mounted on";
test("mount point resolution keeps mount points that contain spaces", () => {
  const seen = [];
  const runner = (name, args) => {
    seen.push([name, ...args]);
    return `${dfHeader}\n/dev/disk3s5 488245288 1 2 3% /Volumes/My Disk`;
  };
  assert.equal(mountPointOf("/some/dir", runner), "/Volumes/My Disk");
  assert.deepEqual(seen, [["df", "-P", "/some/dir"]]);
  assert.throws(() => mountPointOf("/x", () => `${dfHeader}\n`));
});
test("macOS filesystem lookup passes the df mount point to diskutil", () => {
  const calls = [];
  const runner = (name, args, input) => {
    calls.push([name, ...args]);
    if (name === "df")
      return `${dfHeader}\n/dev/disk3s5 1 2 3 4% /System/Volumes/Data`;
    if (name === "diskutil") return "<plist/>";
    assert.equal(name, "plutil");
    assert.equal(input, "<plist/>");
    return '{"FilesystemType":"APFS"}';
  };
  assert.equal(darwinFilesystem("/checkout/nested", runner), "apfs");
  assert.deepEqual(calls[1], [
    "diskutil",
    "info",
    "-plist",
    "/System/Volumes/Data",
  ]);
  // A non-APFS volume is reported as-is so the lane assertion can reject it.
  assert.equal(
    darwinFilesystem("/x", (name) =>
      name === "df"
        ? `${dfHeader}\n/dev/disk4 1 2 3 4% /Volumes/X`
        : name === "diskutil"
          ? "<plist/>"
          : '{"FilesystemType":"exfat"}',
    ),
    "exfat",
  );
});
test(
  "macOS filesystem lookup resolves nested directories and root on APFS",
  { skip: process.platform !== "darwin" },
  () => {
    const root = mkdtempSync(path.join(os.tmpdir(), "ccow-mount-"));
    try {
      const nested = path.join(root, "a", "b");
      mkdirSync(nested, { recursive: true });
      assert.equal(darwinFilesystem(nested), "apfs");
      assert.equal(darwinFilesystem("/"), "apfs");
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  },
);

// A stub cargo that mimics rustup: the checkout override selects nightly
// unless RUSTUP_TOOLCHAIN pins the version, and a missing toolchain fails.
const stubCargo = (body) => {
  const dir = mkdtempSync(path.join(os.tmpdir(), "ccow-cargo-"));
  const file = path.join(dir, "cargo");
  writeFileSync(file, `#!/bin/sh\n${body}\n`);
  chmodSync(file, 0o755);
  return dir;
};
test(
  "cargo probe pins the toolchain over the checkout override",
  { skip: process.platform === "win32" },
  () => {
    const dir = stubCargo(
      'if [ "$RUSTUP_TOOLCHAIN" = 1.99.1 ] && [ "$RUSTUP_AUTO_INSTALL" = 0 ]; then echo "cargo 1.99.1 (stub)"; else echo "cargo 1.101.0-nightly (stub)"; fi',
    );
    try {
      assert.equal(cargoProbeEnv.RUSTUP_TOOLCHAIN, "1.99.1");
      assert.equal(cargoProbeEnv.RUSTUP_AUTO_INSTALL, "0");
      probeCargo(undefined, {
        ...process.env,
        PATH: `${dir}:${process.env.PATH}`,
      });
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  },
);
test(
  "cargo probe fails when the pinned toolchain is unavailable",
  { skip: process.platform === "win32" },
  () => {
    const nightly = stubCargo('echo "cargo 1.101.0-nightly (stub)"');
    const missing = stubCargo("exit 1");
    try {
      for (const dir of [nightly, missing])
        assert.throws(() =>
          probeCargo(undefined, {
            ...process.env,
            PATH: `${dir}:${process.env.PATH}`,
          }),
        );
    } finally {
      rmSync(nightly, { recursive: true, force: true });
      rmSync(missing, { recursive: true, force: true });
    }
  },
);
