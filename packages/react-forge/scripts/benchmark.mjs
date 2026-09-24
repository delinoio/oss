// Test-only benchmark. Each case runs in a fresh process to isolate peak RSS.
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { cpus, release } from "node:os";
import { monitorEventLoopDelay, performance } from "node:perf_hooks";

const exec = promisify(execFile);
const file = fileURLToPath(import.meta.url);
const formats = ["pptx", "docx", "xlsx", "pdf"];
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const round = n => Number(n.toFixed(3));
if (process.argv[2] === "--case") {
  const [format, workload] = process.argv.slice(3);
  if (!formats.includes(format) || !["representative", "near_limit", "preserved_edit"].includes(workload)) throw new Error("Invalid benchmark case.");
  const React = await import("react");
  const { createSession, importOffice, Format } = await import("../dist/index.js");
  const e = React.createElement;
  let session;
  let elements;
  const eventLoop = monitorEventLoopDelay({ resolution: 5 });
  eventLoop.enable();
  await delay(10);
  const usage = performance.eventLoopUtilization();
  const start = performance.now();
  try {
    if (workload === "representative") {
      const { tsImport } = await import("tsx/esm/api");
      const name = { pptx: "presentation", docx: "document", xlsx: "workbook", pdf: "pdf" }[format];
      const task = await tsImport(new URL(`../examples/${name}.tsx`, import.meta.url).href, import.meta.url);
      session = await task.default({ signal: new AbortController().signal });
    } else if (workload === "preserved_edit") {
      const folder = { pptx: "forge-pptx", docx: "forge-docx", xlsx: "forge-xlsx" }[format];
      const path = fileURLToPath(new URL(`../../../crates/${folder}/tests/fixtures/external.${format}`, import.meta.url));
      session = await importOffice(format, { path });
      const components = await import(`../dist/${format}.js`);
      const kind = { pptx: "text", docx: "paragraph", xlsx: "cell" }[format];
      const target = session.inspect().targets.find(t => t.kind === kind && t.editable);
      if (!target) throw new Error("Benchmark fixture target is missing.");
      const content = format === "xlsx" ? e(components.Cell, { value: "Benchmark edit" })
        : e(components[format === "pptx" ? "Text" : "Paragraph"], null, "Benchmark edit");
      await session.mount(target, content);
    } else {
      session = createSession(format);
      const c = await import(`../dist/${format}.js`);
      let view;
      if (format === Format.Pptx) {
        elements = 950; // 95% of the stricter presentation slide ceiling.
        view = e(c.Presentation, null, ...Array.from({ length: elements }, (_, key) => e(c.Slide, { key }, e(c.Column, { padding: 24 }, e(c.Text, null, `Slide ${key}`)))));
      } else if (format === Format.Xlsx) {
        elements = 19_000; // Cell hosts + two containers stay below 20,000 nodes.
        view = e(c.Workbook, null, e(c.Worksheet, { name: "Near limit" }, ...Array.from({ length: elements }, (_, key) => e(c.Cell, { key, address: { row: key, column: 0 }, value: key }))));
      } else {
        elements = 9_500; // Paragraph and text hosts: 19,003 rendered nodes.
        view = e(c.Document, { language: "en" }, e(c[format === Format.Docx ? "Section" : "Page"], null,
          ...Array.from({ length: elements }, (_, key) => e(c.Paragraph, { key }, `Paragraph ${key}: native document flow.`))));
      }
      await session.render(view);
    }
    const prepared = performance.now();
    const bytes = await session.exportBuffer();
    const exported = performance.now();
    await session.dispose();
    await delay(10);
    eventLoop.disable();
    process.stdout.write(`${JSON.stringify({ format, workload, elements, prepareMs: round(prepared - start), exportMs: round(exported - prepared), totalMs: round(exported - start), outputBytes: bytes.length,
      peakRssBytes: process.resourceUsage().maxRSS * 1024, eventLoopP99Ms: round(eventLoop.percentile(99) / 1e6), eventLoopMaxMs: round(eventLoop.max / 1e6), eventLoopUtilization: round(performance.eventLoopUtilization(usage).utilization) })}\n`);
  } finally { eventLoop.disable(); await session?.dispose(); }
} else {
  const output = process.argv[2] === "--output" && process.argv[3] ? resolve(process.argv[3]) : undefined;
  if (!output || process.argv.length !== 4) throw new Error("Usage: benchmark.mjs --output <report.json>");
  const samples = [];
  for (const workload of ["representative", "preserved_edit", "near_limit"]) {
    for (const format of formats) {
      if (workload === "preserved_edit" && format === "pdf") continue;
      const { stdout } = await exec(process.execPath, [file, "--case", format, workload], { maxBuffer: 1024 * 1024 });
      const sample = JSON.parse(stdout);
      samples.push(sample);
      process.stdout.write(`${format} ${workload}: ${sample.totalMs} ms, ${Math.round(sample.peakRssBytes / 1048576)} MiB peak RSS\n`);
    }
  }
  const report = { schemaVersion: 1, measuredAt: new Date().toISOString(), platform: process.platform, architecture: process.arch, osRelease: release(), cpu: cpus()[0]?.model,
    node: process.version, react: "19.2.8", reconciler: "0.33.0", nativeProfile: "dev", warmup: "Fresh Node process per case; shared OS caches may be warm", samples, guarantee: "Observations only; no numerical SLO" };
  await mkdir(dirname(output), { recursive: true });
  await writeFile(output, `${JSON.stringify(report, null, 2)}\n`);
}
