import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { once } from "node:events";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { crc32 } from "node:zlib";
import test from "node:test";
import { spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";
import { desktopIcon, prepareAssets } from "./prepare-assets.mjs";

const png = readFileSync(new URL("../src-tauri/icons/icon.png", import.meta.url));
const success = { code: 0, signal: null };
const hash = bytes => createHash("sha256").update(bytes).digest("hex");
const pointerFor = bytes => Buffer.from(`version https://git-lfs.github.com/spec/v1\noid sha256:${hash(bytes)}\nsize ${bytes.length}\n`);
const otherAsset = "unrelated.png";
function withComment(bytes) {
  const comment = Buffer.from("fixture\0A local icon edit");
  const chunk = Buffer.alloc(comment.length + 12);
  chunk.writeUInt32BE(comment.length);
  chunk.write("tEXt", 4);
  comment.copy(chunk, 8);
  chunk.writeUInt32BE(crc32(chunk.subarray(4, -4)), chunk.length - 4);
  return Buffer.concat([bytes.subarray(0, -12), chunk, bytes.subarray(-12)]);
}
const editedPng = withComment(png);

function fixture(t, bytes = png) {
  const directory = mkdtempSync(join(tmpdir(), "delidev-assets-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const environment = Object.fromEntries(["PATH", "Path", "SystemRoot", "SYSTEMROOT", "WINDIR", "TMP", "TEMP", "TMPDIR"].filter(key => process.env[key]).map(key => [key, process.env[key]]));
  Object.assign(environment, { HOME: directory, USERPROFILE: directory, GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: join(directory, "gitconfig"), GIT_LFS_SKIP_SMUDGE: "1", GIT_TERMINAL_PROMPT: "0" });
  writeFileSync(environment.GIT_CONFIG_GLOBAL, "");
  const git = (root, ...args) => execFileSync("git", args, { cwd: root, env: environment, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  const root = join(directory, "source");
  mkdirSync(root);
  git(root, "init", "--initial-branch=main");
  git(root, "config", "user.name", "DeliDev asset fixture");
  git(root, "config", "user.email", "assets@example.invalid");
  git(root, "config", "commit.gpgSign", "false");
  git(root, "lfs", "install", "--local", "--skip-smudge");
  const asset = join(root, desktopIcon);
  mkdirSync(dirname(asset), { recursive: true });
  writeFileSync(asset, bytes);
  writeFileSync(join(root, otherAsset), editedPng);
  writeFileSync(join(root, ".gitattributes"), `${desktopIcon} filter=lfs diff=lfs merge=lfs -text\n${otherAsset} filter=lfs diff=lfs merge=lfs -text\n`);
  git(root, "add", ".gitattributes", desktopIcon, otherAsset);
  git(root, "commit", "-m", "Add isolated fixture icons");
  const pointer = Buffer.from(git(root, "show", `HEAD:${desktopIcon}`));
  assert.deepEqual(pointer, pointerFor(bytes));
  writeFileSync(asset, pointer);
  const calls = [];
  const logs = [];
  const options = {
    root, environment, log: entry => logs.push(entry),
    run: async (...args) => { calls.push(args[1]); return spawnDevServer(...args); },
  };
  return { directory, root, environment, git, asset, pointer, calls, logs, options };
}

test("a cached LFS icon is restored offline without fetching or changing other assets", async t => {
  const f = fixture(t);
  const unrelated = join(f.root, otherAsset);
  const unrelatedPointer = pointerFor(editedPng);
  writeFileSync(unrelated, unrelatedPointer);
  f.git(f.root, "remote", "add", "origin", join(f.directory, "missing-remote"));
  assert.deepEqual(await prepareAssets(f.options), success);
  assert.deepEqual(readFileSync(f.asset), png);
  assert.deepEqual(readFileSync(unrelated), unrelatedPointer);
  assert.equal(f.calls.some(args => args.includes("fetch")), false);
  assert.equal(f.git(f.root, "diff", "--exit-code", "HEAD"), "");
  assert.equal(f.logs.at(-1).stage, "local-cache");
});

test("a fresh pointer-only clone fetches only the exact icon from a local LFS remote", async t => {
  const f = fixture(t);
  const remote = join(f.directory, "remote.git");
  f.git(f.directory, "init", "--bare", "--initial-branch=main", remote);
  f.git(f.root, "remote", "add", "origin", remote);
  f.git(f.root, "push", "origin", "main");
  const checkout = join(f.directory, "checkout with spaces 한글");
  f.git(f.directory, "clone", remote, checkout);
  f.git(checkout, "lfs", "install", "--local", "--skip-smudge");
  // Caller filters must neither exclude the required icon nor widen the fetch.
  f.git(checkout, "config", "lfs.fetchexclude", "*");
  f.git(checkout, "config", "lfs.fetchinclude", otherAsset);
  f.git(checkout, "config", "lfs.fetchrecentalways", "true");
  assert.deepEqual(readFileSync(join(checkout, desktopIcon)), f.pointer);
  assert.deepEqual(await prepareAssets({ ...f.options, root: checkout }), success);
  assert.deepEqual(readFileSync(join(checkout, desktopIcon)), png);
  assert.deepEqual(readFileSync(join(checkout, otherAsset)), pointerFor(editedPng));
  const otherOid = hash(editedPng);
  assert.equal(existsSync(join(checkout, ".git/lfs/objects", otherOid.slice(0, 2), otherOid.slice(2, 4), otherOid)), false);
  assert.equal(f.logs.at(-1).stage, "download");
  assert.equal(f.git(checkout, "status", "--porcelain"), "");
});

test("valid local PNG edits work without Git and are never replaced", async t => {
  const f = fixture(t);
  writeFileSync(f.asset, editedPng);
  rmSync(join(f.root, ".git"), { recursive: true, force: true });
  assert.deepEqual(await prepareAssets({ ...f.options, run: () => assert.fail("a hydrated PNG needs no Git") }), success);
  assert.deepEqual(readFileSync(f.asset), editedPng);
});

test("missing and malformed icons fail before Git without overwriting local edits", async t => {
  const f = fixture(t);
  const corrupt = Buffer.from(png);
  corrupt[corrupt.length - 1] ^= 1;
  for (const bytes of [Buffer.from("private local edit"), png.subarray(0, 40), corrupt]) {
    writeFileSync(f.asset, bytes);
    assert.equal((await prepareAssets(f.options)).code, 1);
    assert.deepEqual(readFileSync(f.asset), bytes);
    assert.equal(f.logs.at(-1).code, "invalid-icon");
  }
  rmSync(f.asset);
  assert.equal((await prepareAssets(f.options)).code, 1);
  assert.equal(existsSync(f.asset), false);
  assert.equal(f.calls.length, 0);
});

test("Git checkout preserves a locally modified LFS pointer", async t => {
  const f = fixture(t);
  for (const modified of [pointerFor(editedPng), Buffer.from(f.pointer.toString().replace(`size ${png.length}`, `size ${png.length + 1}`))]) {
    writeFileSync(f.asset, modified);
    assert.notEqual((await prepareAssets(f.options)).code, 0);
    assert.deepEqual(readFileSync(f.asset), modified);
    assert.equal(f.logs.at(-1).code, "icon-locally-modified");
  }
  assert.equal(f.calls.some(args => args.includes("checkout") || args.includes("fetch")), false);
});

test("Git and Git LFS failures expose stable actionable diagnostics, never raw errors", async t => {
  const f = fixture(t);
  for (const [run, code] of [
    [async () => { throw Object.assign(new Error("/private/credential SECRET"), { code: "ENOENT" }); }, "git-unavailable"],
    [async () => ({ code: 7, signal: null }), "git-lfs-unavailable"],
  ]) {
    assert.notEqual((await prepareAssets({ ...f.options, run })).code, 0);
    const entry = f.logs.at(-1);
    assert.equal(entry.code, code);
    assert.match(entry.guidance, /git lfs fetch/);
    assert.match(entry.guidance, /git lfs checkout/);
    assert.doesNotMatch(JSON.stringify(f.logs), /credential|SECRET|private/);
    assert.deepEqual(readFileSync(f.asset), f.pointer);
  }
});

test("a failed download stops preparation and retains the original pointer", async t => {
  const f = fixture(t);
  rmSync(join(f.root, ".git/lfs/objects"), { recursive: true, force: true });
  f.git(f.root, "remote", "add", "origin", join(f.directory, "missing-remote"));
  assert.notEqual((await prepareAssets(f.options)).code, 0);
  assert.equal(f.logs.at(-1).code, "icon-download-failed");
  assert.deepEqual(readFileSync(f.asset), f.pointer);
  assert.equal(f.calls.filter(args => args.includes("checkout")).length, 1);
});

test("restored bytes must match the pointer size, digest, and PNG container", async t => {
  const f = fixture(t);
  for (const bytes of [editedPng, Buffer.from(png).fill(0, 40, 41)]) {
    writeFileSync(f.asset, f.pointer);
    const run = async (command, args, options, lifecycle) => {
      const result = await spawnDevServer(command, args, options, lifecycle);
      if (args.includes("checkout")) writeFileSync(f.asset, bytes);
      return result;
    };
    assert.equal((await prepareAssets({ ...f.options, run })).code, 1);
    assert.equal(f.logs.at(-1).code, "icon-integrity-failed");
    assert.deepEqual(readFileSync(f.asset), bytes);
  }
  const malformed = fixture(t, Buffer.from("a hash-matching object that is not a PNG"));
  assert.equal((await prepareAssets(malformed.options)).code, 1);
  assert.equal(malformed.logs.at(-1).code, "icon-integrity-failed");
});

test("a local edit during fetch is retained and never followed by checkout", async t => {
  const f = fixture(t);
  let checkouts = 0;
  const run = async (_command, args) => {
    if (args.includes("checkout")) checkouts++;
    if (args.includes("fetch")) writeFileSync(f.asset, editedPng);
    return success;
  };
  assert.equal((await prepareAssets({ ...f.options, run })).code, 1);
  assert.equal(checkouts, 1);
  assert.deepEqual(readFileSync(f.asset), editedPng);
});

test("SIGTERM joins the active asset child and prevents further preparation", { skip: process.platform === "win32", timeout: 20_000 }, async t => {
  const f = fixture(t);
  const receipt = join(f.directory, "terminated");
  const childCode = `
    const fs = require('node:fs');
    process.on('SIGTERM', () => { fs.writeFileSync(${JSON.stringify(receipt)}, 'terminated'); process.exit(0); });
    setInterval(() => {}, 1000);
    process.stdout.write('child-ready\\n');
  `;
  const wrapper = spawn(process.execPath, ["--input-type=module", "-e", `
    import { prepareAssets } from ${JSON.stringify(new URL("./prepare-assets.mjs", import.meta.url).href)};
    import { spawnDevServer, exitLikeChild } from ${JSON.stringify(new URL("../../../scripts/spawn-dev-server.mjs", import.meta.url).href)};
    exitLikeChild(await prepareAssets({
      root: ${JSON.stringify(f.root)}, log() {},
      run: (_command, _args, _options, lifecycle) => {
        const running = spawnDevServer(process.execPath, ['-e', ${JSON.stringify(childCode)}], { stdio: 'inherit', shell: false }, lifecycle);
        process.stdout.write('wrapper-ready\\n');
        return running;
      },
    }));
  `], { stdio: ["ignore", "pipe", "pipe"] });
  t.after(() => { if (wrapper.exitCode === null && wrapper.signalCode === null) wrapper.kill("SIGTERM"); });
  const exited = once(wrapper, "exit");
  // Child stdout can precede signal-handler installation in the wrapper under
  // host load. Synchronize both processes before testing signal forwarding.
  let output = "";
  for await (const chunk of wrapper.stdout.iterator({ destroyOnReturn: false })) {
    output += chunk;
    if (output.includes("child-ready\n") && output.includes("wrapper-ready\n")) break;
  }
  assert.ok(output.includes("child-ready\n") && output.includes("wrapper-ready\n"));
  wrapper.kill("SIGTERM");
  assert.deepEqual(await exited, [null, "SIGTERM"]);
  assert.equal(readFileSync(receipt, "utf8"), "terminated");
  assert.deepEqual(readFileSync(f.asset), f.pointer);
});
