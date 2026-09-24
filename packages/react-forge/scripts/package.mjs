import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, copyFileSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { parseArgs } from "node:util";
import { fileURLToPath } from "node:url";
import { gunzipSync } from "node:zlib";

export const packageRoot = fileURLToPath(new URL("..", import.meta.url));
export const root = path.resolve(packageRoot, "../..");
export const registry = "https://registry.npmjs.org";
export const platforms = JSON.parse(readFileSync(path.join(packageRoot, "src/native-platforms.json"), "utf8"));
const source = JSON.parse(readFileSync(path.join(packageRoot, "package.json"), "utf8"));
const license = readFileSync(path.join(root, "LICENSE"));
const notice = readFileSync(path.join(root, "NOTICE"));
const repository = { type: "git", url: "git+https://github.com/delinoio/oss.git", directory: "packages/react-forge" };
export const ensure = (condition, message) => { if (!condition) throw new Error(message); };
export const integrity = (bytes) => `sha512-${createHash("sha512").update(bytes).digest("base64")}`;
export const tarballName = (name, version) => `${name.replace(/^@/u, "").replace("/", "-")}-${version}.tgz`;
export const nativeName = (host) => `@delino/react-forge-${host.id}`;
export const sourceRevision = () => execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
export const packageNames = () => [...platforms.map(nativeName), source.name];

function npm(args, options = {}) {
  const windows = process.platform === "win32";
  const command = windows ? process.execPath : "npm";
  const prefix = windows ? [path.join(path.dirname(process.execPath), "node_modules/npm/bin/npm-cli.js")] : [];
  return execFileSync(command, [...prefix, ...args], { encoding: "utf8", stdio: "pipe", ...options });
}

function tarEntries(bytes) {
  const tar = gunzipSync(bytes, { maxOutputLength: 256 * 1024 * 1024 });
  const entries = new Map();
  const text = (buffer) => buffer.toString("utf8").replace(/\0.*$/su, "");
  let offset = 0;
  for (; offset + 512 <= tar.length;) {
    const header = tar.subarray(offset, offset + 512);
    if (header.every((value) => value === 0)) break;
    const checksum = Number.parseInt(text(header.subarray(148, 156)).trim(), 8);
    ensure(header.reduce((sum, value, i) => sum + (i >= 148 && i < 156 ? 32 : value), 0) === checksum, "Invalid tar checksum");
    const name = text(header.subarray(0, 100));
    const size = Number.parseInt(text(header.subarray(124, 136)).trim(), 8);
    ensure([0, 48].includes(header[156]) && !entries.has(name), "Unexpected tar entry");
    ensure(Number.isSafeInteger(size) && size >= 0 && offset + 512 + size <= tar.length, "Invalid tar length");
    ensure(name.startsWith("package/") && !name.split("/").includes("..") && !name.includes("\\"), "Invalid tar path");
    entries.set(name.slice(8), tar.subarray(offset + 512, offset + 512 + size));
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  ensure(offset + 1024 <= tar.length && tar.subarray(offset).every((value) => value === 0), "Invalid tar ending");
  return entries;
}

export function manifest(host = null, revision = sourceRevision()) {
  ensure(/^[a-f0-9]{40}$/u.test(revision), "Exact source commit required");
  ensure(source.name === "@delino/react-forge" && source.private === true && source.license === "Apache-2.0", "Unexpected source package identity");
  const common = { name: host ? nativeName(host) : source.name, version: source.version,
    description: host ? `React Forge native binding for ${host.id}` : "React document authoring for PPTX, DOCX, XLSX and PDF",
    license: "Apache-2.0", repository, gitHead: revision,
    publishConfig: { access: "public", registry } };
  if (host) {
    ensure(platforms.some(({ id }) => id === host.id), "Unknown native host");
    return { ...common, main: "./react-forge.node", files: ["react-forge.node", "README.md", "LICENSE", "NOTICE"],
      os: [host.platform], cpu: [host.architecture], ...(host.platform === "linux" ? { libc: ["glibc"] } : {}) };
  }
  return { ...common, type: "module", engines: source.engines, exports: source.exports, bin: source.bin,
    files: ["dist", "bin", "README.md", "LICENSE", "NOTICE"], dependencies: source.dependencies,
    optionalDependencies: Object.fromEntries(platforms.map((item) => [nativeName(item), source.version])) };
}

function expectedFiles(host) {
  if (host) return ["package.json", "react-forge.node", "README.md", "LICENSE", "NOTICE"].sort();
  const dist = readdirSync(path.join(packageRoot, "dist")).filter((name) => /\.(?:js|d\.ts|json)$/u.test(name));
  ensure(dist.includes("index.js") && dist.includes("native-platforms.json"), "Build the TypeScript package before packing");
  return ["package.json", "bin/react-forge.mjs", "README.md", "LICENSE", "NOTICE", ...dist.map((name) => `dist/${name}`)].sort();
}

function binaryMatches(host, bytes) {
  if (host.platform === "linux") return bytes.subarray(0, 4).equals(Buffer.from([0x7f, 0x45, 0x4c, 0x46])) && bytes.readUInt16LE(18) === (host.architecture === "x64" ? 62 : 183);
  if (host.platform === "darwin") return bytes.readUInt32LE(0) === 0xfeedfacf && bytes.readUInt32LE(4) === (host.architecture === "x64" ? 0x01000007 : 0x0100000c);
  if (bytes.toString("ascii", 0, 2) !== "MZ") return false;
  const pe = bytes.readUInt32LE(0x3c);
  return pe + 6 <= bytes.length && bytes.toString("ascii", pe, pe + 4) === "PE\0\0" && bytes.readUInt16LE(pe + 4) === (host.architecture === "x64" ? 0x8664 : 0xaa64);
}

export function inspect(file, revision = sourceRevision()) {
  const bytes = readFileSync(file);
  const entries = tarEntries(bytes);
  const actual = JSON.parse(entries.get("package.json")?.toString("utf8") ?? "null");
  const host = platforms.find((item) => nativeName(item) === actual?.name);
  ensure(host || actual?.name === source.name, "Unknown React Forge package");
  const expected = manifest(host, revision);
  ensure(JSON.stringify(actual) === JSON.stringify(expected), "Package manifest mismatch");
  ensure(path.basename(file) === tarballName(actual.name, source.version), "Unexpected tarball name");
  const names = expectedFiles(host);
  ensure(JSON.stringify([...entries.keys()].sort()) === JSON.stringify(names), "Package file inventory mismatch");
  ensure(entries.get("LICENSE").equals(license), "Package license mismatch");
  ensure(entries.get("NOTICE").equals(notice), "Package notice mismatch");
  ensure(entries.get("README.md").equals(readFileSync(path.join(packageRoot, "README.md"))), "Package README mismatch");
  if (host) ensure(binaryMatches(host, entries.get("react-forge.node")), "Native binary target mismatch");
  else {
    ensure(entries.get("bin/react-forge.mjs").equals(readFileSync(path.join(packageRoot, "bin/react-forge.mjs"))), "CLI mismatch");
    for (const name of names.filter((item) => item.startsWith("dist/"))) ensure(entries.get(name).equals(readFileSync(path.join(packageRoot, name))), `Compiled file mismatch: ${name}`);
  }
  return { name: actual.name, version: actual.version, revision, filename: path.basename(file), integrity: integrity(bytes) };
}

export function pack(host, output, revision = sourceRevision()) {
  if (host) ensure(host.platform === process.platform && host.architecture === process.arch, "Native package must be packed on its host");
  const directory = path.resolve(output, host?.id ?? "main");
  rmSync(directory, { recursive: true, force: true });
  mkdirSync(directory, { recursive: true });
  copyFileSync(path.join(root, "LICENSE"), path.join(directory, "LICENSE"));
  copyFileSync(path.join(root, "NOTICE"), path.join(directory, "NOTICE"));
  copyFileSync(path.join(packageRoot, "README.md"), path.join(directory, "README.md"));
  if (host) {
    const binary = path.join(packageRoot, "dist", `react-forge.${host.id}.node`);
    ensure(binaryMatches(host, readFileSync(binary)), "Native build does not match host");
    copyFileSync(binary, path.join(directory, "react-forge.node"));
  } else {
    mkdirSync(path.join(directory, "dist"));
    for (const name of expectedFiles(null).filter((item) => item.startsWith("dist/"))) copyFileSync(path.join(packageRoot, name), path.join(directory, name));
    mkdirSync(path.join(directory, "bin"));
    copyFileSync(path.join(packageRoot, "bin/react-forge.mjs"), path.join(directory, "bin/react-forge.mjs"));
    chmodSync(path.join(directory, "bin/react-forge.mjs"), 0o755);
  }
  writeFileSync(path.join(directory, "package.json"), JSON.stringify(manifest(host, revision), null, 2) + "\n");
  const destination = path.resolve(output, "tarballs");
  mkdirSync(destination, { recursive: true });
  const [packed] = JSON.parse(npm(["pack", "--json", "--ignore-scripts", "--pack-destination", destination], { cwd: directory }));
  const file = path.join(destination, packed.filename);
  ensure(packed.filename === tarballName(manifest(host, revision).name, source.version) && packed.integrity === integrity(readFileSync(file)), "npm pack integrity mismatch");
  return inspect(file, revision);
}

export function verifySet(output, revision = sourceRevision()) {
  const directory = path.resolve(output, "tarballs");
  const names = packageNames().map((name) => tarballName(name, source.version));
  ensure(JSON.stringify(readdirSync(directory).sort()) === JSON.stringify([...names].sort()), "Expected exactly seven npm tarballs");
  return names.map((name) => inspect(path.join(directory, name), revision));
}

export function main() {
  const { positionals, values } = parseArgs({ allowPositionals: true, options: { output: { type: "string", default: path.join(packageRoot, "dist/release") } } });
  ensure(positionals.length === 1, "Expected native, main, or verify");
  const [action] = positionals;
  if (action === "native") {
    const host = platforms.find((item) => item.platform === process.platform && item.architecture === process.arch);
    ensure(host, "Unsupported package host");
    console.log(JSON.stringify({ event: "react_forge_pack", ...pack(host, values.output) }));
  } else if (action === "main") console.log(JSON.stringify({ event: "react_forge_pack", ...pack(null, values.output) }));
  else if (action === "verify") console.log(JSON.stringify({ event: "react_forge_verify", packages: verifySet(values.output) }));
  else throw new Error("Unknown package action");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
