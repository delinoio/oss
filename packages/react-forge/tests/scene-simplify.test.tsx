import { test } from "node:test";
import assert from "node:assert/strict";
import React from "react";
import { createRequire } from "node:module";
import {
  createSession,
  ErrorCode,
  Format,
  type ForgeError,
} from "../src/index.js";
import { Scene, Mesh, type GeometryInput } from "../src/glb.js";

const sceneTest =
  process.env.REACT_FORGE_SKIP_SCENE_TESTS === "1" ? test.skip : test;
const validator = createRequire(import.meta.url)("gltf-validator") as {
  validateBytes(bytes: Uint8Array): Promise<{ issues: { numErrors: number } }>;
};
function grid(): GeometryInput {
  const positions = new Float32Array(27);
  const normals = new Float32Array(27);
  for (let y = 0; y < 3; y++)
    for (let x = 0; x < 3; x++) {
      const i = y * 3 + x;
      positions.set([x / 2, y / 2, 0], i * 3);
      normals[i * 3 + 2] = 1;
    }
  const indices: number[] = [];
  for (let y = 0; y < 2; y++)
    for (let x = 0; x < 2; x++) {
      const a = y * 3 + x,
        b = a + 1,
        c = a + 3,
        d = c + 1;
      indices.push(a, b, d, a, d, c);
    }
  return { positions, normals, indices: new Uint32Array(indices) };
}

for (const format of [Format.Glb, Format.Fbx] as const) {
  sceneTest(
    `${format}: static simplification is explicit, conservative and deterministic`,
    async () => {
      const session = createSession(format);
      try {
        const source = await session.registerGeometry(grid());
        const six = await session.simplifyGeometry(source, {
          targetTriangles: 6,
          maxError: 0,
        });
        assert.deepEqual(
          [
            six.inputTriangles,
            six.outputTriangles,
            six.inputVertices,
            six.outputVertices,
          ],
          [8, 6, 9, 8],
        );
        assert.equal(six.targetReached, true);
        assert.equal(six.estimatedError, 0);
        const repeat = await session.simplifyGeometry(source, {
          targetTriangles: 6,
          maxError: 0,
        });
        assert.equal(repeat.geometry.assetId, six.geometry.assetId);
        const four = await session.simplifyGeometry(source, {
          targetTriangles: 4,
          maxError: 0,
        });
        assert.equal(four.outputTriangles, 6);
        assert.equal(four.targetReached, false);
        const noOp = await session.simplifyGeometry(source, {
          targetTriangles: 8,
          maxError: 0,
        });
        assert.equal(noOp.geometry.assetId, source.assetId);
        await session.render(
          <Scene>
            <Mesh geometry={source} />
          </Scene>,
        );
        const original = await session.export();
        await session.render(
          <Scene>
            <Mesh geometry={six.geometry} />
          </Scene>,
        );
        const derived = await session.export();
        assert.notDeepEqual(derived, original);
        if (format === Format.Glb) {
          assert.equal(
            (await validator.validateBytes(original)).issues.numErrors,
            0,
          );
          assert.equal(
            (await validator.validateBytes(derived)).issues.numErrors,
            0,
          );
        } else {
          assert.equal(
            original.subarray(0, 20).toString(),
            "Kaydara FBX Binary  ",
          );
          assert.equal(
            derived.subarray(0, 20).toString(),
            "Kaydara FBX Binary  ",
          );
        }
        await session.render(
          <Scene>
            <Mesh geometry={source} />
          </Scene>,
        );
        assert.deepEqual(await session.export(), original);
      } finally {
        await session.dispose();
      }
    },
  );

  sceneTest(
    `${format}: malformed, foreign, deformation and cancellation do not publish`,
    async () => {
      const session = createSession(format),
        other = createSession(format);
      try {
        const source = await session.registerGeometry(grid());
        for (const options of [
          { targetTriangles: 0, maxError: 0 },
          { targetTriangles: 8.5, maxError: 0 },
          { targetTriangles: 1, maxError: -1 },
          { targetTriangles: 1, maxError: Number.POSITIVE_INFINITY },
          { targetTriangles: 1, maxError: 0, extra: 1 },
        ]) {
          assert.throws(
            () => session.simplifyGeometry(source, options),
            (error: ForgeError) => error.code === ErrorCode.MalformedInput,
          );
        }
        assert.throws(
          () =>
            other.simplifyGeometry(source, { targetTriangles: 6, maxError: 0 }),
          (error: ForgeError) => error.code === ErrorCode.InvalidTarget,
        );
        const aborted = new AbortController();
        aborted.abort();
        assert.throws(
          () =>
            session.simplifyGeometry(
              source,
              { targetTriangles: 6, maxError: 0 },
              { signal: aborted.signal },
            ),
          (error: ForgeError) => error.code === ErrorCode.Cancelled,
        );
        const withMorph = grid();
        withMorph.morphTargets = [
          { name: "smile", positions: new Float32Array(27) },
        ];
        const morph = await session.registerGeometry(withMorph);
        await assert.rejects(
          session.simplifyGeometry(morph, { targetTriangles: 6, maxError: 1 }),
          (error: ForgeError) => error.code === ErrorCode.UnsupportedEdit,
        );
        const withSkin = grid();
        const joints = new Uint16Array(9 * 4);
        const weights = new Float32Array(9 * 4);
        for (let i = 0; i < 9; i++) weights[i * 4] = 1;
        withSkin.skin = { joints, weights };
        const skin = await session.registerGeometry(withSkin);
        await assert.rejects(
          session.simplifyGeometry(skin, { targetTriangles: 6, maxError: 1 }),
          (error: ForgeError) => error.code === ErrorCode.UnsupportedEdit,
        );
        await session.render(
          <Scene>
            <Mesh geometry={source} />
          </Scene>,
        );
        assert.ok((await session.export()).length > 0);
      } finally {
        await Promise.all([session.dispose(), other.dispose()]);
      }
    },
  );
}

sceneTest(
  "mid-operation cancellation leaves source usable and releases admission",
  async () => {
    const session = createSession(Format.Glb);
    const side = 72;
    const positions = new Float32Array(side * side * 3);
    const normals = new Float32Array(side * side * 3);
    const indices: number[] = [];
    for (let y = 0; y < side; y++)
      for (let x = 0; x < side; x++) {
        const i = y * side + x;
        positions.set([x / (side - 1), y / (side - 1), 0], i * 3);
        normals[i * 3 + 2] = 1;
        if (x + 1 < side && y + 1 < side) {
          const b = i + 1,
            c = i + side,
            d = c + 1;
          indices.push(i, b, d, i, d, c);
        }
      }
    try {
      const source = await session.registerGeometry({
        positions,
        normals,
        indices: new Uint32Array(indices),
      });
      const controller = new AbortController();
      const pending = session.simplifyGeometry(
        source,
        { targetTriangles: 1, maxError: 1000 },
        { signal: controller.signal },
      );
      setTimeout(() => controller.abort(), 25);
      await assert.rejects(
        pending,
        (error: ForgeError) => error.code === ErrorCode.Cancelled,
      );
      const next = await session.registerGeometry(grid());
      await session.render(
        <Scene>
          <Mesh geometry={next} />
        </Scene>,
      );
      assert.ok((await session.export()).length > 0);
    } finally {
      await session.dispose();
    }
  },
);
