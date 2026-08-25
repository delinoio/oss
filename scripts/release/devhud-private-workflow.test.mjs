import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";

const workflow = readFileSync(fileURLToPath(new URL("../../.github/workflows/package-devhud-private.yml", import.meta.url)), "utf8");

test("private workflow fails immediately when Windows platform smoke fails", () => {
  assert.ok(workflow.includes('pnpm --filter devhud smoke:platform -- --artifact "$installDir\\devhud.exe"\n          if ($LASTEXITCODE -ne 0) { throw "platform smoke failed with exit code $LASTEXITCODE" }'));
});

test("private workflow smokes the root-prepared extracted AppImage layout", () => {
  for (const command of [
    "sandbox=$(realpath squashfs-root/usr/share/DevHUD/chrome-sandbox)",
    'sudo chown root:root "$sandbox"',
    'sudo chmod 4755 "$sandbox"',
    "executable=$(realpath squashfs-root/usr/bin/devhud)",
    'smoke:platform -- --artifact "$executable"',
  ]) assert.ok(workflow.includes(command), `missing AppImage validation command: ${command}`);
  assert.ok(!workflow.includes('smoke:platform -- --artifact "$RUNNER_TEMP/devhud-installed/DevHUD.AppImage"'));
});

test("private workflow validates the combined Android App Bundle once", () => {
  const command = 'verify:mobile -- --android-artifact "$PWD/private-artifacts/devhud-android-arm64-armv7-google-play.aab" --android-abi arm64-v8a --android-abi armeabi-v7a';
  assert.equal(workflow.split(command).length - 1, 1);
});
