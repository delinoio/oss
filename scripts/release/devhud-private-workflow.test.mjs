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

test("private workflow verifies both generated iOS extension product names", () => {
  assert.ok(workflow.includes('test -d "$app/PlugIns/DevHUD Deck.appex"'));
  assert.ok(workflow.includes('test -d "$app/PlugIns/DevHUD Deck Selection.appex"'));
  assert.ok(!workflow.includes('test -d "$app/PlugIns/DevHudWidget.appex"'));
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
