import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const schemaVersion = 1;
const metadataFile = "native-build.json";
const fields = [
  "bindingFile",
  "profile",
  "schemaVersion",
  "sha256",
  "sourceRevision",
];
const repositoryRoot = fileURLToPath(new URL("../../../", import.meta.url));

export const bindingFilename = (host) => `react-forge.${host.id}.node`;

const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");

export const sourceRevision = () =>
  execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: repositoryRoot,
    encoding: "utf8",
  }).trim();

export async function writeNativeBuildProvenance(
  output,
  host,
  profile,
  revision = sourceRevision(),
) {
  if (profile !== "dev" && profile !== "release")
    throw new Error("Invalid React Forge native build profile.");
  if (!/^[a-f0-9]{40}$/u.test(revision))
    throw new Error("Invalid React Forge source revision.");
  const bindingFile = bindingFilename(host);
  const sha256 = digest(await readFile(join(output, bindingFile)));
  const metadataDirectory = join(output, "release");
  await mkdir(metadataDirectory, { recursive: true });
  await writeFile(
    join(metadataDirectory, metadataFile),
    `${JSON.stringify(
      { schemaVersion, bindingFile, profile, sha256, sourceRevision: revision },
      null,
      2,
    )}\n`,
  );
}

export async function readNativeBuildMetadata(output, host) {
  let metadata;
  try {
    metadata = JSON.parse(
      await readFile(join(output, "release", metadataFile), "utf8"),
    );
  } catch {
    throw new Error(
      "React Forge native build provenance is missing or invalid.",
    );
  }
  if (
    !metadata ||
    typeof metadata !== "object" ||
    Array.isArray(metadata) ||
    JSON.stringify(Object.keys(metadata).sort()) !== JSON.stringify(fields) ||
    metadata.schemaVersion !== schemaVersion ||
    metadata.bindingFile !== bindingFilename(host) ||
    (metadata.profile !== "dev" && metadata.profile !== "release") ||
    typeof metadata.sha256 !== "string" ||
    !/^[a-f0-9]{64}$/u.test(metadata.sha256) ||
    typeof metadata.sourceRevision !== "string" ||
    !/^[a-f0-9]{40}$/u.test(metadata.sourceRevision)
  )
    throw new Error("React Forge native build provenance is invalid.");
  let actual;
  try {
    actual = digest(await readFile(join(output, metadata.bindingFile)));
  } catch {
    throw new Error("React Forge native binding is missing.");
  }
  if (actual !== metadata.sha256)
    throw new Error("React Forge native build provenance digest mismatch.");
  return metadata;
}

export async function readNativeBuildProvenance(output, host) {
  const metadata = await readNativeBuildMetadata(output, host);
  return metadata.profile;
}
