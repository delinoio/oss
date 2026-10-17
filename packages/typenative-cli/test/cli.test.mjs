import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import {
  Code,
  createProject,
  hostTarget,
  inspectTools,
  probeNative,
  Target,
  versions,
} from "../dist/core.js";

const cli = fileURLToPath(new URL("../dist/main.js", import.meta.url));
async function temporary(t) {
  const root = await mkdtemp(join(tmpdir(), "typenative-test-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}
function invoke(args) {
  const result = spawnSync(process.execPath, [cli, ...args, "--json"], {
    encoding: "utf8",
  });
  return {
    ...result,
    records: result.stdout.trim().split("\n").filter(Boolean).map(JSON.parse),
  };
}

test("create writes exact pins and preserves an existing project", async (t) => {
  const root = await temporary(t);
  const project = join(root, "example");
  await createProject(project);
  const config = JSON.parse(
    await readFile(join(project, "typenative.json"), "utf8"),
  );
  assert.deepEqual(config.versions, versions);
  assert.equal(
    JSON.parse(await readFile(join(project, "package.json"), "utf8"))
      .dependencies.react,
    "19.3.1",
  );
  await writeFile(join(project, "App.tsx"), "private existing source");
  await assert.rejects(createProject(project), { code: Code.Create });
  assert.equal(
    await readFile(join(project, "App.tsx"), "utf8"),
    "private existing source",
  );
});

test("create refuses symlinks and invalid package names", async (t) => {
  const root = await temporary(t);
  const destination = join(root, "linked");
  await symlink(
    root,
    destination,
    process.platform === "win32" ? "junction" : "dir",
  );
  await assert.rejects(createProject(destination), { code: Code.Create });
  await assert.rejects(createProject(join(root, "Bad Name")), {
    code: Code.Arguments,
  });
  assert.equal(existsSync(join(root, "package.json")), false);
});

test("doctor rejects missing tools, failed commands and wrong compiler versions without raw output", () => {
  const results = inspectTools(
    Target.Linux,
    "/compiler path/scriptc",
    (command) => {
      if (command === "/compiler path/scriptc")
        return { status: 0, failed: false, stdout: "0.3.6\nprivate-token" };
      return { status: null, failed: true, stdout: "private-token" };
    },
  );
  assert.equal(results[0].code, Code.Version);
  assert.equal(results[1].code, Code.Tool);
  assert.equal(JSON.stringify(results).includes("private-token"), false);
});

test("doctor passes arguments literally and checks the pinned compiler", () => {
  const calls = [];
  const checks = inspectTools(
    Target.IOS,
    "compiler; do-not-execute",
    (command, args) => {
      calls.push([command, args]);
      return {
        status: 0,
        failed: false,
        stdout: command.startsWith("compiler") ? "0.3.5\n" : "tool",
      };
    },
  );
  assert.equal(
    checks.every((check) => check.ok),
    true,
  );
  assert.deepEqual(calls[0], ["compiler; do-not-execute", ["--version"]]);
  assert.deepEqual(calls.at(-1), [
    "xcrun",
    ["--sdk", "iphonesimulator", "--show-sdk-path"],
  ]);
});

test("native probe rejects cross-target execution before invoking tools", async () => {
  await assert.rejects(
    probeNative("scriptc", Target.IOS, () => assert.fail("unexpected tool")),
    { code: Code.Host },
  );
});

test(
  "native probe removes its scratch directory after compilation failure",
  { skip: !hostTarget() },
  async () => {
    let scratch;
    await assert.rejects(
      probeNative("compiler", hostTarget(), (_command, args, cwd) => {
        scratch = cwd;
        assert.equal(dirname(args[1]), cwd);
        return { status: 1, failed: false, stdout: "sensitive diagnostic" };
      }),
      { code: Code.Compile },
    );
    assert.equal(existsSync(scratch), false);
  },
);

test(
  "native probe rejects a non-native artifact before execution and cleans up",
  { skip: !hostTarget() },
  async () => {
    let scratch;
    let calls = 0;
    // Runner is synchronous, matching child_process.spawnSync.
    const { writeFileSync } = await import("node:fs");
    await assert.rejects(
      probeNative("compiler", hostTarget(), (_command, args, cwd) => {
        calls++;
        scratch = cwd;
        writeFileSync(
          args.at(-1),
          "#!/bin/sh\necho typenative-native-probe-v1\n",
        );
        return { status: 0, failed: false, stdout: "" };
      }),
      { code: Code.Artifact },
    );
    assert.equal(calls, 1);
    assert.equal(existsSync(scratch), false);
  },
);

test(
  "native probe checks execution output and reports a digest of the fixture",
  { skip: !hostTarget() },
  async () => {
    const { writeFileSync } = await import("node:fs");
    const target = hostTarget();
    const header =
      target === Target.MacOS
        ? "cffaedfe"
        : target === Target.Linux
          ? "7f454c46"
          : "4d5a0000";
    let scratch;
    const runner = (_command, args, cwd) => {
      scratch = cwd;
      if (args[0] === "build") {
        writeFileSync(args.at(-1), Buffer.from(header, "hex"));
        return { status: 0, failed: false, stdout: "" };
      }
      return {
        status: 0,
        failed: false,
        stdout: "typenative-native-probe-v1\n",
      };
    };
    const result = await probeNative("compiler", target, runner);
    assert.match(result.sha256, /^[a-f0-9]{64}$/);
    assert.equal(result.bytes, 4);
    assert.equal(existsSync(scratch), false);
  },
);

test("CLI reports unsupported application commands as failures without launching", () => {
  for (const command of ["build", "dev", "run"]) {
    const result = invoke([command]);
    assert.equal(result.status, 1);
    assert.equal(result.records[0].code, Code.Pipeline);
    assert.equal(result.records[0].phase, command);
  }
});

test("CLI rejects malformed arguments without reflecting their contents", () => {
  for (const args of [
    ["doctor", "--target", "private-input"],
    ["doctor", "--private-token"],
    ["create"],
    ["doctor", "extra"],
  ]) {
    const result = invoke(args);
    assert.equal(result.status, 1);
    assert.equal(result.records[0].code, Code.Arguments);
    assert.equal(result.stdout.includes("private-input"), false);
    assert.equal(result.stdout.includes("private-token"), false);
  }
});

test("CLI create returns structured metadata and explicit incomplete framework status", async (t) => {
  const root = await temporary(t);
  const result = invoke(["create", join(root, "example")]);
  assert.equal(result.status, 0);
  const record = result.records[0];
  assert.equal(record.frameworkReady, false);
  assert.equal(record.sourceLocation, null);
  assert.ok(record.durationMs >= 0);
  assert.deepEqual(record.versions, versions);
  assert.ok(record.buildId);
});

test(
  "doctor surfaces a compiler version mismatch in the aggregate record",
  { skip: process.platform === "win32" },
  async (t) => {
    const { chmod } = await import("node:fs/promises");
    const root = await temporary(t);
    const compiler = join(root, "scriptc");
    await writeFile(compiler, '#!/bin/sh\nprintf "0.0.0\\n"\n');
    await chmod(compiler, 0o755);
    const result = invoke([
      "doctor",
      "--target",
      "linux-x64",
      "--scriptc",
      compiler,
    ]);
    assert.equal(result.status, 1);
    assert.equal(result.records[0].code, Code.Version);
  },
);

test("doctor rejects an explicitly empty target", () => {
  for (const args of [
    ["doctor", "--target="],
    ["doctor", "--target", ""],
  ]) {
    const result = invoke(args);
    assert.equal(result.status, 1);
    assert.equal(result.records[0].code, Code.Arguments);
  }
});
