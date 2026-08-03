import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { load } from "js-yaml";

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

const workflowDocument = load(workflow);
const topLevelPermissions = workflowDocument?.permissions;
requireCondition(
  topLevelPermissions !== null &&
    typeof topLevelPermissions === "object" &&
    !Array.isArray(topLevelPermissions) &&
    Object.keys(topLevelPermissions).length === 2 &&
    topLevelPermissions.contents === "read" &&
    topLevelPermissions["pull-requests"] === "read",
  "RealQA CI must retain read-only repository permissions",
);
const hasNestedPermissions = (document) =>
  Object.values(document?.jobs ?? {}).some(
    (job) =>
      Object.hasOwn(job ?? {}, "permissions") ||
      (Array.isArray(job?.steps) &&
        job.steps.some((step) => Object.hasOwn(step ?? {}, "permissions"))),
  );
requireCondition(
  !hasNestedPermissions(workflowDocument),
  "RealQA CI must not override read-only permissions at the job or step level",
);
requireCondition(
  hasNestedPermissions(
    load('jobs:\n  fixture:\n    "permissions":\n      id-token: write\n    steps: []'),
  ),
  "RealQA CI safety guard must reject quoted job permissions overrides",
);
const hasJobKey = (document, key) =>
  Object.values(document?.jobs ?? {}).some((job) => Object.hasOwn(job ?? {}, key));
requireCondition(
  !hasJobKey(workflowDocument, "environment"),
  "RealQA CI must not reference a GitHub deployment environment",
);
requireCondition(
  hasJobKey(load('jobs:\n  fixture:\n    "environment": production\n    steps: []'), "environment"),
  "RealQA CI safety guard must reject quoted deployment environments",
);
requireCondition(
  !hasJobKey(workflowDocument, "secrets"),
  "RealQA CI must not pass secrets to a reusable workflow",
);
requireCondition(
  hasJobKey(
    load('jobs:\n  fixture:\n    uses: example/repository/.github/workflows/publish.yml@main\n    "secrets": inherit'),
    "secrets",
  ),
  "RealQA CI safety guard must reject reusable workflow secret inheritance",
);

const workflowShellSource = (source) => {
  const document = load(source);
  const runCommands = Object.values(document?.jobs ?? {}).flatMap((job) =>
    Array.isArray(job?.steps)
      ? job.steps.flatMap((step) => (typeof step?.run === "string" ? [step.run] : []))
      : [],
  );
  return [source, ...runCommands].join("\n").replace(/\\\r?\n[ \t]*/gu, " ");
};

const shellWorkflow = workflowShellSource(workflow);
const shellValue = String.raw`(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s]+)`;
const dockerGlobalOption = String.raw`(?:(?:--(?:config|context|host|log-level|tlscacert|tlscert|tlskey)|-[cHl])(?:=|\s+)${shellValue}|(?:--(?:debug|tls|tlsverify)|-D)(?:=(?:true|false))?)`;
const buildxGlobalOption = String.raw`(?:--builder(?:=|\s+)${shellValue}|(?:--debug|-D)(?:=(?:true|false))?)`;
const dockerCommand = String.raw`\bdocker\s+(?:${dockerGlobalOption}\s+)*`;
const dockerBuildCommand = String.raw`${dockerCommand}(?:build|builder\s+build|image\s+build|buildx\s+(?:${buildxGlobalOption}\s+)*(?:build|b))\b`;
const forbiddenWorkflowPatterns = [
  [/docker\/login-action/u, "must not authenticate to a container registry"],
  [
    new RegExp(`${dockerCommand}login\\b`, "u"),
    "must not authenticate to a container registry from the shell",
  ],
  [/docker\/build-push-action/u, "must not use an image publishing action"],
  [new RegExp(`${dockerCommand}push\\b`, "u"), "must not push an image from the shell"],
  [
    new RegExp(`${dockerBuildCommand}[^\\r\\n]*?--push\\b`, "u"),
    "must not push a buildx image from the shell",
  ],
  [
    new RegExp(
      `${dockerBuildCommand}[^\\r\\n]*?(?:--output|-o)(?:=|\\s+)["']?type=registry\\b`,
      "u",
    ),
    "must not export a buildx image to a registry",
  ],
  [
    new RegExp(
      `${dockerBuildCommand}[^\\r\\n]*?(?:--output|-o)(?:=|\\s+)["']?type=image,[^\\s"']*\\bpush=true\\b`,
      "u",
    ),
    "must not push a buildx image through the image exporter",
  ],
  [/\bghcr\.io\b/u, "must not name a GHCR publication target"],
  [/\bactions\/attest\b/u, "must not publish GitHub attestations"],
  [/\b(?:wrangler|cloudflare\/wrangler-action)\b/iu, "must not invoke Cloudflare provisioning"],
  [/\bsecrets\b/u, "must not read repository or environment secrets"],
  [/\bDEVHUD_CHROME_EXTENSION_ID\s*[:=]/u, "must not inject a production extension identity"],
  [/\bpush:\s*true\b/u, "must not push an image"],
];
for (const [pattern, message] of forbiddenWorkflowPatterns) {
  requireCondition(!pattern.test(shellWorkflow), `RealQA CI ${message}`);
}

for (const [source, expectedMessage] of [
  [
    "jobs:\n  fixture:\n    steps:\n      - run: docker -c default login registry.example.com",
    "must not authenticate to a container registry from the shell",
  ],
  [
    "jobs:\n  fixture:\n    steps:\n      - run: docker -l debug push registry.example.com/image",
    "must not push an image from the shell",
  ],
  [
    "jobs:\n  fixture:\n    steps:\n      - run: docker buildx -D build --push .",
    "must not push a buildx image from the shell",
  ],
  [
    "jobs:\n  fixture:\n    steps:\n      - run: >\n          docker buildx build\n          --push .",
    "must not push a buildx image from the shell",
  ],
  [
    "jobs:\n  fixture:\n    steps:\n      - run: docker buildx build -o type=registry .",
    "must not export a buildx image to a registry",
  ],
  [
    "jobs:\n  fixture:\n    steps:\n      - run: docker buildx build -o=type=image,push=true .",
    "must not push a buildx image through the image exporter",
  ],
  [
    "jobs:\n  fixture:\n    steps:\n      - run: docker build --push .",
    "must not push a buildx image from the shell",
  ],
  [
    "jobs:\n  fixture:\n    steps:\n      - run: docker builder build -o type=registry .",
    "must not export a buildx image to a registry",
  ],
  [
    "jobs:\n  fixture:\n    steps:\n      - run: docker image build -o=type=image,push=true .",
    "must not push a buildx image through the image exporter",
  ],
  [
    "jobs:\n  fixture:\n    steps:\n      - run: docker buildx b --push .",
    "must not push a buildx image from the shell",
  ],
  [
    "jobs:\n  fixture:\n    steps:\n      - run: echo '${{ toJSON(secrets) }}'",
    "must not read repository or environment secrets",
  ],
]) {
  const fixtureSource = workflowShellSource(source);
  const matchedMessage = forbiddenWorkflowPatterns.find(([pattern]) => pattern.test(fixtureSource))?.[1];
  requireCondition(
    matchedMessage === expectedMessage,
    `RealQA CI safety guard must reject fixture command: ${source}`,
  );
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
