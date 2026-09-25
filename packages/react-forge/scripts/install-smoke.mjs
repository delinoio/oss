import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { parseArgs } from "node:util";
import { fileURLToPath, pathToFileURL } from "node:url";
import { nativeName, packageRoot, platforms, sourceRevision, inspect, tarballName } from "./package.mjs";

const localFormats = [["presentation", "pptx"], ["document", "docx"], ["workbook", "xlsx"], ["pdf", "pdf"], ["sprite", "sprite"]];

export function main() {
  const { values } = parseArgs({ options: { output: { type: "string", default: path.join(packageRoot, "dist/release") } } });
  const host = platforms.find((item) => item.platform === process.platform && item.architecture === process.arch);
  assert.ok(host, "Unsupported smoke-test host");
  const version = JSON.parse(readFileSync(path.join(packageRoot, "package.json"), "utf8")).version;
  const tarballs = path.resolve(values.output, "tarballs");
  const main = path.join(tarballs, tarballName("@delino/react-forge", version));
  const native = path.join(tarballs, tarballName(nativeName(host), version));
  inspect(main, sourceRevision());
  inspect(native, sourceRevision());
  const directory = mkdtempSync(path.join(tmpdir(), "react-forge-public-smoke-"));
  try {
    writeFileSync(path.join(directory, "package.json"), JSON.stringify({ private: true, type: "module", dependencies: {
      "@delino/react-forge": `file:${main}`, [nativeName(host)]: `file:${native}`,
    } }));
    // Both tested tarballs are explicit dependencies. Other host packages are
    // optional; retain optional dependencies because tsx needs esbuild's host binary.
    const windows = process.platform === "win32";
    const command = windows ? process.execPath : "npm";
    const prefix = windows ? [path.join(path.dirname(process.execPath), "node_modules/npm/bin/npm-cli.js")] : [];
    execFileSync(command, [...prefix, "install", "--ignore-scripts", "--no-audit", "--no-fund"], { cwd: directory, stdio: "pipe" });
    const installed = path.join(directory, "node_modules/@delino/react-forge");
    assert.equal(JSON.parse(readFileSync(path.join(installed, "package.json"), "utf8")).private, undefined);
    const mcpModule = pathToFileURL(path.join(installed, "dist/mcp/server.js")).href;
    const imported = `import { Format } from "@delino/react-forge";
      import { Page } from "@delino/react-forge/figma";
      const { serve } = await import(${JSON.stringify(mcpModule)});
      if (Format.Figma !== "figma" || typeof Page !== "function" || typeof serve !== "function") throw new Error("Figma or MCP export unavailable");`;
    execFileSync(process.execPath, ["--input-type=module", "--eval", imported], { cwd: directory, encoding: "utf8" });
    cpSync(path.join(packageRoot, "examples"), path.join(directory, "tasks"), { recursive: true });
    for (const [task, format] of localFormats) {
      const output = path.join(directory, `report.${format === "sprite" ? "sprite.zip" : format}`);
      const stdout = execFileSync(process.execPath, [path.join(installed, "bin/react-forge.mjs"), "run", path.join(directory, "tasks", `${task}.tsx`), "--output", output, "--json"], { cwd: directory, encoding: "utf8" });
      assert.equal(JSON.parse(stdout).format, format);
      const bytes = readFileSync(output);
      assert.equal(bytes.subarray(0, format === "pdf" ? 5 : 2).toString(), format === "pdf" ? "%PDF-" : "PK");
    }
    assert.equal(execFileSync(process.execPath, [path.join(installed, "bin/react-forge.mjs"), "--version"], { cwd: directory, encoding: "utf8" }).trim(), version);
    const nativeModule = pathToFileURL(path.join(installed, "dist/native.js")).href;
    const unsupported = `Object.defineProperty(process, "platform", { value: "freebsd" });
      const { processDocument } = await import(${JSON.stringify(nativeModule)});
      try { await processDocument("pptx", "generate", {}, Buffer.alloc(0), new Map(), "unused", 0, new AbortController().signal); }
      catch (error) { console.log(JSON.stringify(error)); }`;
    assert.equal(JSON.parse(execFileSync(process.execPath, ["--input-type=module", "--eval", unsupported], { cwd: directory, encoding: "utf8" })).code, "unsupported_package");
    const nativeManifestPath = path.join(directory, "node_modules", "@delino", `react-forge-${host.id}`, "package.json");
    const nativeManifest = readFileSync(nativeManifestPath, "utf8");
    writeFileSync(nativeManifestPath, JSON.stringify({ ...JSON.parse(nativeManifest), version: "999.0.0" }));
    let mismatch;
    try {
      execFileSync(process.execPath, [path.join(installed, "bin/react-forge.mjs"), "run", path.join(directory, "tasks/presentation.tsx"), "--output", path.join(directory, "mismatch.pptx"), "--json"], { cwd: directory, encoding: "utf8" });
    } catch (error) { mismatch = error; }
    assert.ok(mismatch, "Mismatched native package must fail");
    assert.equal(JSON.parse(mismatch.stdout).error.code, "io");
    writeFileSync(nativeManifestPath, nativeManifest);
    rmSync(path.join(directory, "node_modules", "@delino", `react-forge-${host.id}`), { recursive: true, force: true });
    let missing;
    try {
      execFileSync(process.execPath, [path.join(installed, "bin/react-forge.mjs"), "run", path.join(directory, "tasks/presentation.tsx"), "--output", path.join(directory, "missing.pptx"), "--json"], { cwd: directory, encoding: "utf8" });
    } catch (error) { missing = error; }
    assert.ok(missing, "Missing native package must fail");
    assert.equal(JSON.parse(missing.stdout).error.code, "io");
    console.log(JSON.stringify({ event: "react_forge_install_smoke", host: host.id, version, formats: localFormats.length }));
  } finally { rmSync(directory, { recursive: true, force: true }); }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
