#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";

const appRoot = new URL("..", import.meta.url).pathname;
const distRoot = join(appRoot, "dist");

function build() {
  const result = spawnSync("pnpm", ["run", "build:frontend"], {
    cwd: appRoot,
    encoding: "utf8",
    shell: process.platform === "win32",
  });
  if (result.status !== 0) {
    process.stderr.write(result.stdout ?? "");
    process.stderr.write(result.stderr ?? "");
    process.exit(result.status ?? 1);
  }
}

function filesUnder(directory) {
  return readdirSync(directory, { withFileTypes: true })
    .flatMap((entry) => {
      const path = join(directory, entry.name);
      return entry.isDirectory() ? filesUnder(path) : [path];
    })
    .toSorted();
}

function snapshot() {
  return filesUnder(distRoot).map((path) => {
    const contents = readFileSync(path);
    const name = relative(distRoot, path).replaceAll("\\", "/");
    const text = contents.toString("utf8");
    const executableRemoteLoad =
      /<(?:script|link|img|iframe)[^>]+(?:src|href)=["']https?:\/\//iu.test(text) ||
      /\b(?:fetch|import)\s*\(\s*["']https?:\/\//u.test(text);
    if (executableRemoteLoad) {
      throw new Error(`remote URL found in deterministic frontend output: ${name}`);
    }
    return {
      name,
      mode: statSync(path).mode & 0o777,
      sha256: createHash("sha256").update(contents).digest("hex"),
    };
  });
}

build();
const first = snapshot();
build();
const second = snapshot();

if (JSON.stringify(first) !== JSON.stringify(second)) {
  console.error("devhud: frontend output changed between consecutive clean builds");
  console.error(JSON.stringify({ first, second }, null, 2));
  process.exit(1);
}

if (!first.some(({ name }) => name === "index.html")) {
  console.error("devhud: deterministic frontend output is missing index.html");
  process.exit(1);
}

console.log(`devhud: deterministic frontend verified (${first.length} files)`);
