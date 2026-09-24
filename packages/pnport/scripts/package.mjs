import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, copyFileSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { parseArgs } from "node:util";
import { gzipSync, gunzipSync } from "node:zlib";
import { createRequire } from "node:module";
import { companion, launcherManifest, nativeManifest } from "./manifests.mjs";
import { ensure, event, isMain, metadata, npm, packageRoot, revision, root, sourceText } from "./common.mjs";

const require = createRequire(import.meta.url);
const { targets } = require("../src/platforms.cjs");
const mainFiles = ["bin/pnport.cjs", "src/launcher.cjs", "src/platforms.cjs"];
const identity = (bytes) => `sha512-${createHash("sha512").update(bytes).digest("base64")}`;
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
export const tarballName = (name, version) => `${name.replace(/^@/u, "").replace("/", "-")}-${version}.tgz`;
export const archiveName = (target) => `pnport-${target.suffix}.tar.gz`;
export const packageNames = (version) => [...targets.map(({ name }) => name), "@delino/pnport"].map((name) => tarballName(name, version));

// This closed ustar reader rejects links, duplicates, extensions, and traversal.
// Verification never rewrites an already packed or downloaded artifact.
export function tarEntries(bytes, prefix = "package/") {
  const tar = gunzipSync(bytes, { maxOutputLength: 256 * 1024 * 1024 });
  const entries = new Map();
  const text = (buffer) => buffer.toString("utf8").replace(/\0.*$/su, "");
  let offset = 0;
  for (; offset + 512 <= tar.length;) {
    const header = tar.subarray(offset, offset + 512);
    if (header.every((value) => value === 0)) break;
    const checksum = Number.parseInt(text(header.subarray(148, 156)).trim(), 8);
    ensure(header.reduce((sum, value, i) => sum + (i >= 148 && i < 156 ? 32 : value), 0) === checksum, "Invalid tar header checksum");
    const name = `${text(header.subarray(345, 500)) ? `${text(header.subarray(345, 500))}/` : ""}${text(header.subarray(0, 100))}`;
    const size = Number.parseInt(text(header.subarray(124, 136)).trim(), 8);
    const mode = Number.parseInt(text(header.subarray(100, 108)).trim(), 8);
    ensure([0, 48].includes(header[156]) && !entries.has(name), "Unexpected tar entry type or duplicate");
    ensure(Number.isSafeInteger(size) && size >= 0 && offset + 512 + size <= tar.length, "Invalid tar entry length");
    ensure(name.startsWith(prefix) && !name.split("/").includes("..") && !name.includes("\\"), "Invalid tar entry path");
    entries.set(name.slice(prefix.length), { mode, bytes: tar.subarray(offset + 512, offset + 512 + size) });
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  ensure(offset + 1024 <= tar.length && tar.subarray(offset).every((value) => value === 0), "Invalid tar ending");
  return entries;
}

function header(name, size, mode) {
  ensure(Buffer.byteLength(name) <= 100, "Archive path too long");
  const bytes = Buffer.alloc(512);
  const octal = (value, offset, width) => bytes.write(`${value.toString(8).padStart(width - 1, "0")}\0`, offset, width, "ascii");
  bytes.write(name, 0, "utf8");
  octal(mode, 100, 8); octal(0, 108, 8); octal(0, 116, 8); octal(size, 124, 12); octal(0, 136, 12);
  bytes.fill(32, 148, 156); bytes[156] = 48; bytes.write("ustar\0", 257); bytes.write("00", 263);
  bytes.write(`${bytes.reduce((sum, byte) => sum + byte, 0).toString(8).padStart(6, "0")}\0 `, 148, 8, "ascii");
  return bytes;
}

export function nativeArchive(target, binary, preload) {
  const files = [[target.binary, binary, target.os === "win32" ? 0o644 : 0o755], [companion(target), preload, 0o644], ["LICENSE", Buffer.from(sourceText("crates/pnport/LICENSE")), 0o644]];
  if (target.os !== "win32") files.push(["LICENSE.fspy", Buffer.from(sourceText("crates/fspy/LICENSE")), 0o644]);
  const parts = files.flatMap(([name, bytes, mode]) => [header(name, bytes.length, mode), bytes, Buffer.alloc((512 - bytes.length % 512) % 512)]);
  return gzipSync(Buffer.concat([...parts, Buffer.alloc(1024)]), { level: 9 });
}

// NTFS does not carry POSIX executable bits. Set the npm bin mode before
// calculating immutable integrity; inspection only checks, never repairs.
function executableTarball(bytes, executable) {
  const entries = tarEntries(bytes);
  ensure(entries.has(executable), "Missing executable in npm package");
  const tar = gunzipSync(bytes, { maxOutputLength: 256 * 1024 * 1024 });
  let offset = 0;
  for (const [name, entry] of entries) {
    if (name === executable) {
      const block = tar.subarray(offset, offset + 512);
      block.write("0000755\0", 100, 8, "ascii"); block.fill(32, 148, 156);
      block.write(`${block.reduce((sum, value) => sum + value, 0).toString(8).padStart(6, "0")}\0 `, 148, 8, "ascii");
      break;
    }
    offset += 512 + Math.ceil(entry.bytes.length / 512) * 512;
  }
  return gzipSync(tar, { level: 9 });
}

function manifest(target, version, sourceRevision) {
  const value = target ? nativeManifest(target.suffix, version, sourceRevision) : launcherManifest(version, sourceRevision);
  value.repository = { ...value.repository, directory: "packages/pnport" };
  value.publishConfig = { access: "public", registry: "https://registry.npmjs.org" };
  if (target) value.files = [`bin/${target.binary}`, `bin/${companion(target)}`, "README.md", "LICENSE", ...(target.os !== "win32" ? ["LICENSE.fspy"] : [])];
  else value.files = [...mainFiles, "README.md", "LICENSE"];
  return value;
}

export function inspectTarball(file, version, sourceRevision) {
  const bytes = readFileSync(file);
  const entries = tarEntries(bytes);
  const actual = JSON.parse(entries.get("package.json")?.bytes.toString("utf8") ?? "null");
  const target = targets.find(({ name }) => name === actual?.name);
  ensure(target || actual?.name === "@delino/pnport", "Unknown pnport package");
  const expected = manifest(target, version, sourceRevision);
  ensure(JSON.stringify(actual) === JSON.stringify(expected), "pnport package metadata mismatch");
  const files = [...expected.files, "package.json"].sort();
  ensure(JSON.stringify([...entries.keys()].sort()) === JSON.stringify(files), "pnport package file inventory mismatch");
  const executable = target ? `bin/${target.binary}` : "bin/pnport.cjs";
  ensure(entries.get(executable).bytes.length > 0, "Empty pnport executable");
  if (target && target.os !== "win32") ensure((entries.get(executable).mode & 0o111) === 0o111, "Missing executable mode");
  if (target) {
    const preload = entries.get(`bin/${companion(target)}`)?.bytes;
    ensure(preload?.length > 0, "Missing native companion");
    if (target.os === "darwin") ensure(preload.includes(Buffer.from("PNPORT_PRELOAD_0.1.0_FORMAT_1_READY")), "fspy companion ABI mismatch");
  }
  else for (const name of mainFiles) ensure(entries.get(name).bytes.equals(Buffer.from(sourceText(`packages/pnport/${name}`))), `Launcher source mismatch: ${name}`);
  for (const [name, source] of [["LICENSE", "crates/pnport/LICENSE"], ["README.md", "packages/pnport/README.md"], ...(target && target.os !== "win32" ? [["LICENSE.fspy", "crates/fspy/LICENSE"]] : [])]) ensure(entries.get(name).bytes.equals(Buffer.from(sourceText(source))), `${name} source mismatch`);
  ensure(path.basename(file) === tarballName(expected.name, version), "npm tarball name mismatch");
  return { name: expected.name, version, revision: sourceRevision, filename: path.basename(file), integrity: identity(bytes) };
}

export function inspectArchive(file, target, npmFile) {
  const archive = tarEntries(readFileSync(file), "");
  const packed = tarEntries(readFileSync(npmFile));
  const names = [target.binary, companion(target), "LICENSE", ...(target.os !== "win32" ? ["LICENSE.fspy"] : [])].sort();
  ensure(JSON.stringify([...archive.keys()].sort()) === JSON.stringify(names), "Native archive inventory mismatch");
  for (const name of names) ensure(archive.get(name).bytes.equals(packed.get(name.startsWith("LICENSE") ? name : `bin/${name}`).bytes), "Native archive/npm payload mismatch");
  if (target.os !== "win32") ensure((archive.get(target.binary).mode & 0o111) === 0o111, "Native archive executable mode missing");
  return { target: target.suffix, name: path.basename(file), sha256: sha256(readFileSync(file)) };
}

export function verifySet(directory, sourceRevision = revision()) {
  const { version } = metadata();
  const names = packageNames(version);
  const tarballs = path.join(directory, "tarballs");
  const archives = path.join(directory, "archives");
  ensure(JSON.stringify(readdirSync(tarballs).sort()) === JSON.stringify([...names].sort()), "Expected exactly seven pnport npm tarballs");
  ensure(JSON.stringify(readdirSync(archives).sort()) === JSON.stringify(targets.map(archiveName).sort()), "Expected exactly six native archives");
  const packages = names.map((name) => inspectTarball(path.join(tarballs, name), version, sourceRevision));
  const native = targets.map((target) => inspectArchive(path.join(archives, archiveName(target)), target, path.join(tarballs, tarballName(target.name, version))));
  return { version, revision: sourceRevision, packages, native };
}

export function buildPackage({ target, binary, preload, output, sourceRevision = revision() }) {
  const { version } = metadata();
  ensure(/^[a-f0-9]{40}$/u.test(sourceRevision), "Exact source revision required");
  if (target) {
    ensure(targets.includes(target) && target.os === process.platform && target.cpu === process.arch, "Native package must be built on its target host");
    ensure(binary && preload, "Executable and matching preload are required");
    ensure(execFileSync(path.resolve(binary), ["--version"], { encoding: "utf8" }).trim() === `pnport ${version}`, "Native version mismatch");
  }
  const directory = path.join(path.resolve(output), target?.suffix ?? "main");
  rmSync(directory, { recursive: true, force: true });
  mkdirSync(path.join(directory, "bin"), { recursive: true });
  writeFileSync(path.join(directory, "README.md"), sourceText("packages/pnport/README.md"));
  writeFileSync(path.join(directory, "LICENSE"), sourceText("crates/pnport/LICENSE"));
  if (target && target.os !== "win32") writeFileSync(path.join(directory, "LICENSE.fspy"), sourceText("crates/fspy/LICENSE"));
  if (target) {
    for (const [name, input] of [[target.binary, binary], [companion(target), preload]]) copyFileSync(path.resolve(input), path.join(directory, "bin", name));
    if (target.os !== "win32") chmodSync(path.join(directory, "bin", target.binary), 0o755);
  } else {
    mkdirSync(path.join(directory, "src"));
    for (const name of mainFiles) writeFileSync(path.join(directory, name), sourceText(`packages/pnport/${name}`));
    chmodSync(path.join(directory, "bin/pnport.cjs"), 0o755);
  }
  const value = manifest(target, version, sourceRevision);
  writeFileSync(path.join(directory, "package.json"), JSON.stringify(value, null, 2) + "\n");
  const tarballs = path.resolve(output, "tarballs");
  mkdirSync(tarballs, { recursive: true });
  const [packed] = JSON.parse(npm(["pack", "--json", "--ignore-scripts", "--pack-destination", tarballs], { cwd: directory }));
  ensure(packed.filename === tarballName(value.name, version), "Unexpected npm pack filename");
  const npmFile = path.join(tarballs, packed.filename);
  ensure(identity(readFileSync(npmFile)) === packed.integrity, "npm pack integrity mismatch");
  writeFileSync(npmFile, executableTarball(readFileSync(npmFile), target ? `bin/${target.binary}` : "bin/pnport.cjs"));
  const artifact = inspectTarball(npmFile, version, sourceRevision);
  if (target) {
    const archives = path.resolve(output, "archives");
    mkdirSync(archives, { recursive: true });
    const file = path.join(archives, archiveName(target));
    writeFileSync(file, nativeArchive(target, readFileSync(binary), readFileSync(preload)));
    inspectArchive(file, target, npmFile);
  }
  event("pack", artifact);
  return artifact;
}

export function main() {
  const { values, positionals } = parseArgs({ allowPositionals: true, options: { target: { type: "string" }, binary: { type: "string" }, preload: { type: "string" }, output: { type: "string", default: path.join(packageRoot, "dist") } } });
  ensure(positionals.length === 1, "Expected main, binary, or verify");
  const [command] = positionals;
  if (command === "verify") event("verified", verifySet(values.output));
  else if (command === "main") buildPackage({ output: values.output });
  else if (command === "binary") {
    const target = targets.find(({ rust }) => rust === values.target);
    ensure(target, "Unsupported target");
    buildPackage({ target, binary: values.binary, preload: values.preload, output: values.output });
  } else throw new Error("Unknown package command");
}

if (isMain(import.meta.url)) main();
