// Run with `node --test crates/bundlewhy/adapters/`.
import assert from "node:assert/strict";
import test from "node:test";

import {
  applyFinalChunkGraph,
  chunkGraphFields,
  externalImportNames,
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

test("applyFinalChunkGraph takes the finished bundle or drops the lists", () => {
  const chunk = (fileName, imports) => ({
    type: "chunk",
    fileName,
    imports,
    dynamicImports: [],
  });
  const cells = [
    {
      name: "output-only plugin rewrote the imports",
      result: { output: [chunk("a.js", ["b.js"]), chunk("b.js", [])] },
      matches: true,
      imports: ["b.js"],
    },
    {
      name: "chunk set changed",
      result: { output: [chunk("a.js", ["b.js"])] },
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
        { file: "a.js", imports: ["stale.js"] },
        { file: "b.js", imports: [] },
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
