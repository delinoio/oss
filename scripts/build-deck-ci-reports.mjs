import { createHash } from "node:crypto";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { resolve } from "node:path";

const repositoryRoot = resolve(import.meta.dirname, "..");
const outputArgument = process.argv.slice(2).find((argument) => argument !== "--") ??
  "build/deck-ci-reports";
const outputRoot = resolve(repositoryRoot, outputArgument);
const read = (path) => readFile(resolve(repositoryRoot, path), "utf8");
const byteLength = async (path) => Buffer.byteLength(await read(path));
const testNames = (source, pattern) =>
  [...source.matchAll(/^func (Test[A-Za-z0-9_]+)\(/gmu)]
    .map((match) => match[1])
    .filter((name) => pattern.test(name))
    .toSorted();

const [contracts, queryTests, githubTests, serviceTests] = await Promise.all([
  read("servers/devhud-deck/internal/contracts/contracts.go"),
  read("servers/devhud-deck/internal/query/query_test.go"),
  read("servers/devhud-deck/internal/github/client_test.go"),
  read("servers/devhud-deck/internal/service/refresh_test.go"),
]);

const common = {
  schemaVersion: 1,
  artifactOnly: true,
  containsUserContent: false,
  containsCredentials: false,
  numericSlo: null,
};
const reports = {
  "latency.json": {
    ...common,
    report: "deck-refresh-latency-interface",
    observation: {
      event: "deck_refresh_latency",
      closedFields: ["event", "latency_ms", "outcome"],
      implementationPresent:
        contracts.includes('"event", "deck_refresh_latency"') &&
        contracts.includes('"latency_ms", elapsed.Milliseconds()'),
    },
  },
  "query.json": {
    ...common,
    report: "deck-query-fixture-coverage",
    observation: {
      deterministicTestNames: testNames(queryTests, /./u),
    },
  },
  "mutation.json": {
    ...common,
    report: "deck-mutation-fixture-coverage",
    observation: {
      deterministicTestNames: [
        ...testNames(githubTests, /Mutation|Merge|Assignee|Label|Reviewer/u),
        ...testNames(serviceTests, /Provider|RefreshResults/u),
      ].toSorted(),
    },
  },
  "widget-size.json": {
    ...common,
    report: "deck-widget-source-size-observation",
    observation: {
      byteSizes: {
        "widget-configuration.v1.json": await byteLength(
          "apps/devhud/native-widgets/fixtures/widget-configuration.v1.json",
        ),
        "DevHudWidget.swift": await byteLength(
          "apps/devhud/native-widgets/ios/Sources/Extension/DevHudWidget.swift",
        ),
        "DevHudWidgetProvider.kt": await byteLength(
          "apps/devhud/native-widgets/android/widget-foundation/src/main/java/dev/deli/devhud/widget/DevHudWidgetProvider.kt",
        ),
      },
    },
  },
};

await rm(outputRoot, { recursive: true, force: true });
await mkdir(outputRoot, { recursive: true });
const manifest = [];
for (const name of Object.keys(reports).toSorted()) {
  const body = `${JSON.stringify(reports[name], null, 2)}\n`;
  await writeFile(resolve(outputRoot, name), body);
  manifest.push({
    name,
    sha256: createHash("sha256").update(body).digest("hex"),
  });
}
await writeFile(
  resolve(outputRoot, "manifest.json"),
  `${JSON.stringify({ ...common, reports: manifest }, null, 2)}\n`,
);

console.log(
  JSON.stringify({
    check: "deck-ci-reports",
    status: "generated",
    deterministicInputsOnly: true,
    numericSlo: null,
  }),
);
