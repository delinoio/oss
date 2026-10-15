import { test } from "node:test";
import assert from "node:assert/strict";
import React, { createRef } from "react";
import { createRequire } from "node:module";
import {
  createSession,
  ErrorCode,
  ForgeError,
  Format,
  limits,
  type NodeHandle,
} from "../src/index.js";
import { Mesh, Scene, type GeometryInput } from "../src/glb.js";

const sceneTest =
  process.env.REACT_FORGE_SKIP_SCENE_TESTS === "1" ? test.skip : test;
const validator = createRequire(import.meta.url)("gltf-validator") as {
  validateBytes(bytes: Uint8Array): Promise<{ issues: { numErrors: number } }>;
};
const sample = (u: number, v: number) => ({
  position: [u, v, 0] as const,
  normal: [0, 0, 1] as const,
  uv: [u, 1 - v] as const,
  tangent: [1, 0, 0, -1] as const,
});
const grid = (uSegments: number, vSegments: number): GeometryInput => {
  const vertices = (uSegments + 1) * (vSegments + 1);
  const positions = new Float32Array(vertices * 3);
  const normals = new Float32Array(vertices * 3);
  const uv = new Float32Array(vertices * 2);
  const tangents = new Float32Array(vertices * 4);
  const indices = new Uint32Array(6 * uSegments * vSegments);
  let vertex = 0;
  for (let j = 0; j <= vSegments; j++)
    for (let i = 0; i <= uSegments; i++, vertex++) {
      const result = sample(i / uSegments, j / vSegments);
      positions.set(result.position, vertex * 3);
      normals.set(result.normal, vertex * 3);
      uv.set(result.uv, vertex * 2);
      tangents.set(result.tangent, vertex * 4);
    }
  let offset = 0;
  for (let j = 0; j < vSegments; j++)
    for (let i = 0; i < uSegments; i++) {
      const a = j * (uSegments + 1) + i;
      indices.set(
        [a, a + 1, a + uSegments + 2, a, a + uSegments + 2, a + uSegments + 1],
        offset,
      );
      offset += 6;
    }
  return { positions, normals, uv, tangents, indices };
};

for (const format of [Format.Glb, Format.Fbx] as const) {
  sceneTest(
    `${format}: parametric grid has exact samples and immutable manual equivalent`,
    async () => {
      const session = createSession(format);
      const calls: [number, number][] = [];
      const mesh = createRef<NodeHandle>();
      try {
        const baked = await session.bakeParametricGeometry({
          uSegments: 2,
          vSegments: 3,
          maxTriangles: 12,
          sample: (u, v) => {
            calls.push([u, v]);
            return sample(u, v);
          },
        });
        assert.equal(calls.length, 12);
        assert.deepEqual(calls, [
          [0, 0],
          [0.5, 0],
          [1, 0],
          [0, 1 / 3],
          [0.5, 1 / 3],
          [1, 1 / 3],
          [0, 2 / 3],
          [0.5, 2 / 3],
          [1, 2 / 3],
          [0, 1],
          [0.5, 1],
          [1, 1],
        ]);
        const manual = await session.registerGeometry(grid(2, 3));
        assert.equal(baked.assetId, manual.assetId);
        await session.render(
          <Scene>
            <Mesh
              ref={mesh}
              geometry={baked}
              material={{ doubleSided: true }}
            />
          </Scene>,
        );
        const snapshot = await session.snapshot();
        assert.ok(mesh.current);
        const bounds = await session.measure(mesh.current, {
          revision: snapshot.revision,
        });
        assert.deepEqual(bounds.min, [0, 0, 0]);
        assert.deepEqual(bounds.max, [1, 1, 0]);
        const bytes = await session.export();
        if (format === Format.Glb) {
          const document = JSON.parse(
            bytes.subarray(20, 20 + bytes.readUInt32LE(12)).toString(),
          );
          const accessor =
            document.accessors[document.meshes[0].primitives[0].indices];
          const view = document.bufferViews[accessor.bufferView];
          const start = 20 + bytes.readUInt32LE(12) + 8 + view.byteOffset;
          const decoded = Array.from({ length: accessor.count }, (_, i) =>
            bytes.readUInt32LE(start + 4 * i),
          );
          assert.equal(accessor.count, 36);
          assert.deepEqual(
            decoded.slice(0, 12),
            [0, 1, 4, 0, 4, 3, 1, 2, 5, 1, 5, 4],
          );
          assert.deepEqual(decoded, Array.from(grid(2, 3).indices));
          assert.equal(
            (await validator.validateBytes(bytes)).issues.numErrors,
            0,
          );
        } else {
          assert.equal(bytes.subarray(0, 18).toString(), "Kaydara FBX Binary");
        }
      } finally {
        await session.dispose();
      }
    },
  );

  sceneTest(
    `${format}: parametric preflight and failures release admission`,
    async () => {
      const session = createSession(format);
      let calls = 0;
      const input = {
        uSegments: 2,
        vSegments: 3,
        maxTriangles: 12,
        sample: (u: number, v: number) => {
          calls++;
          return sample(u, v);
        },
      };
      try {
        assert.throws(
          () => session.bakeParametricGeometry({ ...input, maxTriangles: 11 }),
          { code: ErrorCode.ResourceLimit },
        );
        assert.throws(
          () =>
            session.bakeParametricGeometry({
              ...input,
              uSegments: 2000,
              vSegments: 2000,
              maxTriangles: 8_000_000,
            }),
          { code: ErrorCode.ResourceLimit },
        );
        for (const value of [
          0,
          -1,
          1.5,
          Number.MAX_SAFE_INTEGER + 1,
          NaN,
          Infinity,
        ])
          assert.throws(
            () =>
              session.bakeParametricGeometry({ ...input, uSegments: value }),
            { code: ErrorCode.MalformedInput },
          );
        assert.throws(
          () =>
            session.bakeParametricGeometry({
              ...input,
              uSegments: Number.MAX_SAFE_INTEGER,
              vSegments: Number.MAX_SAFE_INTEGER,
              maxTriangles: Number.MAX_SAFE_INTEGER,
            }),
          { code: ErrorCode.ResourceLimit },
        );
        assert.throws(
          () =>
            session.bakeParametricGeometry({ ...input, extra: true } as never),
          { code: ErrorCode.MalformedInput },
        );
        assert.throws(
          () =>
            session.bakeParametricGeometry({ ...input, sample: null } as never),
          { code: ErrorCode.MalformedInput },
        );
        assert.throws(
          () => session.bakeParametricGeometry(input, { signal: 1 } as never),
          { code: ErrorCode.MalformedInput },
        );
        assert.throws(
          () => session.bakeParametricGeometry(input, { extra: true } as never),
          { code: ErrorCode.MalformedInput },
        );
        assert.equal(calls, 0);
        for (const bad of [
          () => ({ ...sample(0, 0), position: [NaN, 0, 0] }),
          () => ({ ...sample(0, 0), position: new Array(3) }),
          () => ({ ...sample(0, 0), uv: [0] }),
          () => Promise.resolve(sample(0, 0)),
          () => {
            throw Error("PRIVATE_SAMPLE_DATA");
          },
          () => {
            throw new ForgeError(
              ErrorCode.MalformedInput,
              "PRIVATE_SAMPLE_DATA",
            );
          },
        ]) {
          await assert.rejects(
            session.bakeParametricGeometry({ ...input, sample: bad as never }),
            (error: unknown) => {
              assert.ok(
                [ErrorCode.MalformedInput, ErrorCode.Render].includes(
                  (error as { code: ErrorCode }).code,
                ),
              );
              assert.doesNotMatch(JSON.stringify(error), /PRIVATE_SAMPLE_DATA/);
              return true;
            },
          );
        }
        await assert.rejects(
          session.bakeParametricGeometry({
            ...input,
            sample: () => ({ ...sample(0, 0), normal: [0, 0, 2] }),
          }),
          { code: ErrorCode.MalformedInput },
        );
        await assert.rejects(
          session.bakeParametricGeometry({
            ...input,
            sample: () => ({ ...sample(0, 0), tangent: [0, 0, 1, -1] }),
          }),
          { code: ErrorCode.MalformedInput },
        );
        await session.bakeParametricGeometry(input);
      } finally {
        await session.dispose();
      }
    },
  );

  sceneTest(
    `${format}: parametric cancellation and disposal stop callbacks`,
    async () => {
      const session = createSession(format);
      const controller = new AbortController();
      let calls = 0;
      try {
        controller.abort();
        assert.throws(
          () =>
            session.bakeParametricGeometry(
              { uSegments: 20, vSegments: 20, maxTriangles: 800, sample },
              { signal: controller.signal },
            ),
          { code: ErrorCode.Cancelled },
        );
        const later = new AbortController();
        const pending = session.bakeParametricGeometry(
          {
            uSegments: 20,
            vSegments: 20,
            maxTriangles: 800,
            sample: (u, v) => {
              if (++calls === 130) later.abort();
              return sample(u, v);
            },
          },
          { signal: later.signal },
        );
        await assert.rejects(pending, { code: ErrorCode.Cancelled });
        assert.equal(calls, 130);
        await session.bakeParametricGeometry({
          uSegments: 1,
          vSegments: 1,
          maxTriangles: 2,
          sample,
        });
        const work = session.bakeParametricGeometry({
          uSegments: 20,
          vSegments: 20,
          maxTriangles: 800,
          sample,
        });
        const rejection = assert.rejects(work, { code: ErrorCode.Cancelled });
        await session.dispose();
        await rejection;
      } finally {
        await session.dispose();
      }
    },
  );
}

sceneTest(
  "parametric reservation respects aggregate admission before any sample",
  async () => {
    const session = createSession(Format.Glb);
    try {
      const state = session as unknown as { bytes: number };
      state.bytes = limits.sceneBytes - 200;
      let calls = 0;
      assert.throws(
        () =>
          session.bakeParametricGeometry({
            uSegments: 2,
            vSegments: 3,
            maxTriangles: 12,
            sample: (u, v) => {
              calls++;
              return sample(u, v);
            },
          }),
        { code: ErrorCode.ResourceLimit },
      );
      assert.equal(calls, 0);
      state.bytes = 0;
      await session.bakeParametricGeometry({
        uSegments: 2,
        vSegments: 3,
        maxTriangles: 12,
        sample,
      });
    } finally {
      await session.dispose();
    }
  },
);

sceneTest(
  "parametric byte boundary is checked before the first callback",
  async () => {
    const session = createSession(Format.Glb);
    const vSegments = Math.floor((limits.geometryBytes - 112) / 120);
    const controller = new AbortController();
    let calls = 0;
    try {
      assert.throws(
        () =>
          session.bakeParametricGeometry({
            uSegments: 1,
            vSegments: vSegments + 1,
            maxTriangles: 2 * (vSegments + 1),
            sample: () => {
              calls++;
              return sample(0, 0);
            },
          }),
        { code: ErrorCode.ResourceLimit },
      );
      assert.equal(calls, 0);
      await assert.rejects(
        session.bakeParametricGeometry(
          {
            uSegments: 1,
            vSegments,
            maxTriangles: 2 * vSegments,
            sample: (u, v) => {
              calls++;
              controller.abort();
              return sample(u, v);
            },
          },
          { signal: controller.signal },
        ),
        { code: ErrorCode.Cancelled },
      );
      assert.equal(calls, 1);
      await session.bakeParametricGeometry({
        uSegments: 1,
        vSegments: 1,
        maxTriangles: 2,
        sample,
      });
    } finally {
      await session.dispose();
    }
  },
);

sceneTest(
  "parametric baking keeps aggregate admission and export pending",
  async () => {
    const session = createSession(Format.Glb);
    try {
      const baseline = await session.registerGeometry(grid(1, 1));
      await session.render(
        <Scene>
          <Mesh geometry={baseline} />
        </Scene>,
      );
      const state = session as unknown as { bytes: number };
      const actualBytes = state.bytes;
      const uSegments = 20;
      const vSegments = 60;
      const length =
        16 +
        48 * ((uSegments + 1) * (vSegments + 1)) +
        12 * (2 * uSegments * vSegments);
      state.bytes = limits.sceneBytes - length - 1;
      let calls = 0;
      const pending = session.bakeParametricGeometry({
        uSegments,
        vSegments,
        maxTriangles: 2400,
        sample: (u, v) => {
          calls++;
          return sample(u, v);
        },
      });
      assert.throws(() => session.registerGeometry(grid(1, 1)), {
        code: ErrorCode.ResourceLimit,
      });
      state.bytes = actualBytes;
      const exported = session.export().then((bytes) => {
        assert.equal(calls, (uSegments + 1) * (vSegments + 1));
        return bytes;
      });
      const baked = await pending;
      const bytes = await exported;
      assert.equal((await validator.validateBytes(bytes)).issues.numErrors, 0);
      await session.render(
        <Scene>
          <Mesh geometry={baked} material={{ doubleSided: true }} />
        </Scene>,
      );
      const surface = await session.export();
      const document = JSON.parse(
        surface.subarray(20, 20 + surface.readUInt32LE(12)).toString(),
      );
      const accessor =
        document.accessors[document.meshes[0].primitives[0].indices];
      assert.equal(accessor.count, 7200);
      assert.equal(
        (await validator.validateBytes(surface)).issues.numErrors,
        0,
      );
    } finally {
      await session.dispose();
    }
  },
);
