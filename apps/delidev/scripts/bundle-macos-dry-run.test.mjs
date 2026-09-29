import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { dryRunEnvironment, verifyBundle } from "./bundle-macos-dry-run.mjs";

test("dry runs exclude signing, publication and executable-injection authority", () => {
  assert.deepEqual(dryRunEnvironment({ PATH: "/safe/bin", HOME: "/safe/home", CARGO_HOME: "/safe/cargo", APPLE_CERTIFICATE: "fixture", APPLE_PASSWORD: "fixture", APPLE_API_KEY_PATH: "/private/key", TAURI_SIGNING_PRIVATE_KEY: "fixture", GH_TOKEN: "fixture", NODE_OPTIONS: "--require injected", RUSTFLAGS: "injected", DYLD_INSERT_LIBRARIES: "injected", CARGO_TARGET_DIR: "/foreign" }), { PATH: "/safe/bin", HOME: "/safe/home", CARGO_HOME: "/safe/cargo" });
});

test("bundle verification rejects wrong architecture, minimum OS and non-ad-hoc signatures", t => {
  const root = mkdtempSync(join(tmpdir(), "delidev-bundle-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const paths = ["Contents/MacOS/delidev", "Contents/MacOS/delidev-desktop", "Contents/Frameworks/Chromium Embedded Framework.framework/Chromium Embedded Framework", ...["icudtl.dat", "resources.pak", "chrome_100_percent.pak", "chrome_200_percent.pak"].map(name => `Contents/Frameworks/Chromium Embedded Framework.framework/Resources/${name}`), ...["", " (Renderer)", " (GPU)", " (Plugin)", " (Alerts)"].map(suffix => `Contents/Frameworks/delidev-desktop Helper${suffix}.app/Contents/MacOS/delidev-desktop Helper${suffix}`)];
  for (const path of paths) { mkdirSync(dirname(join(root,path)), { recursive: true }); writeFileSync(join(root,path), "fixture", { mode: 0o700 }); }
  const run = (command, args) => command === "lipo" ? "arm64\n" : command.endsWith("PlistBuddy") ? (args[1].includes("CFBundleIdentifier") ? "io.delino.delidev\n" : "13.0\n") : args[0] === "--display" ? "Signature=adhoc\n" : "";
  verifyBundle(root, run, "arm64");
  assert.throws(() => verifyBundle(root, run, "x86_64"), /architecture/);
  assert.throws(() => verifyBundle(root, (command,args) => args[1]?.includes("LSMinimumSystemVersion") ? "14.0" : run(command,args), "arm64"), /macOS 13/);
  assert.throws(() => verifyBundle(root, (command,args) => args[0] === "--display" ? "Authority=Fixture" : run(command,args), "arm64"), /ad-hoc/);
  rmSync(join(root,"Contents/Frameworks/Chromium Embedded Framework.framework/Resources/icudtl.dat"));
  assert.throws(() => verifyBundle(root, run, "arm64"));
});
