import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { load } from "js-yaml";
import { jobPaths } from "./plan.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
const workflowSource = readFileSync(`${root}/.github/workflows/CI.yml`, "utf8");
const apiDockerfileSource = readFileSync(`${root}/servers/devhud-api/Dockerfile`, "utf8");
const workflow = load(workflowSource);
const packages = Object.fromEntries([
  "package.json",
  "apps/devhud/package.json",
  "apps/devhud-admin/package.json",
  "apps/devhud-chrome-extension/package.json",
  "apps/public-docs/package.json",
  "packages/devhud-api-client/package.json",
  "servers/devhud-api/package.json",
].map((path) => [path, JSON.parse(readFileSync(`${root}/${path}`, "utf8"))]));
const turbo = JSON.parse(readFileSync(`${root}/turbo.json`, "utf8"));
const adminTurbo = JSON.parse(readFileSync(`${root}/apps/devhud-admin/turbo.json`, "utf8"));
const extensionTurbo = JSON.parse(readFileSync(`${root}/apps/devhud-chrome-extension/turbo.json`, "utf8"));
const devhudTauri = JSON.parse(readFileSync(`${root}/apps/devhud/src-tauri/tauri.conf.json`, "utf8"));

const legacyJobs = [
  "go-quality", "go-test", "repository-environment", "rust-fmt", "rust-clippy", "rust-test",
  "forge-test", "forge-render", "react-forge", "react-forge-scenes", "linux-packages", "node-public-docs-test", "node-clibox-test", "node-pnport-test", "pnport-native",
];
const devhudJobs = [
  "devhud-frontend", "devhud-extension", "devhud-rust-conformance", "devhud-security", "devhud-desktop",
  "devhud-mobile-contracts", "devhud-ios-simulator", "devhud-android-emulator", "devhud-protocol", "devhud-admin",
  "devhud-api", "devhud-oci", "devhud-supply-chain", "devhud-release-contracts",
];
const achJobs = ["async-commit-hook"];

function step(job, id) {
  return job.steps.find((candidate) => candidate.id === id);
}

function namedStep(job, name) {
  return job.steps.find((candidate) => candidate.name === name);
}

test("async-commit-hook retains runner, interface, protocol and unsigned archive validation", () => {
  const commands = workflow.jobs["async-commit-hook"].steps.map(({ run }) => run ?? "").join("\n");
  for (const command of [
    "pnpm --filter async-commit-hook build:embedded", "go test -race ./cmds/async-commit-hook/...", "pnpm --filter async-commit-hook test", "pnpm --filter public-docs test",
    "pnpm --filter @delinoio/async-commit-hook-api-client test", "pnpm proto:check",
    "node --test scripts/release/async-commit-hook.test.mjs",
    'python3 scripts/release/build-async-commit-hook.py --output "$RUNNER_TEMP/ach-release"',
  ]) assert.ok(commands.includes(command), command);
  assert.equal(commands.match(/pnpm install --frozen-lockfile --ignore-scripts/gu)?.length, 1);
});

test("CI keeps every legacy check and aggregates every required job", () => {
  const jobs = Object.keys(workflow.jobs);
  for (const id of ["ci-contracts", ...legacyJobs, ...devhudJobs, ...achJobs, "ci-result"]) assert.ok(jobs.includes(id), id);
  const required = jobs.filter((id) => id !== "ci-result").sort();
  assert.deepEqual([...workflow.jobs["ci-result"].needs].sort(), required);
  assert.equal(workflow.jobs["ci-result"].if, "always()");
  const result = namedStep(workflow.jobs["ci-result"], "Validate every planned result");
  assert.equal(result.run, "node scripts/ci/result.mjs");
  assert.equal(result.env.CI_NEEDS, "${{ toJSON(needs) }}");
});

test("one change plan gates every domain job before runner allocation", () => {
  assert.equal(workflow.jobs.changes.steps.find(({ id }) => id === "plan").run, "node scripts/ci/plan.mjs");
  assert.deepEqual(Object.keys(jobPaths).sort(), [...legacyJobs, ...devhudJobs, ...achJobs].sort());
  for (const id of [...legacyJobs, ...devhudJobs, ...achJobs]) {
    const job = workflow.jobs[id];
    assert.deepEqual(job.needs, id === "react-forge-scenes" ? ["changes", "react-forge"] : "changes", id);
    assert.equal(job.if, "${{ needs.changes.result == 'success' && fromJSON(needs.changes.outputs.jobs)['" + id + "'] }}", id);
    assert.equal(step(job, "filter"), undefined, id);
    assert.equal(step(job, "gate"), undefined, id);
  }
  const filters = JSON.stringify(jobPaths);
  for (const path of [
    "servers/**", "protos/**", "packages/**", "apps/devhud/**", "apps/devhud-admin/**",
    "apps/devhud-chrome-extension/**", "crates/devhud-native-messaging-host/**", "packaging/devhud/**",
    "apps/public-docs/**", ".github/workflows/package-devhud-private.yml", ".github/workflows/release-devhud.yml",
    ".github/workflows/devhud-cef-security-review.yml", "scripts/ci/check-go-format.mjs", ".dockerignore",
  ]) assert.ok(filters.includes(path), path);
  for (const id of ["devhud-frontend", "devhud-extension", "devhud-security", "devhud-admin", "devhud-api", "devhud-protocol", "repository-environment"]) {
    for (const path of [".nvmrc", "package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "turbo.json"]) {
      assert.ok(jobPaths[id].paths.includes(path), `${id}: ${path}`);
    }
  }
});

test("Node jobs use one frozen install and the planner's exact comparison through committed Turbo", () => {
  assert.doesNotMatch(workflowSource, /pnpm\s+dlx\s+turbo/iu);
  assert.match(packages["package.json"].scripts["ci:affected"], /^turbo run$/u);
  for (const [id, rule] of Object.entries(jobPaths).filter(([, rule]) => rule.workspace)) {
    const job = workflow.jobs[id];
    const run = job.steps.find(({ run }) => run?.includes("scripts/ci/run-affected.mjs"));
    assert.equal(run.env.TURBO_SCM_BASE, "${{ needs.changes.outputs.base }}");
    assert.equal(run.env.TURBO_SCM_HEAD, "${{ needs.changes.outputs.head }}");
    assert.equal(run.env.FORCE_RUN, "${{ fromJSON(needs.changes.outputs.forced)['" + id + "'] }}");
    assert.ok(run.run.includes(rule.workspace));
    assert.equal(job.steps.filter(({ run }) => run?.includes("pnpm install")).length, 1, id);
  }
  for (const job of Object.values(workflow.jobs)) {
    for (const { run } of job.steps) {
      if (run?.includes("pnpm install")) assert.doesNotMatch(run, /pnpm install --frozen-lockfile(?! --ignore-scripts)/u);
    }
  }
  const frontend = workflow.jobs["devhud-frontend"];
  assert.equal(namedStep(frontend, "Run affected frontend contracts").run, "node scripts/ci/run-affected.mjs devhud test");
  assert.match(namedStep(frontend, "Verify immutable desktop and mobile pins").run, /verify:pins/u);
  const testScript = packages["apps/devhud/package.json"].scripts.test;
  for (const fixture of ["verify-frontend-output.mjs", "run-tauri.test.mjs", "run-mobile.test.mjs", "verify-pins-policy.test.mjs", "mobile:check"]) assert.ok(testScript.includes(fixture), fixture);
});

test("caches restore on PRs and save only after successful main validation", () => {
  for (const path of ["CI.yml", "runmoor.yml"]) {
    const { jobs } = load(readFileSync(`${root}/.github/workflows/${path}`, "utf8"));
    for (const [id, job] of Object.entries(jobs)) {
      for (const kind of ["node", "go"]) {
        const setup = job.steps.find(({ uses }) => uses === `./.github/actions/setup-ci-${kind}`);
        if (!setup) continue;
        const save = job.steps.find(({ with: inputs, uses }) => uses === "actions/cache/save@v5" && inputs.key.includes(`ci-${kind}`));
        assert.ok(save, `${id}: ${kind}`);
        assert.match(save.if, /success\(\) && github.ref == 'refs\/heads\/main'/u);
        assert.ok(job.steps.indexOf(save) > job.steps.indexOf(setup));
      }
      for (const candidate of job.steps.filter(({ uses }) => uses === "Swatinem/rust-cache@v2")) {
        assert.equal(candidate.with["save-if"], "${{ github.ref == 'refs/heads/main' }}");
        assert.equal(candidate.with["cache-on-failure"], false);
      }
    }
  }
  assert.ok(!workflow.jobs["rust-fmt"].steps.some(({ uses }) => uses?.includes("cache")));
  for (const kind of ["node", "go"]) {
    const action = load(readFileSync(`${root}/.github/actions/setup-ci-${kind}/action.yml`, "utf8"));
    assert.ok(!action.runs.steps.some(({ uses }) => uses === "actions/cache/save@v5"));
    const cache = action.runs.steps.find(({ uses }) => uses === "actions/cache/restore@v5");
    for (const marker of ["runner.os", "runner.arch", "hashFiles", kind === "node" ? "node-version" : "go-version"]) assert.ok(cache.with.key.includes(marker), marker);
    const setup = action.runs.steps.find(({ uses }) => uses?.startsWith(`actions/setup-${kind}@`));
    assert.equal(setup.with[kind === "node" ? "package-manager-cache" : "cache"], false);
  }
});

test("DevHud desktop retries its locked Rust fetch before offline contract verification", () => {
  const desktop = workflow.jobs["devhud-desktop"];
  const cache = namedStep(desktop, "Cache Rust dependencies");
  const fetch = namedStep(desktop, "Fetch locked Rust dependencies");
  const verify = namedStep(desktop, "Verify frontend and immutable pins");

  assert.equal(cache.uses, "Swatinem/rust-cache@v2");
  assert.equal(cache.with["save-if"], "${{ github.ref == 'refs/heads/main' }}");
  assert.equal(cache.with["cache-on-failure"], false);
  assert.equal(cache.with["cache-targets"], false);
  for (const expected of ["for attempt in 1 2 3", "cargo fetch --locked", "attempt * 15", "Cargo dependency fetch failed after three attempts"]) {
    assert.ok(fetch.run.includes(expected), expected);
  }
  assert.equal(verify.env.CARGO_NET_OFFLINE, "true");
  assert.ok(desktop.steps.indexOf(cache) < desktop.steps.indexOf(fetch));
  assert.ok(desktop.steps.indexOf(fetch) < desktop.steps.indexOf(verify));
});

test("DevHud API PostgreSQL runs only inside its selected domain job", () => {
  const job = workflow.jobs["devhud-api"];
  assert.equal(job.services, undefined);
  const startIndex = job.steps.findIndex((candidate) => candidate.name === "Start PostgreSQL");
  const integrationIndex = job.steps.findIndex((candidate) => candidate.name === "Run PostgreSQL migration and integration tests");
  const stopIndex = job.steps.findIndex((candidate) => candidate.name === "Stop PostgreSQL");
  assert.ok(startIndex >= 0 && startIndex < integrationIndex && integrationIndex < stopIndex);
  const startPostgreSQL = job.steps[startIndex];
  assert.equal(startPostgreSQL.if, undefined);
  for (const expected of [
    "docker run --detach", "postgres:15-bookworm", "--publish 5432:5432", "pg_isready",
    "docker inspect", "State.Health.Status", "docker logs devhud-postgres",
  ]) assert.ok(startPostgreSQL.run.includes(expected), expected);
  const stopPostgreSQL = job.steps[stopIndex];
  assert.equal(stopPostgreSQL.if, "${{ always() }}");
  assert.match(stopPostgreSQL.run, /docker rm --force devhud-postgres/u);
});

test("desktop and mobile matrices match the committed architecture contracts", () => {
  const desktop = workflow.jobs["devhud-desktop"].strategy.matrix.include.map(({ id }) => id);
  assert.deepEqual(desktop, [
    "macos-x64", "macos-arm64", "windows-x64-nsis", "windows-x64-msi", "windows-arm64-nsis",
    "windows-arm64-msi", "ubuntu-x64-deb", "ubuntu-x64-appimage", "ubuntu-arm64-deb", "ubuntu-arm64-appimage",
  ]);
  const platforms = JSON.parse(readFileSync(`${root}/apps/devhud/platforms.json`, "utf8"));
  for (const target of platforms.targets) assert.ok(desktop.some((id) => id === target.id || id.startsWith(`${target.id}-`)), target.id);
  assert.deepEqual(workflow.jobs["devhud-ios-simulator"].strategy.matrix.include.map(({ target }) => target), ["aarch64", "aarch64-sim", "x86_64"]);
  const android = workflow.jobs["devhud-android-emulator"].strategy.matrix.include;
  assert.deepEqual(android.map(({ target }) => target), ["aarch64", "armv7", "x86_64", "aarch64-armv7"]);
  assert.deepEqual(android.find(({ target }) => target === "aarch64-armv7"), {
    target: "aarch64-armv7",
    artifacts: "--aab",
    production: true,
    combined: true,
  });
  const combinedAndroid = namedStep(workflow.jobs["devhud-android-emulator"], "Build combined production Android App Bundle");
  assert.match(combinedAndroid.run, /android build --target aarch64 --target armv7 --aab/u);
  const combinedAndroidVerification = namedStep(workflow.jobs["devhud-android-emulator"], "Verify combined production Android App Bundle contracts");
  assert.match(combinedAndroidVerification.run, /--android-abi arm64-v8a --android-abi armeabi-v7a/u);
  assert.deepEqual(workflow.jobs["devhud-oci"].strategy.matrix.target, ["api", "sweeper"]);
  assert.deepEqual(devhudTauri.bundle.icon, ["icons/icon.png", "icons/icon.ico"]);
  for (const icon of devhudTauri.bundle.icon) {
    assert.ok(readFileSync(`${root}/apps/devhud/src-tauri/${icon}`).length > 0, icon);
  }
});

test("implemented DevHud conformance commands are wired to their owning jobs", () => {
  const commands = new Map([
    ["devhud-protocol", ["proto:check", "go test ./protos/", "@delinoio/devhud-api-client"]],
    ["devhud-api", ["ci:format", "ci:vet", "ci:build", "ci:unit", "ci:migrations", "ci:integration", "ci:api", "ci:sweeper"]],
    ["devhud-rust-conformance", ["test:native:capture", "test:native:shortcuts", "test:native:ipc", "test:native:updater"]],
    ["devhud-frontend", ["run-affected.mjs devhud test", "verify:pins"]],
    ["devhud-admin", ["devhud-admin --fail-if-no-match test"]],
    ["devhud-extension", ["test:unit", "test:components", "test:accessibility", "test:package", "devhud-chrome-web-store.zip", "devhud-chrome-github-validation.zip"]],
    ["devhud-security", ["test:security", "test:adapters", "diagnostics-policy.test.mjs", "native-bridge.test.mjs", "mobile-policy.test.mjs"]],
    ["devhud-desktop", ["verify:pins", "smoke:platform", "xvfb-run", "io.delino.devhud.native_messaging"]],
    ["devhud-mobile-contracts", ["mobile:check", "test:components"]],
    ["devhud-ios-simulator", ["mobile:generate", "run-mobile.mjs ios build"]],
    ["devhud-android-emulator", ["mobile:generate", "run-mobile.mjs android build", "--bundletool-jar"]],
    ["devhud-supply-chain", ["finalize-devhud-deb.test.mjs", "generate-devhud-supply-chain.test.mjs", "generate-devhud-updater.test.mjs", "validate-devhud-private-build.test.mjs"]],
    ["devhud-release-contracts", ["scripts/release/*.test.mjs"]],
  ]);
  for (const [id, expected] of commands) {
    const source = JSON.stringify(workflow.jobs[id]);
    for (const command of expected) assert.ok(source.includes(command), `${id}: ${command}`);
  }
  const apiCommands = workflow.jobs["devhud-api"].steps
    .filter((candidate) => typeof candidate.run === "string")
    .flatMap((candidate) => candidate.run.split("\n"))
    .filter((line) => line.includes("pnpm --filter @delinoio/devhud-api"));
  assert.equal(apiCommands.length, 8);
  for (const command of apiCommands) assert.match(command, /--fail-if-no-match ci:/u);
  assert.match(packages["apps/devhud/package.json"].scripts.test, /^pnpm lint && pnpm test:unit && pnpm test:components/u);
  assert.match(JSON.stringify(workflow.jobs["devhud-security"]), /pnpm exec turbo run test:security test:adapters --filter devhud/u);
});

test("OCI validation is multi-architecture, non-root, migration-bearing, and local-only", () => {
  const job = workflow.jobs["devhud-oci"];
  const source = JSON.stringify(job);
  for (const expected of [
    "linux/amd64,linux/arm64", "type=oci", "65532", "io.delino.devhud.migrations",
    "io.delino.devhud.administrator-assets", "spdx-json", "packages | length > 0",
    "go test -tags=integration ./servers/devhud-api/internal/postgres", "--load", "--platform linux/amd64", "docker:$image",
    "--user 65532:65532", "devhud-api migrate", "migrate", "--once",
  ]) assert.ok(source.includes(expected), expected);
  assert.equal(job.services, undefined);
  const startPostgreSQL = namedStep(job, "Start PostgreSQL");
  assert.equal(startPostgreSQL.if, undefined);
  for (const expected of [
    "docker run --detach", "postgres:15-bookworm", "--publish 5432:5432", "pg_isready",
    "docker inspect", "State.Health.Status", "docker logs devhud-postgres",
  ]) assert.ok(startPostgreSQL.run.includes(expected), expected);
  const sweeperAssetCondition = "${{ matrix.target == 'sweeper' }}";
  const setupNode = namedStep(job, "Setup Node.js");
  const generateAssets = namedStep(job, "Generate and verify embedded administrator assets");
  for (const step of [setupNode, generateAssets]) assert.equal(step.if, sweeperAssetCondition);
  assert.equal(setupNode.uses, "./.github/actions/setup-ci-node");
  assert.match(generateAssets.run, /pnpm install --frozen-lockfile --ignore-scripts/u);
  assert.match(generateAssets.run, /pnpm --filter devhud-admin build:embedded/u);
  const generateAssetsIndex = job.steps.indexOf(generateAssets);
  const buildAndInspectIndex = job.steps.findIndex(({ name }) => name === "Build and inspect amd64/arm64 OCI layout");
  assert.ok(generateAssetsIndex >= 0 && generateAssetsIndex < buildAndInspectIndex);
  const stopPostgreSQL = namedStep(job, "Stop PostgreSQL");
  assert.equal(stopPostgreSQL.if, "${{ always() }}");
  assert.match(stopPostgreSQL.run, /docker rm --force devhud-postgres/u);
  const buildAndInspect = namedStep(job, "Build and inspect amd64/arm64 OCI layout").run;
  assert.match(
    buildAndInspect,
    /if \[ "\$OCI_TARGET" = api \]; then\s+docker run "\$\{docker_args\[@\]\}" "\$image" migrate\s+else\s+DEVHUD_DATABASE_URL="\$DEVHUD_TEST_DATABASE_URL" go run \.\/servers\/devhud-api\/cmd\/devhud-api migrate/u,
  );
  assert.doesNotMatch(source, /(?:docker|skopeo) push/iu);
  assert.doesNotMatch(source, /docker-daemon:/u);
  assert.match(apiDockerfileSource, /^FROM --platform=\$BUILDPLATFORM golang:/mu);
  const moduleGoVersion = readFileSync(`${root}/go.mod`, "utf8").match(/^go (\S+)$/mu)?.[1];
  const imageGoVersion = apiDockerfileSource.match(/^FROM --platform=\$BUILDPLATFORM golang:([\d.]+)-bookworm AS build$/mu)?.[1];
  assert.ok(moduleGoVersion);
  assert.equal(imageGoVersion, moduleGoVersion, "OCI builder must match the module toolchain floor");
});

test("Debian desktop validation installs, launches, unregisters, and removes the package", () => {
  const source = JSON.stringify(workflow.jobs["devhud-desktop"]);
  for (const expected of [
    "finalize-devhud-deb.sh", "sudo dpkg -i", "/usr/bin/devhud", "gnome-keyring-daemon",
    "/run/user/$(id -u)", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS#unix:path=", "sudo dpkg -r", "test ! -e /usr/bin/devhud",
    "test ! -e /etc/opt/chrome/native-messaging-hosts/io.delino.devhud.native_messaging.json",
  ]) assert.ok(source.includes(expected), expected);
  const lifecycle = namedStep(workflow.jobs["devhud-desktop"], "Verify Ubuntu Debian Native Messaging install and uninstall lifecycle").run;
  for (const expected of [
    'if [ -S "$runtime/bus" ]', 'export DBUS_SESSION_BUS_ADDRESS="unix:path=$runtime/bus"',
    'sudo ln -s "$session_socket" "$runtime/bus"', "created_bus=true", 'if [ "$created_bus" = true ]',
  ]) assert.ok(lifecycle.includes(expected), expected);
  const register = lifecycle.indexOf('"$host" register "$host" "$user_manifest"');
  const registered = lifecycle.indexOf('test -e "$user_manifest"', register);
  const uninstall = lifecycle.indexOf('sudo dpkg -r "$package"');
  const removed = lifecycle.indexOf('test ! -e "$user_manifest"');
  assert.ok(register >= 0 && register < registered && registered < uninstall && uninstall < removed);
});

test("Windows desktop validation removes Native Messaging and executable payloads", () => {
  const lifecycle = namedStep(workflow.jobs["devhud-desktop"], "Verify Windows Native Messaging install and uninstall lifecycle").run;
  const manifestDeclared = lifecycle.indexOf("$manifestPath = $registration.'(default)'");
  const manifestPresent = lifecycle.indexOf("if (-not (Test-Path -LiteralPath $manifestPath))", manifestDeclared);
  const uninstall = lifecycle.indexOf('if ($env:DEVHUD_BUNDLE -eq "msi")', manifestPresent);
  const registryRemoved = lifecycle.indexOf('if (Test-Path "HKCU:\\Software\\Google\\Chrome\\NativeMessagingHosts\\io.delino.devhud.native_messaging")', uninstall);
  const manifestRemoved = lifecycle.indexOf("if (Test-Path -LiteralPath $manifestPath)", registryRemoved);
  const executableRemoved = lifecycle.indexOf("if (Test-Path -LiteralPath $env:DEVHUD_SMOKE_ARTIFACT)", manifestRemoved);
  assert.ok(
    manifestDeclared >= 0 && manifestDeclared < manifestPresent && manifestPresent < uninstall &&
    uninstall < registryRemoved && registryRemoved < manifestRemoved && manifestRemoved < executableRemoved,
  );
});

test("macOS and AppImage desktop validation exercises packaged Native Messaging lifecycles", () => {
  const macOS = namedStep(workflow.jobs["devhud-desktop"], "Verify macOS Native Messaging register and unregister lifecycle").run;
  for (const expected of [
    "DEVHUD_SMOKE_NATIVE_HOST", "devhud-native-home", "Library/Application Support/Google/Chrome/NativeMessagingHosts",
    "jq -e", "register", "unregister",
  ]) assert.ok(macOS.includes(expected), `macOS: ${expected}`);
  const macRegister = macOS.indexOf('"$DEVHUD_SMOKE_NATIVE_HOST" register "$DEVHUD_SMOKE_NATIVE_HOST" "$user_manifest"');
  const macRegistered = macOS.indexOf('test -e "$user_manifest"', macRegister);
  const macUnregister = macOS.indexOf('"$DEVHUD_SMOKE_NATIVE_HOST" unregister "$user_manifest"', macRegistered);
  const macRemoved = macOS.indexOf('test ! -e "$user_manifest"', macUnregister);
  assert.ok(macRegister >= 0 && macRegister < macRegistered && macRegistered < macUnregister && macUnregister < macRemoved);

  const appImage = namedStep(workflow.jobs["devhud-desktop"], "Verify Ubuntu AppImage Native Messaging register and unregister lifecycle").run;
  const appImagePreparation = namedStep(workflow.jobs["devhud-desktop"], "Prepare Ubuntu installed layout").run;
  for (const expected of [
    "appdir=$(realpath squashfs-root)", 'sandbox=$(realpath "$appdir/shared/bin/chrome-sandbox")',
    'artifact="$appdir/bin/devhud"', 'host="$appdir/bin/devhud-native-messaging-host"',
  ]) assert.ok(appImagePreparation.includes(expected), `AppImage preparation: ${expected}`);
  for (const expected of [
    "DEVHUD_SMOKE_NATIVE_HOST", "devhud-native-home", ".config/google-chrome/NativeMessagingHosts",
    "dbus-run-session", "gnome-keyring-daemon", "jq -e",
  ]) assert.ok(appImage.includes(expected), `AppImage: ${expected}`);
  const appImageRegister = appImage.indexOf('"$host" register "$host"');
  const appImageRegistered = appImage.indexOf('test -e "$user_manifest"', appImageRegister);
  const appImageUnregister = appImage.indexOf('"$host" unregister', appImageRegistered);
  const appImageRemoved = appImage.indexOf('test ! -e "$user_manifest"', appImageUnregister);
  assert.ok(appImageRegister >= 0 && appImageRegister < appImageRegistered && appImageRegistered < appImageUnregister && appImageUnregister < appImageRemoved);
});

test("package-local CI commands and deterministic cache boundaries are explicit", () => {
  const requiredScripts = new Map([
    ["apps/devhud/package.json", ["typecheck", "lint", "test:unit", "test:components", "test:accessibility", "test:security", "test:adapters", "build:frontend", "test:native:capture", "test:native:shortcuts", "test:native:ipc", "test:native:updater"]],
    ["apps/devhud-admin/package.json", ["typecheck", "lint", "test:unit", "test:components", "test:accessibility", "build:frontend", "verify:embedded"]],
    ["apps/devhud-chrome-extension/package.json", ["typecheck", "lint", "test:unit", "test:components", "test:accessibility", "test:package", "build:frontend"]],
    ["servers/devhud-api/package.json", ["ci:format", "ci:vet", "ci:build", "ci:unit", "ci:migrations", "ci:integration", "ci:api", "ci:sweeper"]],
    ["apps/public-docs/package.json", ["build:frontend", "test:routes"]],
  ]);
  for (const [path, names] of requiredScripts) {
    for (const name of names) assert.equal(typeof packages[path].scripts[name], "string", `${path}#${name}`);
  }
  for (const output of ["dist/**", "build/**", "artifacts/**", "doc_build/**"]) assert.ok(turbo.tasks["build:frontend"].outputs.includes(output), output);
  assert.ok(turbo.tasks.build.inputs.includes("$TURBO_DEFAULT$"), "build must hash package-default tracked inputs");
  for (const output of ["protos/gen/**", "packages/devhud-api-client/src/gen/**"]) assert.ok(turbo.tasks["//#proto:generate"].outputs.includes(output), output);
  assert.equal(packages["package.json"].scripts["proto:generate:cached"], "turbo run //#proto:generate");
  assert.match(packages["package.json"].scripts["proto:fresh"], /^turbo run \/\/#proto:generate --force &&/u);
  const adminScripts = packages["apps/devhud-admin/package.json"].scripts;
  for (const task of ["build:embedded", "verify:embedded"]) {
    assert.match(adminScripts[task], /^pnpm --filter @delinoio\/devhud-api-client build && pnpm build &&/u, task);
  }
  for (const task of ["build", "build:frontend", "build:embedded", "verify:embedded"]) {
    assert.equal(adminTurbo.tasks[task].cache, false, task);
  }
  assert.deepEqual(extensionTurbo.extends, ["//"]);
  for (const task of ["build", "build:frontend"]) {
    assert.deepEqual(extensionTurbo.tasks[task].inputs, [
      "$TURBO_DEFAULT$",
      "$TURBO_ROOT$/apps/devhud/src-tauri/icons/icon.png",
    ], task);
  }
  const nativeTurbo = JSON.parse(readFileSync(`${root}/apps/devhud/turbo.json`, "utf8"));
  for (const task of ["test:unit", "test:components", "test:security", "test:adapters"]) assert.deepEqual(nativeTurbo.tasks[task].dependsOn, ["^build"], task);
  for (const task of [
    "test", "build", "test:native:capture", "test:native:shortcuts", "test:native:ipc", "test:native:updater",
    "mobile:generate", "build:ios", "build:android", "smoke:platform",
  ]) assert.equal(nativeTurbo.tasks[task].cache, false, task);

  const devhudScripts = packages["apps/devhud/package.json"].scripts;
  const devhudTestFiles = readdirSync(`${root}/apps/devhud/src`).filter((path) => /\.test\.tsx?$/u.test(path));
  for (const path of devhudTestFiles) {
    const script = path.endsWith(".tsx") ? devhudScripts["test:components"] : devhudScripts["test:unit"];
    assert.ok(script.includes(`src/${path}`), path);
  }
  assert.doesNotMatch(`${devhudScripts["test:unit"]} ${devhudScripts["test:components"]}`, /[*?]/u);
});

test("CI is read-only and contains no publication or secret injection path", () => {
  assert.deepEqual(workflow.permissions, { contents: "read", "pull-requests": "read" });
  assert.doesNotMatch(workflowSource, /\$\{\{\s*secrets\./u);
  for (const forbidden of [
    /\bgit push\b/iu,
    /\bgh release (?:create|edit|upload|delete)/iu,
    /\bdocker push\b/iu,
    /\bcosign sign\b/iu,
    /wrangler\s+pages\s+deploy/iu,
    /devhud-store-release\.mjs\s+(?:submit|publish|withdraw)/iu,
    /devhud-release-controller\.mjs\s+(?:prepare|promote|rollback)/iu,
  ]) assert.doesNotMatch(workflowSource, forbidden);
});

test("local CI commands are documented by repository contracts", () => {
  const contract = readFileSync(`${root}/docs/repository-workflow-contract.md`, "utf8");
  const project = readFileSync(`${root}/docs/project-devhud.md`, "utf8");
  for (const command of ["pnpm ci:workflows", "pnpm ci:contracts", "pnpm ci:release-fixtures"]) assert.ok(contract.includes(command), command);
  for (const command of ["test:native:capture", "test:native:shortcuts", "test:native:ipc", "test:security", "test:adapters"]) assert.ok(project.includes(command), command);
});

test("native Go integration retains an explicit bounded package watchdog", () => {
  assert.equal(namedStep(workflow.jobs["go-test"], "Run go test").run, "go test -timeout=20m ./...");
});


test("Forge retains three-platform interoperability and mandatory Linux rendering", () => {
  assert.deepEqual(workflow.jobs["forge-test"].strategy.matrix.os, ["ubuntu-latest", "macos-latest", "windows-latest"]);
  const testCommands = workflow.jobs["forge-test"].steps.map(({ run }) => run ?? "").join("\n");
  assert.match(testCommands, /cargo test -p forge-tree-doc -p forge-pptx -p delino-forge/u);
  const renderCommands = workflow.jobs["forge-render"].steps.map(({ run }) => run ?? "").join("\n");
  assert.match(renderCommands, /libreoffice-impress poppler-utils/u);
  assert.match(renderCommands, /--test render -- --ignored/u);
});


test("React Forge validates its supported runtime with uncached native and rendering work", () => {
  const job = workflow.jobs["react-forge"];
  assert.equal(job["runs-on"], "${{ matrix.runner }}");
  assert.equal(job.strategy["fail-fast"], false);
  const platforms = JSON.parse(readFileSync(`${root}/packages/react-forge/src/native-platforms.json`, "utf8"));
  assert.deepEqual(job.strategy.matrix.include.map(({ id }) => id), platforms.map(({ id }) => id));
  for (const host of job.strategy.matrix.include) {
    const declared = platforms.find(({ id }) => id === host.id);
    assert.equal(host.platform, declared.platform);
    assert.equal(host.architecture, declared.architecture);
    assert.equal(host.target, declared.target);
  }
  assert.match(namedStep(job, "Verify Windows console cancellation").run, /windows_console/u);
  assert.match(namedStep(job, "Generate travel investor example").run, /examples\/travel-ir\.tsx/u);
  assert.match(namedStep(job, "Verify supported host and native contracts").run, /--include-ignored/u);
  const commands = job.steps.map(({ run }) => run ?? "").join("\n");
  for (const command of ["forge-package", "forge-document", "forge-docx", "forge-xlsx", "forge-pdf", "react-forge-node", "turbo run build typecheck lint test --filter=@delino/react-forge", "test:render", "benchmark", "render-requirements.txt"]) assert.ok(commands.includes(command), command);
  assert.equal(namedStep(job, "Remove generated package output").if, "always()");
  const evidence = namedStep(job, "Retain rendering and benchmark evidence");
  assert.equal(evidence.with["retention-days"], 7);
  assert.equal(evidence.if, "always()");
  const tasks = JSON.parse(readFileSync(`${root}/packages/react-forge/turbo.json`, "utf8")).tasks;
  for (const name of ["build", "test", "test:render", "benchmark"]) assert.equal(tasks[name].cache, false, name);
});

test("React Forge renders every product pair independently without reducing image quality", () => {
  const native = workflow.jobs["react-forge"];
  const renders = workflow.jobs["react-forge-scenes"];
  assert.deepEqual(jobPaths["react-forge-scenes"], jobPaths["react-forge"]);
  assert.equal(native["timeout-minutes"], 60);
  const prepare = namedStep(native, "Prepare and inspect generated GLB and FBX scenes");
  assert.equal(prepare.if, "matrix.id == 'linux-x64-gnu'");
  assert.match(prepare.run, /test:scenes.*--prepare-only/u);
  const inputs = namedStep(native, "Retain prepared 3D inputs");
  assert.equal(inputs.if, prepare.if);
  assert.equal(inputs.with.name, "react-forge-scene-inputs");
  assert.equal(inputs.with["if-no-files-found"], "error");
  assert.deepEqual(renders.needs, ["changes", "react-forge"]);
  assert.equal(renders["timeout-minutes"], 120);
  assert.equal(renders.strategy["fail-fast"], false);
  assert.deepEqual(renders.strategy.matrix.product, ["studio", "headphones", "dac", "stand"]);
  assert.equal(renders.steps.find(({ uses }) => uses === "actions/download-artifact@v4").with.name, inputs.with.name);
  const render = namedStep(renders, "Render and compare both exported formats");
  assert.match(render.run, /test-scene-comparison\.py/u);
  assert.match(render.run, /--python-exit-code 1.*render-scenes\.py.*--products "\$PRODUCT" --formats glb,fbx --views hero,front,back,detail --resolution 2048 --samples 96 --device CPU/u);
  assert.match(render.run, /compare-scenes\.py.*--product "\$PRODUCT"/u);
  assert.equal(render.env.PRODUCT, "${{ matrix.product }}");
  assert.match(namedStep(renders, "Install pinned test-only render tools").run, /9ba871ff2ecd36526b77432745980b7e6664ecd0c7ca11c48849073dcfe06da3/u);
  const evidence = namedStep(renders, "Retain product render evidence");
  assert.equal(evidence.if, "always()");
  assert.equal(evidence.with.name, "react-forge-scene-${{ matrix.product }}");
  assert.equal(evidence.with["retention-days"], 7);
  assert.equal(evidence.with["if-no-files-found"], "error");
});
