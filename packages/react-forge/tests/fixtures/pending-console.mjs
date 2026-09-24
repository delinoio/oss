// Runs only inside the Rust integration's isolated Windows console.
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import { main } from "../../dist/cli.js";
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
  export default async function task() { const s = createSession(Format.Pdf); await s.render(React.createElement(App)); return s; }
`);
process.exitCode = await main(["run", entry, "--output", join(directory, "result.pdf"), "--json"]);
