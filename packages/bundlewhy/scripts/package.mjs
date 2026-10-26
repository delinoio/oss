import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  chmodSync,
  copyFileSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { parseArgs } from "node:util";
import {
  tarEntries,
  executableTarball,
} from "../../clibox/scripts/package.mjs";
export { tarEntries, executableTarball };
import {
  ensure,
  event,
  isMain,
  metadata,
  npm,
  packageRoot,
  registry,
  repository,
  revision,
  sourceText,
  targets,
} from "./common.mjs";

const mainName = "@delino/bundlewhy";
const mainFiles = ["bin/bundlewhy.cjs"];
export const integrity = (bytes) =>
  `sha512-${createHash("sha512").update(bytes).digest("base64")}`;
export const tarballName = (name, version) =>
  `${name.replace(/^@/u, "").replace("/", "-")}-${version}.tgz`;

export function packageManifest(target, version, sourceRevision) {
  ensure(
    /^[a-f0-9]{40}$/u.test(sourceRevision),
    "Expected an exact source commit",
  );
  const manifest = {
    name: target?.name ?? mainName,
    version,
    description: target
      ? `Native bundlewhy executable for ${target.suffix}`
      : metadata().description,
    license: "Apache-2.0",
    repository: {
      type: "git",
      url: repository,
      directory: "packages/bundlewhy",
    },
    gitHead: sourceRevision,
    publishConfig: { access: "public", registry },
    files: target
      ? [`bin/${target.binary}`, "NOTICE"]
      : [...mainFiles, "NOTICE"],
  };
  if (target) {
    Object.assign(manifest, { os: [target.os], cpu: [target.cpu] });
    if (target.libc) manifest.libc = [target.libc];
  } else {
    Object.assign(manifest, {
      engines: { node: ">=22.18" },
      bin: { bundlewhy: "bin/bundlewhy.cjs" },
      exports: {},
      optionalDependencies: Object.fromEntries(
        targets.map(({ name }) => [name, version]),
      ),
    });
  }
  return manifest;
}

export function buildPackage({
  target,
  binary,
  output,
  sourceRevision = revision(),
}) {
  const { version } = metadata();
  if (target) {
    ensure(targets.includes(target), "Unknown binary target");
    ensure(
      target.os === process.platform && target.cpu === process.arch,
      "Pack and smoke-test each binary on its native OS/architecture",
    );
    ensure(
      execFileSync(path.resolve(binary), ["--version"], {
        encoding: "utf8",
      }).trim() === `bundlewhy ${version}`,
      "Executable/source version mismatch",
    );
  }
  const directory = path.join(path.resolve(output), target?.suffix ?? "main");
  rmSync(directory, { recursive: true, force: true });
  mkdirSync(path.join(directory, "bin"), { recursive: true });
  writeFileSync(
    path.join(directory, "README.md"),
    sourceText("packages/bundlewhy/README.md"),
  );
  writeFileSync(path.join(directory, "LICENSE"), sourceText("LICENSE"));
  writeFileSync(path.join(directory, "NOTICE"), sourceText("NOTICE"));
  if (target) {
    copyFileSync(
      path.resolve(binary),
      path.join(directory, "bin", target.binary),
    );
    chmodSync(path.join(directory, "bin", target.binary), 0o755);
  } else {
    for (const file of mainFiles)
      writeFileSync(
        path.join(directory, file),
        sourceText(`packages/bundlewhy/${file}`),
      );
    chmodSync(path.join(directory, "bin/bundlewhy.cjs"), 0o755);
  }
  const manifest = packageManifest(target, version, sourceRevision);
  writeFileSync(
    path.join(directory, "package.json"),
    JSON.stringify(manifest, null, 2) + "\n",
  );
  const destination = path.resolve(output, "tarballs");
  mkdirSync(destination, { recursive: true });
  const [packed] = JSON.parse(
    npm(
      ["pack", "--json", "--ignore-scripts", "--pack-destination", destination],
      { cwd: directory },
    ),
  );
  ensure(
    packed.filename === tarballName(manifest.name, version),
    "Unexpected npm tarball filename",
  );
  const file = path.join(destination, packed.filename);
  const bytes = readFileSync(file);
  ensure(integrity(bytes) === packed.integrity, "npm pack integrity mismatch");
  writeFileSync(
    file,
    executableTarball(
      bytes,
      target ? `bin/${target.binary}` : "bin/bundlewhy.cjs",
    ),
  );
  const artifact = inspectTarball(file, { version, sourceRevision });
  event("pack", artifact);
  return artifact;
}

export function inspectTarball(file, { version, sourceRevision }) {
  const bytes = readFileSync(file);
  const entries = tarEntries(bytes);
  const manifest = JSON.parse(
    entries.get("package.json")?.bytes.toString("utf8") ?? "null",
  );
  const target = targets.find(({ name }) => name === manifest?.name);
  ensure(target || manifest?.name === mainName, "Unexpected npm package");
  const expected = packageManifest(target, version, sourceRevision);
  ensure(
    JSON.stringify(manifest) === JSON.stringify(expected),
    `Package metadata mismatch: ${manifest.name}`,
  );
  const expectedFiles = [
    ...expected.files,
    "package.json",
    "README.md",
    "LICENSE",
  ].sort();
  ensure(
    JSON.stringify([...entries.keys()].sort()) ===
      JSON.stringify(expectedFiles),
    "Unexpected package file inventory",
  );
  const executable = target ? `bin/${target.binary}` : "bin/bundlewhy.cjs";
  // NTFS has no POSIX execute bits, and npm may preserve mode 0644 for .exe
  // payloads packed on Windows. Only Unix executables and the npm bin shim need
  // the archive execute bits; the Windows loader uses the PE executable itself.
  ensure(entries.get(executable).bytes.length > 0, "Empty executable");
  if (target?.os !== "win32")
    ensure(
      (entries.get(executable).mode & 0o111) === 0o111,
      "Missing executable mode",
    );
  if (!target)
    for (const file of mainFiles)
      ensure(
        entries
          .get(file)
          .bytes.equals(Buffer.from(sourceText(`packages/bundlewhy/${file}`))),
        `Launcher source mismatch: ${file}`,
      );
  ensure(
    entries.get("LICENSE").bytes.equals(Buffer.from(sourceText("LICENSE"))),
    "License mismatch",
  );
  ensure(
    entries.get("NOTICE").bytes.equals(Buffer.from(sourceText("NOTICE"))),
    "Notice mismatch",
  );
  ensure(
    entries
      .get("README.md")
      .bytes.equals(Buffer.from(sourceText("packages/bundlewhy/README.md"))),
    "README mismatch",
  );
  ensure(
    path.basename(file) === tarballName(manifest.name, version),
    "Tarball name mismatch",
  );
  return {
    name: manifest.name,
    version,
    revision: sourceRevision,
    filename: path.basename(file),
    integrity: integrity(bytes),
  };
}

export function verifySet(directory, sourceRevision = revision()) {
  const { version } = metadata();
  const expected = [...targets.map(({ name }) => name), mainName];
  const files = readdirSync(directory).sort();
  ensure(
    JSON.stringify(files) ===
      JSON.stringify(expected.map((name) => tarballName(name, version)).sort()),
    "Expected exactly four bundlewhy tarballs",
  );
  return expected.map((name) =>
    inspectTarball(path.join(directory, tarballName(name, version)), {
      version,
      sourceRevision,
    }),
  );
}

export function main() {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: {
      target: { type: "string" },
      binary: { type: "string" },
      output: { type: "string", default: path.join(packageRoot, "dist") },
    },
  });
  ensure(positionals.length === 1, "Expected main, binary, or verify");
  const [command] = positionals;
  if (command === "verify")
    event("verified", {
      packages: verifySet(path.join(values.output, "tarballs")),
    });
  else if (command === "main") buildPackage({ output: values.output });
  else if (command === "binary") {
    const target = targets.find(({ rust }) => rust === values.target);
    ensure(
      target && values.binary,
      "Expected a supported --target and --binary",
    );
    buildPackage({ target, binary: values.binary, output: values.output });
  } else throw new Error("Expected main, binary, or verify");
}

if (isMain(import.meta.url)) main();
