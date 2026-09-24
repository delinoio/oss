import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { cp, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import test from "node:test";
const exec = promisify(execFile);
const cases = [["presentation", "pptx"], ["document", "docx"], ["workbook", "xlsx"], ["pdf", "pdf"]] as const;

test("scoped workspace archive installs and its CLI generates all four formats", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-installed-"));
  const packageRoot = new URL("../", import.meta.url).pathname;
  const env = { ...process.env, PATH: `${dirname(process.execPath)}${delimiter}${process.env.PATH ?? ""}` };
  try {
    await exec("pnpm", ["pack", "--pack-destination", directory], { cwd: packageRoot, env });
    const tarball = join(directory, "delino-react-forge-0.0.0.tgz");
    await writeFile(join(directory, "package.json"), JSON.stringify({ private: true, type: "module", packageManager: "pnpm@10.26.2", dependencies: { "@delino/react-forge": `file:${tarball}`, react: "19.2.8" } }));
    // Test the distributable layout without requiring lifecycle scripts,
    // workspace resolution or source files from this checkout.
    await exec("pnpm", ["install", "--ignore-scripts"], { cwd: directory, env });
    await cp(join(packageRoot, "examples"), join(directory, "tasks"), { recursive: true });
    const installedRoot = join(directory, "node_modules", "@delino", "react-forge");
    const manifest = JSON.parse(await readFile(join(installedRoot, "package.json"), "utf8"));
    assert.equal(manifest.name, "@delino/react-forge");
    const cli = join(installedRoot, "bin", "react-forge.mjs");
    for (const [task, format] of cases) {
      const output = join(directory, `report.${format}`);
      const { stdout } = await exec(process.execPath, [cli, "run", join(directory, "tasks", `${task}.tsx`), "--output", output, "--json"], { cwd: directory, env });
      const result = JSON.parse(stdout); assert.equal(result.ok, true); assert.equal(result.format, format);
      const bytes = await readFile(output); assert.equal(bytes.subarray(0, format === "pdf" ? 5 : 2).toString(), format === "pdf" ? "%PDF-" : "PK");
    }
    const version = await exec(join(directory, "node_modules", ".bin", "react-forge"), ["--version"], { cwd: directory, env });
    assert.equal(version.stdout.trim(), "0.0.0");
  } finally { await rm(directory, { recursive: true, force: true }); }
});
