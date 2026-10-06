import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import yaml from "js-yaml";

const workflow = readFileSync(fileURLToPath(new URL("../../.github/workflows/package-devhud-private.yml", import.meta.url)), "utf8");
const apiDockerfile = readFileSync(fileURLToPath(new URL("../../servers/devhud-api/Dockerfile", import.meta.url)), "utf8");
const windowsStep = yaml.load(workflow).jobs.desktop.steps.find(({ name }) => name === "Normalize and validate Windows artifact and lifecycle");
const signatureGuard = 'if ($LASTEXITCODE -ne 0) { throw "Authenticode verification failed with exit code $LASTEXITCODE" }';

test("private workflow checks Authenticode status immediately before any installer work", () => {
  assert.equal(windowsStep.shell, "pwsh");
  const lines = windowsStep.run.trim().split("\n");
  const verification = lines.indexOf("signtool.exe verify /pa /all /v $source.FullName");
  assert.ok(verification >= 0);
  assert.equal(lines[verification + 1], signatureGuard);
  for (const command of ["$installDir =", "Start-Process", "smoke:platform", "scan", "devhud-evidence.mjs record"]) {
    assert.ok(windowsStep.run.indexOf(command) > windowsStep.run.indexOf(signatureGuard), command);
  }
  const steps = yaml.load(workflow).jobs.desktop.steps;
  const upload = steps.findIndex(({ uses }) => uses?.startsWith("actions/upload-artifact@"));
  assert.ok(upload > steps.findIndex(({ name }) => name === windowsStep.name));
  assert.equal(steps[upload].if, undefined);
  assert.equal(windowsStep["continue-on-error"], undefined);
});

test("Windows PowerShell blocks packaging after native SignTool failure and proceeds after success", { skip: process.platform !== "win32" }, async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "devhud-signature-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const bin = join(directory, "bin");
  mkdirSync(bin);
  const source = join(directory, "stub.go");
  const signTool = join(bin, "signtool.exe");
  const sentinel = join(bin, "sentinel.exe");
  writeFileSync(source, `package main
import ("os"; "path/filepath"; "strconv")
func main() {
  stage := os.Args[1]
  status := 0
  if filepath.Base(os.Args[0]) == "signtool.exe" {
    if len(os.Args) != 6 || os.Args[1] != "verify" || os.Args[2] != "/pa" || os.Args[3] != "/all" || os.Args[4] != "/v" { os.Exit(90) }
    var err error
    status, err = strconv.Atoi(os.Getenv("DEVHUD_SIGNATURE_STATUS"))
    if err != nil { os.Exit(91) }
  }
  log, err := os.OpenFile(os.Getenv("DEVHUD_SIGNATURE_TRACE"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
  if err != nil { os.Exit(92) }
  if _, err = log.WriteString(stage + "\\n"); err != nil { os.Exit(93) }
  if err = log.Close(); err != nil { os.Exit(94) }
  os.Exit(status)
}
`);
  execFileSync("go", ["build", "-o", signTool, source], { cwd: directory, timeout: 120_000, stdio: "pipe" });
  copyFileSync(signTool, sentinel);
  const installStart = windowsStep.run.indexOf("$installDir =");
  assert.ok(installStart > 0);
  // Execute the real validation prefix. Successful native sentinels model the
  // later stages that used to overwrite SignTool's status. No installer runs.
  const prefix = windowsStep.run.slice(0, installStart);
  const stages = ["install", "sbom", "evidence", "upload"];
  for (const bundle of ["msi", "nsis"]) {
    const artifacts = join(directory, "target", "release", "bundle", bundle);
    mkdirSync(artifacts, { recursive: true });
    writeFileSync(join(artifacts, bundle === "msi" ? "fixture.msi" : "fixture.exe"), "unsigned fixture");
    for (const status of [1, 2, 0]) {
      await t.test(`${bundle}: native verification exits ${status}`, () => {
        const trace = join(directory, `${bundle}-${status}.log`);
        const script = join(directory, `${bundle}-${status}.ps1`);
        writeFileSync(script, [
          "$ErrorActionPreference = 'Stop'",
          "$PSNativeCommandUseErrorActionPreference = $false",
          '$env:PATH = "$env:DEVHUD_SIGNATURE_BIN;$env:PATH"',
          prefix.replaceAll("${{ matrix.bundle }}", bundle),
          ...stages.map((stage) => `& $env:DEVHUD_SIGNATURE_SENTINEL ${stage}`),
          "if (Test-Path -LiteralPath variable:LASTEXITCODE) { exit $LASTEXITCODE }",
        ].join("\n"));
        const result = spawnSync("pwsh", ["-NoLogo", "-NoProfile", "-NonInteractive", "-File", script], {
          cwd: directory,
          encoding: "utf8",
          timeout: 30_000,
          env: { ...process.env, DEVHUD_SIGNATURE_BIN: bin, DEVHUD_SIGNATURE_SENTINEL: sentinel, DEVHUD_SIGNATURE_STATUS: String(status), DEVHUD_SIGNATURE_TRACE: trace },
        });
        assert.ifError(result.error);
        assert.equal(result.signal, null);
        assert.ok(existsSync(trace), "SignTool stub must run");
        const reached = readFileSync(trace, "utf8").trim().split("\n");
        if (status === 0) {
          assert.equal(result.status, 0, result.stderr);
          assert.deepEqual(reached, ["verify", ...stages]);
        } else {
          assert.equal(result.status, 1, result.stderr);
          assert.deepEqual(reached, ["verify"]);
          assert.ok(result.stderr.includes(`Authenticode verification failed with exit code ${status}`));
        }
      });
    }
  }
});

test("private workflow fails immediately when Windows platform smoke fails", () => {
  assert.ok(workflow.includes('pnpm --filter devhud smoke:platform -- --artifact "$installDir\\devhud.exe"\n          if ($LASTEXITCODE -ne 0) { throw "platform smoke failed with exit code $LASTEXITCODE" }'));
});

test("private OCI packaging generates verified administrator assets before Go validation", () => {
  const oci = workflow.slice(
    workflow.indexOf("\n  oci:"),
    workflow.indexOf("\n  assemble:"),
  );
  const install = "pnpm install --frozen-lockfile --ignore-scripts";
  const assets = "pnpm --filter devhud-admin build:embedded";
  const goTest = "go test ./servers/devhud-api/...";
  const dockerBuild = "docker buildx build";
  for (const command of [install, assets, goTest, dockerBuild]) {
    assert.ok(oci.includes(command), `missing OCI prerequisite: ${command}`);
  }
  assert.ok(oci.indexOf(install) < oci.indexOf(assets));
  assert.ok(oci.indexOf(assets) < oci.indexOf(goTest));
  assert.ok(oci.indexOf(goTest) < oci.indexOf(dockerBuild));
});

test("API Docker builds own one verified bundle for both binaries", () => {
  assert.match(apiDockerfile, /FROM --platform=\$BUILDPLATFORM node:24-bookworm-slim AS administrator-assets/u);
  assert.match(apiDockerfile, /pnpm --filter devhud-admin build:embedded/u);
  assert.match(
    apiDockerfile,
    /COPY --from=administrator-assets \/src\/servers\/devhud-api\/internal\/adminassets\/dist \.\/servers\/devhud-api\/internal\/adminassets\/dist/u,
  );
  assert.equal(
    apiDockerfile.split("COPY --from=administrator-assets").length - 1,
    1,
  );
  assert.ok(
    apiDockerfile.indexOf("COPY --from=administrator-assets") <
      apiDockerfile.indexOf("go build -trimpath"),
  );
});

test("private workflow validates AppImage sandbox metadata before preparing its smoke layout", () => {
  const ubuntu = workflow.slice(workflow.indexOf("- name: Normalize and validate Ubuntu artifact and lifecycle"), workflow.indexOf("\n      - uses: actions/upload-artifact@v7", workflow.indexOf("- name: Normalize and validate Ubuntu artifact and lifecycle")));
  const metadataInspection = 'sandbox_metadata=$(unsquashfs -lln -o "$offset" "$source" shared/bin/chrome-sandbox';
  const extraction = '"$source" --appimage-extract';
  const repair = 'sudo chown root:root "$sandbox"';
  assert.ok(workflow.includes("squashfs-tools"));
  for (const command of [
    'offset=$("$source" --appimage-offset)',
    metadataInspection,
    'if [ "$sandbox_metadata" != "-rwsr-xr-x 0/0" ]',
    "appdir=$(realpath squashfs-root)",
    'sandbox=$(realpath "$appdir/shared/bin/chrome-sandbox")',
    repair,
    'sudo chmod 4755 "$sandbox"',
    'executable=$(realpath "$appdir/bin/devhud")',
    'host=$(realpath "$appdir/bin/devhud-native-messaging-host")',
    'smoke:platform -- --artifact "$executable"',
  ]) assert.ok(workflow.includes(command), `missing AppImage validation command: ${command}`);
  assert.ok(ubuntu.indexOf(metadataInspection) < ubuntu.indexOf(extraction));
  assert.ok(ubuntu.indexOf(extraction) < ubuntu.indexOf(repair));
  assert.ok(ubuntu.includes('appimage=$(realpath "$RUNNER_TEMP/devhud-installed/DevHUD.AppImage")'));
  assert.ok(ubuntu.includes('APPIMAGE="$appimage" APPDIR="$appdir" dbus-run-session -- xvfb-run -a /usr/bin/python3 apps/devhud/scripts/with-linux-smoke-tray.py pnpm --filter devhud smoke:platform -- --artifact "$executable"'));
  assert.ok(!workflow.includes('smoke:platform -- --artifact "$RUNNER_TEMP/devhud-installed/DevHUD.AppImage"'));
});

test("private workflow compiles the exact installed package kind for every desktop row", () => {
  const packageKind = 'DEVHUD_PACKAGE_KIND: "${{ startsWith(matrix.id, \'macos-\') && \'macos-app\' || startsWith(matrix.id, \'windows-\') && format(\'windows-{0}\', matrix.bundle) || format(\'linux-{0}\', matrix.bundle) }}"';
  assert.ok(workflow.includes(packageKind));
  assert.ok(!workflow.includes("format('linux-{0}', matrix.bundle) || ''"));
});

test("private workflow creates component SBOMs from package-aware scan targets", () => {
  const assemble = workflow.slice(workflow.indexOf("\n  assemble:"));
  assert.equal(workflow.split("uses: anchore/sbom-action/download-syft@v0.21.0").length - 1, 4);
  for (const target of [
    'scan "dir:$app"',
    'scan "dir:$installDir"',
    'scan "dir:$sbom_root"',
    'scan "dir:$appdir"',
    "scan dir:apps/devhud-chrome-extension/dist",
    'scan "oci-archive:private-artifacts/${{ matrix.artifact }}"',
  ]) assert.ok(workflow.includes(target), `missing package-aware SBOM target: ${target}`);
  assert.ok(workflow.includes("devhud-android-arm64-armv7-google-play.aab.spdx.json"));
  assert.ok(workflow.includes("devhud-ios-arm64-app-store.ipa.spdx.json"));
  assert.ok(!assemble.includes("download-syft"));
  assert.ok(!workflow.includes('scan "file:'));
});

test("private workflow keeps each Linux Native Messaging lifecycle inside a D-Bus session", () => {
  const ubuntu = workflow.slice(workflow.indexOf("- name: Normalize and validate Ubuntu artifact and lifecycle"), workflow.indexOf("\n      - uses: actions/upload-artifact@v7", workflow.indexOf("- name: Normalize and validate Ubuntu artifact and lifecycle")));
  const lifecycle = ubuntu.slice(ubuntu.indexOf("run_native_messaging_lifecycle()"), ubuntu.indexOf("          source=$(find"));
  assert.ok(workflow.includes("dbus-x11 gnome-keyring libayatana-appindicator3-dev"));
  assert.ok(lifecycle.includes("dbus-run-session -- bash -euo pipefail -c"));
  assert.ok(lifecycle.includes("gnome-keyring-daemon --foreground --components=secrets"));
  assert.ok(lifecycle.includes("trap stop_keyring EXIT"));
  assert.ok(lifecycle.indexOf("gdbus wait --session --timeout=5 org.freedesktop.secrets") < lifecycle.indexOf('"$host" register "$host"'));
  assert.ok(lifecycle.indexOf('"$host" register "$host"') < lifecycle.indexOf('"$host" unregister "$user_manifest"'));
  assert.ok(lifecycle.indexOf('"$host" unregister "$user_manifest"') < lifecycle.indexOf('test ! -e "$user_manifest"'));
  assert.equal(ubuntu.split('run_native_messaging_lifecycle "$host" "$user_manifest"').length - 1, 2);
});

test("private workflow validates the combined Android App Bundle once", () => {
  const command = 'verify:mobile -- --android-artifact "$PWD/private-artifacts/devhud-android-arm64-armv7-google-play.aab" --android-abi arm64-v8a --android-abi armeabi-v7a --bundletool-jar "${{ steps.bundletool.outputs.jar }}"';
  assert.equal(workflow.split(command).length - 1, 1);
});

test("private workflow inspects the packaged Android manifest before widget evidence", () => {
  const mobile = workflow.slice(workflow.indexOf("\n  mobile:"), workflow.indexOf("\n  oci:"));
  const download = "Download checksum-pinned bundletool";
  const checksum = 'clibox hash verify "$expected" --input="$jar" --quiet';
  const verification = "--bundletool-jar \"${{ steps.bundletool.outputs.jar }}\"";
  const evidence = "record --id android-google-play";
  assert.ok(mobile.includes(download) && mobile.includes(checksum));
  assert.ok(mobile.indexOf(download) < mobile.indexOf(verification));
  assert.ok(mobile.indexOf(verification) < mobile.indexOf(evidence));
});

test("private workflow installs native prerequisites before desktop and mobile builds", () => {
  const desktop = workflow.slice(workflow.indexOf("\n  desktop:"), workflow.indexOf("\n  ios-simulators:"));
  const simulators = workflow.slice(workflow.indexOf("\n  ios-simulators:"), workflow.indexOf("\n  extension:"));
  const mobile = workflow.slice(workflow.indexOf("\n  mobile:"), workflow.indexOf("\n  oci:"));
  const appleTargets = "rustup target add aarch64-apple-darwin x86_64-apple-darwin";
  assert.equal(desktop.split(appleTargets).length - 1, 1);
  assert.equal(simulators.split(appleTargets).length - 1, 1);
  assert.ok(simulators.indexOf(appleTargets) < simulators.indexOf("run-mobile.mjs ios build"));
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
