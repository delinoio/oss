import { createRequire } from "node:module";
import { test } from "node:test";
import assert from "node:assert/strict";
import React, { createRef } from "react";
import * as THREE from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import {
  createSession,
  ErrorCode,
  Format,
  type NodeHandle,
} from "../src/index.js";
import {
  AnimationClip,
  AnimationPath,
  AnimationTrack,
  Joint,
  Mesh,
  Scene,
  type GeometryHandle,
  type GeometryInput,
} from "../src/glb.js";

const sceneTest =
  process.env.REACT_FORGE_SKIP_SCENE_TESTS === "1" ? test.skip : test;
const validator = createRequire(import.meta.url)("gltf-validator") as {
  validateBytes(
    bytes: Uint8Array,
  ): Promise<{ issues: { numErrors: number; messages: unknown[] } }>;
};
const sixVertices = (): GeometryInput => ({
  positions: new Float32Array([
    0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 5, 5, 5, 6, 6, 6,
  ]),
  normals: new Float32Array([
    0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1,
  ]),
  indices: new Uint32Array([0, 1, 2, 3, 1, 2]),
});
const near = (actual: readonly number[], expected: readonly number[]) => {
  for (let i = 0; i < expected.length; i++)
    assert.ok(Math.abs(actual[i]! - expected[i]!) < 1e-5);
};
async function glbTriangleCorners(bytes: Buffer) {
  const gltf = await new GLTFLoader().parseAsync(
    bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
    "",
  );
  const mesh = gltf.scene.getObjectByProperty(
    "isSkinnedMesh",
    true,
  ) as THREE.SkinnedMesh;
  assert.ok(mesh);
  const indices = mesh.geometry.getIndex();
  assert.ok(indices);
  assert.equal(indices.count, 6);
  const mixer = new THREE.AnimationMixer(gltf.scene);
  const action = mixer.clipAction(gltf.animations[0]!);
  action.setLoop(THREE.LoopOnce, 1);
  action.clampWhenFinished = true;
  action.play();
  const samples = [];
  for (const time of [0, 0.5, 1]) {
    mixer.setTime(time);
    gltf.scene.updateMatrixWorld(true);
    mesh.skeleton.update();
    const corners = [];
    for (let i = 0; i < indices.count; i++) {
      const vertex = new THREE.Vector3();
      mesh.getVertexPosition(indices.getX(i), vertex);
      corners.push(vertex.applyMatrix4(mesh.matrixWorld).toArray());
    }
    samples.push(corners);
  }
  return samples;
}

for (const format of [Format.Glb, Format.Fbx] as const) {
  sceneTest(
    `${format}: explicit compaction preserves faces and source`,
    async () => {
      const session = createSession(format);
      const mesh = createRef<NodeHandle>();
      try {
        const source = await session.registerGeometry(sixVertices());
        await session.render(
          <Scene>
            <Mesh ref={mesh} geometry={source} />
          </Scene>,
        );
        let snapshot = await session.snapshot();
        near(
          (
            await session.measure(mesh.current!, {
              revision: snapshot.revision,
            })
          ).max,
          [6, 6, 6],
        );
        const originalExport = await session.exportBuffer();

        const first = await session.compactGeometry(source);
        assert.deepEqual(
          [first.inputVertices, first.outputVertices, first.triangles],
          [6, 3, 2],
        );
        assert.ok(first.outputBytes < first.inputBytes);
        assert.notEqual(first.geometry.assetId, source.assetId);
        assert.deepEqual(await session.exportBuffer(), originalExport);

        await session.render(
          <Scene>
            <Mesh ref={mesh} geometry={first.geometry} />
          </Scene>,
        );
        snapshot = await session.snapshot();
        near(
          (
            await session.measure(mesh.current!, {
              revision: snapshot.revision,
            })
          ).max,
          [1, 1, 0],
        );
        const compactedExport = await session.exportBuffer();
        if (format === Format.Glb) {
          const checked = await validator.validateBytes(compactedExport);
          assert.equal(
            checked.issues.numErrors,
            0,
            JSON.stringify(checked.issues.messages),
          );
          const model = JSON.parse(
            compactedExport
              .subarray(20, 20 + compactedExport.readUInt32LE(12))
              .toString(),
          );
          assert.equal(model.accessors[0].count, 3);
          assert.equal(model.meshes[0].primitives[0].mode, 4);
        } else {
          assert.ok(
            compactedExport
              .subarray(0, 20)
              .toString()
              .includes("Kaydara FBX Binary"),
          );
        }
        const second = await session.compactGeometry(first.geometry);
        assert.equal(second.geometry.assetId, first.geometry.assetId);
        assert.deepEqual(
          [second.inputVertices, second.outputVertices, second.triangles],
          [3, 3, 2],
        );
        assert.equal(second.outputBytes, first.outputBytes);
      } finally {
        await session.dispose();
      }
    },
  );

  sceneTest(
    `${format}: skin and morph deformation remain identical`,
    async () => {
      const session = createSession(format);
      const joint = createRef<NodeHandle>();
      const mesh = createRef<NodeHandle>();
      const clip = createRef<NodeHandle>();
      try {
        const input = sixVertices();
        input.skin = {
          joints: new Uint16Array(24),
          weights: new Float32Array([
            1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0,
            0,
          ]),
        };
        input.morphTargets = [
          {
            name: "rise",
            positions: new Float32Array([
              0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0,
            ]),
            normals: new Float32Array(18),
          },
        ];
        const source = await session.registerGeometry(input);
        const compact = await session.compactGeometry(source);
        assert.equal(compact.outputVertices, 3);
        const move = await session.registerAnimationSampler({
          path: AnimationPath.Translation,
          times: new Float32Array([0, 1]),
          values: new Float32Array([0, 0, 0, 0, 2, 0]),
        });
        const morph = await session.registerAnimationSampler({
          path: AnimationPath.Weights,
          times: new Float32Array([0, 1]),
          values: new Float32Array([0, 1]),
        });
        const render = (geometry: GeometryHandle) =>
          session.render(
            <Scene>
              <Joint ref={joint} />
              <Mesh ref={mesh} geometry={geometry} skin={{ joints: [joint] }} />
              <AnimationClip ref={clip} name="deform">
                <AnimationTrack target={joint} sampler={move} />
                <AnimationTrack target={mesh} sampler={morph} />
              </AnimationClip>
            </Scene>,
          );
        await render(source);
        let snapshot = await session.snapshot();
        const before = [];
        for (const time of [0, 0.5, 1]) {
          const bounds = await session.measure(mesh.current!, {
            revision: snapshot.revision,
            animation: { clip: clip.current!, time },
          });
          before.push(bounds);
        }
        const originalExport = await session.exportBuffer();
        await render(compact.geometry);
        snapshot = await session.snapshot();
        for (const [i, time] of [0, 0.5, 1].entries()) {
          const bounds = await session.measure(mesh.current!, {
            revision: snapshot.revision,
            animation: { clip: clip.current!, time },
          });
          near(bounds.min, before[i]!.min);
          assert.ok(bounds.max[0] < before[i]!.max[0]);
        }
        const compactedExport = await session.exportBuffer();
        assert.notDeepEqual(compactedExport, originalExport);
        if (format === Format.Glb) {
          for (const bytes of [originalExport, compactedExport]) {
            const checked = await validator.validateBytes(bytes);
            assert.equal(
              checked.issues.numErrors,
              0,
              JSON.stringify(checked.issues.messages),
            );
          }
          const originalCorners = await glbTriangleCorners(originalExport);
          const compactedCorners = await glbTriangleCorners(compactedExport);
          for (let sample = 0; sample < originalCorners.length; sample++)
            for (let corner = 0; corner < 6; corner++)
              near(
                compactedCorners[sample]![corner]!,
                originalCorners[sample]![corner]!,
              );
        }
      } finally {
        await session.dispose();
      }
    },
  );
}

sceneTest(
  "compaction rejects foreign, missing and cancelled targets without poisoning the source",
  async () => {
    const session = createSession(Format.Glb);
    const other = createSession(Format.Glb);
    try {
      const source = await session.registerGeometry(sixVertices());
      const foreign = await other.registerGeometry(sixVertices());
      assert.throws(() => session.compactGeometry(foreign), {
        code: ErrorCode.InvalidTarget,
      });
      assert.throws(
        () =>
          session.compactGeometry({ ...source, assetId: "geometry:missing" }),
        { code: ErrorCode.InvalidTarget },
      );
      const already = new AbortController();
      already.abort();
      assert.throws(
        () => session.compactGeometry(source, { signal: already.signal }),
        { code: ErrorCode.Cancelled },
      );
      const during = new AbortController();
      const pending = session.compactGeometry(source, {
        signal: during.signal,
      });
      during.abort();
      await assert.rejects(pending, { code: ErrorCode.Cancelled });
      const variant = sixVertices();
      variant.positions[12] = 7;
      const [derived, registered] = await Promise.all([
        session.compactGeometry(source),
        session.registerGeometry(variant),
      ]);
      assert.equal(derived.outputVertices, 3);
      assert.notEqual(registered.assetId, source.assetId);
      await session.render(
        <Scene>
          <Mesh geometry={source} />
        </Scene>,
      );
      assert.ok((await session.exportBuffer()).length > 0);
    } finally {
      await Promise.all([session.dispose(), other.dispose()]);
    }
    assert.throws(() => session.compactGeometry({} as GeometryHandle), {
      code: ErrorCode.Disposed,
    });
  },
);
