import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const repositoryRoot = resolve(import.meta.dirname, "..");
const read = (path) => readFile(resolve(repositoryRoot, path), "utf8");
const [workflow, githubManifestSource, r2FixtureSource, registry, extensionBuilder, catalogSource] =
  await Promise.all([
    read(".github/workflows/CI.yml"),
    read("servers/devhud-realqa/github-app-manifest.json"),
    read("servers/devhud-realqa/artifacts/cloudflare-public-images.fixture.json"),
    read("apps/devhud/src/tools/registry.ts"),
    read("apps/devhud/scripts/build-realqa-extension.mjs"),
    read("servers/delibase/catalog.json"),
  ]);
const failures = [];
const requireCondition = (condition, message) => {
  if (!condition) failures.push(message);
};

const workflowLines = workflow.split(/\r?\n/u);
const permissionsStart = workflowLines.indexOf("permissions:");
let permissionsEnd = permissionsStart + 1;
while (
  permissionsEnd < workflowLines.length &&
  (workflowLines[permissionsEnd].trim() === "" || /^[ \t]/u.test(workflowLines[permissionsEnd]))
) {
  permissionsEnd += 1;
}
const topLevelPermissions = workflowLines
  .slice(permissionsStart, permissionsEnd)
  .filter((line) => line.trim() !== "")
  .join("\n");
requireCondition(
  topLevelPermissions === "permissions:\n  contents: read\n  pull-requests: read",
  "RealQA CI must retain read-only repository permissions",
);
const permissionDeclarations = workflow.match(/^[ \t]*permissions\s*:/gmu) ?? [];
requireCondition(
  permissionDeclarations.length === 1 && permissionDeclarations[0] === "permissions:",
  "RealQA CI must not override read-only permissions at the job or step level",
);
for (const [pattern, message] of [
  [/docker\/login-action/u, "must not authenticate to a container registry"],
  [/\bdocker\s+login\b/u, "must not authenticate to a container registry from the shell"],
  [/docker\/build-push-action/u, "must not use an image publishing action"],
  [/\bdocker\s+push\b/u, "must not push an image from the shell"],
  [
    /\bdocker\s+buildx\s+build\b[\s\S]*?--push\b/u,
    "must not push a buildx image from the shell",
  ],
  [
    /\bdocker\s+buildx\s+build\b[\s\S]*?--output(?:=|\s+)["']?type=registry\b/u,
    "must not export a buildx image to a registry",
  ],
  [
    /\bdocker\s+buildx\s+build\b[\s\S]*?--output(?:=|\s+)["']?type=image,[^\s"']*\bpush=true\b/u,
    "must not push a buildx image through the image exporter",
  ],
  [/\bghcr\.io\b/u, "must not name a GHCR publication target"],
  [/\bactions\/attest\b/u, "must not publish GitHub attestations"],
  [/\b(?:wrangler|cloudflare\/wrangler-action)\b/iu, "must not invoke Cloudflare provisioning"],
  [/\bsecrets(?:\.|\s*\[)/u, "must not read repository or environment secrets"],
  [/\bDEVHUD_CHROME_EXTENSION_ID\s*[:=]/u, "must not inject a production extension identity"],
  [/\bpush:\s*true\b/u, "must not push an image"],
]) {
  requireCondition(!pattern.test(workflow), `RealQA CI ${message}`);
}

const requiredAggregateJobs = [
  "go-quality",
  "go-test",
  "delibase-server",
  "devhud-deck-server",
  "devhud-realqa-server",
  "proto-contracts",
  "rust-fmt",
  "rust-clippy",
  "rust-test",
  "devhud-realqa-macos",
  "node-mpapp-test",
  "node-mpapp-lint",
  "node-devhud",
  "devhud-windows-capture",
  "devhud-widget-android",
  "devhud-widget-ios",
  "node-binpm-docs-test",
  "node-nodeup-docs-test",
  "node-public-docs-test",
  "node-delidev-app",
  "realqa-changes",
  "realqa-proto",
  "realqa-go-quality",
  "realqa-postgres",
  "realqa-integrations",
  "realqa-billing-races",
  "realqa-frontend",
  "realqa-rust",
  "realqa-linux-capture",
  "realqa-extension-native-messaging",
  "realqa-delidev-settings",
  "realqa-server-images",
  "realqa-fixture-artifacts",
  "realqa-safety",
];
const ciResult = workflow.slice(workflow.indexOf("\n  ci-result:\n"));
for (const job of requiredAggregateJobs) {
  requireCondition(
    workflow.includes(`\n  ${job}:\n`) && ciResult.includes(`      - ${job}\n`),
    `CI Result must retain the ${job} aggregate dependency`,
  );
}

const githubManifest = JSON.parse(githubManifestSource);
requireCondition(
  JSON.stringify(githubManifest.default_permissions) ===
    JSON.stringify({ issues: "write", metadata: "read", contents: "read" }),
  "the fixture GitHub App manifest must keep only its minimum base permissions",
);
requireCondition(
  JSON.stringify(githubManifest.default_events.toSorted()) ===
    JSON.stringify(["installation_target", "issues", "repository"]),
  "the fixture GitHub App manifest must keep only required events",
);

const r2Fixture = JSON.parse(r2FixtureSource);
requireCondition(
  r2Fixture.artifact_only === true &&
    r2Fixture.provisions_resources === false &&
    r2Fixture.deploys_resources === false,
  "the R2/WAF definition must remain an artifact-only fixture",
);
const realqaTool =
  registry.match(
    /defineTool\(\{\s*toolId: "realqa",[\s\S]*?EntryPoint: RealQaToolEntry,\s*\}\),/u,
  )?.[0] ?? "";
requireCondition(
  realqaTool.includes("supportedPlatforms: new Set([ToolPlatform.Desktop])") &&
    !realqaTool.includes("ToolPlatform.Ios") &&
    !realqaTool.includes("ToolPlatform.Android"),
  "RealQA must remain absent from mobile platforms",
);
requireCondition(
  extensionBuilder.includes("DEVHUD_APPROVED_CHROME_EXTENSION_IDS") &&
    extensionBuilder.includes("extensionId === FIXTURE_EXTENSION_ID") &&
    extensionBuilder.includes("release packaging requires the production ID"),
  "release extension packaging must retain external approval and fixture-ID rejection",
);
const catalog = JSON.parse(catalogSource);
const realqaApps = catalog.apps.filter(({ slug }) => slug === "realqa");
const realqaAppIds = new Set(realqaApps.map(({ id }) => id));
const realqaMeters = catalog.meters.filter(({ app_id }) => realqaAppIds.has(app_id));
const realqaMeterIds = new Set(realqaMeters.map(({ id }) => id));
const realqaServices = catalog.services.filter(({ allowed_meter_ids: allowedMeterIds }) =>
  allowedMeterIds.some((meterId) => realqaMeterIds.has(meterId)),
);
requireCondition(
  realqaApps.length > 0 &&
    realqaApps.every(({ enabled }) => enabled === false) &&
    realqaMeters.length > 0 &&
    realqaMeters.every(({ enabled }) => enabled === false) &&
    realqaServices.length > 0 &&
    realqaServices.every(({ enabled }) => enabled === false) &&
    !catalogSource.includes("realqa_image_transfer") &&
    !catalogSource.includes("realqa_image_storage"),
  "production RealQA catalog records must remain absent and inactive",
);

if (failures.length > 0) {
  throw new Error(`RealQA CI safety contract failed:\n- ${failures.join("\n- ")}`);
}
console.log(JSON.stringify({
  check: "realqa-ci-safety",
  status: "passed",
  deploymentAuthority: false,
  productionIdentity: false,
  mobileRealQA: false,
}));
