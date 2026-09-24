// Test-only Office/Poppler evidence. Production authoring never calls converters.
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { access, mkdir, readdir, rm, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
const exec = promisify(execFile);
const output = process.argv[2] === "--output" && process.argv[3] ? resolve(process.argv[3]) : undefined;
if (!output || process.argv.length !== 4) throw new Error("Usage: render-check.mjs --output <evidence-directory>");
const cli = fileURLToPath(new URL("../bin/react-forge.mjs", import.meta.url));
const python = process.env.REACT_FORGE_PYTHON ?? "python3";
const poppler = process.env.REACT_FORGE_PDFTOPPM ?? "pdftoppm";
const officeDefault = "/Applications/LibreOffice.app/Contents/MacOS/soffice";
const office = process.env.REACT_FORGE_SOFFICE ?? (await access(officeDefault).then(() => officeDefault, () => "soffice"));
await mkdir(output, { recursive: true });
const profile = join(output, "office-profile");
const cache = join(output, "font-cache");
const fontconfig = join(output, "fonts.conf");
const escape = value => value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll('"', "&quot;");
// Headless LibreOffice's fontconfig backend does not necessarily discover
// macOS system fonts. Declare them explicitly only for this test-owned profile.
await writeFile(fontconfig, `<?xml version="1.0"?><!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd"><fontconfig><dir>/System/Library/Fonts</dir><dir>/Library/Fonts</dir><cachedir>${escape(cache)}</cachedir></fontconfig>`);
const env = { ...process.env, FONTCONFIG_FILE: fontconfig };
const version = async (command, args) => { const result = await exec(command, args, { env }); return (result.stdout + result.stderr).trim().split("\n")[0]; };
const versions = { node: process.version, libreOffice: await version(office, ["--headless", "--version"]), poppler: await version(poppler, ["-v"]), python: await version(python, ["--version"]) };
const prepareDirectory = async (directory, pdf) => {
  await mkdir(directory, { recursive: true });
  // Repeated runs must not accept stale converter output or leftover pages
  // after pagination shrinks. These exact artifact names belong to this harness.
  for (const name of await readdir(directory)) {
    if (name === pdf || /^page-\d+\.png$/u.test(name)) await rm(join(directory, name));
  }
};
const cases = [];
try {
  for (const [format, example] of [["pptx", "presentation"], ["docx", "document"], ["xlsx", "workbook"], ["pdf", "pdf"]]) {
    for (const edited of [false, true]) {
      if (format === "pdf" && edited) continue;
      const name = `${edited ? "edited" : "created"}-${format}`;
      const directory = join(output, name);
      await prepareDirectory(directory, "document.pdf");
      const source = join(directory, `document.${format}`);
      const task = fileURLToPath(new URL(`../examples/${edited ? "edit-office" : example}.tsx`, import.meta.url));
      const args = [cli, "run", task, "--output", source, "--overwrite", "--json"];
      if (edited) args.push("--data", JSON.stringify({ format, source: fileURLToPath(new URL(`../../../crates/forge-${format}/tests/fixtures/external.${format}`, import.meta.url)), text: "Edited by React Forge" }));
      const result = JSON.parse((await exec(process.execPath, args, { maxBuffer: 1024 * 1024 })).stdout);
      if (!result.ok || !result.published) throw new Error(`CLI evidence failed: ${name}`);
      if (edited) {
        const baseline = join(directory, "baseline");
        await prepareDirectory(baseline, "external.pdf");
        const original = fileURLToPath(new URL(`../../../crates/forge-${format}/tests/fixtures/external.${format}`, import.meta.url));
        await exec(office, ["--headless", `-env:UserInstallation=${pathToFileURL(profile).href}`, "--convert-to", "pdf", "--outdir", baseline, original], { env, maxBuffer: 1024 * 1024 });
        await exec(poppler, ["-scale-to", "1050", "-png", join(baseline, "external.pdf"), join(baseline, "page")], { maxBuffer: 1024 * 1024 });
      }
      if (format !== "pdf") await exec(office, ["--headless", `-env:UserInstallation=${pathToFileURL(profile).href}`, "--convert-to", "pdf", "--outdir", directory, source], { env, maxBuffer: 1024 * 1024 });
      await exec(poppler, ["-scale-to", "1050", "-png", join(directory, "document.pdf"), join(directory, "page")], { maxBuffer: 1024 * 1024 });
      cases.push({ name, format, edited });
      process.stdout.write(`Rendered ${name}.\n`);
    }
  }
  await writeFile(join(output, "manifest.json"), `${JSON.stringify({ schemaVersion: 1, versions, cases, claim: "Test-only LibreOffice and Poppler evidence; no Microsoft Office or PDF/UA claim" }, null, 2)}\n`);
  const verification = await exec(python, [fileURLToPath(new URL("verify-render.py", import.meta.url)), output], { maxBuffer: 1024 * 1024 });
  process.stdout.write(verification.stdout);
} finally {
  await Promise.all([rm(profile, { recursive: true, force: true }), rm(cache, { recursive: true, force: true })]);
}
