import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, copyFileSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { parseArgs } from "node:util";
import { gzipSync, gunzipSync } from "node:zlib";
import platforms from "../src/platforms.cjs";
import { ensure, event, isMain, metadata, npm, packageRoot, registry, repository, revision, sourceText } from "./common.mjs";

const { targets } = platforms;
const mainName = "@delino/clibox";
const mainFiles = ["bin/clibox.cjs", "src/launcher.cjs", "src/platforms.cjs"];
export const integrity = (bytes) => `sha512-${createHash("sha512").update(bytes).digest("base64")}`;
export const tarballName = (name, version) => `${name.replace(/^@/u, "").replace("/", "-")}-${version}.tgz`;

export function packageManifest(target, version, sourceRevision) {
  ensure(/^[a-f0-9]{40}$/u.test(sourceRevision), "Expected an exact source commit");
  const manifest = {
    name: target?.name ?? mainName,
    version,
    description: target ? `Native clibox executable for ${target.suffix}` : metadata().description,
    license: "Apache-2.0",
    repository: { type: "git", url: repository, directory: "packages/clibox" },
    gitHead: sourceRevision,
    publishConfig: { access: "public", registry },
    files: target ? [`bin/${target.binary}`] : mainFiles,
  };
  if (target) {
    Object.assign(manifest, { os: [target.os], cpu: [target.cpu] });
    if (target.libc) manifest.libc = [target.libc];
  } else {
    Object.assign(manifest, {
      engines: { node: ">=22" },
      bin: { clibox: "bin/clibox.cjs" },
      exports: {},
      optionalDependencies: Object.fromEntries(targets.map(({ name }) => [name, version])),
    });
  }
  return manifest;
}

export function buildPackage({ target, binary, output, sourceRevision = revision() }) {
  const { version } = metadata();
  if (target) {
    ensure(targets.includes(target), "Unknown binary target");
    ensure(target.os === process.platform && target.cpu === process.arch, "Pack and smoke-test each binary on its native OS/architecture");
    ensure(execFileSync(path.resolve(binary), ["--version"], { encoding: "utf8" }).trim() === `clibox ${version}`, "Executable/source version mismatch");
  }
  const directory = path.join(path.resolve(output), target?.suffix ?? "main");
  rmSync(directory, { recursive: true, force: true });
  mkdirSync(path.join(directory, "bin"), { recursive: true });
  writeFileSync(path.join(directory, "README.md"), sourceText("packages/clibox/README.md"));
  writeFileSync(path.join(directory, "LICENSE"), sourceText("crates/clibox/LICENSE"));
  if (target) {
    copyFileSync(path.resolve(binary), path.join(directory, "bin", target.binary));
    chmodSync(path.join(directory, "bin", target.binary), 0o755);
  } else {
    mkdirSync(path.join(directory, "src"));
    for (const file of mainFiles) writeFileSync(path.join(directory, file), sourceText(`packages/clibox/${file}`));
    chmodSync(path.join(directory, "bin/clibox.cjs"), 0o755);
  }
  const manifest = packageManifest(target, version, sourceRevision);
  writeFileSync(path.join(directory, "package.json"), JSON.stringify(manifest, null, 2) + "\n");
  const destination = path.resolve(output, "tarballs");
  mkdirSync(destination, { recursive: true });
  const [packed] = JSON.parse(npm(["pack", "--json", "--ignore-scripts", "--pack-destination", destination], { cwd: directory }));
  ensure(packed.filename === tarballName(manifest.name, version), "Unexpected npm tarball filename");
  const file = path.join(destination, packed.filename);
  const bytes = readFileSync(file);
  ensure(integrity(bytes) === packed.integrity, "npm pack integrity mismatch");
  writeFileSync(file, executableTarball(bytes, target ? `bin/${target.binary}` : "bin/clibox.cjs"));
  const artifact = inspectTarball(file, { version, sourceRevision });
  event("pack", artifact);
  return artifact;
}

// npm pack emits short ustar paths for this closed file set. Reject links,
// extensions, duplicate paths, and oversized archives instead of extracting
// arbitrary input. This reader is deliberately not a general tar implementation.
export function tarEntries(bytes) {
  const tar = gunzipSync(bytes, { maxOutputLength: 64 * 1024 * 1024 });
  const entries = new Map();
  const text = (buffer) => buffer.toString("utf8").replace(/\0.*$/su, "");
  let offset = 0;
  for (; offset + 512 <= tar.length; ) {
    const header = tar.subarray(offset, offset + 512);
    if (header.every((value) => value === 0)) break;
    const checksum = Number.parseInt(text(header.subarray(148, 156)).trim(), 8);
    ensure(header.reduce((sum, value, i) => sum + (i >= 148 && i < 156 ? 32 : value), 0) === checksum, "Invalid tar header checksum");
    const prefix = text(header.subarray(345, 500));
    const name = `${prefix ? `${prefix}/` : ""}${text(header.subarray(0, 100))}`;
    const size = Number.parseInt(text(header.subarray(124, 136)).trim(), 8);
    const mode = Number.parseInt(text(header.subarray(100, 108)).trim(), 8);
    ensure([0, 48].includes(header[156]) && !entries.has(name), "Unexpected tar entry type or duplicate file");
    ensure(Number.isSafeInteger(size) && size >= 0 && offset + 512 + size <= tar.length, "Invalid tar entry length");
    ensure(name.startsWith("package/") && !name.split("/").includes("..") && !name.includes("\\"), "Invalid tar entry path");
    entries.set(name.slice(8), { mode, bytes: tar.subarray(offset + 512, offset + 512 + size) });
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  ensure(offset + 1024 <= tar.length && tar.subarray(offset).every((value) => value === 0), "Invalid tar ending");
  return entries;
}

// NTFS cannot represent Unix execute bits, and npm pack does not always add
// them to bin entries. Set the generated archive's executable mode before
// recording its final integrity. Keep this at creation only: verification of
// downloaded/retry artifacts must reject missing modes without changing bytes.
export function executableTarball(bytes, executable) {
  const entries = tarEntries(bytes);
  ensure(entries.has(executable), "Missing generated executable");
  const tar = gunzipSync(bytes, { maxOutputLength: 64 * 1024 * 1024 });
  let offset = 0;
  for (const [name, entry] of entries) {
    if (name === executable) {
      const header = tar.subarray(offset, offset + 512);
      header.write("0000755\0", 100, 8, "ascii");
      header.fill(32, 148, 156);
      const checksum = header.reduce((sum, value) => sum + value, 0);
      header.write(`${checksum.toString(8).padStart(6, "0")}\0 `, 148, 8, "ascii");
      break;
    }
    offset += 512 + Math.ceil(entry.bytes.length / 512) * 512;
  }
  return gzipSync(tar, { level: 9 });
}

export function inspectTarball(file, { version, sourceRevision }) {
  const bytes = readFileSync(file);
  const entries = tarEntries(bytes);
  const manifest = JSON.parse(entries.get("package.json")?.bytes.toString("utf8") ?? "null");
  const target = targets.find(({ name }) => name === manifest?.name);
  ensure(target || manifest?.name === mainName, "Unexpected npm package");
  const expected = packageManifest(target, version, sourceRevision);
  ensure(JSON.stringify(manifest) === JSON.stringify(expected), `Package metadata mismatch: ${manifest.name}`);
  const expectedFiles = [...expected.files, "package.json", "README.md", "LICENSE"].sort();
  ensure(JSON.stringify([...entries.keys()].sort()) === JSON.stringify(expectedFiles), "Unexpected package file inventory");
  const executable = target ? `bin/${target.binary}` : "bin/clibox.cjs";
  // NTFS has no POSIX execute bits, and npm may preserve mode 0644 for .exe
  // payloads packed on Windows. Only Unix executables and the npm bin shim need
  // the archive execute bits; the Windows loader uses the PE executable itself.
  ensure(entries.get(executable).bytes.length > 0, "Empty executable");
  if (target?.os !== platforms.Platform.Windows) ensure((entries.get(executable).mode & 0o111) === 0o111, "Missing executable mode");
  if (!target) for (const file of mainFiles) ensure(entries.get(file).bytes.equals(Buffer.from(sourceText(`packages/clibox/${file}`))), `Launcher source mismatch: ${file}`);
  ensure(entries.get("LICENSE").bytes.equals(Buffer.from(sourceText("crates/clibox/LICENSE"))), "License mismatch");
  ensure(entries.get("README.md").bytes.equals(Buffer.from(sourceText("packages/clibox/README.md"))), "README mismatch");
  ensure(path.basename(file) === tarballName(manifest.name, version), "Tarball name mismatch");
  return { name: manifest.name, version, revision: sourceRevision, filename: path.basename(file), integrity: integrity(bytes) };
}

export function verifySet(directory, sourceRevision = revision()) {
  const { version } = metadata();
  const expected = [...targets.map(({ name }) => name), mainName];
  const files = readdirSync(directory).sort();
  ensure(JSON.stringify(files) === JSON.stringify(expected.map((name) => tarballName(name, version)).sort()), "Expected exactly nine clibox tarballs");
  return expected.map((name) => inspectTarball(path.join(directory, tarballName(name, version)), { version, sourceRevision }));
}

export function main() {
  const { values, positionals } = parseArgs({ allowPositionals: true, options: { target: { type: "string" }, binary: { type: "string" }, output: { type: "string", default: path.join(packageRoot, "dist") } } });
  ensure(positionals.length === 1, "Expected main, binary, or verify");
  const [command] = positionals;
  if (command === "verify") event("verified", { packages: verifySet(values.output) });
  else if (command === "main") buildPackage({ output: values.output });
  else if (command === "binary") {
    const target = targets.find(({ rust }) => rust === values.target);
    ensure(target && values.binary, "Expected a supported --target and --binary");
    buildPackage({ target, binary: values.binary, output: values.output });
  } else throw new Error("Expected main, binary, or verify");
}

if (isMain(import.meta.url)) main();
