import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { load } from "js-yaml";

const root = fileURLToPath(new URL("../..", import.meta.url));
const workflowSource = readFileSync(`${root}/.github/workflows/CI.yml`, "utf8");
const apiDockerfileSource = readFileSync(`${root}/servers/devhud-api/Dockerfile`, "utf8");
const workflow = load(workflowSource);
const packages = Object.fromEntries([
  "package.json",
  "apps/binpm-docs/package.json",
  "apps/nodeup-docs/package.json",
  "apps/mpapp/package.json",
  "apps/devhud/package.json",
  "apps/devhud-admin/package.json",
  "apps/devhud-chrome-extension/package.json",
  "apps/public-docs/package.json",
  "packages/devhud-api-client/package.json",
  "servers/devhud-api/package.json",
].map((path) => [path, JSON.parse(readFileSync(`${root}/${path}`, "utf8"))]));
const configs = Object.fromEntries(await Promise.all(Object.keys(packages).map(async (path) => [
  path, (await import(`${root}/${path.replace("package.json", "vite.config.ts")}`)).default.run.tasks,
])));
const taskCommands = Object.fromEntries(Object.entries(configs).map(([path, tasks]) => [
  path, Object.fromEntries(Object.entries(tasks).map(([name, task]) => [name, task.command])),
]));
const devhudTauri = JSON.parse(readFileSync(`${root}/apps/devhud/src-tauri/tauri.conf.json`, "utf8"));

const legacyJobs = [
  "go-quality", "go-test", "repository-environment", "rust-fmt", "rust-clippy", "rust-test",
  "node-mpapp-test", "node-mpapp-lint", "node-binpm-docs-test", "node-nodeup-docs-test", "node-public-docs-test",
];
const devhudJobs = [
  "devhud-frontend", "devhud-extension", "devhud-rust-conformance", "devhud-security", "devhud-desktop",
  "devhud-mobile-contracts", "devhud-ios-simulator", "devhud-android-emulator", "devhud-protocol", "devhud-admin",
  "devhud-api", "devhud-oci", "devhud-supply-chain", "devhud-release-contracts",
];

function step(job, id) {
  return job.steps.find((candidate) => candidate.id === id);
}

function namedStep(job, name) {
  return job.steps.find((candidate) => candidate.name === name);
}

test("CI keeps every legacy check and aggregates every required job", () => {
  const jobs = Object.keys(workflow.jobs);
  for (const id of ["ci-contracts", ...legacyJobs, ...devhudJobs, "ci-result"]) assert.ok(jobs.includes(id), id);
  const required = jobs.filter((id) => id !== "ci-result").sort();
  assert.deepEqual([...workflow.jobs["ci-result"].needs].sort(), required);
  assert.equal(workflow.jobs["ci-result"].if, "always()");
  const resultCondition = JSON.stringify(workflow.jobs["ci-result"].steps);
  assert.match(resultCondition, /failure/u);
  assert.match(resultCondition, /cancelled/u);
});

test("DevHud jobs self-gate and the path contract covers every implemented boundary", () => {
  for (const id of devhudJobs) {
    const job = workflow.jobs[id];
    assert.equal(job.if, undefined, `${id} must not be skipped at job level`);
    assert.ok(step(job, "filter"), `${id} filter`);
    assert.ok(step(job, "gate"), `${id} gate`);
  }
  for (const path of [
    "servers/**", "protos/**", "packages/**", "apps/devhud/**", "apps/devhud-admin/**",
    "apps/devhud-chrome-extension/**", "crates/devhud-native-messaging-host/**", "packaging/devhud/**",
    "apps/public-docs/**", ".github/workflows/package-devhud-private.yml", ".github/workflows/release-devhud.yml",
    ".github/workflows/devhud-cef-security-review.yml",
  ]) assert.ok(workflowSource.includes(`- ${path}`), path);
  const apiFilter = JSON.stringify(step(workflow.jobs["devhud-api"], "filter"));
  for (const path of ["pnpm-workspace.yaml", "scripts/ci/check-go-format.mjs"]) {
    assert.ok(apiFilter.includes(path), `devhud-api: ${path}`);
  }
  const ociFilter = JSON.stringify(step(workflow.jobs["devhud-oci"], "filter"));
  for (const path of [".dockerignore", "package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml"]) {
    assert.ok(ociFilter.includes(path), `devhud-oci: ${path}`);
  }
  assert.ok(JSON.stringify(step(workflow.jobs["devhud-admin"], "filter")).includes(".nvmrc"));
  assert.ok(JSON.stringify(step(workflow.jobs["devhud-protocol"], "filter")).includes("vite.config.ts"));
  for (const id of ["devhud-frontend", "devhud-extension", "devhud-rust-conformance"]) {
    const filter = JSON.stringify(step(workflow.jobs[id], "filter"));
    assert.ok(filter.includes(".nvmrc"), `${id}: .nvmrc`);
  }
  const securityFilter = JSON.stringify(step(workflow.jobs["devhud-security"], "filter"));
  for (const path of [".nvmrc", "package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "vite.config.ts"]) {
    assert.ok(securityFilter.includes(path), `devhud-security: ${path}`);
  }
  for (const id of ["devhud-supply-chain", "devhud-release-contracts"]) {
    const filter = JSON.stringify(step(workflow.jobs[id], "filter"));
    assert.ok(filter.includes(".nvmrc"), `${id}: .nvmrc`);
  }
  const releaseFilter = JSON.stringify(step(workflow.jobs["devhud-release-contracts"], "filter"));
  for (const path of ["AGENTS.md", "docs/README.md"]) {
    assert.ok(releaseFilter.includes(path), `devhud-release-contracts: ${path}`);
  }
});

test("Node jobs use pinned Vite Task with path gates and frozen installs", () => {
  assert.equal(packages["package.json"].devDependencies["vite-plus"], "0.3.3");
  assert.doesNotMatch(workflowSource, /turbo|--affected|--dry=json|TURBO_SCM_|pnpm\s+dlx/iu);
  assert.match(workflowSource, /pnpm install --frozen-lockfile --ignore-scripts/u);
  for (const [id, packageName] of [
    ["node-mpapp-test", "mpapp"], ["node-mpapp-lint", "mpapp"],
    ["node-binpm-docs-test", "binpm-docs"], ["node-nodeup-docs-test", "nodeup-docs"],
    ["node-public-docs-test", "public-docs"],
  ]) {
    const job = workflow.jobs[id];
    const filter = step(job, "filter").with.filters;
    for (const path of [`.github/workflows/CI.yml`, `apps/${packageName}/**`, "vite.config.ts", "pnpm-lock.yaml", "scripts/**"]) {
      assert.ok(filter.includes(path), `${id}: ${path}`);
    }
    const gate = step(job, "gate");
    assert.equal(gate.env.CHANGED, "${{ steps.filter.outputs.workspace }}");
    assert.match(gate.run, /workflow_dispatch/u);
    const run = job.steps.find((candidate) => candidate.run?.startsWith(`pnpm exec vp run ${packageName}#`));
    assert.equal(run.if, "${{ steps.gate.outputs.run == 'true' }}");
  }
  const frontend = namedStep(workflow.jobs["devhud-frontend"], "Run frontend contracts");
  for (const task of ["typecheck", "lint", "test:unit", "test:components", "test:accessibility", "build:frontend"]) {
    assert.ok(frontend.run.includes(`pnpm exec vp run devhud#${task}`));
  }
});

test("DevHud API PostgreSQL starts only after its path gate", () => {
  const job = workflow.jobs["devhud-api"];
  assert.equal(job.services, undefined);
  const gateIndex = job.steps.findIndex((candidate) => candidate.id === "gate");
  const startIndex = job.steps.findIndex((candidate) => candidate.name === "Start PostgreSQL");
  const integrationIndex = job.steps.findIndex((candidate) => candidate.name === "Run PostgreSQL migration and integration tests");
  const stopIndex = job.steps.findIndex((candidate) => candidate.name === "Stop PostgreSQL");
  assert.ok(gateIndex >= 0 && gateIndex < startIndex && startIndex < integrationIndex && integrationIndex < stopIndex);
  const startPostgreSQL = job.steps[startIndex];
  assert.equal(startPostgreSQL.if, "${{ steps.gate.outputs.run == 'true' }}");
  for (const expected of [
    "docker run --detach", "postgres:15-bookworm", "--publish 5432:5432", "pg_isready",
    "docker inspect", "State.Health.Status", "docker logs devhud-postgres",
  ]) assert.ok(startPostgreSQL.run.includes(expected), expected);
  const stopPostgreSQL = job.steps[stopIndex];
  assert.equal(stopPostgreSQL.if, "${{ always() && steps.gate.outputs.run == 'true' }}");
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
    ["devhud-frontend", ["typecheck", "lint", "test:unit", "test:components", "test:accessibility", "build:frontend"]],
    ["devhud-admin", ["devhud-admin#test"]],
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
    .filter((line) => line.includes("pnpm exec vp run @delinoio/devhud-api#"));
  assert.equal(apiCommands.length, 8);
  for (const command of apiCommands) assert.match(command, /@delinoio\/devhud-api#ci:/u);
  assert.match(taskCommands["apps/devhud/package.json"].test, /^vp run lint && vp run test:unit && vp run test:components/u);
  assert.match(JSON.stringify(workflow.jobs["devhud-security"]), /pnpm exec vp run devhud#test:security[\s\S]*pnpm exec vp run devhud#test:adapters/u);
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
  assert.equal(startPostgreSQL.if, "${{ steps.gate.outputs.run == 'true' }}");
  for (const expected of [
    "docker run --detach", "postgres:15-bookworm", "--publish 5432:5432", "pg_isready",
    "docker inspect", "State.Health.Status", "docker logs devhud-postgres",
  ]) assert.ok(startPostgreSQL.run.includes(expected), expected);
  const sweeperAssetCondition = "${{ steps.gate.outputs.run == 'true' && matrix.target == 'sweeper' }}";
  const setupPNPM = namedStep(job, "Setup pnpm");
  const setupNode = namedStep(job, "Setup Node.js");
  const generateAssets = namedStep(job, "Generate and verify embedded administrator assets");
  for (const step of [setupPNPM, setupNode, generateAssets]) assert.equal(step.if, sweeperAssetCondition);
  assert.equal(setupPNPM.with.version, "10.26.2");
  assert.equal(setupNode.with["node-version-file"], ".nvmrc");
  assert.match(generateAssets.run, /pnpm install --frozen-lockfile --ignore-scripts/u);
  assert.match(generateAssets.run, /pnpm exec vp run devhud-admin#build:embedded/u);
  const generateAssetsIndex = job.steps.indexOf(generateAssets);
  const buildAndInspectIndex = job.steps.findIndex(({ name }) => name === "Build and inspect amd64/arm64 OCI layout");
  assert.ok(generateAssetsIndex >= 0 && generateAssetsIndex < buildAndInspectIndex);
  const stopPostgreSQL = namedStep(job, "Stop PostgreSQL");
  assert.equal(stopPostgreSQL.if, "${{ always() && steps.gate.outputs.run == 'true' }}");
  assert.match(stopPostgreSQL.run, /docker rm --force devhud-postgres/u);
  const buildAndInspect = namedStep(job, "Build and inspect amd64/arm64 OCI layout").run;
  assert.match(
    buildAndInspect,
    /if \[ "\$OCI_TARGET" = api \]; then\s+docker run "\$\{docker_args\[@\]\}" "\$image" migrate\s+else\s+DEVHUD_DATABASE_URL="\$DEVHUD_TEST_DATABASE_URL" go run \.\/servers\/devhud-api\/cmd\/devhud-api migrate/u,
  );
  assert.doesNotMatch(source, /(?:docker|skopeo) push/iu);
  assert.doesNotMatch(source, /docker-daemon:/u);
  assert.match(apiDockerfileSource, /^FROM --platform=\$BUILDPLATFORM golang:/mu);
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
  for (const [path, tasks] of Object.entries(configs)) {
    for (const [name, task] of Object.entries(tasks)) {
      assert.equal(typeof task.cache, "boolean", `${path}#${name}: explicitly opt in to caching`);
      assert.equal(packages[path].scripts?.[name], undefined, `${path}#${name}: duplicate script`);
    }
  }
  const requiredScripts = new Map([
    ["apps/devhud/package.json", ["typecheck", "lint", "test:unit", "test:components", "test:accessibility", "test:security", "test:adapters", "build:frontend", "test:native:capture", "test:native:shortcuts", "test:native:ipc", "test:native:updater"]],
    ["apps/devhud-admin/package.json", ["typecheck", "lint", "test:unit", "test:components", "test:accessibility", "build:frontend", "verify:embedded"]],
    ["apps/devhud-chrome-extension/package.json", ["typecheck", "lint", "test:unit", "test:components", "test:accessibility", "test:package", "build:frontend"]],
    ["servers/devhud-api/package.json", ["ci:format", "ci:vet", "ci:build", "ci:unit", "ci:migrations", "ci:integration", "ci:api", "ci:sweeper"]],
    ["apps/public-docs/package.json", ["build:frontend", "test:routes"]],
  ]);
  for (const [path, names] of requiredScripts) {
    for (const name of names) assert.equal(typeof taskCommands[path][name], "string", `${path}#${name}`);
  }
  const rootTasks = configs["package.json"];
  assert.deepEqual(rootTasks["proto:generate"].output, ["protos/gen/**", "packages/devhud-api-client/src/gen/**"]);
  assert.equal(rootTasks["proto:generate"].cache, true);
  assert.equal(rootTasks["proto:generate:cached"].command, "vp run proto:generate");
  assert.match(rootTasks["proto:fresh"].command, /^vp run --no-cache proto:generate &&/u);
  const adminTasks = configs["apps/devhud-admin/package.json"];
  for (const task of ["build", "build:frontend", "build:embedded", "verify:embedded"]) {
    assert.equal(adminTasks[task].cache, false, task);
    assert.deepEqual(adminTasks[task].dependsOn, [task.endsWith(":embedded") ? "build" : "@delinoio/devhud-api-client#build"], task);
  }
  for (const task of ["build", "build:test", "build:frontend"]) {
    const definition = configs["apps/devhud-chrome-extension/package.json"][task];
    assert.ok(definition.input.some((entry) => entry.pattern === "apps/devhud/src-tauri/icons/icon.png" && entry.base === "workspace"));
    assert.deepEqual(definition.output, ["dist/**", "build/**", "artifacts/**"]);
  }
  const nativeTasks = configs["apps/devhud/package.json"];
  for (const [name, task] of Object.entries(configs["servers/devhud-api/package.json"])) {
    if (name.startsWith("ci:") && name !== "ci:format") {
      assert.deepEqual(task.dependsOn, ["devhud-admin#build:embedded"], name);
    }
  }
  for (const task of ["test:unit", "test:components", "test:security", "test:adapters"]) {
    assert.deepEqual(nativeTasks[task].dependsOn, ["@delinoio/devhud-api-client#build"], task);
  }
  for (const task of [
    "build", "test:native:capture", "test:native:shortcuts", "test:native:ipc", "test:native:updater",
    "mobile:generate", "build:ios", "build:android", "smoke:platform",
  ]) assert.equal(nativeTasks[task].cache, false, task);
  assert.deepEqual(nativeTasks["build:frontend"].output, ["dist/**"]);
  const devhudScripts = taskCommands["apps/devhud/package.json"];
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
  for (const command of ["pnpm exec vp run ci:workflows", "pnpm exec vp run ci:contracts", "pnpm exec vp run ci:release-fixtures"]) assert.ok(contract.includes(command), command);
  for (const command of ["test:native:capture", "test:native:shortcuts", "test:native:ipc", "test:security", "test:adapters"]) assert.ok(project.includes(command), command);
});
