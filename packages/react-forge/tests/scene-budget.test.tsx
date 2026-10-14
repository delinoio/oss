import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { createRequire } from "node:module";
import { promisify } from "node:util";
import { mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import React from "react";
import {
  createSession,
  ErrorCode,
  Format,
  ForgeError,
  SceneBudgetAction,
  SceneBudgetMetric,
  type SceneGeometryBudget,
  type SceneBudgetWarning,
} from "../src/index.js";
import { Scene, Mesh, Group, PerspectiveCamera } from "../src/glb.js";
import { connect } from "./mcp/client.js";

const sceneTest =
  process.env.REACT_FORGE_SKIP_SCENE_TESTS === "1" ? test.skip : test;
const exec = promisify(execFile);
const validator = createRequire(import.meta.url)("gltf-validator") as {
  validateBytes(bytes: Uint8Array): Promise<{ issues: { numErrors: number } }>;
};
const quad = () => ({
  positions: new Float32Array([0, 0, 0, 1, 0, 0, 1, 1, 0, 0, 1, 0]),
  normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1]),
  indices: new Uint32Array([0, 1, 2, 0, 2, 3]),
});
const triangle = () => ({
  positions: new Float32Array([0, 0, 0, 1, 0, 0, 0, 1, 0]),
  normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1]),
  indices: new Uint32Array([0, 1, 2]),
});
async function authored(
  format: Format.Glb | Format.Fbx,
  geometryBudget?: SceneGeometryBudget,
) {
  const session = createSession(format, { geometryBudget });
  const q = await session.registerGeometry(quad());
  const t = await session.registerGeometry(triangle());
  const unused = quad();
  unused.positions[3] = 2;
  await session.registerGeometry(unused);
  await session.render(
    <Scene name="PRIVATE_BUDGET_SCENE">
      <Group>
        <Mesh geometry={q} name="PRIVATE_BUDGET_MESH" />
        <Mesh geometry={q} material={{ doubleSided: true }} />
      </Group>
      <Mesh geometry={q} />
      <Mesh geometry={t} />
      <PerspectiveCamera yfov={0.7} />
    </Scene>,
  );
  return session;
}
function budget(
  action: SceneBudgetAction,
  values: Partial<SceneGeometryBudget>,
): SceneGeometryBudget {
  return { action, ...values };
}

for (const format of [Format.Glb, Format.Fbx] as const) {
  sceneTest(
    `${format}: exact limits pass; each lower limit rejects before publication`,
    async () => {
      const passing = await authored(
        format,
        budget(SceneBudgetAction.Error, {
          maxSceneTriangles: 7,
          maxMeshTriangles: 2,
          maxMeshCount: 4,
        }),
      );
      try {
        const bytes = await passing.exportBuffer();
        assert.ok(bytes.length > 0);
        if (format === Format.Glb)
          assert.equal(
            (await validator.validateBytes(bytes)).issues.numErrors,
            0,
          );
        assert.deepEqual((await passing.analyze()).budgetViolations, []);
      } finally {
        await passing.dispose();
      }
      for (const [field, metric, actual, limit] of [
        ["maxSceneTriangles", SceneBudgetMetric.SceneTriangles, 7, 6],
        ["maxMeshTriangles", SceneBudgetMetric.MeshTriangles, 2, 1],
        ["maxMeshCount", SceneBudgetMetric.MeshCount, 4, 3],
      ] as const) {
        const session = await authored(
          format,
          budget(SceneBudgetAction.Error, { [field]: limit }),
        );
        const directory = await mkdtemp(join(tmpdir(), "react-forge-budget-"));
        const output = join(directory, `scene.${format}`);
        await writeFile(output, "SENTINEL");
        try {
          const report = await session.analyze();
          assert.ok(report.budgetViolations.some((v) => v.metric === metric));
          await assert.rejects(
            session.exportFile(output, { overwrite: true }),
            (error: unknown) => {
              assert.ok(error instanceof ForgeError);
              assert.equal(error.code, ErrorCode.ResourceLimit);
              assert.equal(error.context.revision, report.revision);
              assert.equal(error.context.published, false);
              assert.deepEqual(error.context.sceneBudget, {
                metric,
                actual,
                limit,
                ...(metric === SceneBudgetMetric.MeshTriangles && {
                  nodeId: report.budgetViolations[0]!.nodeId,
                }),
              });
              assert.doesNotMatch(
                JSON.stringify(error.toJSON()),
                /PRIVATE_BUDGET|positions|indices|SENTINEL/,
              );
              return true;
            },
          );
          assert.equal(await readFile(output, "utf8"), "SENTINEL");
          assert.deepEqual(await readdir(directory), [`scene.${format}`]);
        } finally {
          await session.dispose();
          await rm(directory, { recursive: true, force: true });
        }
      }
    },
  );

  sceneTest(
    `${format}: warn keeps bytes and listener failures isolated`,
    async () => {
      const reference = await authored(format);
      const session = await authored(
        format,
        budget(SceneBudgetAction.Warn, { maxSceneTriangles: 6 }),
      );
      const warnings: SceneBudgetWarning[] = [];
      const unsubscribe = session.onSceneWarning((warning) => {
        warnings.push(warning);
        throw Error("observer");
      });
      const directory = await mkdtemp(join(tmpdir(), "react-forge-budget-"));
      try {
        const warnedBytes = await session.exportBuffer();
        assert.equal(
          warnedBytes.length,
          (await reference.exportBuffer()).length,
        );
        assert.deepEqual(await session.exportBuffer(), warnedBytes);
        assert.equal(warnings.length, 2);
        assert.deepEqual(warnings[0], {
          code: "scene_budget_exceeded",
          format,
          revision: session.revision,
          metric: SceneBudgetMetric.SceneTriangles,
          actual: 7,
          limit: 6,
        });
        assert.ok(Object.isFrozen(warnings[0]));
        const result = await session.exportFile(
          join(directory, `scene.${format}`),
        );
        assert.equal(result.published, true);
        assert.deepEqual(result.warnings, [warnings[0]]);
        assert.deepEqual(
          await readFile(join(directory, `scene.${format}`)),
          warnedBytes,
        );
        assert.equal(warnings.length, 3);
        unsubscribe();
        await session.exportBuffer();
        assert.equal(warnings.length, 3);
        const abort = new AbortController();
        abort.abort();
        await assert.rejects(
          session.exportFile(join(directory, `cancelled.${format}`), {
            signal: abort.signal,
          }),
          (error: unknown) =>
            error instanceof ForgeError && error.code === ErrorCode.Cancelled,
        );
        assert.deepEqual(await readdir(directory), [`scene.${format}`]);
      } finally {
        await session.dispose();
        await reference.dispose();
        await rm(directory, { recursive: true, force: true });
      }
    },
  );
}

sceneTest(
  "budgets validate strictly and zero allows a camera-only scene",
  async () => {
    for (const value of [
      -1,
      0.5,
      Number.NaN,
      Infinity,
      Number.MAX_SAFE_INTEGER + 1,
      "1",
    ]) {
      assert.throws(
        () =>
          createSession(Format.Glb, {
            geometryBudget: budget(SceneBudgetAction.Warn, {
              maxMeshCount: value as number,
            }),
          }),
        (error: unknown) =>
          error instanceof ForgeError &&
          error.code === ErrorCode.MalformedInput,
      );
    }
    for (const value of [
      { action: SceneBudgetAction.Warn },
      { action: "ignore", maxMeshCount: 1 },
      { action: SceneBudgetAction.Warn, maxMeshCount: 1, hidden: true },
      null,
    ]) {
      assert.throws(
        () =>
          createSession(Format.Glb, {
            geometryBudget: value as SceneGeometryBudget,
          }),
        (error: unknown) =>
          error instanceof ForgeError &&
          error.code === ErrorCode.MalformedInput,
      );
    }
    const session = createSession(Format.Glb, {
      geometryBudget: budget(SceneBudgetAction.Error, {
        maxSceneTriangles: 0,
        maxMeshTriangles: 0,
        maxMeshCount: 0,
      }),
    });
    try {
      await session.render(
        <Scene>
          <PerspectiveCamera yfov={0.7} />
        </Scene>,
      );
      assert.equal((await session.analyze()).budgetViolations.length, 0);
      assert.ok((await session.exportBuffer()).length > 0);
    } finally {
      await session.dispose();
    }
    const mutable = { action: SceneBudgetAction.Error, maxMeshCount: 0 };
    const copied = createSession(Format.Glb, { geometryBudget: mutable });
    try {
      mutable.maxMeshCount = 10;
      const q = await copied.registerGeometry(quad());
      await copied.render(
        <Scene>
          <Mesh geometry={q} />
        </Scene>,
      );
      await assert.rejects(
        copied.exportBuffer(),
        (error: unknown) =>
          error instanceof ForgeError && error.context.sceneBudget?.limit === 0,
      );
    } finally {
      await copied.dispose();
    }
  },
);

sceneTest(
  "queued export keeps A's revision, violation order and explicit recovery",
  async () => {
    const session = createSession(Format.Glb, {
      geometryBudget: budget(SceneBudgetAction.Warn, {
        maxSceneTriangles: 0,
        maxMeshTriangles: 0,
        maxMeshCount: 0,
      }),
    });
    const directory = await mkdtemp(
      join(tmpdir(), "react-forge-budget-revision-"),
    );
    const warnings: SceneBudgetWarning[] = [];
    session.onSceneWarning((warning) => warnings.push(warning));
    try {
      const q = await session.registerGeometry(quad());
      const renderA = session.render(
        <Scene>
          <Mesh geometry={q} />
          <Mesh geometry={q} />
        </Scene>,
      );
      const exportA = session.exportFile(join(directory, "a.glb"));
      const renderB = session.render(
        <Scene>
          <PerspectiveCamera yfov={0.7} />
        </Scene>,
      );
      await renderA;
      const publishedA = await exportA;
      await renderB;
      const snapshotB = await session.snapshot();
      assert.ok(publishedA.revision < snapshotB.revision);
      assert.deepEqual(
        warnings.map((warning) => warning.metric),
        [
          SceneBudgetMetric.SceneTriangles,
          SceneBudgetMetric.MeshTriangles,
          SceneBudgetMetric.MeshTriangles,
          SceneBudgetMetric.MeshCount,
        ],
      );
      assert.ok(
        warnings.every((warning) => warning.revision === publishedA.revision),
      );
      assert.deepEqual(publishedA.warnings, warnings);
      assert.deepEqual((await session.analyze()).budgetViolations, []);
      const publishedB = await session.exportFile(join(directory, "b.glb"));
      assert.equal(publishedB.revision, snapshotB.revision);
      assert.equal("warnings" in publishedB, false);
      assert.equal(warnings.length, 4);
    } finally {
      await session.dispose();
      await rm(directory, { recursive: true, force: true });
    }
  },
);

sceneTest("CLI and real SDK MCP return structured scene warnings", async () => {
  const directory = await mkdtemp(
    join(tmpdir(), "react-forge-budget-cli-mcp-"),
  );
  const cli = fileURLToPath(new URL("../bin/react-forge.mjs", import.meta.url));
  const entry = fileURLToPath(
    new URL("../examples/scene-analysis.tsx", import.meta.url),
  );
  const data = JSON.stringify({
    geometryBudget: budget(SceneBudgetAction.Warn, { maxSceneTriangles: 6 }),
  });
  try {
    const json = await exec(process.execPath, [
      cli,
      "run",
      entry,
      "--output",
      join(directory, "cli.glb"),
      "--data",
      data,
      "--json",
    ]);
    const result = JSON.parse(json.stdout);
    assert.equal(result.published, true);
    assert.equal(result.warnings[0].actual, 7);
    const text = await exec(process.execPath, [
      cli,
      "run",
      entry,
      "--output",
      join(directory, "text.glb"),
      "--data",
      data,
    ]);
    assert.match(text.stderr, /scene_budget_exceeded/);
    assert.doesNotMatch(text.stdout, /scene_budget_exceeded/);
    await assert.rejects(
      exec(process.execPath, [
        cli,
        "run",
        entry,
        "--output",
        join(directory, "rejected.glb"),
        "--data",
        JSON.stringify({
          geometryBudget: budget(SceneBudgetAction.Error, {
            maxSceneTriangles: 6,
          }),
        }),
        "--json",
      ]),
      (error: unknown) => {
        const failure = JSON.parse((error as { stdout: string }).stdout);
        assert.equal(failure.ok, false);
        assert.equal(failure.error.code, ErrorCode.ResourceLimit);
        assert.equal(failure.error.sceneBudget.actual, 7);
        assert.equal(failure.error.published, false);
        return true;
      },
    );
    const peer = await connect(directory);
    try {
      const created = await peer.call("execute", {
        code: `import {createSession,Format,SceneBudgetAction} from '@delino/react-forge';
import {Scene,Mesh} from '@delino/react-forge/glb';
export default async () => { const s=createSession(Format.Glb,{geometryBudget:{action:SceneBudgetAction.Warn,maxMeshCount:0}});
const g=await s.registerGeometry({positions:new Float32Array([0,0,0,1,0,0,0,1,0]),normals:new Float32Array([0,0,1,0,0,1,0,0,1]),indices:new Uint32Array([0,1,2])});
await s.render(<Scene><Mesh geometry={g}/></Scene>);return s;};`,
      });
      const report = await peer.call("inspect", {
        sessionId: created.sessionId,
        view: "scene_analysis",
      });
      assert.equal(
        report.budgetViolations[0].metric,
        SceneBudgetMetric.MeshCount,
      );
      const exported = await peer.call("export", {
        sessionId: created.sessionId,
        output: "mcp.glb",
      });
      assert.equal(exported.published, true);
      assert.equal(exported.warnings[0].metric, SceneBudgetMetric.MeshCount);
      assert.equal(exported.warnings[0].revision, report.revision);
      assert.doesNotMatch(peer.stderr(), /scene_budget_exceeded/);
      await peer.call("close", { sessionId: created.sessionId });
      const rejected = await peer.call("execute", {
        code: `import {createSession,Format,SceneBudgetAction} from '@delino/react-forge';
import {Scene,Mesh} from '@delino/react-forge/glb';
export default async () => { const s=createSession(Format.Glb,{geometryBudget:{action:SceneBudgetAction.Error,maxMeshCount:0}});
const g=await s.registerGeometry({positions:new Float32Array([0,0,0,1,0,0,0,1,0]),normals:new Float32Array([0,0,1,0,0,1,0,0,1]),indices:new Uint32Array([0,1,2])});
await s.render(<Scene><Mesh geometry={g}/></Scene>);return s;};`,
      });
      const refusal = await peer.raw("export", {
        sessionId: rejected.sessionId,
        output: "rejected-mcp.glb",
      });
      assert.equal(refusal.isError, true);
      const error = (refusal.structuredContent as any).error;
      assert.equal(error.code, ErrorCode.ResourceLimit);
      assert.equal(error.sceneBudget.metric, SceneBudgetMetric.MeshCount);
      assert.equal(error.published, false);
      await peer.call("close", { sessionId: rejected.sessionId });
    } finally {
      await peer.close();
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
