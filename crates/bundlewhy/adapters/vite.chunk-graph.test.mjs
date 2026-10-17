// Run with `node --test crates/bundlewhy/adapters/vite.chunk-graph.test.mjs`
// (the explicit file path; the directory form fails on current Node versions).
import assert from "node:assert/strict";
import test from "node:test";

import {
  applyFinalChunkGraph,
  chunkCodeDigest,
  chunkGraphFields,
  externalImportIdentity,
  finalCodeMatches,
  finalPlacementMatches,
} from "./vite.mjs";

/** Identity of externals whose rendered names are known. */
const known = (names = []) => ({
  names: new Set(names),
  renderedNamesUnknown: false,
});

/** A function that counts its calls. */
const forbiddenCall = () => {
  let calls = 0;
  const paths = () => {
    calls += 1;
    return "vendor.js";
  };
  return { paths, calls: () => calls };
};

test("externalImportIdentity reads a record and never calls a function", () => {
  const cells = [
    {
      name: "no paths",
      ids: ["react"],
      paths: undefined,
      names: ["react"],
      unknown: false,
    },
    {
      name: "null paths",
      ids: ["react"],
      paths: null,
      names: ["react"],
      unknown: false,
    },
    {
      name: "record",
      ids: ["react"],
      paths: { react: "vendor.js" },
      names: ["react", "vendor.js"],
      unknown: false,
    },
    {
      name: "record entry for a module that is not external",
      ids: ["react"],
      paths: { vue: "vue.js" },
      names: ["react"],
      unknown: false,
    },
    {
      name: "record with a non-string replacement",
      ids: ["react"],
      paths: { react: 1 },
      names: ["react"],
      unknown: false,
    },
    {
      name: "inherited members are not replacements",
      ids: ["constructor", "toString", "__proto__"],
      paths: {},
      names: ["constructor", "toString", "__proto__"],
      unknown: false,
    },
    {
      name: "empty external set with a record",
      ids: [],
      paths: { react: "vendor.js" },
      names: [],
      unknown: false,
    },
    {
      name: "array is not a documented shape",
      ids: ["react"],
      paths: ["vendor.js"],
      names: ["react"],
      unknown: true,
    },
    {
      name: "string is not a documented shape",
      ids: ["react"],
      paths: "vendor.js",
      names: ["react"],
      unknown: true,
    },
  ];
  for (const cell of cells) {
    const identity = externalImportIdentity(cell.ids, cell.paths);
    assert.deepEqual(
      [...identity.names].sort(),
      [...cell.names].sort(),
      cell.name,
    );
    assert.equal(identity.renderedNamesUnknown, cell.unknown, cell.name);
  }

  // A stateful function answers differently on every call. It is never
  // called, whatever the external ids are, so a replayed answer cannot name an
  // external that the output does not contain.
  const spy = forbiddenCall();
  const identity = externalImportIdentity(["react", "vue"], spy.paths);
  assert.equal(spy.calls(), 0, "function paths is not called");
  assert.equal(identity.renderedNamesUnknown, true);
  assert.deepEqual([...identity.names].sort(), ["react", "vue"]);

  // The same holds for a function that would throw, and without externals.
  const throwing = externalImportIdentity(["react"], () => {
    throw new Error("must not be called");
  });
  assert.equal(throwing.renderedNamesUnknown, true);
  assert.deepEqual([...throwing.names], ["react"]);
  assert.equal(
    externalImportIdentity([], () => "x").renderedNamesUnknown,
    true,
    "no external module is still unknown: external detection is not trusted to prove absence",
  );
});

test("chunkGraphFields marks rewritten externals of a record", () => {
  const fields = chunkGraphFields(
    { imports: ["vendor.js", "dep.js"], dynamicImports: ["lazy.js"] },
    externalImportIdentity(["react"], { react: "vendor.js" }),
  );
  assert.deepEqual(fields, {
    imports: ["vendor.js", "dep.js"],
    externalImports: ["vendor.js"],
    dynamicImports: ["lazy.js"],
  });
});

/** The generated code that a chunk has unless a test edits it. */
const originalCode = (fileName) => `// generated code of ${fileName}\n`;

/**
 * Digests the collector would take of the unedited chunks of `collection`
 * in its `generateBundle` hook.
 */
const snapshotCodeOf = (collection) =>
  new Map(
    collection.chunks.map((chunk) => [
      chunk.file,
      chunkCodeDigest({ code: originalCode(chunk.file) }),
    ]),
  );

test("chunkGraphFields never calls a stateful output.paths function", () => {
  // The bundler rendered `react` as `vendor.js` (the first call), which is also
  // an emitted chunk file. A second call would answer `cdn/react` and the
  // collision would go unnoticed.
  let calls = 0;
  const paths = (id) => (calls++ === 0 ? "vendor.js" : `cdn/${id}`);
  const identity = externalImportIdentity(["react"], paths);
  assert.equal(calls, 0);
  const fields = chunkGraphFields(
    { imports: ["vendor.js", "dep.js", "react"], dynamicImports: [] },
    identity,
  );
  assert.equal(calls, 0, "collecting the fields does not call paths");
  assert.deepEqual(fields.imports, ["vendor.js", "dep.js", "react"]);
  // Every static entry is ambiguous, including the ones that look internal.
  assert.deepEqual(fields.externalImports, ["vendor.js", "dep.js", "react"]);
});

test("chunkGraphFields with an unknown identity keeps a chunk without imports clean", () => {
  const identity = externalImportIdentity(["react"], () => "x");
  assert.deepEqual(chunkGraphFields({}, identity), {
    imports: [],
    externalImports: [],
    dynamicImports: [],
  });
  assert.deepEqual(
    chunkGraphFields({ dynamicImports: ["lazy.js"] }, identity),
    { imports: [], externalImports: [], dynamicImports: ["lazy.js"] },
    "dynamic imports are not marked",
  );
});

test("chunkGraphFields with a record keeps ordinary internal edges", () => {
  const fields = chunkGraphFields(
    { imports: ["shared.js", "react"], dynamicImports: [] },
    externalImportIdentity(["react"], { react: "cdn/react" }),
  );
  assert.deepEqual(fields.externalImports, ["react"]);
  // A record colliding with an emitted chunk file stays ambiguous.
  const collision = chunkGraphFields(
    { imports: ["vendor.js", "shared.js"], dynamicImports: [] },
    externalImportIdentity(["react"], { react: "vendor.js" }),
  );
  assert.deepEqual(collision.externalImports, ["vendor.js"]);
});

/** A chunk of the finished build; `modules` are the ids it holds. */
const finalChunk = (fileName, imports, modules = [], dynamicImports = []) => ({
  type: "chunk",
  fileName,
  code: originalCode(fileName),
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
      applyFinalChunkGraph(
        collection,
        cell.result,
        known(),
        snapshotCodeOf(collection),
      ),
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
      applyFinalChunkGraph(
        collection,
        { output: cell.output },
        known(),
        snapshotCodeOf(collection),
      ),
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
    applyFinalChunkGraph(
      collection,
      { output },
      known(["react"]),
      snapshotCodeOf(collection),
    ),
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

test("applyFinalChunkGraph marks every import ambiguous for a function output.paths", () => {
  // `vendor.js` is both an emitted chunk and the string an external was
  // rendered as; the function cannot be asked again, so the final lists carry
  // every static entry as possibly external.
  const collection = snapshotCollection();
  const output = [
    finalChunk(
      "entry.js",
      ["vendor.js", "shared.js"],
      ["requester"],
      ["shared.js"],
    ),
    finalChunk("shared.js", [], ["target"]),
    finalChunk("lazy.js", [], []),
  ];
  let calls = 0;
  const paths = () => {
    calls += 1;
    return "unused";
  };
  const identity = externalImportIdentity(["react"], paths);
  assert.equal(
    applyFinalChunkGraph(
      collection,
      { output },
      identity,
      snapshotCodeOf(collection),
    ),
    true,
  );
  assert.equal(calls, 0);
  const [entry, shared, lazy] = collection.chunks;
  assert.deepEqual(entry.imports, ["vendor.js", "shared.js"]);
  assert.deepEqual(entry.externalImports, ["vendor.js", "shared.js"]);
  assert.deepEqual(entry.dynamicImports, ["shared.js"]);
  assert.deepEqual(shared.externalImports, []);
  assert.deepEqual(lazy.externalImports, []);
});

test("applyFinalChunkGraph keeps the unchanged-membership check for a function output.paths", () => {
  const collection = snapshotCollection();
  const output = [
    finalChunk("entry.js", ["shared.js"], ["requester", "extra"]),
    finalChunk("shared.js", [], ["target"]),
    finalChunk("lazy.js", [], []),
  ];
  const identity = externalImportIdentity(["react"], () => "x");
  assert.equal(
    applyFinalChunkGraph(
      collection,
      { output },
      identity,
      snapshotCodeOf(collection),
    ),
    false,
  );
  for (const chunk of collection.chunks) {
    for (const field of listFields) assert.equal(chunk[field], undefined);
  }
});

test("applyFinalChunkGraph ignores the order of modules inside a chunk", () => {
  const collection = {
    chunks: [{ file: "a.js", modules: ["x", "y"], imports: ["stale.js"] }],
  };
  const output = [finalChunk("a.js", [], ["y", "x"])];
  assert.equal(
    applyFinalChunkGraph(
      collection,
      { output },
      known(),
      snapshotCodeOf(collection),
    ),
    true,
  );
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
  assert.equal(
    applyFinalChunkGraph(
      collection,
      { output },
      known(),
      snapshotCodeOf(collection),
    ),
    false,
  );
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

test("applyFinalChunkGraph drops every list when a chunk's code changed after the snapshot", () => {
  // An output-only plugin edits `OutputChunk.code` (for example it moves the
  // target's code into the requester) but leaves `OutputChunk.modules` as it
  // was, so the membership comparison alone accepts the stale placement.
  const cells = [
    {
      name: "code of the requester chunk edited",
      edit: (output) => {
        output[0].code += "// moved target code\n";
      },
    },
    {
      name: "code of an unrelated chunk edited",
      edit: (output) => {
        output[2].code = "";
      },
    },
    {
      name: "final chunk has no readable code",
      edit: (output) => {
        delete output[1].code;
      },
    },
  ];
  for (const cell of cells) {
    const collection = snapshotCollection();
    const output = [
      finalChunk("entry.js", ["shared.js"], ["requester"], ["shared.js"]),
      finalChunk("shared.js", [], ["target"]),
      finalChunk("lazy.js", [], []),
    ];
    cell.edit(output);
    assert.equal(
      applyFinalChunkGraph(
        collection,
        { output },
        known(),
        snapshotCodeOf(collection),
      ),
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
  }
});

test("applyFinalChunkGraph drops the lists when the snapshot has no code digests", () => {
  const output = [
    finalChunk("entry.js", [], ["requester"]),
    finalChunk("shared.js", [], ["target"]),
    finalChunk("lazy.js", [], []),
  ];
  for (const snapshotCode of [undefined, null, new Map(), {}]) {
    const collection = snapshotCollection();
    assert.equal(
      applyFinalChunkGraph(collection, { output }, known(), snapshotCode),
      false,
    );
    assert.equal(collection.chunks[0].imports, undefined);
  }
});

test("finalCodeMatches compares the code digest per chunk file", () => {
  const snapshot = [{ file: "a.js" }, { file: "b.js" }];
  const digests = new Map([
    ["a.js", chunkCodeDigest({ code: "a" })],
    ["b.js", chunkCodeDigest({ code: "b" })],
  ]);
  const final = (a, b) =>
    new Map([
      ["a.js", { code: a }],
      ["b.js", { code: b }],
    ]);
  assert.equal(finalCodeMatches(digests, snapshot, final("a", "b")), true);
  assert.equal(finalCodeMatches(digests, snapshot, final("a", "b2")), false);
  assert.equal(
    finalCodeMatches(digests, snapshot, final("a", undefined)),
    false,
  );
  assert.equal(finalCodeMatches(digests, snapshot, new Map()), false);
  assert.equal(finalCodeMatches(new Map(), snapshot, final("a", "b")), false);
  assert.equal(finalCodeMatches(undefined, snapshot, final("a", "b")), false);
  assert.equal(finalCodeMatches(digests, [], new Map()), true);
  assert.equal(chunkCodeDigest({}), null);
  assert.equal(chunkCodeDigest({ code: 1 }), null);
});
