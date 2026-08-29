import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";
import {
  desktopTauriArguments,
  desktopTauriConfigPath,
  desktopTauriEnvironment,
  privateReleaseTauriConfigPath,
  repositoryAppleSigningEnvironment,
  repositoryAppleSigningIdentityKey,
} from "./run-tauri-arguments.mjs";

const scriptPath = fileURLToPath(new URL("./run-tauri.mjs", import.meta.url));
const processTreeChildPath = fileURLToPath(new URL("./process-tree-child.mjs", import.meta.url));
const configOverrides = [
  ["separated short option", ["build", "--", "-c", "alternate.json"]],
  ["equals-delimited short option", ["build", "--", "-c=alternate.json"]],
  ["attached short option", ["build", "--", "-calternate.json"]],
  ["separated long option", ["build", "--", "--config", "alternate.json"]],
  ["equals-delimited long option", ["build", "--", "--config=alternate.json"]],
];
const targetOverrides = [
  ["separated short target option", ["build", "--", "-t", "aarch64-apple-darwin"]],
  ["equals-delimited short target option", ["build", "--", "-t=aarch64-apple-darwin"]],
  ["attached short target option", ["build", "--", "-taarch64-apple-darwin"]],
  ["separated long target option", ["build", "--", "--target", "aarch64-apple-darwin"]],
  ["equals-delimited long target option", ["build", "--", "--target=aarch64-apple-darwin"]],
];

test("forwards the desktop CEF feature to the pinned Tauri command", () => {
  assert.deepEqual(desktopTauriArguments("dev", []), [
    "dev",
    "--features",
    "desktop-cef",
    "--config",
    desktopTauriConfigPath,
  ]);
  assert.deepEqual(
    desktopTauriArguments("build", ["--bundles", "app"]),
    [
      "build",
      "--features",
      "desktop-cef",
      "--config",
      desktopTauriConfigPath,
      "--bundles",
      "app",
    ],
  );
});

test("uses only the repository-owned hardened release overlay for private builds", () => {
  const arguments_ = desktopTauriArguments("build", ["--bundles", "app"], { DEVHUD_PRIVATE_RELEASE: "1" });
  assert.equal(arguments_[4], privateReleaseTauriConfigPath);
  assert.ok(!arguments_.includes(desktopTauriConfigPath));
});

test("loads an opt-in Apple signing identity from repository-local Git config", () => {
  const calls = [];
  const environment = repositoryAppleSigningEnvironment(
    "dev",
    "darwin",
    { EXISTING: "value" },
    (...args) => {
      calls.push(args);
      return { status: 0, stdout: "Apple Development: Developer (TEAMID)\n" };
    },
  );

  assert.deepEqual(environment, {
    APPLE_SIGNING_IDENTITY: "Apple Development: Developer (TEAMID)",
    EXISTING: "value",
  });
  assert.deepEqual(calls, [[
    "git",
    ["config", "--local", "--get", repositoryAppleSigningIdentityKey],
    {
      encoding: "utf8",
      env: { EXISTING: "value", LC_ALL: "C" },
      shell: false,
      stdio: ["ignore", "pipe", "pipe"],
    },
  ]]);
});

test("keeps an explicit Apple signing identity ahead of repository-local config", () => {
  const environment = { APPLE_SIGNING_IDENTITY: "Explicit Identity" };
  const resolved = repositoryAppleSigningEnvironment(
    "dev",
    "darwin",
    environment,
    () => assert.fail("Git config should not be read"),
  );
  assert.equal(resolved, environment);
});

test("does not use repository-local signing for private releases", () => {
  const environment = { DEVHUD_PRIVATE_RELEASE: "1" };
  const resolved = repositoryAppleSigningEnvironment(
    "build",
    "darwin",
    environment,
    () => assert.fail("Git config should not be read"),
  );
  assert.equal(resolved, environment);
});

test("leaves ad hoc signing unchanged when local signing is unavailable", () => {
  const environment = {};
  const missing = repositoryAppleSigningEnvironment(
    "dev",
    "darwin",
    environment,
    () => ({ status: 1, stdout: "" }),
  );
  const noGitMetadata = repositoryAppleSigningEnvironment(
    "dev",
    "darwin",
    environment,
    () => ({
      status: 128,
      stderr: "fatal: --local can only be used inside a git repository\n",
      stdout: "",
    }),
  );
  const otherPlatform = repositoryAppleSigningEnvironment(
    "dev",
    "linux",
    environment,
    () => assert.fail("Git config should not be read"),
  );
  assert.equal(missing, environment);
  assert.equal(noGitMetadata, environment);
  assert.equal(otherPlatform, environment);
});

test("surfaces fatal repository-local Git configuration failures", () => {
  assert.throws(
    () => repositoryAppleSigningEnvironment(
      "dev",
      "darwin",
      {},
      () => ({
        status: 128,
        stderr: "fatal: bad config line 1 in file .git/config\n",
        stdout: "",
      }),
    ),
    /exited with status 128/u,
  );
});

test("rejects malformed repository-local signing identities", () => {
  assert.throws(
    () => repositoryAppleSigningEnvironment(
      "dev",
      "darwin",
      {},
      () => ({ status: 0, stdout: "Identity One\nIdentity Two\n" }),
    ),
    /must be one non-empty line/u,
  );
});

test("derives the compiled package kind from one explicit Windows bundle", () => {
  assert.deepEqual(
    desktopTauriEnvironment("build", ["--bundles", "msi"], "win32", { EXISTING: "value" }),
    { EXISTING: "value", DEVHUD_PACKAGE_KIND: "windows-msi" },
  );
  assert.deepEqual(
    desktopTauriEnvironment("build", ["--bundles=nsis"], "win32", {}),
    { DEVHUD_PACKAGE_KIND: "windows-nsis" },
  );
});

test("derives the compiled package kind from one explicit Linux bundle", () => {
  assert.deepEqual(
    desktopTauriEnvironment("build", ["--bundles", "deb"], "linux", { EXISTING: "value" }),
    { EXISTING: "value", DEVHUD_PACKAGE_KIND: "linux-deb" },
  );
  assert.deepEqual(
    desktopTauriEnvironment("build", ["--bundles=appimage"], "linux", {}),
    { DEVHUD_PACKAGE_KIND: "linux-appimage" },
  );
});

test("rejects ambiguous or conflicting Windows package builds", () => {
  assert.throws(
    () => desktopTauriEnvironment("build", [], "win32", {}),
    /require exactly one --bundles msi or --bundles nsis/u,
  );
  assert.throws(
    () => desktopTauriEnvironment("build", ["--bundles", "all"], "win32", {}),
    /require exactly one --bundles msi or --bundles nsis/u,
  );
  assert.throws(
    () => desktopTauriEnvironment("build", ["--bundles", "msi", "nsis"], "win32", {}),
    /require exactly one --bundles msi or --bundles nsis/u,
  );
  assert.throws(
    () => desktopTauriEnvironment("build", ["-b=msi"], "win32", { DEVHUD_PACKAGE_KIND: "windows-nsis" }),
    /does not match the selected msi bundle/u,
  );
});

test("rejects ambiguous or conflicting Linux package builds", () => {
  assert.throws(
    () => desktopTauriEnvironment("build", [], "linux", {}),
    /require exactly one --bundles deb or --bundles appimage/u,
  );
  assert.throws(
    () => desktopTauriEnvironment("build", ["--bundles", "all"], "linux", {}),
    /require exactly one --bundles deb or --bundles appimage/u,
  );
  assert.throws(
    () => desktopTauriEnvironment("build", ["--bundles", "deb", "appimage"], "linux", {}),
    /require exactly one --bundles deb or --bundles appimage/u,
  );
  assert.throws(
    () => desktopTauriEnvironment("build", ["-b=deb"], "linux", { DEVHUD_PACKAGE_KIND: "linux-appimage" }),
    /does not match the selected deb bundle/u,
  );
});

test("does not require a package kind for development or macOS builds", () => {
  const environment = {};
  assert.equal(desktopTauriEnvironment("dev", [], "win32", environment), environment);
  assert.equal(desktopTauriEnvironment("dev", [], "linux", environment), environment);
  assert.equal(desktopTauriEnvironment("build", [], "darwin", environment), environment);
});

for (const [name, args] of configOverrides) {
  test(`rejects the ${name}`, () => {
    const result = spawnSync(process.execPath, [scriptPath, ...args], {
      encoding: "utf8",
    });

    assert.equal(result.signal, null);
    assert.equal(result.status, 1);
    assert.match(
      result.stderr,
      /devhud: -c\/--config cannot override the pinned application, CSP, or development origin/,
    );
  });
}

for (const [name, args] of targetOverrides) {
  test(`rejects the ${name}`, () => {
    const result = spawnSync(process.execPath, [scriptPath, ...args], {
      encoding: "utf8",
    });

    assert.equal(result.signal, null);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /devhud: -t\/--target cannot override the pinned desktop target/);
  });
}

async function waitForStatus(statusPath) {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    try {
      return JSON.parse(readFileSync(statusPath, "utf8"));
    } catch (error) {
      if (error.code !== "ENOENT") {
        throw error;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error("timed out waiting for the process-tree child status");
}

async function waitForProcessExit(pid) {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    try {
      process.kill(pid, 0);
    } catch (error) {
      if (error.code === "ESRCH") {
        return;
      }
      throw error;
    }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error(`timed out waiting for process ${pid} to exit`);
}

function listenOnPort(port) {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(port, "127.0.0.1", () => {
      server.close(resolve);
    });
  });
}

test(
  "forwards an escalation signal while the child remains alive",
  { skip: process.platform === "win32", timeout: 20_000 },
  async (t) => {
    const temporaryDirectory = mkdtempSync(join(tmpdir(), "devhud-signal-escalation-"));
    const statusPath = join(temporaryDirectory, "status.json");
    let pid;
    t.after(() => {
      if (pid) {
        try {
          process.kill(pid, "SIGKILL");
        } catch (error) {
          if (error.code !== "ESRCH") {
            throw error;
          }
        }
      }
      rmSync(temporaryDirectory, { force: true, recursive: true });
    });

    const resultPromise = spawnDevServer(
      process.execPath,
      [processTreeChildPath, "signal-escalation", statusPath],
      {
        shell: false,
        stdio: "ignore",
        windowsHide: true,
      },
    );
    ({ pid } = await waitForStatus(statusPath));

    process.emit("SIGINT");
    await new Promise((resolve) => setTimeout(resolve, 100));
    process.kill(pid, 0);

    process.emit("SIGTERM");
    assert.deepEqual(await resultPromise, { code: null, signal: "SIGTERM" });
  },
);

test(
  "terminates and awaits the complete process tree",
  { timeout: 20_000 },
  async (t) => {
    const temporaryDirectory = mkdtempSync(join(tmpdir(), "devhud-process-tree-"));
    const statusPath = join(temporaryDirectory, "status.json");
    t.after(() => rmSync(temporaryDirectory, { force: true, recursive: true }));

    const resultPromise = spawnDevServer(
      process.execPath,
      [processTreeChildPath, "manager", statusPath],
      {
        shell: false,
        stdio: "ignore",
        windowsHide: true,
      },
      { terminateProcessTree: true },
    );
    const { pid, port } = await waitForStatus(statusPath);

    process.emit("SIGTERM");
    const result = await resultPromise;

    assert.deepEqual(result, { code: null, signal: "SIGTERM" });
    assert.throws(() => process.kill(pid, 0));
    await listenOnPort(port);
  },
);

test(
  "forwards escalation after the managed POSIX root exits",
  { skip: process.platform === "win32", timeout: 20_000 },
  async (t) => {
    const temporaryDirectory = mkdtempSync(join(tmpdir(), "devhud-group-escalation-"));
    const statusPath = join(temporaryDirectory, "status.json");
    let processGroupId;
    t.after(() => {
      if (processGroupId) {
        try {
          process.kill(-processGroupId, "SIGKILL");
        } catch (error) {
          if (error.code !== "ESRCH") {
            throw error;
          }
        }
      }
      rmSync(temporaryDirectory, { force: true, recursive: true });
    });

    const resultPromise = spawnDevServer(
      process.execPath,
      [processTreeChildPath, "exiting-manager", statusPath],
      {
        shell: false,
        stdio: "ignore",
        windowsHide: true,
      },
      { terminateProcessTree: true },
    );
    const { managerPid, pid, port } = await waitForStatus(statusPath);
    processGroupId = managerPid;

    process.emit("SIGINT");
    await waitForProcessExit(managerPid);
    process.kill(pid, 0);
    await assert.rejects(listenOnPort(port), { code: "EADDRINUSE" });

    process.emit("SIGTERM");
    assert.deepEqual(await resultPromise, { code: null, signal: "SIGTERM" });
    await listenOnPort(port);
  },
);
