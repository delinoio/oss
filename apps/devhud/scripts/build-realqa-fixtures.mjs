import { createHash } from "node:crypto";
import { cp, mkdir, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { basename, join, relative, resolve } from "node:path";

import { run, runPackageManager } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const extensionRoot = resolve(appRoot, "build/realqa-extension");
const outputRoot = resolve(appRoot, "build/realqa-fixtures");
const cargo = process.platform === "win32" ? "cargo.exe" : "cargo";
const executableName = process.platform === "win32" ? "devhud-native-host.exe" : "devhud-native-host";
const configuredHost = process.env.DEVHUD_REALQA_FIXTURE_NATIVE_HOST;
const nativeHost = configuredHost === undefined
  ? resolve(repositoryRoot, "target/debug", executableName)
  : resolve(configuredHost);

async function filesUnder(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) files.push(...(await filesUnder(path)));
    if (entry.isFile()) files.push(path);
  }
  return files.toSorted();
}

await runPackageManager(["run", "build"], { cwd: appRoot });
await runPackageManager(["run", "build:realqa:extension"], { cwd: appRoot });
const nativeMessagingManifest = JSON.parse(
  await readFile(resolve(extensionRoot, "dev.deli.devhud.realqa.json"), "utf8"),
);
const [allowedOrigin] = nativeMessagingManifest.allowed_origins ?? [];
const extensionId = /^chrome-extension:\/\/([a-p]{32})\/$/u.exec(
  allowedOrigin ?? "",
)?.[1];
if (extensionId === undefined) {
  throw new Error("the fixture Native Messaging manifest has no exact Chrome extension origin");
}
if (configuredHost === undefined) {
  await run(cargo, ["build", "-p", "devhud", "--bin", "devhud-native-host", "--locked"], {
    cwd: repositoryRoot,
    env: { ...process.env, DEVHUD_CHROME_EXTENSION_ID: extensionId },
  });
}
if (!(await stat(nativeHost)).isFile()) {
  throw new Error("the RealQA fixture Native Messaging host is not a file");
}

await rm(outputRoot, { force: true, recursive: true });
await mkdir(resolve(outputRoot, "desktop/frontend"), { recursive: true });
await cp(extensionRoot, resolve(outputRoot, "extension"), { recursive: true });
await cp(resolve(appRoot, "dist"), resolve(outputRoot, "desktop/frontend"), {
  recursive: true,
});
await cp(nativeHost, resolve(outputRoot, "desktop", executableName));
await cp(
  resolve(extensionRoot, "dev.deli.devhud.realqa.json"),
  resolve(outputRoot, "desktop/dev.deli.devhud.realqa.json"),
);

const artifacts = [];
for (const file of await filesUnder(outputRoot)) {
  const bytes = await readFile(file);
  artifacts.push({
    path: relative(outputRoot, file).replaceAll("\\", "/"),
    sha256: createHash("sha256").update(bytes).digest("hex"),
    size: bytes.length,
  });
}
const manifest = {
  schemaVersion: 1,
  fixtureOnly: true,
  publishable: false,
  signedHostReady: true,
  productionIdentityInjected: false,
  target: process.env.DEVHUD_REALQA_FIXTURE_TARGET ?? `${process.platform}-${process.arch}`,
  extensionId,
  nativeHost: basename(nativeHost),
  artifacts,
};
await writeFile(
  resolve(outputRoot, "artifact-manifest.json"),
  `${JSON.stringify(manifest, null, 2)}\n`,
  "utf8",
);

console.log(JSON.stringify({
  check: "devhud-realqa-fixture-artifacts",
  status: "passed",
  artifactCount: artifacts.length,
  target: manifest.target,
}));
