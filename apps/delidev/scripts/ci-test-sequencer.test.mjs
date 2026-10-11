// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { BaseSequencer } from "vitest/node";
import CISequencer, {
  partitionTests,
  sortByWeight,
} from "./ci-test-sequencer.mjs";

const app = fileURLToPath(new URL("../", import.meta.url));
const root = join(app, "src");
const specs = (names) => names.map((name) => ({ moduleId: join(root, name) }));
const paths = (shards) =>
  shards.map((shard) => shard.files.map((file) => file.moduleId));

test("weighted partition separates expensive files and covers every file exactly once", () => {
  const files = specs([
    "App.test.tsx",
    "account-deletion.test.tsx",
    "new.test.ts",
    "renamed.test.ts",
    "nested/new.test.ts",
  ]);
  const timings = {
    "App.test.tsx": 125,
    "account-deletion.test.tsx": 74,
    "deleted.test.ts": 999,
  };
  const shards = partitionTests(files, root, 4, timings);
  assert.deepEqual(
    paths(shards),
    paths(partitionTests(files.toReversed(), root, 4, timings)),
  );
  assert.deepEqual(
    shards.map((shard) => shard.seconds),
    [125, 74, 2, 1],
  );
  const actual = paths(shards).flat();
  assert.equal(new Set(actual).size, files.length);
  assert.deepEqual(
    actual.toSorted(),
    files.map((file) => file.moduleId).toSorted(),
  );
  assert.deepEqual(sortByWeight(files.toReversed(), root, timings), [
    files[0],
    files[1],
    files[4],
    files[2],
    files[3],
  ]);
  assert.deepEqual(
    partitionTests([], root, 4),
    Array.from({ length: 4 }, () => ({ files: [], seconds: 0 })),
  );
});

test("unit and integration inventories are partitioned independently", () => {
  const files = specs([
    "a.test.ts",
    "b.test.ts",
    "a.integration.test.ts",
    "b.integration.test.ts",
  ]);
  for (const integration of [false, true]) {
    const inventory = files.filter(
      (file) => file.moduleId.includes(".integration.test.") === integration,
    );
    const actual = paths(partitionTests(inventory, root, 4)).flat();
    assert.deepEqual(
      actual.toSorted(),
      inventory.map((file) => file.moduleId).toSorted(),
    );
  }
});

test("unsharded local sorting delegates to Vitest, while shards use the same weights", async () => {
  const files = specs(["short.test.ts", "long.test.ts"]);
  for (const file of files)
    file.project = { name: "", config: { sequence: { groupOrder: 0 } } };
  const context = {
    config: { root },
    cache: {
      getFileTestResults: () => undefined,
      getFileStats: (key) => ({ size: key.includes("short") ? 100 : 1 }),
    },
  };
  assert.deepEqual(
    await new CISequencer(context).sort(files),
    await new BaseSequencer(context).sort(files),
  );
  assert.deepEqual(
    (
      await new CISequencer({
        ...context,
        config: { root: app, shard: { index: 1, count: 4 } },
      }).sort(specs(["App.test.tsx", "account-deletion.test.tsx"]))
    )[0].moduleId,
    join(root, "App.test.tsx"),
  );
});

test("installed Vitest executes all four weighted shards and propagates failures", () => {
  const fixture = mkdtempSync(join(tmpdir(), "delidev-shard-"));
  try {
    symlinkSync(
      join(app, "node_modules"),
      join(fixture, "node_modules"),
      process.platform === "win32" ? "junction" : "dir",
    );
    mkdirSync(join(fixture, "src"));
    writeFileSync(join(fixture, "package.json"), '{"type":"module"}\n');
    const sequencer = new URL("./ci-test-sequencer.mjs", import.meta.url).href;
    writeFileSync(
      join(fixture, "vitest.config.mjs"),
      `import sequencer from ${JSON.stringify(sequencer)};\nexport default {test:{include:["src/**/*.test.mjs"],maxWorkers:2,sequence:{sequencer}}};\n`,
    );
    for (const kind of ["unit", "integration"]) {
      for (let index = 0; index < 4; index++) {
        const name =
          kind === "unit"
            ? `${index}.test.mjs`
            : `${index}.integration.test.mjs`;
        writeFileSync(
          join(fixture, "src", name),
          `import {it,expect} from "vitest";\nit("fixture",()=>expect(process.env.SHARD_FIXTURE_FAIL === ${JSON.stringify(name)}).toBe(false));\n`,
        );
      }
    }
    const cli = join(
      dirname(fileURLToPath(import.meta.resolve("vitest/package.json"))),
      "vitest.mjs",
    );
    const run = (index, filters, failure = "") => {
      const report = join(fixture, "result.json");
      rmSync(report, { force: true });
      const result = spawnSync(
        process.execPath,
        [
          cli,
          "run",
          `--shard=${index}/4`,
          ...filters,
          "--reporter=json",
          `--outputFile=${report}`,
        ],
        {
          cwd: fixture,
          env: { ...process.env, SHARD_FIXTURE_FAIL: failure },
          encoding: "utf8",
          timeout: 30000,
        },
      );
      assert.ifError(result.error);
      assert.equal(
        result.status,
        failure ? 1 : 0,
        result.stdout + result.stderr,
      );
      return JSON.parse(readFileSync(report, "utf8")).testResults.map(
        (file) => file.name,
      );
    };
    for (const filters of [
      [],
      ["--exclude", "**/*.integration.test.*"],
      [".integration.test."],
    ]) {
      const files = [1, 2, 3, 4].flatMap((index) => run(index, filters));
      assert.equal(files.length, filters.length ? 4 : 8);
      assert.equal(new Set(files).size, files.length);
    }
    run(1, ["--exclude", "**/*.integration.test.*"], "0.test.mjs");
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});
