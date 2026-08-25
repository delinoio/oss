#!/usr/bin/env node

import { mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { updaterTargets } from "./devhud-release.mjs";

const desktopChecks = ["namesVersionsTargets", "cefHelpers", "cefSandbox", "trayLifecycle", "updaterMaterial", "nativeMessaging", "installLaunchQuitUninstall"];
const required = new Map([
  ...updaterTargets.map(({ id }) => [id, [...desktopChecks, ...(id.startsWith("macos") || id.startsWith("windows") ? ["platformSignature"] : [])]]),
  ["ios-app-store", ["namesVersionsTargets", "platformSignature", "nativeDeckWidget", "installLaunchQuit"]],
  ["android-google-play", ["namesVersionsTargets", "platformSignature", "nativeDeckWidget", "installLaunchQuit"]],
  ["chrome-extension", ["namesVersionsTargets", "permissions", "reproducible", "byteParity", "nativeMessagingIdentity"]],
  ["devhud-api-oci", ["namesVersionsTargets", "multiArch", "nonRoot", "health", "migrations", "administratorAssets"]],
  ["devhud-api-sweeper-oci", ["namesVersionsTargets", "multiArch", "nonRoot", "health", "migrations", "administratorAssets"]],
]);

function evidenceEntry(id, checks) {
  const expected = required.get(id);
  if (!expected) throw new Error(`unsupported evidence target: ${id}`);
  const supplied = new Set(checks);
  const missing = expected.filter((check) => !supplied.has(check));
  const unexpected = [...supplied].filter((check) => !expected.includes(check));
  if (missing.length || unexpected.length) throw new Error(`evidence checks mismatch for ${id}; missing=${missing.join(",")} unexpected=${unexpected.join(",")}`);
  return { schemaVersion: 1, id, checks: Object.fromEntries(expected.map((check) => [check, true])) };
}

export function recordEvidence(id, outputPath, checks) {
  const evidence = evidenceEntry(id, checks);
  mkdirSync(dirname(outputPath), { recursive: true });
  writeFileSync(outputPath, `${JSON.stringify(evidence, null, 2)}\n`);
}

export function validateEvidenceEntries(entries) {
  const seen = new Set(entries.map(({ id }) => id));
  const missing = [...required.keys()].filter((id) => !seen.has(id));
  const unexpected = [...seen].filter((id) => !required.has(id));
  if (missing.length || unexpected.length || seen.size !== entries.length) throw new Error(`evidence target set is incomplete, unexpected, or duplicated: missing=${missing.join(",")} unexpected=${unexpected.join(",")}`);
  for (const entry of entries) evidenceEntry(entry.id, Object.keys(entry.checks ?? {}).filter((key) => entry.checks[key] === true));
}

export function mergeEvidence(inputDirectory, outputPath) {
  const entries = readdirSync(inputDirectory).filter((name) => name.endsWith(".json")).sort().map((name) => JSON.parse(readFileSync(join(inputDirectory, name), "utf8")));
  validateEvidenceEntries(entries);
  const result = { schemaVersion: 1, readiness: "private-signed-candidate", publicReady: false, targets: entries };
  mkdirSync(dirname(outputPath), { recursive: true });
  writeFileSync(outputPath, `${JSON.stringify(result, null, 2)}\n`);
}

function main(arguments_) {
  const command = arguments_.shift();
  const options = {};
  for (let index = 0; index < arguments_.length; index += 2) options[arguments_[index].replace(/^--/u, "")] = arguments_[index + 1];
  if (command === "record") recordEvidence(options.id, resolve(options.output), (options.checks ?? "").split(",").filter(Boolean));
  else if (command === "merge") mergeEvidence(resolve(options.input), resolve(options.output));
  else throw new Error("usage: devhud-evidence.mjs <record|merge> ...");
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { main(process.argv.slice(2)); } catch (error) {
    console.error(`[devhud.evidence] ${error.message}`);
    process.exit(1);
  }
}
