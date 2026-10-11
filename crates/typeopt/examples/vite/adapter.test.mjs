import assert from "node:assert/strict";
import { runInNewContext } from "node:vm";
import { test } from "node:test";
import { mkdtemp, mkdir, writeFile, rm, realpath } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { createHash } from "node:crypto";
import { typeopt } from "./adapter.mjs";

const sha256 = (text) => createHash("sha256").update(text).digest("hex");

test("keeps original module identity, bypasses query/JS/asset requests and rejects stale output", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "typeopt-vite-"));
  try {
    const source = path.join(root, "workspace", "main.tsx");
    const original = "export const value = 1;";
    await mkdir(path.dirname(source));
    await writeFile(source, original);
    await writeFile(path.join(root, "preserved.ts"), "export {};");
    await writeFile(path.join(root, "unchanged.js"), "export {};");
    await writeFile(
      path.join(root, "modules.json"),
      JSON.stringify({
        schema_version: 0,
        modules: [
          {
            source,
            source_sha256: sha256(original),
            output: "0.js",
            source_map: "0.js.map",
            kind: "optimized",
          },
          {
            source: path.join(root, "preserved.ts"),
            source_sha256: sha256("export {};"),
            output: "1.js",
            kind: "preserved",
          },
          {
            source: path.join(root, "unchanged.js"),
            source_sha256: sha256("export {};"),
            output: "unchanged.js",
            kind: "passthrough",
          },
        ],
      }),
    );
    await writeFile(
      path.join(root, "0.js"),
      'import "./asset.css"; export const value = 1;',
    );
    await writeFile(
      path.join(root, "0.js.map"),
      JSON.stringify({
        version: 3,
        sources: [source],
        names: [],
        mappings: "AAAA",
      }),
    );
    const plugin = typeopt(root);
    await plugin.buildStart();
    assert.match((await plugin.load(source)).code, /\.\/asset.css/);
    assert.equal(await plugin.load(source + "?raw"), null);
    assert.equal(await plugin.load(path.join(root, "unchanged.js")), null);
    assert.equal(await plugin.load(path.join(root, "preserved.ts")), null);
    assert.equal(await plugin.load(path.join(root, "asset.css")), null);
    await writeFile(source, "export const value = 2;");
    await assert.rejects(plugin.load(source), /stale/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("Vite builds mapped TSX with original aliases, JS, CSS, JSON and source maps", async () => {
  const { build } = await import("vite");
  const root = await realpath(
    await mkdtemp(path.join(tmpdir(), "typeopt-vite-build-")),
  );
  try {
    const source = path.join(root, "main.tsx");
    const output = path.join(root, "optimized");
    await mkdir(output);
    const original = 'throw new Error("original TSX was not replaced");';
    await writeFile(source, original);
    await writeFile(path.join(root, "value.js"), "export default 7;");
    await writeFile(path.join(root, "style.css"), "body { color: red; }");
    await writeFile(path.join(root, "data.json"), '{"value":3}');
    await writeFile(
      path.join(root, "index.html"),
      '<script type="module" src="/main.tsx"></script>',
    );
    await writeFile(
      path.join(output, "0.js"),
      'import value from "@value"; import data from "./data.json"; import "./style.css"; console.log(value + data.value);',
    );
    await writeFile(
      path.join(output, "0.js.map"),
      JSON.stringify({
        version: 3,
        sources: [source],
        sourcesContent: ["console.log(<span/>);"],
        names: [],
        mappings: "AAAA",
      }),
    );
    await writeFile(
      path.join(output, "modules.json"),
      JSON.stringify({
        schema_version: 0,
        modules: [
          {
            source,
            source_sha256: sha256(original),
            output: "0.js",
            source_map: "0.js.map",
            kind: "optimized",
          },
        ],
      }),
    );
    const result = await build({
      root,
      configFile: false,
      plugins: [typeopt(output)],
      resolve: { alias: { "@value": path.join(root, "value.js") } },
      build: { write: false, sourcemap: true, modulePreload: false },
      logLevel: "silent",
    });
    const chunk = result.output.find((file) => file.type === "chunk");
    const values = [];
    runInNewContext(chunk.code, {
      console: { log: (value) => values.push(value) },
    });
    assert.deepEqual(values, [10]);
    assert.ok(chunk.map.sources.some((file) => file.endsWith("main.tsx")));
    assert.ok(result.output.some((file) => file.fileName.endsWith(".css")));
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("optimizer output composes through a real TSX workspace build", async () => {
  const { execFileSync } = await import("node:child_process");
  const { fileURLToPath } = await import("node:url");
  const { readFile } = await import("node:fs/promises");
  const { build } = await import("vite");
  const root = await realpath(
    await mkdtemp(path.join(tmpdir(), "typeopt-vite-pipeline-")),
  );
  try {
    const source = path.join(root, "main.tsx");
    const output = path.join(root, "optimized");
    await writeFile(
      path.join(root, "tsconfig.json"),
      JSON.stringify({
        compilerOptions: {
          module: "ESNext",
          target: "ES2022",
          moduleResolution: "bundler",
          allowJs: true,
          jsx: "react",
          jsxFactory: "h",
          resolveJsonModule: true,
          esModuleInterop: true,
          paths: { "@value": ["./value.js"] },
        },
      }),
    );
    await writeFile(
      path.join(root, "globals.d.ts"),
      "declare module '*.css'; declare namespace JSX { interface Element {} interface IntrinsicElements { span: any } }",
    );
    await writeFile(
      source,
      'import value from "@value"; import data from "./data.json"; import "./style.css"; function h(tag: string, props: unknown, child: number) { return child; } const obj = {longProperty: value}; console.log(<span>{obj.longProperty + data.value}</span>);',
    );
    await writeFile(path.join(root, "value.js"), "export default 7;");
    await writeFile(path.join(root, "style.css"), "body { color: red; }");
    await writeFile(path.join(root, "data.json"), '{"value":3}');
    await writeFile(
      path.join(root, "index.html"),
      '<script type="module" src="/main.tsx"></script>',
    );
    const binary =
      process.env.TYPEOPT_BIN ||
      fileURLToPath(
        new URL("../../../../target/debug/typeopt", import.meta.url),
      );
    execFileSync(
      binary,
      [
        "--project",
        path.join(root, "tsconfig.json"),
        "--entry",
        source,
        "--out-dir",
        output,
      ],
      { stdio: "pipe" },
    );
    const report = JSON.parse(
      await readFile(path.join(output, "report.json"), "utf8"),
    );
    assert.equal(report.renamed_properties, 1);
    const result = await build({
      root,
      configFile: false,
      plugins: [typeopt(output)],
      resolve: { alias: { "@value": path.join(root, "value.js") } },
      build: { write: false, sourcemap: true, modulePreload: false },
      logLevel: "silent",
    });
    const chunk = result.output.find((file) => file.type === "chunk");
    const values = [];
    runInNewContext(chunk.code, {
      console: { log: (value) => values.push(value) },
    });
    assert.deepEqual(values, [10]);
    const sourceIndex = chunk.map.sources.findIndex((file) =>
      file.endsWith("main.tsx"),
    );
    assert.ok(sourceIndex >= 0);
    assert.match(chunk.map.sourcesContent[sourceIndex], /obj.longProperty/);
    assert.ok(result.output.some((file) => file.fileName.endsWith(".css")));
    await writeFile(
      source,
      (await readFile(source, "utf8")).replace("data.value", "data.value + 1"),
    );
    await assert.rejects(
      build({
        root,
        configFile: false,
        plugins: [typeopt(output)],
        resolve: { alias: { "@value": path.join(root, "value.js") } },
        build: { write: false, sourcemap: true, modulePreload: false },
        logLevel: "silent",
      }),
      /stale/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("rejects stale transitive modules, configuration, declarations and unknown schemas", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "typeopt-vite-snapshot-"));
  try {
    const files = {
      "main.ts": "import { f } from './dep';",
      "dep.ts": "export const f = 1;",
      "tsconfig.json": "{}",
      "globals.d.ts": "declare const g: number;",
    };
    for (const [name, text] of Object.entries(files))
      await writeFile(path.join(root, name), text);
    const manifest = {
      schema_version: 0,
      modules: ["main.ts", "dep.ts"].map((name, index) => ({
        source: path.join(root, name),
        source_sha256: sha256(files[name]),
        output: `${index}.js`,
        source_map: `${index}.js.map`,
        kind: index === 0 ? "optimized" : "preserved",
      })),
      inputs: [
        {
          path: path.join(root, "tsconfig.json"),
          sha256: sha256(files["tsconfig.json"]),
          kind: "configuration",
        },
        {
          path: path.join(root, "globals.d.ts"),
          sha256: sha256(files["globals.d.ts"]),
          kind: "dependency",
        },
      ],
    };
    const out = path.join(root, "out");
    await mkdir(out);
    await writeFile(path.join(out, "modules.json"), JSON.stringify(manifest));
    await typeopt(out).buildStart();
    for (const [name, changed] of [
      ["dep.ts", "export const f = 2;"],
      ["tsconfig.json", '{"compilerOptions":{}}'],
      ["globals.d.ts", "declare const g: string;"],
    ]) {
      await writeFile(path.join(root, name), changed);
      await assert.rejects(typeopt(out).buildStart(), /stale/, name);
      await writeFile(path.join(root, name), files[name]);
    }
    await rm(path.join(root, "globals.d.ts"));
    await assert.rejects(typeopt(out).buildStart(), /stale/);
    await writeFile(path.join(root, "globals.d.ts"), files["globals.d.ts"]);
    await writeFile(
      path.join(out, "modules.json"),
      JSON.stringify({ ...manifest, schema_version: 1 }),
    );
    await assert.rejects(typeopt(out).buildStart(), /Unsupported/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("loads a structurally transformed module without property renaming", async () => {
  const { execFileSync } = await import("node:child_process");
  const { fileURLToPath } = await import("node:url");
  const { readFile } = await import("node:fs/promises");
  const { build } = await import("vite");
  const root = await realpath(
    await mkdtemp(path.join(tmpdir(), "typeopt-vite-structural-")),
  );
  try {
    const source = path.join(root, "main.ts");
    const output = path.join(root, "optimized");
    await writeFile(
      path.join(root, "tsconfig.json"),
      JSON.stringify({
        compilerOptions: {
          module: "ESNext",
          target: "ES2022",
          moduleResolution: "bundler",
          strict: true,
        },
      }),
    );
    await writeFile(
      path.join(root, "events.ts"),
      'export enum EventName { PointerDown = "pointerdown", PointerUp = "pointerup" }',
    );
    await writeFile(
      source,
      'import { EventName } from "./events"; console.log(EventName.PointerDown, EventName.PointerUp);',
    );
    await writeFile(
      path.join(root, "index.html"),
      '<script type="module" src="/main.ts"></script>',
    );
    const binary =
      process.env.TYPEOPT_BIN ||
      fileURLToPath(
        new URL("../../../../target/debug/typeopt", import.meta.url),
      );
    execFileSync(
      binary,
      [
        "--project",
        path.join(root, "tsconfig.json"),
        "--entry",
        source,
        "--out-dir",
        output,
        "--disable-pass",
        "property-shortening",
      ],
      { stdio: "pipe" },
    );
    const report = JSON.parse(
      await readFile(path.join(output, "report.json"), "utf8"),
    );
    assert.equal(report.renamed_properties, 0);
    assert.ok(
      report.transformations.some((t) => t.pass === "enum-runtime-reduction"),
    );
    const manifest = JSON.parse(
      await readFile(path.join(output, "modules.json"), "utf8"),
    );
    assert.ok(
      manifest.modules.some(
        (m) => m.kind === "optimized" && m.source === source,
      ),
    );
    const result = await build({
      root,
      configFile: false,
      plugins: [typeopt(output)],
      build: { write: false, sourcemap: true, modulePreload: false },
      logLevel: "silent",
    });
    const chunk = result.output.find((file) => file.type === "chunk");
    const values = [];
    runInNewContext(chunk.code, {
      console: { log: (...args) => values.push(...args) },
    });
    assert.deepEqual(values, ["pointerdown", "pointerup"]);
    const sourceIndex = chunk.map.sources.findIndex((file) =>
      file.endsWith("main.ts"),
    );
    assert.ok(sourceIndex >= 0);
    assert.match(chunk.map.sourcesContent[sourceIndex], /EventName.PointerDown/);
    await writeFile(
      path.join(root, "events.ts"),
      'export enum EventName { PointerDown = "down", PointerUp = "pointerup" }',
    );
    await assert.rejects(
      build({
        root,
        configFile: false,
        plugins: [typeopt(output)],
        build: { write: false, modulePreload: false },
        logLevel: "silent",
      }),
      /stale/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
