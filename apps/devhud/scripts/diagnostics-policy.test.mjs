import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const diagnosticsSources = [
  new URL("../src/diagnostics.ts", import.meta.url),
  new URL("../src/diagnostics-ui.tsx", import.meta.url),
  new URL("../src/service-boundary.tsx", import.meta.url),
];
const nativeDiagnosticsSource = new URL("../src-tauri/src/native_plugin.rs", import.meta.url);

test("diagnostics has no analytics SDK or remote feature-flag dependency", async () => {
  const source = (await Promise.all(diagnosticsSources.map((file) => readFile(file, "utf8")))).join("\n");
  for (const prohibited of [
    /segment\.com|@segment\//iu,
    /sentry|datadog|mixpanel|amplitude/iu,
    /launchdarkly|split\.io|statsig|unleash/iu,
  ]) {
    assert.doesNotMatch(source, prohibited);
  }
});

test("persistent native diagnostics omit backend exception text", async () => {
  const source = await readFile(nativeDiagnosticsSource, "utf8");
  assert.doesNotMatch(source, /(?:%|\?)(?:reason|error)\b|(?:reason|error)\s*=\s*(?:%|\?)/u);
});
