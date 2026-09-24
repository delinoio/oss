import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { promisify } from "node:util";
import { mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createRequire } from "node:module";
const exec = promisify(execFile);

test("workspace CLI runs TSX tasks and reports JSON, help, version and safe errors", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-cli-"));
  const cli = new URL("../bin/react-forge.mjs", import.meta.url).pathname;
  const task = new URL("../examples/presentation.tsx", import.meta.url).pathname;
  try {
    const { stdout } = await exec(process.execPath, [cli, "run", task, "--output", join(directory, "result.pptx"), "--data", '{"title":"CLI test"}', "--json"]);
    assert.equal(JSON.parse(stdout).ok, true);
    assert.equal((await readFile(join(directory, "result.pptx"))).subarray(0, 2).toString(), "PK");
    for (const [name, format] of [["document", "docx"], ["workbook", "xlsx"], ["pdf", "pdf"]]) {
      const task = new URL(`../examples/${name}.tsx`, import.meta.url).pathname;
      const { stdout } = await exec(process.execPath, [cli, "run", task, "--output", join(directory, `result.${format}`), "--json"]);
      assert.equal(JSON.parse(stdout).format, format);
      assert.equal(JSON.parse(stdout).published, true);
    }
    // The returned session belongs to tsx's isolated module namespace. Its
    // typed error must survive that boundary instead of becoming a task error.
    await assert.rejects(exec(process.execPath, [cli, "run", task, "--output", join(directory, "result.pptx"), "--json"]), (error: unknown) => {
      const result = JSON.parse((error as { stdout: string }).stdout);
      assert.equal(result.error.code, "conflict");
      return true;
    });
    assert.match((await exec(process.execPath, [cli, "--help"])).stdout, /default task/);
    assert.equal((await exec(process.execPath, [cli, "--version"])).stdout.trim(), "0.0.0");
    await assert.rejects(exec(process.execPath, [cli, "run", task, "--unknown", "--json"]), (error: unknown) => {
      const result = JSON.parse((error as { stdout: string }).stdout);
      assert.equal(result.ok, false);
      assert.equal(result.error.code, "malformed_input");
      return true;
    });
  } finally { await rm(directory, { recursive: true, force: true }); }
});


test("CLI task failures stay redacted and SIGINT/SIGTERM dispose pending sessions", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-signals-"));
  const cli = new URL("../bin/react-forge.mjs", import.meta.url).pathname;
  const entry = join(directory, "entry.tsx");
  const quote = JSON.stringify;
  try {
    await writeFile(entry, 'export default function task() { throw new Error("PRIVATE_TASK_CONTENT_AND_PATH"); }');
    await assert.rejects(exec(process.execPath, [cli, "run", entry, "--output", join(directory, "failure.pdf"), "--json"]), (error: unknown) => {
      const stdout = (error as { stdout: string }).stdout;
      assert.equal(JSON.parse(stdout).error.code, "render");
      assert.doesNotMatch(stdout, /PRIVATE_TASK/);
      return true;
    });
    for (const signal of ["SIGINT", "SIGTERM"] as const) {
      const ready = join(directory, `${signal}.ready`);
      const cleaned = join(directory, `${signal}.cleaned`);
      await writeFile(entry, `
        import React, { Suspense, use, useEffect } from ${quote(createRequire(import.meta.url).resolve("react"))};
        import { writeFileSync } from "node:fs";
        import { createSession, Format } from ${quote(new URL("../dist/index.js", import.meta.url).pathname)};
        import { Document, Page, Paragraph } from ${quote(new URL("../dist/pdf.js", import.meta.url).pathname)};
        const pending = new Promise(() => {});
        function Pending() { use(pending); return null; }
        function App() {
          useEffect(() => { writeFileSync(${quote(ready)}, "ready"); return () => writeFileSync(${quote(cleaned)}, "cleaned"); }, []);
          return React.createElement(Document, { language: "en" }, React.createElement(Page, null, React.createElement(Suspense, { fallback: React.createElement(Paragraph, null, "Pending") }, React.createElement(Pending))));
        }
        export default async function task() { const s = createSession(Format.Pdf); await s.render(React.createElement(App)); return s; }
      `);
      const child = spawn(process.execPath, [cli, "run", entry, "--output", join(directory, `${signal}.pdf`), "--json"], { stdio: ["ignore", "pipe", "pipe"] });
      let stdout = ""; let stderr = "";
      child.stdout.on("data", value => { stdout += value; });
      child.stderr.on("data", value => { stderr += value; });
      const exit = new Promise<number | null>((resolve, reject) => { child.once("error", reject); child.once("exit", resolve); });
      try {
        let started = false;
        for (let i = 0; i < 200; i++) {
          if (await readFile(ready).then(() => true, () => false)) { started = true; break; }
          if (child.exitCode !== null) break;
          await new Promise(resolve => setTimeout(resolve, 10));
        }
        assert.ok(started, stderr + stdout);
        child.kill(signal);
        assert.equal(await exit, signal === "SIGINT" ? 130 : 143);
        assert.equal(JSON.parse(stdout).error.code, "cancelled");
        assert.equal((await readFile(cleaned)).toString(), "cleaned");
        assert.ok(!(await readdir(directory)).some(name => name.endsWith(".pdf") || name.endsWith(".tmp")));
      } finally { if (child.exitCode === null) { child.kill("SIGKILL"); await exit; } }
    }
  } finally { await rm(directory, { recursive: true, force: true }); }
});
