#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { chmodSync, cpSync, lstatSync, mkdirSync, mkdtempSync, readdirSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const epoch = new Date(0);
const maxArchiveBytes = 64 * 1024 * 1024;

function normalizeTree(path) {
  const metadata = lstatSync(path);
  if (metadata.isDirectory()) {
    chmodSync(path, 0o755);
    for (const name of readdirSync(path).sort()) normalizeTree(join(path, name));
    utimesSync(path, epoch, epoch);
    return;
  }
  if (!metadata.isFile()) throw new Error(`updater input contains an unsupported entry type: ${path}`);
  chmodSync(path, 0o644);
  utimesSync(path, epoch, epoch);
}

function checkedSpawn(command, arguments_, options = {}) {
  const result = spawnSync(command, arguments_, { encoding: null, maxBuffer: maxArchiveBytes, ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed while packaging updater input: ${result.stderr.toString("utf8").trim()}`);
  return result.stdout;
}

export function packageUpdaterInput({ artifactsDirectory, output }) {
  const artifacts = resolve(artifactsDirectory);
  const destination = resolve(output);
  const staging = mkdtempSync(join(tmpdir(), "devhud-updater-input-"));
  try {
    for (const name of ["manifests", "signatures"]) {
      const source = join(artifacts, "updater", name);
      const target = join(staging, "updater", name);
      mkdirSync(dirname(target), { recursive: true });
      cpSync(source, target, { recursive: true, errorOnExist: true, force: false, preserveTimestamps: false });
    }
    normalizeTree(staging);
    const archive = checkedSpawn("tar", [
      "--sort=name", "--format=gnu", "--mtime=@0", "--owner=0", "--group=0", "--numeric-owner",
      "-C", staging, "-cf", "-", "updater/manifests", "updater/signatures",
    ]);
    const compressed = checkedSpawn("gzip", ["-n", "-9"], { input: archive });
    writeFileSync(destination, compressed, { mode: 0o600 });
    chmodSync(destination, 0o600);
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}

function parse(arguments_) {
  const options = {};
  for (let index = 0; index < arguments_.length; index += 2) {
    const name = arguments_[index];
    const value = arguments_[index + 1];
    if (!name?.startsWith("--") || value === undefined) throw new Error(`invalid argument: ${name ?? "missing"}`);
    options[name.slice(2)] = value;
  }
  for (const name of ["artifacts-dir", "output"]) if (!options[name]) throw new Error(`--${name} is required`);
  return options;
}

export function main(arguments_ = process.argv.slice(2)) {
  const options = parse(arguments_);
  packageUpdaterInput({ artifactsDirectory: options["artifacts-dir"], output: options.output });
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { main(); } catch (error) {
    process.stderr.write(`[devhud.updater-input] ${error.message}\n`);
    process.exit(1);
  }
}
