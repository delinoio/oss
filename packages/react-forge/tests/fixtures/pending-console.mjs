// Runs only inside the Rust integration's isolated Windows console.
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
const directory = process.argv[2];
const entry = join(directory, "entry.tsx");
const quote = JSON.stringify;
await writeFile(entry, `
  import React, { Suspense, use, useEffect } from ${quote(pathToFileURL(createRequire(import.meta.url).resolve("react")).href)};
  import { writeFileSync } from "node:fs";
  import { createSession, Format } from ${quote(new URL("../../dist/index.js", import.meta.url).href)};
  import { Document, Page, Paragraph } from ${quote(new URL("../../dist/pdf.js", import.meta.url).href)};
  const pending = new Promise(() => {});
  function Pending() { use(pending); return null; }
  function App() {
    useEffect(() => { writeFileSync(${quote(join(directory, "ready"))}, "ready"); return () => writeFileSync(${quote(join(directory, "cleaned"))}, "cleaned"); }, []);
    return React.createElement(Document, null, React.createElement(Page, null, React.createElement(Suspense, { fallback: React.createElement(Paragraph, null, "Pending") }, React.createElement(Pending))));
  }
  // Deliberately retain task-owned work: the real CLI must exit after cleanup.
  export default async function task() { setInterval(() => {}, 1000); const s = createSession(Format.Pdf); await s.render(React.createElement(App)); return s; }
`);
const cli = new URL("../../bin/react-forge.mjs", import.meta.url);
process.argv = [process.execPath, fileURLToPath(cli), "run", entry, "--output", join(directory, "result.pdf"), "--json"];
await import(cli.href);
