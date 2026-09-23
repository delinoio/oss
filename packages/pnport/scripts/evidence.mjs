import { createHash } from "node:crypto";
import { copyFileSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { parseArgs } from "node:util";
import { ensure, event, isMain, metadata, packageRoot } from "./common.mjs";
import { archiveName, buildPackage, inspectArchive, inspectTarball, tarballName, verifySet } from "./package.mjs";

const require = createRequire(import.meta.url);
const { targets } = require("../src/platforms.cjs");
const sha256 = (file) => createHash("sha256").update(readFileSync(file)).digest("hex");
const hash = /^[a-f0-9]{40}$/u;

export function record(target, output, { base, tree, sourceRevision }) {
  ensure(targets.includes(target) && hash.test(base) && hash.test(tree) && hash.test(sourceRevision), "Invalid native evidence identity");
  const { version } = metadata();
  const npmName = tarballName(target.name, version);
  const archive = archiveName(target);
  inspectTarball(path.join(output, "tarballs", npmName), version, sourceRevision);
  inspectArchive(path.join(output, "archives", archive), target, path.join(output, "tarballs", npmName));
  const evidence = { schemaVersion: 1, target: target.suffix, version, base, tree, sourceRevision, npm: { name: npmName, sha256: sha256(path.join(output, "tarballs", npmName)) }, archive: { name: archive, sha256: sha256(path.join(output, "archives", archive)) }, gates: ["cargo-test", "native-typescript", "npm-consumer", "yarn-pnp-consumer", "direct-install"] };
  writeFileSync(path.join(output, "evidence.json"), JSON.stringify(evidence, null, 2) + "\n");
  return evidence;
}

export function assemble(input, output, { base, tree, sourceRevision }) {
  ensure(hash.test(base) && hash.test(tree) && hash.test(sourceRevision), "Invalid assembly source identity");
  const { version } = metadata();
  for (const directory of ["tarballs", "archives"]) mkdirSync(path.join(output, directory), { recursive: true });
  ensure(JSON.stringify(readdirSync(input).sort()) === JSON.stringify(targets.map(({ suffix }) => `pnport-native-${suffix}`).sort()), "Six native evidence sets are required");
  for (const target of targets) {
    const folder = path.join(input, `pnport-native-${target.suffix}`);
    const evidence = JSON.parse(readFileSync(path.join(folder, "evidence.json"), "utf8"));
    const npmName = tarballName(target.name, version);
    const archive = archiveName(target);
    ensure(JSON.stringify(evidence) === JSON.stringify({ schemaVersion: 1, target: target.suffix, version, base, tree, sourceRevision, npm: { name: npmName, sha256: sha256(path.join(folder, "tarballs", npmName)) }, archive: { name: archive, sha256: sha256(path.join(folder, "archives", archive)) }, gates: ["cargo-test", "native-typescript", "npm-consumer", "yarn-pnp-consumer", "direct-install"] }), "Native evidence mismatch");
    ensure(JSON.stringify(readdirSync(path.join(folder, "tarballs"))) === JSON.stringify([npmName]) && JSON.stringify(readdirSync(path.join(folder, "archives"))) === JSON.stringify([archive]), "Unexpected native artifact inventory");
    copyFileSync(path.join(folder, "tarballs", npmName), path.join(output, "tarballs", npmName));
    copyFileSync(path.join(folder, "archives", archive), path.join(output, "archives", archive));
  }
  buildPackage({ output, sourceRevision });
  const set = verifySet(output, sourceRevision);
  event("assemble", { version, tree, sourceRevision, targets: set.native.length });
  return set;
}

export function main() {
  const { values, positionals } = parseArgs({ allowPositionals: true, options: { target: { type: "string" }, input: { type: "string" }, output: { type: "string", default: path.join(packageRoot, "dist") }, base: { type: "string" }, tree: { type: "string" }, revision: { type: "string" } } });
  ensure(positionals.length === 1, "Expected record or assemble");
  if (positionals[0] === "record") {
    const target = targets.find(({ rust }) => rust === values.target);
    ensure(target, "Invalid native evidence target");
    event("evidence", record(target, values.output, { base: values.base, tree: values.tree, sourceRevision: values.revision }));
  } else if (positionals[0] === "assemble") {
    ensure(values.input, "Native evidence input required");
    assemble(values.input, values.output, { base: values.base, tree: values.tree, sourceRevision: values.revision });
  } else throw new Error("Unknown pnport evidence command");
}

if (isMain(import.meta.url)) main();
