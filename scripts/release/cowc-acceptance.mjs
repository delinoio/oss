// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  identity,
  verifyRelease,
  version,
} from "../../packages/cowc/scripts/package.mjs";

export const lanes = Object.freeze({
  "macos15-apfs": { platform: "darwin", arch: "arm64", filesystem: "apfs" },
  "ubuntu22-btrfs": { platform: "linux", arch: "x64", filesystem: "btrfs" },
  "ubuntu22-xfs": { platform: "linux", arch: "x64", filesystem: "xfs" },
  "windows24h2-refs": { platform: "win32", arch: "x64", filesystem: "refs" },
});
export const checks = Object.freeze([
  "physical-sharing",
  "write-isolation",
  "clean-isolation",
  "reflink-required",
  "cargo-minimum",
  "rspack-minimum",
  "rsbuild-minimum",
  "native-install-no-node",
  "npm-install",
  "rollback",
  "freshness",
  "replacement-recovery",
  "locking",
  "process-tree",
  "shims",
  "shell-activation",
  "privacy",
]);
export const phases = Object.freeze([
  "cold",
  "warm-source",
  "cloned-worktree",
  "changed-worktree",
]);
const presets = ["cargo", "rspack", "rsbuild"];
const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");
const run = (command, args) =>
  execFileSync(command, args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"],
  }).trim();

export function releaseIdentity(requested, revision, ref) {
  assert.equal(ref, "refs/heads/main", "Release acceptance must use main");
  assert.equal(
    requested,
    version,
    "Requested version must match the npm source version",
  );
  assert.match(revision, /^[0-9a-f]{40}$/u);
  assert(!/^0+$/u.test(revision));
  return { version, revision, tag: `cowc@v${version}` };
}

// Probe the volume that owns the disposable acceptance workspace. Labels alone
// cannot establish OS or filesystem acceptance. Never format a runner volume.
export function probeHost(workspace, lane) {
  const expected = lanes[lane];
  assert(expected, "Unknown acceptance lane");
  assert.equal(process.platform, expected.platform);
  assert.equal(process.arch, expected.arch);
  let osVersion;
  let filesystem;
  if (process.platform === "darwin") {
    osVersion = run("sw_vers", ["-productVersion"]);
    assert.match(
      osVersion,
      /^15\./u,
      "Minimum macOS 15 acceptance requires macOS 15",
    );
    const plist = execFileSync("diskutil", ["info", "-plist", workspace]);
    const info = JSON.parse(
      execFileSync("plutil", ["-convert", "json", "-o", "-", "-"], {
        input: plist,
        encoding: "utf8",
      }),
    );
    filesystem = String(info.FilesystemType).toLowerCase();
  } else if (process.platform === "linux") {
    const release = readFileSync("/etc/os-release", "utf8");
    assert.match(release, /^ID=ubuntu$/mu);
    assert.match(release, /^VERSION_ID="22\.04"$/mu);
    osVersion = "Ubuntu 22.04";
    filesystem = run("findmnt", [
      "-n",
      "-o",
      "FSTYPE",
      "--target",
      workspace,
    ]).toLowerCase();
  } else {
    // Pass the path as an environment value, never as PowerShell source.
    const info = JSON.parse(
      execFileSync(
        "pwsh",
        [
          "-NoProfile",
          "-NonInteractive",
          "-Command",
          "$o=Get-CimInstance Win32_OperatingSystem; $v=Get-Volume -FilePath $env:COWC_ACCEPTANCE_WORKSPACE; @{caption=$o.Caption;build=$o.BuildNumber;fs=$v.FileSystemType.ToString()} | ConvertTo-Json -Compress",
        ],
        {
          env: { ...process.env, COWC_ACCEPTANCE_WORKSPACE: workspace },
          encoding: "utf8",
          stdio: ["ignore", "pipe", "ignore"],
        },
      ),
    );
    assert.match(
      info.caption,
      /Windows 11/u,
      "Windows Server is not Windows 11 acceptance",
    );
    assert.equal(
      info.build,
      "26100",
      "Minimum Windows 11 24H2 acceptance requires build 26100",
    );
    osVersion = "Windows 11 24H2 (26100)";
    filesystem = info.fs.toLowerCase();
  }
  assert.equal(filesystem, expected.filesystem);
  assert.match(run("cargo", ["--version"]), /^cargo 1\.99\.1 /u);
  assert.equal(Number(process.versions.node.split(".")[0]), 24);
  return {
    ...expected,
    osVersion,
    cargo: "1.99.1",
    node: process.versions.node,
  };
}

// Project only typed measurements and fixed command IDs into public artifacts.
// Driver stdout/stderr, source contents, paths and arbitrary arguments are never
// copied into release evidence. The driver owns real executions and must not
// substitute mocks, cross-builds or apparent sizes for native measurements.
export function validateMeasurements(result) {
  assert.equal(result.kind, "native");
  assert.deepEqual(result.unperformed, []);
  assert.deepEqual(Object.keys(result.checks).sort(), [...checks].sort());
  for (const id of checks)
    assert.equal(result.checks[id], true, `Missing native check: ${id}`);
  assert.deepEqual(result.tools, {
    cargo: "1.99.1",
    rspack: "2.3.0",
    rsbuild: "2.3.0",
  });
  assert.equal(result.measurements.length, presets.length * phases.length);
  const measurements = [];
  for (const preset of presets)
    for (const phase of phases) {
      const matches = result.measurements.filter(
        (m) => m.preset === preset && m.phase === phase,
      );
      assert.equal(
        matches.length,
        1,
        `Missing or duplicate measurement: ${preset}/${phase}`,
      );
      const value = { preset, phase };
      for (const key of [
        "physicalBytes",
        "preparationMs",
        "buildMs",
        "peakMemoryBytes",
      ]) {
        assert(
          Number.isFinite(matches[0][key]) && matches[0][key] >= 0,
          `Invalid ${key}`,
        );
        value[key] = matches[0][key];
      }
      measurements.push(value);
    }
  return {
    kind: "native",
    checks: Object.fromEntries(checks.map((id) => [id, true])),
    tools: result.tools,
    measurements,
    unperformed: [],
  };
}

export function projectProgress(observation, reasons = {}) {
  const unperformed = checks.filter((id) => observation.checks?.[id] !== true);
  return {
    unperformed,
    completedChecks: checks.filter((id) => observation.checks?.[id] === true),
    // reasons is the reviewed driver's source constant, not runtime diagnostics.
    blockers: unperformed.map((id) => ({
      id,
      reason: reasons[id] || "scenario-assertion-or-prerequisite-failed",
    })),
  };
}

export async function accept({ lane, artifacts, workspace, output, revision }) {
  const record = {
    schema: 1,
    lane,
    revision,
    version,
    status: "blocked",
    blocker: "artifact-verification",
    commands: [
      "node packages/cowc/scripts/verify.mjs --directory <candidate> --revision <source-sha>",
      "git rev-parse HEAD",
      "cargo --version",
      "cowc-native-driver.execute",
    ],
    unperformed: [...checks],
    limitations: [
      "No fixed storage or memory reduction is promised.",
      "Linux physicalBytes counts the union of FIEMAP data extents across source and derived caches, excluding metadata.",
      "peakMemoryBytes is native time maximum RSS, not summed concurrent process-tree RSS.",
    ],
  };
  try {
    assert.equal(run("git", ["rev-parse", "HEAD"]), revision);
    verifyRelease(artifacts, identity(revision, false));
    record.artifactDigest = digest(
      readFileSync(path.join(artifacts, "SHA256SUMS")),
    );
    record.blocker = "host-probe";
    record.environment = probeHost(workspace, lane);
    record.blocker = "native-driver-unavailable";
    // Full native scenarios require a reviewed repository-owned driver. Until
    // that driver exists, fail closed instead of reporting fixture acceptance.
    const driver = fileURLToPath(
      new URL("./cowc-native-driver.mjs", import.meta.url),
    );
    assert(
      existsSync(driver),
      "Native scenario/measurement driver is not implemented",
    );
    assert.equal(
      run("git", [
        "status",
        "--porcelain",
        "--untracked-files=all",
        "--",
        "scripts/release/cowc-native-driver.mjs",
      ]),
      "",
      "Native driver must be committed",
    );
    run("git", [
      "ls-files",
      "--error-unmatch",
      "scripts/release/cowc-native-driver.mjs",
    ]);
    record.blocker = "native-scenarios";
    const { execute, blockers = {} } = await import("./cowc-native-driver.mjs");
    const observation = await execute({ lane, artifacts, workspace, revision });
    // Preserve bounded, source-owned blocker descriptions even when complete
    // acceptance fails. Never persist driver exception text or child output.
    Object.assign(record, projectProgress(observation, blockers));
    const result = validateMeasurements(observation);
    Object.assign(record, result, { status: "passed" });
    delete record.blocker;
  } finally {
    writeFileSync(output, JSON.stringify(record, null, 2) + "\n");
  }
  return record;
}

export function gate(directory, artifacts, revision) {
  verifyRelease(artifacts, identity(revision, false));
  const expectedDigest = digest(
    readFileSync(path.join(artifacts, "SHA256SUMS")),
  );
  for (const lane of Object.keys(lanes)) {
    const record = JSON.parse(
      readFileSync(path.join(directory, `${lane}.json`), "utf8"),
    );
    assert.equal(record.schema, 1);
    assert.equal(record.lane, lane);
    assert.equal(record.status, "passed");
    assert.equal(record.revision, revision);
    assert.equal(record.version, version);
    assert.equal(record.artifactDigest, expectedDigest);
    for (const [key, value] of Object.entries(lanes[lane]))
      assert.equal(record.environment[key], value);
    validateMeasurements(record);
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [command, ...args] = process.argv.slice(2);
  if (command === "plan") console.log(JSON.stringify(releaseIdentity(...args)));
  else if (command === "native") {
    const [lane, artifacts, workspace, output, revision] = args;
    await accept({ lane, artifacts, workspace, output, revision });
  } else if (command === "gate") gate(...args);
  else throw new Error("Expected plan, native or gate");
}
