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
  "apps/devhud/package.json",
  "apps/devhud-admin/package.json",
  "apps/devhud-chrome-extension/package.json",
  "apps/public-docs/package.json",
  "packages/devhud-api-client/package.json",
  "servers/devhud-api/package.json",
].map((path) => [path, JSON.parse(readFileSync(`${root}/${path}`, "utf8"))]));
const turbo = JSON.parse(readFileSync(`${root}/turbo.json`, "utf8"));
const adminTurbo = JSON.parse(readFileSync(`${root}/apps/devhud-admin/turbo.json`, "utf8"));
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
  assert.ok(JSON.stringify(step(workflow.jobs["devhud-api"], "filter")).includes("scripts/ci/check-go-format.mjs"));
  assert.ok(JSON.stringify(step(workflow.jobs["devhud-admin"], "filter")).includes(".nvmrc"));
  assert.ok(JSON.stringify(step(workflow.jobs["devhud-protocol"], "filter")).includes("turbo.json"));
  const securityFilter = JSON.stringify(step(workflow.jobs["devhud-security"], "filter"));
  for (const path of [".nvmrc", "package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml", "turbo.json"]) {
    assert.ok(securityFilter.includes(path), `devhud-security: ${path}`);
  }
  const releaseFilter = JSON.stringify(step(workflow.jobs["devhud-release-contracts"], "filter"));
  for (const path of ["AGENTS.md", "docs/README.md"]) {
    assert.ok(releaseFilter.includes(path), `devhud-release-contracts: ${path}`);
  }
});

test("Node jobs use the committed Turbo binary with frozen installs and affected no-ops", () => {
  assert.doesNotMatch(workflowSource, /pnpm\s+dlx\s+turbo/iu);
  assert.match(workflowSource, /pnpm install --frozen-lockfile --ignore-scripts/u);
  assert.match(workflowSource, /pnpm exec turbo run test --affected --filter/u);
  assert.match(workflowSource, /--dry=json/u);
  assert.match(packages["package.json"].scripts["ci:affected"], /^turbo run$/u);
  const affectedLegacyJobs = new Map([
    ["node-mpapp-test", "Run mpapp tests"],
    ["node-mpapp-lint", "Run mpapp lint"],
    ["node-binpm-docs-test", "Run binpm-docs tests"],
    ["node-nodeup-docs-test", "Run nodeup-docs tests"],
    ["node-public-docs-test", "Run public-docs tests"],
  ]);
  for (const [id, runStepName] of affectedLegacyJobs) {
    const job = workflow.jobs[id];
    for (const candidate of [step(job, "gate"), namedStep(job, runStepName)]) {
      const source = JSON.stringify(candidate);
      for (const expected of ["github.event.before", "github.sha", "TURBO_SCM_BASE", "TURBO_SCM_HEAD"]) {
        assert.ok(source.includes(expected), `${id} push range: ${expected}`);
      }
    }
  }
  const frontend = JSON.stringify(workflow.jobs["devhud-frontend"]);
  for (const expected of ["github.event.before", "TURBO_SCM_BASE", "TURBO_SCM_HEAD"]) {
    assert.ok(frontend.includes(expected), `devhud-frontend push range: ${expected}`);
  }
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
  assert.deepEqual(workflow.jobs["devhud-android-emulator"].strategy.matrix.include.map(({ target }) => target), ["aarch64", "armv7", "x86_64"]);
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
    ["devhud-admin", ["devhud-admin test", "adminassets/dist"]],
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
  assert.match(packages["apps/devhud/package.json"].scripts.test, /^pnpm lint && pnpm test:unit && pnpm test:components/u);
  assert.match(JSON.stringify(workflow.jobs["devhud-security"]), /pnpm exec turbo run test:security test:adapters --filter devhud/u);
});

test("OCI validation is multi-architecture, non-root, migration-bearing, and local-only", () => {
  const source = JSON.stringify(workflow.jobs["devhud-oci"]);
  for (const expected of [
    "linux/amd64,linux/arm64", "type=oci", "65532", "io.delino.devhud.migrations",
    "io.delino.devhud.administrator-assets", "spdx-json", "packages | length > 0",
    "go test -tags=integration ./servers/devhud-api/internal/postgres", "--load", "--platform linux/amd64", "--provenance=false",
    "--user 65532:65532", "devhud-api migrate", "migrate", "--once",
  ]) assert.ok(source.includes(expected), expected);
  const buildAndInspect = namedStep(workflow.jobs["devhud-oci"], "Build and inspect amd64/arm64 OCI layout").run;
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
    "/run/user/$(id -u)", "DBUS_SESSION_BUS_ADDRESS#unix:path=", "sudo dpkg -r", "test ! -e /usr/bin/devhud",
    "test ! -e /etc/opt/chrome/native-messaging-hosts/io.delino.devhud.native_messaging.json",
  ]) assert.ok(source.includes(expected), expected);
  const lifecycle = namedStep(workflow.jobs["devhud-desktop"], "Verify Ubuntu Debian Native Messaging install and uninstall lifecycle").run;
  const register = lifecycle.indexOf('"$host" register "$host" "$user_manifest"');
  const registered = lifecycle.indexOf('test -e "$user_manifest"', register);
  const uninstall = lifecycle.indexOf('sudo dpkg -r "$package"');
  const removed = lifecycle.indexOf('test ! -e "$user_manifest"');
  assert.ok(register >= 0 && register < registered && registered < uninstall && uninstall < removed);
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
  for (const output of ["protos/gen/**", "packages/devhud-api-client/src/gen/**"]) assert.ok(turbo.tasks["//#proto:generate"].outputs.includes(output), output);
  assert.deepEqual(adminTurbo.tasks.build.inputs, ["$TURBO_EXTENDS$", "index.html"]);
  const nativeTurbo = JSON.parse(readFileSync(`${root}/apps/devhud/turbo.json`, "utf8"));
  for (const task of ["test:unit", "test:components", "test:security", "test:adapters"]) assert.deepEqual(nativeTurbo.tasks[task].dependsOn, ["^build"], task);
  for (const task of ["build", "mobile:generate", "build:ios", "build:android", "smoke:platform"]) assert.equal(nativeTurbo.tasks[task].cache, false, task);

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
