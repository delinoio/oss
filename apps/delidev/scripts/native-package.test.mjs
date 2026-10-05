import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, symlinkSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { targets, selectTarget, binaryArchitecture, acquireNativeBuildLock, verifyPackageRevision, verifyAppImagePayload, cefResourcePath, cefCredits, prepareCefCredits, verifyNativePayload, verifyNotices, findOneFile } from "./native-package.mjs";
import { nativeEnvironment } from "./bundle-native-dry-run.mjs";

function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), "delidev-package-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}
function write(root, name, bytes) {
  const path = join(root, name);
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, bytes);
  return path;
}
function binary(platform, arch) {
  const buffer = Buffer.alloc(256);
  if (platform === "linux") {
    buffer.set([0x7f, 0x45, 0x4c, 0x46, 2, 1]);
    buffer.writeUInt16LE(arch === "arm64" ? 183 : 62, 18);
  } else {
    buffer.write("MZ"); buffer.writeUInt32LE(128, 60);
    buffer.writeUInt32LE(0x4550, 128); buffer.writeUInt16LE(arch === "arm64" ? 0xaa64 : 0x8664, 132);
    buffer.writeUInt16LE(0x20b, 152);
  }
  return buffer;
}

test("all six native targets require the exact process platform and architecture", () => {
  assert.equal(targets.length, 6);
  assert.equal(new Set(targets.map(item => item.target)).size, 6);
  for (const selected of targets) {
    assert.equal(selectTarget(selected.target, selected.platform, selected.arch), selected);
    assert.throws(() => selectTarget(selected.target, selected.platform, selected.arch === "arm64" ? "x64" : "arm64"));
    assert.throws(() => selectTarget(selected.target, "foreign", selected.arch));
  }
  assert.throws(() => selectTarget("i686-pc-windows-msvc", "win32", "x64"));
});

test("Windows receives only bounded system/toolchain context and never signing or injection flags", () => {
  const environment = nativeEnvironment({ Path: "tools", SystemRoot: "system", localappdata: "cache", INCLUDE: "headers", LIB: "libraries", SIGNTOOL_PATH: "signer", APPLE_PASSWORD: "secret", TAURI_SIGNING_PRIVATE_KEY: "secret", GH_TOKEN: "secret", NODE_OPTIONS: "injected", RUSTFLAGS: "injected", CARGO_TARGET_DIR: "elsewhere", CL: "injected", LINK: "injected", HTTP_PROXY: "credential" }, "win32");
  assert.deepEqual(environment, { PATH: "tools", SystemRoot: "system", LOCALAPPDATA: "cache", INCLUDE: "headers", LIB: "libraries", CI: "true" });
  assert.deepEqual(nativeEnvironment({ SystemRoot: "system", INCLUDE: "headers" }, "linux"), { CI: "true" });
});

test("bounded native header inspection rejects mixed, truncated and out-of-bound PE data", t => {
  const root = fixture(t);
  for (const platform of ["linux", "win32"]) for (const arch of ["x64", "arm64"]) {
    const path = write(root, `${platform}-${arch}`, binary(platform, arch));
    assert.equal(binaryArchitecture(path, platform), arch);
    assert.throws(() => binaryArchitecture(path, platform === "linux" ? "win32" : "linux"));
  }
  assert.throws(() => binaryArchitecture(write(root, "short", "MZ"), "win32"), /Truncated/);
  const malformed = binary("win32", "x64"); malformed.writeUInt32LE(0x7fffffff, 60);
  assert.throws(() => binaryArchitecture(write(root, "offset", malformed), "win32"), /PE64/);
  malformed.writeUInt32LE(128, 60); malformed.writeUInt16LE(0x10b, 152);
  assert.throws(() => binaryArchitecture(write(root, "pe32", malformed), "win32"), /PE64/);
});

test("CEF credits retain the exact pinned distribution and original packaged bytes", t => {
  const root = fixture(t), selected = targets[1];
  const cache = "Library/Caches/tauri-cef/151.3.24/cef_macos_aarch64";
  const manifest = { type: "minimal", name: "cef_binary_151.3.24+g2384915+chromium-151.0.7922.174_macosarm64_minimal.tar.bz2" };
  write(root, `${cache}/archive.json`, JSON.stringify(manifest));
  const credits = write(root, `${cache}/CREDITS.html`, "original credits");
  assert.equal(cefCredits(selected, {}, root), credits);
  write(root, "payload/notices/Chromium-CREDITS.html", "original credits");
  verifyNotices(join(root, "payload"), { [credits]: "notices/Chromium-CREDITS.html" });
  write(root, "payload/notices/Chromium-CREDITS.html", "changed credits");
  assert.throws(() => verifyNotices(join(root, "payload"), { [credits]: "notices/Chromium-CREDITS.html" }), /notice/);
  write(root, `${cache}/archive.json`, JSON.stringify({ ...manifest, name: manifest.name.replace("151.3.24", "151.0.0") }));
  assert.throws(() => cefCredits(selected, {}, root), /pinned/);
});

test("extracted native packages need matching app, sidecar, CEF and complete resources", t => {
  const root = fixture(t), selected = targets[3];
  for (const name of ["delidev-desktop.exe", "delidev.exe", "libcef.dll"]) write(root, name, binary("win32", "arm64"));
  for (const name of ["icudtl.dat", "resources.pak", "chrome_100_percent.pak", "chrome_200_percent.pak", "v8_context_snapshot.bin", "locales/en-US.pak"]) write(root, name, "resource");
  verifyNativePayload(root, selected);
  write(root, "delidev.exe", binary("win32", "x64"));
  assert.throws(() => verifyNativePayload(root, selected), /foreign/);
  write(root, "delidev.exe", binary("win32", "arm64"));
  write(root, "locales/en-US.pak", "");
  assert.throws(() => verifyNativePayload(root, selected), /resource/);
  assert.equal(findOneFile(root, "delidev-desktop.exe"), join(root, "delidev-desktop.exe"));
  write(root, "other/delidev-desktop.exe", "duplicate");
  assert.throws(() => findOneFile(root, "delidev-desktop.exe"), /exactly one/);
});

test("Debian verification follows only the exact packaged CEF launcher", { skip: process.platform === "win32" }, t => {
  const root = fixture(t), selected = targets[4];
  for (const name of ["usr/share/DeliDev/delidev-desktop", "usr/bin/delidev", "usr/share/DeliDev/libcef.so"]) write(root, name, binary("linux", "x64"));
  symlinkSync("../share/DeliDev/delidev-desktop", join(root, "usr/bin/delidev-desktop"));
  for (const name of ["icudtl.dat", "resources.pak", "chrome_100_percent.pak", "chrome_200_percent.pak", "v8_context_snapshot.bin", "locales/en-US.pak"]) write(root, `usr/share/DeliDev/${name}`, "resource");
  verifyNativePayload(root, selected);
  rmSync(join(root, "usr/bin/delidev-desktop"));
  symlinkSync("delidev", join(root, "usr/bin/delidev-desktop"));
  assert.throws(() => verifyNativePayload(root, selected), /launcher/);
});


test("a clean CEF cache is prepared by the pinned CLI before notice validation", t => {
  for (const selected of targets) {
    const home = fixture(t);
    const environment = { LOCALAPPDATA: join(home, "local") };
    assert.throws(() => cefCredits(selected, environment, home), /ENOENT/);
    const cache = selected.platform === "darwin" ? join(home, "Library/Caches")
      : selected.platform === "win32" ? environment.LOCALAPPDATA : join(home, ".cache");
    const distribution = selected.platform === "darwin" ? `macos${selected.arch === "arm64" ? "arm64" : "x64"}`
      : selected.platform === "win32" ? `windows${selected.arch === "arm64" ? "arm64" : "64"}` : `linux${selected.arch === "arm64" ? "arm64" : "64"}`;
    const directory = join(cache, "tauri-cef", "151.3.24", selected.cef);
    let prepared = 0;
    const credits = prepareCefCredits(selected, environment, (command, args) => {
      assert.equal(command, process.execPath);
      assert.ok(args[0].endsWith("/scripts/tauri-cli.mjs"));
      assert.equal(args[1], "--");
      assert.ok(!args.includes("cli"));
      assert.ok(args.includes("--no-bundle"));
      assert.ok(args.includes("desktop-host,custom-protocol"));
      if (selected.platform !== "darwin") assert.equal(args[args.indexOf("--target") + 1], selected.target);
      write(directory, "archive.json", JSON.stringify({ type: "minimal", name: `cef_binary_151.3.24+g2384915+chromium-151.0.7922.174_${distribution}_minimal.tar.bz2` }));
      write(directory, "CREDITS.html", "original notices");
      prepared++;
    }, home);
    assert.equal(prepared, 1);
    assert.equal(credits, join(directory, "CREDITS.html"));
    assert.throws(() => prepareCefCredits(selected, environment, () => { throw new Error("preparation failed"); }, home), /preparation failed/);
    write(directory, "archive.json", JSON.stringify({ type: "minimal", name: "unverified.tar.bz2" }));
    assert.throws(() => prepareCefCredits(selected, environment, () => {}, home), /pinned distribution/);
  }
});


test("native and updater preparation share one exclusive checkout lock", t => {
  const root = fixture(t);
  const release = acquireNativeBuildLock(root);
  assert.throws(() => acquireNativeBuildLock(root), /EEXIST/);
  release(); release();
  const next = acquireNativeBuildLock(root);
  assert.throws(() => acquireNativeBuildLock(root), /EEXIST/);
  next();
});

test("revision-bound publication rejects changes after updater assembly", () => {
  const revision = "a".repeat(40);
  verifyPackageRevision(revision, revision + "\n", "");
  assert.throws(() => verifyPackageRevision(revision, "b".repeat(40), ""), /Source changed/);
  assert.throws(() => verifyPackageRevision(revision, revision, " M source.rs\n"), /Source changed/);
});

test("AppImage inspection distinguishes native product bytes from sharun launchers", t => {
  const root = fixture(t), selected = targets[4];
  write(root, "sharun", binary("linux", "x64"));
  for (const name of ["delidev-desktop", "delidev"]) {
    write(root, `shared/bin/${name}`, binary("linux", "x64"));
    write(root, `bin/${name}`, binary("linux", "x64"));
  }
  write(root, "bin/libcef.so", binary("linux", "x64"));
  for (const name of ["icudtl.dat", "resources.pak", "chrome_100_percent.pak", "chrome_200_percent.pak", "v8_context_snapshot.bin", "locales/en-US.pak"]) write(root, `bin/${name}`, "resource");
  verifyAppImagePayload(root, selected);
  write(root, "shared/bin/delidev", binary("linux", "arm64"));
  assert.throws(() => verifyAppImagePayload(root, selected), /foreign/);
  write(root, "shared/bin/delidev", binary("linux", "x64"));
  write(root, "bin/libcef.so", binary("linux", "arm64"));
  assert.throws(() => verifyAppImagePayload(root, selected), /foreign CEF/);
});


test("original CEF notices use a checkout-relative source key for Windows drive safety", t => {
  const root = fixture(t), app = join(root, "apps/delidev"), selected = targets[2];
  const original = write(root, "external-cache/CREDITS.html", "unchanged Chromium notices");
  const source = cefResourcePath(app, root, selected, original);
  assert.equal(source, join("../../..", "target/delidev-package-notices", selected.target, "Chromium-CREDITS.html"));
  const staged = join(app, "src-tauri", source);
  const resources = { [original]: "notices/Chromium-CREDITS.html" };
  write(root, "payload/notices/Chromium-CREDITS.html", "unchanged Chromium notices");
  verifyNotices(join(root, "payload"), resources);
  // The staging key must point to a byte-identical original, independent of
  // the caller's platform-specific absolute cache prefix.
  assert.deepEqual(readFileSync(staged), readFileSync(original));
});
