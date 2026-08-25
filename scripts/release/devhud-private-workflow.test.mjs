import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";

const workflow = readFileSync(fileURLToPath(new URL("../../.github/workflows/package-devhud-private.yml", import.meta.url)), "utf8");

test("private workflow fails immediately when Windows platform smoke fails", () => {
  assert.ok(workflow.includes('pnpm --filter devhud smoke:platform -- --artifact "$installDir\\devhud.exe"\n          if ($LASTEXITCODE -ne 0) { throw "platform smoke failed with exit code $LASTEXITCODE" }'));
});

test("private workflow validates AppImage sandbox metadata before preparing its smoke layout", () => {
  const ubuntu = workflow.slice(workflow.indexOf("- name: Normalize and validate Ubuntu artifact and lifecycle"), workflow.indexOf("\n      - uses: actions/upload-artifact@v7", workflow.indexOf("- name: Normalize and validate Ubuntu artifact and lifecycle")));
  const metadataInspection = 'sandbox_metadata=$(unsquashfs -lln -o "$offset" "$source" usr/share/DevHUD/chrome-sandbox';
  const extraction = '"$source" --appimage-extract';
  const repair = 'sudo chown root:root "$sandbox"';
  assert.ok(workflow.includes("squashfs-tools"));
  for (const command of [
    'offset=$("$source" --appimage-offset)',
    metadataInspection,
    'if [ "$sandbox_metadata" != "-rwsr-xr-x 0/0" ]',
    "sandbox=$(realpath squashfs-root/usr/share/DevHUD/chrome-sandbox)",
    repair,
    'sudo chmod 4755 "$sandbox"',
    "executable=$(realpath squashfs-root/usr/bin/devhud)",
    'smoke:platform -- --artifact "$executable"',
  ]) assert.ok(workflow.includes(command), `missing AppImage validation command: ${command}`);
  assert.ok(ubuntu.indexOf(metadataInspection) < ubuntu.indexOf(extraction));
  assert.ok(ubuntu.indexOf(extraction) < ubuntu.indexOf(repair));
  assert.ok(!workflow.includes('smoke:platform -- --artifact "$RUNNER_TEMP/devhud-installed/DevHUD.AppImage"'));
});

test("private workflow validates the combined Android App Bundle once", () => {
  const command = 'verify:mobile -- --android-artifact "$PWD/private-artifacts/devhud-android-arm64-armv7-google-play.aab" --android-abi arm64-v8a --android-abi armeabi-v7a --bundletool-jar "${{ steps.bundletool.outputs.jar }}"';
  assert.equal(workflow.split(command).length - 1, 1);
});

test("private workflow inspects the packaged Android manifest before widget evidence", () => {
  const mobile = workflow.slice(workflow.indexOf("\n  mobile:"), workflow.indexOf("\n  oci:"));
  const download = "Download checksum-pinned bundletool";
  const checksum = "sha256sum --check";
  const verification = "--bundletool-jar \"${{ steps.bundletool.outputs.jar }}\"";
  const evidence = "record --id android-google-play";
  assert.ok(mobile.includes(download) && mobile.includes(checksum));
  assert.ok(mobile.indexOf(download) < mobile.indexOf(verification));
  assert.ok(mobile.indexOf(verification) < mobile.indexOf(evidence));
});

test("private workflow installs native prerequisites before desktop and mobile builds", () => {
  const desktop = workflow.slice(workflow.indexOf("\n  desktop:"), workflow.indexOf("\n  extension:"));
  const mobile = workflow.slice(workflow.indexOf("\n  mobile:"), workflow.indexOf("\n  oci:"));
  const appleTargets = "rustup target add aarch64-apple-darwin x86_64-apple-darwin";
  assert.equal(desktop.split(appleTargets).length - 1, 1);
  assert.equal(mobile.split(appleTargets).length - 1, 1);
  assert.ok(mobile.includes("uses: actions/setup-java@v5"));
  assert.ok(mobile.includes("distribution: temurin\n          java-version: \"17\""));
  for (const dependency of ["clang", "cmake", "libayatana-appindicator3-dev", "libgbm-dev", "libgtk-3-dev", "libx11-dev", "libxtst-dev", "ninja-build"]) {
    assert.ok(mobile.includes(dependency), `missing Android host prerequisite: ${dependency}`);
  }
  assert.ok(mobile.indexOf("uses: actions/setup-java@v5") < mobile.indexOf("mobile:generate"));
  assert.ok(mobile.indexOf("Install Android Linux host prerequisites") < mobile.indexOf("mobile:generate"));
});

test("private workflow verifies Android signatures without public PKIX trust", () => {
  assert.ok(workflow.includes("jarsigner -verify -certs private-artifacts/devhud-android-arm64-armv7-google-play.aab"));
  assert.ok(!workflow.includes("jarsigner -verify -strict"));
  assert.ok(workflow.includes('test -n "$actual" && test "$actual" = "$expected"'));
});

test("private workflow verifies both generated iOS extension product names", () => {
  assert.ok(workflow.includes('test -d "$app/PlugIns/DevHUD Deck.appex"'));
  assert.ok(workflow.includes('test -d "$app/PlugIns/DevHUD Deck Selection.appex"'));
  assert.ok(!workflow.includes('test -d "$app/PlugIns/DevHudWidget.appex"'));
});

test("private workflow validates App Store signing policy before packaging and evidence", () => {
  const mobile = workflow.slice(workflow.indexOf("\n  mobile:"), workflow.indexOf("\n  oci:"));
  const validation = 'node scripts/release/validate-devhud-ios-signing.mjs --app "$app" --team-id "$DEVHUD_APPLE_TEAM_ID"';
  assert.ok(mobile.includes(validation));
  assert.ok(mobile.indexOf(validation) < mobile.indexOf("devhud-ios-arm64-app-store.ipa"));
  assert.ok(mobile.indexOf(validation) < mobile.indexOf("record --id ios-app-store"));
});

test("private workflow runs PostgreSQL schema readiness before recording OCI evidence", () => {
  const ociJob = workflow.slice(workflow.indexOf("\n  oci:"), workflow.indexOf("\n  assemble:"));
  const integration = "go test -tags=integration ./servers/devhud-api/internal/postgres";
  const evidence = "node scripts/release/devhud-evidence.mjs record --id ${{ matrix.id }}";
  assert.ok(ociJob.includes("image: postgres:15-bookworm"));
  assert.ok(ociJob.includes("DEVHUD_TEST_DATABASE_URL: postgres://devhud:devhud@127.0.0.1:5432/devhud_api_test?sslmode=disable"));
  assert.ok(ociJob.indexOf(integration) < ociJob.indexOf(evidence));
});

test("private workflow binds provenance to its run attempt and actual timestamps", () => {
  assert.ok(workflow.includes("started_on: ${{ steps.invocation.outputs.started_on }}"));
  assert.ok(workflow.includes("https://github.com/${{ github.repository }}/actions/runs/${{ github.run_id }}/attempts/${{ github.run_attempt }}"));
  for (const argument of ["--invocation-id", "--started-on", "--finished-on"]) assert.ok(workflow.includes(argument));
});
