import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

import { load } from "js-yaml";

const repositoryRoot = resolve(import.meta.dirname, "..");
const read = (path) => readFile(resolve(repositoryRoot, path), "utf8");
const [workflow, packageSource, turboSource, appPackageSource, protoPackageSource,
  manifestSource, catalogSource, serverContractSource, reportBuilderSource,
  supplyChainSource, loggingTestSource, authTestSource] = await Promise.all([
  read(".github/workflows/CI.yml"),
  read("package.json"),
  read("turbo.json"),
  read("apps/devhud/package.json"),
  read("protos/devhud-deck/package.json"),
  read("servers/devhud-deck/testdata/github-app/manifest.json"),
  read("servers/delibase/catalog.json"),
  read("servers/devhud-deck/internal/service/refresh_test.go"),
  read("scripts/build-deck-ci-reports.mjs"),
  read("servers/devhud-deck/artifacts/supply-chain.fixture.json"),
  read("servers/devhud-deck/internal/contracts/contracts_test.go"),
  read("servers/devhud-deck/internal/authn/authn_test.go"),
]);

const failures = [];
const requireCondition = (condition, message) => {
  if (!condition) failures.push(message);
};
const workflowDocument = load(workflow);
const jobs = workflowDocument?.jobs ?? {};

requireCondition(
  JSON.stringify(workflowDocument?.permissions) ===
    JSON.stringify({ contents: "read", "pull-requests": "read" }),
  "CI must retain only read-only repository permissions",
);
for (const [jobName, job] of Object.entries(jobs)) {
  requireCondition(
    !Object.hasOwn(job ?? {}, "permissions") &&
      !Object.hasOwn(job ?? {}, "environment") &&
      !Object.hasOwn(job ?? {}, "secrets"),
    `${jobName} must not override permissions, use an environment, or inherit secrets`,
  );
}

const workflowShell = [
  workflow,
  ...Object.values(jobs).flatMap((job) =>
    (job?.steps ?? []).flatMap((step) =>
      typeof step?.run === "string" ? [step.run] : [],
    ),
  ),
].join("\n").replace(/\\\r?\n[ \t]*/gu, " ");
const forbiddenWorkflowPatterns = [
  [/docker\/login-action/iu, "authenticate to a registry"],
  [/\bdocker\s+(?:--[^\s]+\s+)*login\b/iu, "authenticate to a registry"],
  [/docker\/build-push-action/iu, "use an image publishing action"],
  [/\bdocker\s+(?:--[^\s]+\s+)*push\b/iu, "push an image"],
  [/\bdocker\s+(?:build|buildx\s+build|builder\s+build|image\s+build)\b[^\r\n]*--push\b/iu,
    "push a built image"],
  [/\bdocker\s+(?:build|buildx\s+build|builder\s+build|image\s+build)\b[^\r\n]*?(?:--output|-o)(?:=|\s+)["']?type=registry\b/iu,
    "export a built image to a registry"],
  [/\bdocker\s+(?:build|buildx\s+build|builder\s+build|image\s+build)\b[^\r\n]*?(?:--output|-o)(?:=|\s+)["']?type=image,[^\s"']*\bpush=true\b/iu,
    "push a built image through the image exporter"],
  [/\bghcr\.io\b/iu, "name a GHCR publication target"],
  [/\bactions\/attest\b/iu, "publish a GitHub attestation"],
  [/\b(?:wrangler|cloudflare\/wrangler-action|terraform\s+apply)\b/iu,
    "provision DNS or infrastructure"],
  [/\$\{\{\s*(?:secrets|vars)\./iu, "read production configuration"],
];
for (const [pattern, action] of forbiddenWorkflowPatterns) {
  requireCondition(!pattern.test(workflowShell), `Deck CI must not ${action}`);
}

const requiredDeckScopes = new Map([
  ["deck-proto", "proto"],
  ["deck-go-quality", "server"],
  ["deck-postgres", "server"],
  ["deck-github-fixtures", "server"],
  ["deck-no-server-scheduler", "server"],
  ["deck-refresh-billing-races", "server"],
  ["deck-frontend", "frontend"],
  ["deck-auth-smoke", "native"],
  ["deck-widget-policy", "widgets"],
  ["deck-observation-reports", "reports"],
  ["deck-server-images", "packaging"],
  ["deck-safety", "safety"],
]);
const deckChanges = jobs["deck-changes"];
for (const scope of new Set(requiredDeckScopes.values())) {
  requireCondition(
    deckChanges?.outputs?.[scope] === `\${{ steps.filter.outputs.${scope} }}`,
    `deck-changes must expose the ${scope} affected scope`,
  );
}
for (const [jobName, scope] of requiredDeckScopes) {
  const job = jobs[jobName];
  requireCondition(job?.needs === "deck-changes", `${jobName} must depend on deck-changes`);
  requireCondition(
    typeof job?.if === "string" &&
      job.if.includes("github.event_name == 'workflow_dispatch'") &&
      job.if.includes(`needs.deck-changes.outputs.${scope} == 'true'`),
    `${jobName} must run only for manual dispatch or its ${scope} affected scope`,
  );
}
requireCondition(
  workflow.includes('Manual dispatch runs every Deck validation scope'),
  "manual dispatch behavior must be documented in the change detector",
);

const ciResult = jobs["ci-result"];
const allNonAggregateJobs = Object.keys(jobs).filter((name) => name !== "ci-result").toSorted();
const aggregateNeeds = Array.isArray(ciResult?.needs) ? ciResult.needs.toSorted() : [];
requireCondition(
  JSON.stringify(aggregateNeeds) === JSON.stringify(allNonAggregateJobs),
  "CI Result must depend on every existing and Deck-specific job exactly once",
);
requireCondition(ciResult?.if === "always()", "CI Result must always evaluate dependencies");
const aggregateSteps = ciResult?.steps ?? [];
requireCondition(
    aggregateSteps.length === 1 &&
    aggregateSteps[0]?.run === "exit 1" &&
    aggregateSteps[0]?.if ===
      "${{ always() && (contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled')) }}",
  "CI Result must fail exactly for failed or cancelled dependencies while accepting affected no-op skips",
);

const rootPackage = JSON.parse(packageSource);
const turbo = JSON.parse(turboSource);
const appPackage = JSON.parse(appPackageSource);
const protoPackage = JSON.parse(protoPackageSource);
for (const task of [
  "ci:deck:proto", "ci:deck:go:unit", "ci:deck:go:github-fixtures",
  "ci:deck:go:no-scheduler", "ci:deck:go:refresh-race", "ci:deck:frontend",
  "ci:deck:auth-smoke", "ci:deck:widget-policy", "ci:deck:widget:android",
  "ci:deck:widget:ios", "ci:deck:reports", "ci:deck:safety",
]) {
  requireCondition(Boolean(rootPackage.scripts?.[task]), `root package must expose ${task}`);
}
for (const task of [
  "ci:deck:proto", "ci:deck:frontend", "ci:deck:auth-smoke",
  "ci:deck:widget-policy", "ci:deck:widget:android", "ci:deck:widget:ios",
]) {
  requireCondition(turbo.tasks?.[task]?.cache === false, `Turborepo must run ${task} uncached`);
}
requireCondition(
  turbo.tasks?.["ci:deck:proto"]?.env?.length === 1 &&
    turbo.tasks["ci:deck:proto"].env[0] === "DEVHUD_DECK_PROTO_BASELINE" &&
    protoPackage.scripts?.["ci:deck:proto"]?.includes("check:proto") &&
    !protoPackage.scripts?.["ci:deck:proto"]?.includes("realqa"),
  "Deck compatibility must resolve only its immutable devhud.deck.v1 baseline",
);
requireCondition(
  appPackage.scripts?.["ci:deck:frontend"]?.includes("typecheck") &&
    appPackage.scripts["ci:deck:frontend"].includes("lint") &&
    appPackage.scripts["ci:deck:frontend"].includes("test:deck") &&
    appPackage.scripts["ci:deck:frontend"].includes("test:a11y") &&
    appPackage.scripts["ci:deck:frontend"].includes("build"),
  "Deck frontend task must retain typecheck, lint, unit, accessibility, and build coverage",
);

const githubManifest = JSON.parse(manifestSource);
requireCondition(
  githubManifest.name.toLowerCase().includes("do not register") &&
    githubManifest.public === false &&
    !/(?:private_key|client_secret|webhook_secret|pem)/iu.test(manifestSource),
  "Deck GitHub App input must remain a credential-free non-registration fixture",
);
requireCondition(
  JSON.stringify(githubManifest.default_events.toSorted()) ===
    JSON.stringify(["installation", "installation_repositories", "installation_target"]),
  "Deck fixture may subscribe only to installation lifecycle events",
);
requireCondition(
  !catalogSource.includes("deck_github_pull_request_refresh") &&
    JSON.parse(catalogSource).apps.every(({ enabled }) => enabled === false) &&
    JSON.parse(catalogSource).meters.every(({ enabled }) => enabled === false) &&
    JSON.parse(catalogSource).services.every(({ enabled }) => enabled === false),
  "Deck catalog identities must remain absent and every checked-in catalog artifact disabled",
);
requireCondition(
  serverContractSource.includes("TestRefreshImplementationHasNoSchedulerOrBackgroundAuthorization") &&
    serverContractSource.includes('"time.NewTicker"') &&
    serverContractSource.includes('"BackgroundUsage"'),
  "Deck must retain a source assertion against schedulers and background billing",
);
requireCondition(
  loggingTestSource.includes("TestRefreshLatencyMetricHasOnlyRedactedClosedFields") &&
    authTestSource.includes("TestStripCredentialsRemovesEveryDeckCredential"),
  "Deck must test content-safe latency fields and credential stripping",
);

const supplyChain = JSON.parse(supplyChainSource);
requireCondition(
  supplyChain.artifact_only === true &&
    JSON.stringify(supplyChain.image_platforms) === JSON.stringify(["linux/amd64", "linux/arm64"]) &&
    supplyChain.runtime_user === "65532:65532" &&
    supplyChain.publishes_image === false && supplyChain.publishes_attestation === false &&
    supplyChain.deploys_service === false && supplyChain.provisions_dns === false &&
    supplyChain.registers_github_app === false && supplyChain.activates_catalog === false,
  "Deck image/SBOM/signature/attestation definitions must be local non-root fixtures only",
);
requireCondition(
  reportBuilderSource.includes('numericSlo: null') &&
    !/(?:threshold|objective|target|budget)\s*:\s*\d/iu.test(reportBuilderSource) &&
    workflow.includes("diff -ru deck-reports-first deck-reports-second") &&
    workflow.includes("latency.json") && workflow.includes("query.json") &&
    workflow.includes("mutation.json") && workflow.includes("widget-size.json"),
  "Deck observations must be deterministic, redacted, and define no numeric SLO",
);

if (failures.length > 0) {
  throw new Error(`Deck CI safety contract failed:\n- ${failures.join("\n- ")}`);
}
console.log(JSON.stringify({
  check: "deck-ci-safety",
  status: "passed",
  deploymentAuthority: false,
  productionIdentity: false,
  numericSlo: null,
}));
