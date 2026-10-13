import assert from "node:assert/strict";
import { runInNewContext } from "node:vm";
import { test } from "node:test";
import {
  mkdtemp,
  mkdir,
  writeFile,
  rm,
  realpath,
  symlink,
} from "node:fs/promises";
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
        // Keep the object so this test exercises property renaming;
        // scalar replacement would otherwise remove it first.
        "--disable-pass",
        "aggregate-elimination",
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

test("rejects output once an absent higher-priority resolution candidate appears", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "typeopt-vite-absent-"));
  try {
    const files = {
      "main.ts": "import { f } from './dep';",
      "dep.tsx": "export const f = 1;",
      "tsconfig.json": "{}",
    };
    for (const [name, text] of Object.entries(files))
      await writeFile(path.join(root, name), text);
    const absent = path.join(root, "dep.ts");
    const manifest = {
      schema_version: 0,
      modules: ["main.ts", "dep.tsx"].map((name, index) => ({
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
        { path: absent, kind: "absent" },
        { path: path.join(root, "dep.d.ts"), kind: "absent" },
      ],
    };
    const out = path.join(root, "out");
    await mkdir(out);
    await writeFile(path.join(out, "modules.json"), JSON.stringify(manifest));
    // A still-absent candidate with unchanged listed files validates.
    await typeopt(out).buildStart();
    // Creating the candidate leaves every positive digest unchanged.
    await writeFile(absent, "export const f = 2;");
    await assert.rejects(typeopt(out).buildStart(), (error) => {
      assert.match(error.message, /typeopt output is stale/);
      assert.ok(error.message.includes(absent));
      return true;
    });
    // A directory at the candidate path also invalidates the output.
    await rm(absent);
    await mkdir(absent);
    await assert.rejects(typeopt(out).buildStart(), /stale/);
    await rm(absent, { recursive: true });
    await typeopt(out).buildStart();
    // A dangling symlink candidate is still absent until its target appears.
    const dangling = path.join(root, "dep.mts");
    const target = path.join(root, "later-target.mts");
    await symlink(target, dangling);
    manifest.inputs.push({ path: dangling, kind: "absent" });
    await writeFile(path.join(out, "modules.json"), JSON.stringify(manifest));
    await typeopt(out).buildStart();
    await writeFile(target, "export const f = 4;");
    await assert.rejects(typeopt(out).buildStart(), (error) => {
      assert.match(error.message, /typeopt output is stale/);
      assert.ok(error.message.includes(dangling));
      return true;
    });
    await rm(target);
    manifest.inputs.pop();
    await rm(dangling);
    await writeFile(path.join(out, "modules.json"), JSON.stringify(manifest));
    await typeopt(out).buildStart();
    // A candidate below a regular file stays absent (ENOTDIR).
    manifest.inputs.push({
      path: path.join(root, "main.ts", "nested.ts"),
      kind: "absent",
    });
    await writeFile(path.join(out, "modules.json"), JSON.stringify(manifest));
    await typeopt(out).buildStart();
    // Positive inputs still invalidate on edit or removal.
    await writeFile(path.join(root, "dep.tsx"), "export const f = 3;");
    await assert.rejects(typeopt(out).buildStart(), /stale/);
    await writeFile(path.join(root, "dep.tsx"), files["dep.tsx"]);
    await rm(path.join(root, "tsconfig.json"));
    await assert.rejects(typeopt(out).buildStart(), /stale/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("rejects unknown input forms, digests on negative inputs and malformed mismatch inputs", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "typeopt-vite-forms-"));
  try {
    const source = path.join(root, "main.ts");
    await writeFile(source, "export {};");
    const out = path.join(root, "out");
    await mkdir(out);
    const base = {
      schema_version: 0,
      modules: [
        {
          source,
          source_sha256: sha256("export {};"),
          output: "0.js",
          source_map: "0.js.map",
          kind: "preserved",
        },
      ],
    };
    for (const input of [
      { path: path.join(root, "dep.ts"), kind: "future_kind" },
      { path: path.join(root, "dep.ts"), kind: "absent", sha256: sha256("") },
      { kind: "absent" },
      { path: path.join(root, "dep.ts"), kind: "dependency" },
      { path: path.join(root, "dep.ts"), kind: "mismatch" },
      { path: path.join(root, "dep.ts"), kind: "mismatch", becomes: "symlink" },
      {
        path: path.join(root, "dep.ts"),
        kind: "mismatch",
        becomes: "file",
        sha256: sha256(""),
      },
      { path: path.join(root, "dep.ts"), kind: "absent", becomes: "file" },
    ]) {
      await writeFile(
        path.join(out, "modules.json"),
        JSON.stringify({ ...base, inputs: [input] }),
      );
      await assert.rejects(
        typeopt(out).buildStart(),
        /invalid input snapshot; regenerate with the matching CLI/,
        JSON.stringify(input),
      );
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("rejects output once a wrong-kind resolution candidate becomes the kind that satisfies the probe", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "typeopt-vite-mismatch-"));
  try {
    const files = {
      "main.ts": "import { f } from './dep';",
      "dep.tsx": "export const f = 1;",
    };
    for (const [name, text] of Object.entries(files))
      await writeFile(path.join(root, name), text);
    // `dep.ts` is a directory, so a file probe skipped it for `dep.tsx`.
    const directory = path.join(root, "dep.ts");
    await mkdir(directory);
    // `dep.mts` is a file that a directory probe skipped.
    const file = path.join(root, "dep.mts");
    await writeFile(file, "export const f = 0;");
    const manifest = {
      schema_version: 0,
      modules: ["main.ts", "dep.tsx"].map((name, index) => ({
        source: path.join(root, name),
        source_sha256: sha256(files[name]),
        output: `${index}.js`,
        source_map: `${index}.js.map`,
        kind: index === 0 ? "optimized" : "preserved",
      })),
      inputs: [
        { path: directory, kind: "mismatch", becomes: "file" },
        { path: file, kind: "mismatch", becomes: "directory" },
      ],
    };
    const out = path.join(root, "out");
    await mkdir(out);
    await writeFile(path.join(out, "modules.json"), JSON.stringify(manifest));
    // Wrong-kind candidates that keep their kind do not invalidate output.
    await typeopt(out).buildStart();
    // Removing a wrong-kind candidate leaves resolution unchanged.
    await rm(directory, { recursive: true });
    await typeopt(out).buildStart();
    // Replacing the directory with a regular file satisfies the file probe.
    await writeFile(directory, "export const f = 2;");
    await assert.rejects(typeopt(out).buildStart(), (error) => {
      assert.match(error.message, /typeopt output is stale/);
      assert.ok(error.message.includes(directory));
      return true;
    });
    await rm(directory);
    await mkdir(directory);
    await typeopt(out).buildStart();
    // Replacing the file with a directory satisfies the directory probe.
    await rm(file);
    await mkdir(file);
    await assert.rejects(typeopt(out).buildStart(), (error) => {
      assert.match(error.message, /typeopt output is stale/);
      assert.ok(error.message.includes(file));
      return true;
    });
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
    assert.match(
      chunk.map.sourcesContent[sourceIndex],
      /EventName.PointerDown/,
    );
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

test("real optimizer output is rejected once a shadowing dep.ts appears", async () => {
  const { execFileSync } = await import("node:child_process");
  const { fileURLToPath } = await import("node:url");
  const { readFile } = await import("node:fs/promises");
  const root = await realpath(
    await mkdtemp(path.join(tmpdir(), "typeopt-vite-shadow-")),
  );
  try {
    const output = path.join(root, "optimized");
    await writeFile(
      path.join(root, "tsconfig.json"),
      JSON.stringify({
        compilerOptions: {
          module: "ESNext",
          target: "ES2022",
          moduleResolution: "bundler",
          jsx: "react-jsx",
          strict: true,
        },
      }),
    );
    await writeFile(
      path.join(root, "main.ts"),
      'import { answer } from "./dep"; console.log(answer());',
    );
    await writeFile(
      path.join(root, "dep.tsx"),
      "export function answer() { return 1; }",
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
        path.join(root, "main.ts"),
        "--out-dir",
        output,
      ],
      { stdio: "pipe" },
    );
    const manifest = JSON.parse(
      await readFile(path.join(output, "modules.json"), "utf8"),
    );
    const absent = path.join(root, "dep.ts");
    assert.ok(
      manifest.inputs.some((i) => i.kind === "absent" && i.path === absent),
      JSON.stringify(manifest.inputs),
    );
    // Unchanged files and a still-absent dep.ts validate.
    await typeopt(output).buildStart();
    // Creating dep.ts changes no recorded digest, yet must invalidate.
    await writeFile(absent, "export function answer() { return 2; }");
    await assert.rejects(typeopt(output).buildStart(), /stale/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
