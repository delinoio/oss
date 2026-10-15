import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const schemaVersion = 1;
const metadataFile = "js-build.json";
const requiredFiles = ["index.js", "glb.js", "fbx.js"];
const repositoryRoot = fileURLToPath(new URL("../../../", import.meta.url));
const moduleImportPatterns = [
  /\b(?:import|export)\s+(?:[^"'`]*?\sfrom\s*)?["']([^"']+)["']/gu,
  /\bimport\s*\(\s*["']([^"']+)["']\s*\)/gu,
];

const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");

function assertCleanSourceTree() {
  const status = execFileSync(
    "git",
    ["status", "--porcelain=v1", "--untracked-files=all"],
    { cwd: repositoryRoot, encoding: "utf8" },
  ).trim();
  if (status)
    throw new Error(
      "Cannot record React Forge JavaScript build provenance from a dirty source tree.",
    );
}

export const sourceRevision = () => {
  assertCleanSourceTree();
  return execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: repositoryRoot,
    encoding: "utf8",
  }).trim();
};

function assertRevision(revision) {
  if (!/^[a-f0-9]{40}$/u.test(revision))
    throw new Error("Invalid React Forge source revision.");
}

async function resolveLocalModule(output, importer, specifier) {
  if (!specifier.startsWith(".")) return undefined;

  const candidate = join(output, dirname(importer), specifier);
  const relativeCandidate = relative(output, candidate);
  if (
    isAbsolute(relativeCandidate) ||
    relativeCandidate === ".." ||
    relativeCandidate.startsWith(`..${sep}`)
  )
    return undefined;

  const candidates = [
    candidate,
    `${candidate}.js`,
    `${candidate}.json`,
    join(candidate, "index.js"),
  ];
  for (const resolved of candidates) {
    try {
      await readFile(resolved);
      return relative(output, resolved).split(sep).join("/");
    } catch {
      // Try the next Node-compatible extension.
    }
  }
  throw new Error(
    `React Forge JavaScript build output dependency is missing: ${specifier} imported by ${importer}.`,
  );
}

async function importedLocalModules(output, file) {
  const source = await readFile(join(output, file), "utf8");
  const specifiers = new Set();
  for (const pattern of moduleImportPatterns) {
    for (const match of source.matchAll(pattern)) specifiers.add(match[1]);
  }
  return (
    await Promise.all(
      [...specifiers].map((specifier) =>
        resolveLocalModule(output, file, specifier),
      ),
    )
  ).filter((dependency) => dependency !== undefined);
}

async function fileDigests(output) {
  const files = new Set(requiredFiles);
  const pending = [...requiredFiles];
  while (pending.length > 0) {
    const file = pending.pop();
    for (const dependency of await importedLocalModules(output, file)) {
      if (!files.has(dependency)) {
        files.add(dependency);
        pending.push(dependency);
      }
    }
  }
  return Object.fromEntries(
    await Promise.all(
      [...files]
        .sort()
        .map(async (file) => [
          file,
          digest(await readFile(join(output, file))),
        ]),
    ),
  );
}

export async function writeJavaScriptBuildProvenanceFiles(output, revision) {
  assertRevision(revision);
  const files = await fileDigests(output);
  const metadataDirectory = join(output, "release");
  await mkdir(metadataDirectory, { recursive: true });
  await writeFile(
    join(metadataDirectory, metadataFile),
    `${JSON.stringify({ schemaVersion, sourceRevision: revision, files }, null, 2)}\n`,
  );
}

export async function writeJavaScriptBuildProvenance(
  output,
  revision = sourceRevision(),
) {
  assertCleanSourceTree();
  await writeJavaScriptBuildProvenanceFiles(output, revision);
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
