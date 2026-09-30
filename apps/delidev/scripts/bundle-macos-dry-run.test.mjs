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
  paths.push(...["DeliDevWidget", "DeliDevWidgetSelection"].map(name => `Contents/PlugIns/${name}.appex/Contents/MacOS/${name}`));
  for (const path of paths) { mkdirSync(dirname(join(root,path)), { recursive: true }); writeFileSync(join(root,path), "fixture", { mode: 0o700 }); }
  const run = (command, args) => {
    if (command === "lipo") return "arm64\n";
    if (command.endsWith("PlistBuddy")) {
      const widget = args[2].includes("DeliDevWidget.appex"), intent = args[2].includes("DeliDevWidgetSelection.appex");
      if (args[1].includes("CFBundleIdentifier")) return widget ? "io.delino.delidev.widget" : intent ? "io.delino.delidev.widget.selection" : "io.delino.delidev";
      if (args[1].includes("NSExtensionPointIdentifier")) return widget ? "com.apple.widgetkit-extension" : "com.apple.intents-service";
      return "13.0";
    }
    if (args.includes("--entitlements")) return `<plist><dict><key>com.apple.security.application-groups</key><array><string>group.io.delino.delidev</string></array>${args.at(-1).includes(".appex") ? "<key>com.apple.security.app-sandbox</key><true/>" : ["allow-jit", "allow-unsigned-executable-memory", "disable-library-validation"].map(key => `<key>com.apple.security.cs.${key}</key><true/>`).join("")}</dict></plist>`;
    return args[0] === "--display" ? "Signature=adhoc\n" : "";
  };
  verifyBundle(root, run, "arm64");
  assert.throws(() => verifyBundle(root, run, "x86_64"), /architecture/);
  assert.throws(() => verifyBundle(root, (command,args) => args[1]?.includes("LSMinimumSystemVersion") ? "14.0" : run(command,args), "arm64"), /macOS 13/);
  assert.throws(() => verifyBundle(root, (command,args) => args.includes("--verbose=4") ? "Authority=Fixture" : run(command,args), "arm64"), /ad-hoc/);
  assert.throws(() => verifyBundle(root, (command,args) => args.includes("--entitlements") ? run(command,args).replace("group.io.delino.delidev", "group.foreign") : run(command,args), "arm64"), /entitlements/);
  assert.throws(() => verifyBundle(root, (command,args) => args.includes("--entitlements") && args.at(-1).includes(".appex") ? run(command,args).replace("</dict>", "<key>com.apple.security.network.client</key><true/></dict>") : run(command,args), "arm64"), /entitlements/);
  rmSync(join(root,"Contents/Frameworks/Chromium Embedded Framework.framework/Resources/icudtl.dat"));
  assert.throws(() => verifyBundle(root, run, "arm64"));
});
