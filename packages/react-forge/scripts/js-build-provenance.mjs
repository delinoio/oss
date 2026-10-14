import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const schemaVersion = 1;
const metadataFile = "js-build.json";
const requiredFiles = ["index.js", "glb.js", "fbx.js"];
const repositoryRoot = fileURLToPath(new URL("../../../", import.meta.url));

const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");

export const sourceRevision = () =>
  execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: repositoryRoot,
    encoding: "utf8",
  }).trim();

function assertRevision(revision) {
  if (!/^[a-f0-9]{40}$/u.test(revision))
    throw new Error("Invalid React Forge source revision.");
}

async function fileDigests(output) {
  return Object.fromEntries(
    await Promise.all(
      requiredFiles.map(async (file) => [
        file,
        digest(await readFile(join(output, file))),
      ]),
    ),
  );
}

export async function writeJavaScriptBuildProvenance(
  output,
  revision = sourceRevision(),
) {
  assertRevision(revision);
  const files = await fileDigests(output);
  const metadataDirectory = join(output, "release");
  await mkdir(metadataDirectory, { recursive: true });
  await writeFile(
    join(metadataDirectory, metadataFile),
    `${JSON.stringify({ schemaVersion, sourceRevision: revision, files }, null, 2)}\n`,
  );
}

export async function readJavaScriptBuildMetadata(output, expectedRevision) {
  let metadata;
  try {
    metadata = JSON.parse(
      await readFile(join(output, "release", metadataFile), "utf8"),
    );
  } catch {
    throw new Error(
      "React Forge JavaScript build provenance is missing or invalid.",
    );
  }
  if (
    !metadata ||
    typeof metadata !== "object" ||
    Array.isArray(metadata) ||
    JSON.stringify(Object.keys(metadata).sort()) !==
      JSON.stringify(["files", "schemaVersion", "sourceRevision"]) ||
    metadata.schemaVersion !== schemaVersion ||
    typeof metadata.sourceRevision !== "string"
  )
    throw new Error("React Forge JavaScript build provenance is invalid.");
  assertRevision(metadata.sourceRevision);
  if (expectedRevision && metadata.sourceRevision !== expectedRevision)
    throw new Error(
      "React Forge JavaScript build does not match source revision.",
    );
  let files;
  try {
    files = await fileDigests(output);
  } catch {
    throw new Error("React Forge JavaScript build output is missing.");
  }
  if (JSON.stringify(files) !== JSON.stringify(metadata.files))
    throw new Error("React Forge JavaScript build provenance digest mismatch.");
  return metadata;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1])
  await writeJavaScriptBuildProvenance(
    fileURLToPath(new URL("../dist/", import.meta.url)),
  );
