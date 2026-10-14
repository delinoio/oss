import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import React from "react";
import {
  parseLockedUfbxVersion,
  runBenchmark,
} from "./benchmark-scenes.mjs";
import {
  assertTopology,
  expectedTopology,
  fixtureHash,
  grid,
  inspectGlb,
} from "./benchmark-scenes-core.mjs";

const file = fileURLToPath(new URL("./benchmark-scenes.mjs", import.meta.url));

test("fixed grids preserve topology, bytes and repeatable source content", () => {
  const expectations = {
    small: [81, 128, 81, 128, 1, 1],
    dense: [66049, 131072, 66049, 131072, 1, 1],
    shared: [1089, 2048, 139392, 262144, 1, 128],
    overlay: [2178, 4096, 2178, 4096, 2, 2],
  };
  for (const [workload, values] of Object.entries(expectations)) {
    const expected = expectedTopology(workload);
    assert.deepEqual(
      [
        expected.uniqueVertexCount,
        expected.uniqueTriangleCount,
        expected.instanceVertexCount,
        expected.instanceTriangleCount,
        expected.geometries,
        expected.meshes,
      ],
      values,
    );
    const first = grid(expected.cells);
    const geometries =
      workload === "overlay" ? [first, grid(expected.cells, 0.001)] : [first];
    assert.equal(
      geometries.reduce(
        (bytes, g) =>
          bytes +
          16 +
          g.positions.byteLength +
          g.normals.byteLength +
          g.uv.byteLength +
          g.indices.byteLength,
        0,
      ),
      expected.registeredGeometryBytes,
    );
    assert.equal(
      fixtureHash(geometries),
      fixtureHash(
        workload === "overlay"
          ? [grid(expected.cells), grid(expected.cells, 0.001)]
          : [grid(expected.cells)],
      ),
    );
  }
});

test("locked ufbx version parsing accepts Windows lockfile line endings", () => {
  assert.equal(
    parseLockedUfbxVersion(
      '[package]\r\nname = "other"\r\nversion = "1.0.0"\r\n\r\n[package]\r\nname = "ufbx"\r\nversion = "0.11.4"\r\n',
    ),
    "0.11.4",
  );
});

function glb(
  indexCount,
  componentType = 5125,
  binaryLength = 0,
  nodes = [{ mesh: 0 }],
  sceneNodes = [0],
) {
  const json = Buffer.from(
    JSON.stringify({
      scene: 0,
      scenes: [{ nodes: sceneNodes }],
      nodes,
      meshes: [
        {
          primitives: [
            {
              attributes: { POSITION: 0, NORMAL: 1, TEXCOORD_0: 2 },
              indices: 3,
              mode: 4,
            },
          ],
        },
      ],
      accessors: [
        { count: 81 },
        { count: 81 },
        { count: 81 },
        { count: indexCount, componentType },
      ],
    }),
  );
  const bytes = Buffer.alloc(
    20 + json.length + (binaryLength ? 8 + binaryLength : 0),
  );
  bytes.write("glTF", 0);
  bytes.writeUInt32LE(2, 4);
  bytes.writeUInt32LE(bytes.length, 8);
  bytes.writeUInt32LE(json.length, 12);
  bytes.write("JSON", 16);
  json.copy(bytes, 20);
  if (binaryLength) {
    bytes.writeUInt32LE(binaryLength, 20 + json.length);
    bytes.write("BIN\0", 24 + json.length);
  }
  return bytes;
}

test("independent GLB counts reject an extra face but allow narrower indices", () => {
  const expected = expectedTopology("small");
  const wide = glb(384, 5125, 1536);
  const narrow = glb(384, 5123, 768);
  assertTopology(inspectGlb(wide), expected);
  assertTopology(inspectGlb(narrow), expected);
  assert.notEqual(wide.length, narrow.length);
  assert.throws(
    () => assertTopology(inspectGlb(glb(387)), expected),
    /triangle/i,
  );
});

test("independent GLB counts traverse only the selected scene graph", () => {
  const expected = expectedTopology("small");
  const orphaned = glb(384, 5125, 0, [{ mesh: 0 }, { mesh: 0 }], [0]);
  assertTopology(inspectGlb(orphaned), expected);
});

test("malformed CLI options fail without publishing a report", async () => {
  const dir = await mkdtemp(join(tmpdir(), "forge-scene-benchmark-test-"));
  try {
    for (const args of [
      [],
      ["--output"],
      ["--output", "--bogus"],
      ["--bogus", join(dir, "report.json")],
      ["--output", join(dir, "report.json"), "extra"],
    ]) {
      const result = spawnSync(process.execPath, [file, ...args], {
        encoding: "utf8",
      });
      assert.notEqual(result.status, 0);
    }
    await assert.rejects(readFile(join(dir, "report.json")));
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

async function withDirectory(work) {
  const dir = await mkdtemp(join(tmpdir(), "forge-scene-benchmark-test-"));
  try {
    await work(dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

test("child failure and truncated JSON preserve previous report and unrelated files", async () => {
  await withDirectory(async (dir) => {
    const output = join(dir, "report.json");
    const unrelated = join(dir, "unrelated.txt");
    await writeFile(output, "prior report");
    await writeFile(unrelated, "keep me");
    for (const source of [
      "process.exit(7);",
      "import { writeFileSync } from 'node:fs'; writeFileSync(process.argv[5], 'partial'); process.stdout.write('{');",
    ]) {
      const child = join(dir, "fault.mjs");
      await writeFile(child, source);
      await assert.rejects(
        runBenchmark(output, {
          cases: [{ format: "glb", workload: "small" }],
          caseFile: child,
        }),
      );
      assert.equal(await readFile(output, "utf8"), "prior report");
      assert.equal(await readFile(unrelated, "utf8"), "keep me");
    }
    const names = (await import("node:fs/promises")).readdir;
    assert.deepEqual((await names(dir)).sort(), [
      "fault.mjs",
      "report.json",
      "unrelated.txt",
    ]);
  });
});

test("a verified sample publishes schema one atomically and an invalid destination preserves its bytes", async () => {
  await withDirectory(async (dir) => {
    const core = new URL("./benchmark-scenes-core.mjs", import.meta.url).href;
    const child = join(dir, "synthetic.mjs");
    await writeFile(
      child,
      `
      import { writeFileSync } from 'node:fs';
      import { createHash } from 'node:crypto';
      import { expectedTopology, fixtureHash, grid } from ${JSON.stringify(core)};
      const [, , , format, workload, output] = process.argv;
      const e = expectedTopology(workload);
      const bytes = Buffer.from('synthetic fixture');
      writeFileSync(output, bytes);
      process.stdout.write(JSON.stringify({ format, workload,
        parameters: { cells: e.cells, geometries: e.geometries, meshes: e.meshes },
        uniqueVertexCount: e.uniqueVertexCount, uniqueTriangleCount: e.uniqueTriangleCount,
        instanceVertexCount: e.instanceVertexCount, instanceTriangleCount: e.instanceTriangleCount,
        registeredGeometryBytes: e.registeredGeometryBytes,
        fixtureSha256: fixtureHash([grid(e.cells)]),
        outputBytes: bytes.length, outputSha256: createHash('sha256').update(bytes).digest('hex'),
        revision: 1, prepareMs: 1, exportMs: 2, totalMs: 3, peakRssBytes: 1024,
        eventLoopP99Ms: 0, eventLoopMaxMs: 0, eventLoopUtilization: 0.5
      }));
    `,
    );
    const report = await runBenchmark(join(dir, "report.json"), {
      cases: [{ format: "glb", workload: "small" }],
      caseFile: child,
      verifyArtifact: () => expectedTopology("small"),
      nativeProvenance: {
        nativeBuildProfile: "test",
        nativeBindingSha256: "0".repeat(64),
      },
    });
    assert.equal(report.schemaVersion, 1);
    assert.match(report.command, /benchmark:scenes --output/);
    assert.match(report.sourceRevision, /^[0-9a-f]{40}$/);
    assert.equal(report.node, process.version);
    assert.equal(report.react, React.version);
    assert.equal(report.nativeBuildProfile, "test");
    assert.equal(report.host.platform, process.platform);
    assert.equal(report.samples.length, 1);
    assert.equal(
      report.samples[0].independentTopology.instanceTriangleCount,
      128,
    );
    assert.match(report.samples[0].outputSha256, /^[0-9a-f]{64}$/);
    assert.equal(
      JSON.parse(await readFile(join(dir, "report.json"), "utf8")).samples[0]
        .outputBytes,
      17,
    );
    await assert.rejects(
      runBenchmark(dir, {
        cases: [{ format: "glb", workload: "small" }],
        caseFile: child,
        verifyArtifact: () => expectedTopology("small"),
        nativeProvenance: {
          nativeBuildProfile: "test",
          nativeBindingSha256: "0".repeat(64),
        },
      }),
    );
    await assert.rejects(
      runBenchmark(join(dir, "report.json", "cannot-write.json"), {
        cases: [{ format: "glb", workload: "small" }],
        caseFile: child,
        verifyArtifact: () => expectedTopology("small"),
        nativeProvenance: {
          nativeBuildProfile: "test",
          nativeBindingSha256: "0".repeat(64),
        },
      }),
    );
    assert.equal(
      JSON.parse(await readFile(join(dir, "report.json"), "utf8")).samples
        .length,
      1,
    );
  });
});

test("cancellation reaps the owned child without replacing prior output", async () => {
  await withDirectory(async (dir) => {
    const marker = join(dir, "child.pid");
    const child = join(dir, "wait.mjs");
    const output = join(dir, "report.json");
    await writeFile(output, "prior report");
    await writeFile(
      child,
      `import { writeFileSync } from 'node:fs'; writeFileSync(${JSON.stringify(marker)}, String(process.pid)); setInterval(() => {}, 1000);`,
    );
    const controller = new AbortController();
    const running = runBenchmark(output, {
      cases: [{ format: "glb", workload: "small" }],
      caseFile: child,
      signal: controller.signal,
    });
    let earlyError;
    const settled = running.catch((error) => {
      earlyError = error;
    });
    try {
      let pid;
      const deadline = Date.now() + 30_000;
      while (Date.now() < deadline) {
        if (earlyError) throw earlyError;
        try {
          pid = Number(await readFile(marker, "utf8"));
          if (pid > 0) break;
        } catch {
          // The child may not have created the marker yet.
        }
        await new Promise((done) => setTimeout(done, 50));
      }
      assert.ok(pid > 0, "child started");
      controller.abort();
      await assert.rejects(running, /cancelled/i);
      assert.throws(() => process.kill(pid, 0), { code: "ESRCH" });
      assert.equal(await readFile(output, "utf8"), "prior report");
    } finally {
      controller.abort();
      await settled;
    }
  });
});
