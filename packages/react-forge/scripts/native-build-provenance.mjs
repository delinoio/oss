import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

const schemaVersion = 1;
const metadataFile = "native-build.json";
const fields = ["bindingFile", "profile", "schemaVersion", "sha256"];

export const bindingFilename = (host) => `react-forge.${host.id}.node`;

const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");

export async function writeNativeBuildProvenance(output, host, profile) {
  if (profile !== "dev" && profile !== "release")
    throw new Error("Invalid React Forge native build profile.");
  const bindingFile = bindingFilename(host);
  const sha256 = digest(await readFile(join(output, bindingFile)));
  const metadataDirectory = join(output, "release");
  await mkdir(metadataDirectory, { recursive: true });
  await writeFile(
    join(metadataDirectory, metadataFile),
    `${JSON.stringify({ schemaVersion, bindingFile, profile, sha256 }, null, 2)}\n`,
  );
}

export async function readNativeBuildProvenance(output, host) {
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
    !/^[a-f0-9]{64}$/u.test(metadata.sha256)
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
  return metadata.profile;
}
