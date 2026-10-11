import assert from "node:assert/strict";
import { runInNewContext } from "node:vm";
import { test } from "node:test";
import { mkdtemp, mkdir, writeFile, rm, realpath } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { typeopt } from "./adapter.mjs";

test("keeps original module identity and bypasses query/JS/asset requests", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "typeopt-vite-"));
  try {
    const source = path.join(root, "workspace", "main.tsx");
    await writeFile(
      path.join(root, "modules.json"),
      JSON.stringify({
        schema_version: 0,
        modules: [
          { source, output: "0.js", source_map: "0.js.map", kind: "optimized" },
          {
            source: path.join(root, "preserved.ts"),
            output: "1.js",
            kind: "preserved",
          },
          {
            source: path.join(root, "unchanged.js"),
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
    await writeFile(
      source,
      'throw new Error("original TSX was not replaced");',
    );
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
          { source, output: "0.js", source_map: "0.js.map", kind: "optimized" },
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
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
