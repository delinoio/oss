// Run with `node --test crates/bundlewhy/adapters/vite.chunk-graph.test.mjs`
// (the explicit file path; the directory form fails on current Node versions).
import assert from "node:assert/strict";
import test from "node:test";

import {
  applyFinalChunkGraph,
  chunkGraphFields,
  externalImportNames,
  finalPlacementMatches,
} from "./vite.mjs";

test("externalImportNames covers the id and its output.paths replacement", () => {
  const cells = [
    { name: "no paths", ids: ["react"], paths: undefined, expected: ["react"] },
    {
      name: "record",
      ids: ["react"],
      paths: { react: "vendor.js" },
      expected: ["react", "vendor.js"],
    },
    {
      name: "function",
      ids: ["react"],
      paths: (id) => `cdn/${id}`,
      expected: ["react", "cdn/react"],
    },
    {
      name: "throwing function",
      ids: ["react"],
      paths: () => {
        throw new Error("boom");
      },
      expected: ["react"],
    },
    {
      name: "non-string replacement",
      ids: ["react"],
      paths: { react: 1 },
      expected: ["react"],
    },
  ];
  for (const cell of cells) {
    assert.deepEqual(
      [...externalImportNames(cell.ids, cell.paths)].sort(),
      [...cell.expected].sort(),
      cell.name,
    );
  }
});

test("chunkGraphFields marks rewritten externals", () => {
  const fields = chunkGraphFields(
    { imports: ["vendor.js", "dep.js"], dynamicImports: ["lazy.js"] },
    externalImportNames(["react"], { react: "vendor.js" }),
  );
  assert.deepEqual(fields, {
    imports: ["vendor.js", "dep.js"],
    externalImports: ["vendor.js"],
    dynamicImports: ["lazy.js"],
  });
});

/** A chunk of the finished build; `modules` are the ids it holds. */
const finalChunk = (fileName, imports, modules = [], dynamicImports = []) => ({
  type: "chunk",
  fileName,
  imports,
  dynamicImports,
  modules: Object.fromEntries(
    modules.map((id) => [id, { renderedLength: 1, renderedExports: [] }]),
  ),
});

test("applyFinalChunkGraph takes the finished bundle or drops the lists", () => {
  const cells = [
    {
      name: "output-only plugin rewrote the imports",
      result: {
        output: [finalChunk("a.js", ["b.js"], ["x"]), finalChunk("b.js", [])],
      },
      matches: true,
      imports: ["b.js"],
    },
    {
      name: "chunk set changed",
      result: { output: [finalChunk("a.js", ["b.js"], ["x"])] },
      matches: false,
      imports: undefined,
    },
    {
      name: "several outputs",
      result: [{ output: [] }, { output: [] }],
      matches: false,
      imports: undefined,
    },
    {
      name: "unreadable result",
      result: undefined,
      matches: false,
      imports: undefined,
    },
  ];
  for (const cell of cells) {
    const collection = {
      chunks: [
        { file: "a.js", modules: ["x"], imports: ["stale.js"] },
        { file: "b.js", modules: [], imports: [] },
      ],
    };
    assert.equal(
      applyFinalChunkGraph(collection, cell.result, new Set()),
      cell.matches,
      cell.name,
    );
    assert.deepEqual(collection.chunks[0].imports, cell.imports, cell.name);
  }
});

/**
 * Snapshot of the three-chunk fixture: `entry.js` holds the requester,
 * `shared.js` the target and `lazy.js` is an empty lazy chunk. Every chunk
 * carries import lists from the collector's own snapshot.
 */
const snapshotCollection = () => ({
  modules: [
    { id: "requester", chunks: ["entry.js"] },
    { id: "target", chunks: ["shared.js"] },
  ],
  chunks: [
    {
      file: "entry.js",
      modules: ["requester"],
      imports: [],
      externalImports: [],
      dynamicImports: ["shared.js"],
    },
    {
      file: "shared.js",
      modules: ["target"],
      imports: [],
      externalImports: [],
      dynamicImports: [],
    },
    {
      file: "lazy.js",
      modules: [],
      imports: [],
      externalImports: [],
      dynamicImports: [],
    },
  ],
});

const listFields = ["imports", "externalImports", "dynamicImports"];

test("applyFinalChunkGraph drops every list when module placement changed", () => {
  const cells = [
    {
      name: "target moved to another chunk with the same file names",
      output: [
        finalChunk("entry.js", ["shared.js"], ["requester"], ["lazy.js"]),
        finalChunk("shared.js", [], []),
        finalChunk("lazy.js", [], ["target"]),
      ],
    },
    {
      name: "target removed from the output",
      output: [
        finalChunk("entry.js", ["shared.js"], ["requester"]),
        finalChunk("shared.js", [], []),
        finalChunk("lazy.js", [], []),
      ],
    },
    {
      name: "requester moved to another chunk",
      output: [
        finalChunk("entry.js", ["shared.js"], []),
        finalChunk("shared.js", [], ["target"]),
        finalChunk("lazy.js", [], ["requester"]),
      ],
    },
    {
      name: "requester removed from the output",
      output: [
        finalChunk("entry.js", ["shared.js"], []),
        finalChunk("shared.js", [], ["target"]),
        finalChunk("lazy.js", [], []),
      ],
    },
    {
      name: "module added to a chunk",
      output: [
        finalChunk("entry.js", [], ["requester", "extra"]),
        finalChunk("shared.js", [], ["target"]),
        finalChunk("lazy.js", [], []),
      ],
    },
    {
      name: "target copied into a second chunk",
      output: [
        finalChunk("entry.js", [], ["requester"]),
        finalChunk("shared.js", [], ["target"]),
        finalChunk("lazy.js", [], ["target"]),
      ],
    },
    {
      name: "chunk without a readable module record",
      output: [
        finalChunk("entry.js", [], ["requester"]),
        finalChunk("shared.js", [], ["target"]),
        { type: "chunk", fileName: "lazy.js", imports: [], dynamicImports: [] },
      ],
    },
    {
      name: "module record is not an id-keyed object",
      output: [
        finalChunk("entry.js", [], ["requester"]),
        finalChunk("shared.js", [], ["target"]),
        {
          type: "chunk",
          fileName: "lazy.js",
          imports: [],
          dynamicImports: [],
          modules: [],
        },
      ],
    },
  ];
  for (const cell of cells) {
    const collection = snapshotCollection();
    assert.equal(
      applyFinalChunkGraph(collection, { output: cell.output }, new Set()),
      false,
      cell.name,
    );
    for (const chunk of collection.chunks) {
      for (const field of listFields) {
        assert.equal(
          chunk[field],
          undefined,
          `${cell.name}: ${chunk.file}.${field}`,
        );
      }
    }
    // The snapshot placement stays exactly as collected: it is never rebuilt
    // from the final output.
    assert.deepEqual(
      collection.chunks.map((chunk) => chunk.modules),
      [["requester"], ["target"], []],
      cell.name,
    );
    assert.deepEqual(
      collection.modules,
      snapshotCollection().modules,
      cell.name,
    );
  }
});

test("applyFinalChunkGraph keeps the final lists when module membership is unchanged", () => {
  const collection = snapshotCollection();
  // An output-only plugin rewrote the imports and moved nothing; the module
  // order inside a chunk and extra assets are not placement.
  const output = [
    finalChunk("lazy.js", [], []),
    finalChunk("shared.js", ["lazy.js"], ["target"]),
    finalChunk("entry.js", ["shared.js", "react"], ["requester"], ["lazy.js"]),
    { type: "asset", fileName: "entry.js.map" },
  ];
  assert.equal(
    applyFinalChunkGraph(collection, { output }, new Set(["react"])),
    true,
  );
  const [entry, shared, lazy] = collection.chunks;
  assert.deepEqual(entry.imports, ["shared.js", "react"]);
  assert.deepEqual(entry.externalImports, ["react"]);
  assert.deepEqual(entry.dynamicImports, ["lazy.js"]);
  assert.deepEqual(shared.imports, ["lazy.js"]);
  assert.deepEqual(lazy.imports, []);
  assert.deepEqual(lazy.dynamicImports, []);
});

test("applyFinalChunkGraph ignores the order of modules inside a chunk", () => {
  const collection = {
    chunks: [{ file: "a.js", modules: ["x", "y"], imports: ["stale.js"] }],
  };
  const output = [finalChunk("a.js", [], ["y", "x"])];
  assert.equal(applyFinalChunkGraph(collection, { output }, new Set()), true);
  assert.deepEqual(collection.chunks[0].imports, []);
});

test("applyFinalChunkGraph invalidates a same-chunk snapshot when the target moves out", () => {
  // The requester and the target share `main.js` in the snapshot; the final
  // output moves the target to a lazy chunk. Dropping only the import lists
  // would leave the stale same-chunk membership, so the lists of every chunk
  // are dropped and the rule reads the collection as having no graph data.
  const collection = {
    modules: [
      { id: "requester", chunks: ["main.js"] },
      { id: "target", chunks: ["main.js"] },
    ],
    chunks: [
      { file: "main.js", modules: ["requester", "target"], imports: [] },
      { file: "lazy.js", modules: [], imports: [] },
    ],
  };
  const output = [
    finalChunk("main.js", ["lazy.js"], ["requester"], ["lazy.js"]),
    finalChunk("lazy.js", [], ["target"]),
  ];
  assert.equal(applyFinalChunkGraph(collection, { output }, new Set()), false);
  for (const chunk of collection.chunks) {
    for (const field of listFields) assert.equal(chunk[field], undefined);
  }
  assert.deepEqual(collection.chunks[0].modules, ["requester", "target"]);
});

test("finalPlacementMatches compares membership per chunk file", () => {
  const snapshot = [
    { file: "a.js", modules: ["x"] },
    { file: "b.js", modules: [] },
  ];
  const final = (a, b) =>
    new Map([
      ["a.js", finalChunk("a.js", [], a)],
      ["b.js", finalChunk("b.js", [], b)],
    ]);
  assert.equal(finalPlacementMatches(snapshot, final(["x"], [])), true);
  assert.equal(finalPlacementMatches(snapshot, final([], ["x"])), false);
  assert.equal(finalPlacementMatches(snapshot, final(["x"], ["x"])), false);
  assert.equal(finalPlacementMatches(snapshot, final(["y"], [])), false);
  assert.equal(finalPlacementMatches(snapshot, new Map()), false);
  assert.equal(finalPlacementMatches([], new Map()), true);
});
