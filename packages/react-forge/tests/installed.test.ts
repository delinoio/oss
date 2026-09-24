import { fileURLToPath } from "node:url";
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
  const directory = await mkdtemp(join(tmpdir(), "react-forge installed-"));
  const packageRoot = fileURLToPath(new URL("../", import.meta.url));
  const env = { ...process.env, PATH: `${dirname(process.execPath)}${delimiter}${process.env.PATH ?? ""}` };
  const pnpm = process.env.npm_execpath;
  assert.ok(pnpm, "Run installed consumer coverage through pnpm test");
  const pm = (args: string[], cwd: string) => exec(process.execPath, [pnpm, ...args], { cwd, env });
  try {
    await pm(["pack", "--pack-destination", directory], packageRoot);
    const tarball = join(directory, "delino-react-forge-0.0.0.tgz");
    await writeFile(join(directory, "package.json"), JSON.stringify({ private: true, type: "module", packageManager: "pnpm@10.26.2", dependencies: { "@delino/react-forge": `file:${tarball}`, react: "19.2.8" } }));
    // Test the distributable layout without requiring lifecycle scripts,
    // workspace resolution or source files from this checkout.
    await pm(["install", "--ignore-scripts"], directory);
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
    const shim = join(directory, "node_modules", ".bin", process.platform === "win32" ? "react-forge.cmd" : "react-forge");
    // cmd files require the Windows command processor. Only the fixture-owned
    // shim and a literal flag enter this command; document input never does.
    const version = process.platform === "win32"
      ? await exec(process.env.ComSpec ?? "cmd.exe", ["/d", "/s", "/c", `""${shim}" --version"`], { cwd: directory, env, windowsVerbatimArguments: true })
      : await exec(shim, ["--version"], { cwd: directory, env });
    assert.equal(version.stdout.trim(), "0.0.0");
  } finally { await rm(directory, { recursive: true, force: true }); }
});
