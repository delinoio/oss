import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { test } from "node:test";
import React, { Suspense, use } from "react";
import {
  createSession,
  ErrorCode,
  Format,
  Stage,
  SceneGeometryFindingCode,
} from "../src/index.js";
import {
  Scene,
  Group,
  Mesh,
  PerspectiveCamera,
  PointLight,
  type GeometryInput,
} from "../src/glb.js";

const sceneTest =
  process.env.REACT_FORGE_SKIP_SCENE_TESTS === "1" ? test.skip : test;
const validator = createRequire(import.meta.url)("gltf-validator") as {
  validateBytes(bytes: Uint8Array): Promise<{ issues: { numErrors: number } }>;
};
const quad = (): GeometryInput => ({
  positions: new Float32Array([0, 0, 0, 1, 0, 0, 1, 1, 0, 0, 1, 0]),
  normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1]),
  indices: new Uint32Array([0, 1, 2, 0, 2, 3]),
});
const triangle = (): GeometryInput => ({
  positions: new Float32Array([0, 0, 0, 1, 0, 0, 0, 1, 0]),
  normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1]),
  indices: new Uint32Array([0, 1, 2]),
});

for (const format of [Format.Glb, Format.Fbx] as const) {
  sceneTest(
    `${format}: a base-pose zero-area triangle can open under a morph`,
    async () => {
      const session = createSession(format);
      try {
        const geometry = await session.registerGeometry({
          positions: new Float32Array([0, 0, 0, 1, 0, 0, 2, 0, 0]),
          normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1]),
          indices: new Uint32Array([0, 1, 2]),
          morphTargets: [
            {
              name: "open",
              positions: new Float32Array([0, 0, 0, 0, 1, 0, 0, 0, 0]),
            },
          ],
        });
        await session.render(
          <Scene>
            <Mesh geometry={geometry} morphWeights={[1]} />
          </Scene>,
        );
        const snapshot = await session.snapshot();
        const mesh = snapshot.targets.find((target) => target.kind === "mesh");
        assert.ok(mesh);
        const measured = await session.measure(mesh, {
          revision: snapshot.revision,
        });
        assert.equal(measured.max[1], 1);
        const before = await session.exportBuffer();
        const quality = await session.analyze({ geometryQuality: true });
        assert.equal(quality.geometryQuality?.[0]?.findings[2]?.count, 1);
        assert.deepEqual(await session.exportBuffer(), before);
      } finally {
        await session.dispose();
      }
    },
  );

  sceneTest(
    `${format}: optional exact-index quality preserves the source`,
    async () => {
      const session = createSession(format);
      try {
        const source: GeometryInput = {
          positions: new Float32Array([
            0, 0, 0, 1, 0, 0, 0, 1, 0, 2, 0, 0, 9, 9, 9,
          ]),
          normals: new Float32Array([
            0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1,
          ]),
          indices: new Uint32Array([
            0, 1, 2, 1, 2, 0, 2, 1, 0, 0, 0, 1, 0, 1, 3,
          ]),
        };
        const handle = await session.registerGeometry(source);
        await session.render(
          <Scene>
            <Mesh geometry={handle} />
            <Mesh
              geometry={handle}
              scale={[-1, 1, 1]}
              material={{ doubleSided: true }}
            />
          </Scene>,
        );
        const before = await session.exportBuffer();
        const plain = await session.analyze();
        assert.equal(plain.geometryQuality, undefined);
        assert.equal(
          (await session.analyze({ geometryQuality: false })).geometryQuality,
          undefined,
        );
        assert.equal(plain.meshes[0]?.geometryId, undefined);
        const quality = await session.analyze({
          geometryQuality: true,
          revision: plain.revision,
        });
        assert.equal(quality.revision, plain.revision);
        assert.equal(quality.geometryQuality?.length, 1);
        assert.deepEqual(
          quality.meshes.map((mesh) => mesh.geometryId),
          [handle.assetId, handle.assetId],
        );
        const findings = quality.geometryQuality![0]!.findings;
        assert.deepEqual(
          findings.map((finding) => finding.code),
          Object.values(SceneGeometryFindingCode),
        );
        assert.deepEqual(
          findings.map((finding) => finding.count),
          [1, 1, 2, 1, 1],
        );
        assert.deepEqual(
          findings.map((finding) => finding.samples),
          [[4], [3], [3, 4], [1], [2]],
        );
        assert.ok(findings.every((finding) => !finding.truncated));
        assert.ok(
          findings.every((finding) => Object.isFrozen(finding.samples)),
        );
        assert.deepEqual(await session.exportBuffer(), before);
        assert.equal(session.revision, plain.revision);
        assert.throws(
          () => session.analyze({ geometryQuality: "yes" } as never),
          { code: ErrorCode.MalformedInput },
        );
        const abort = new AbortController();
        abort.abort();
        await assert.rejects(
          session.analyze({ geometryQuality: true, signal: abort.signal }),
          { code: ErrorCode.Cancelled },
        );
      } finally {
        await session.dispose();
      }
    },
  );

  sceneTest(
    `${format}: analysis counts shared and unused geometry at one revision`,
    async () => {
      const session = createSession(format);
      const events: unknown[] = [];
      session.onDiagnostic((event) => {
        events.push(event);
        throw new Error("observer failure");
      });
      try {
        const source = quad();
        const pending = session.registerGeometry(source);
        source.positions[3] = 99;
        const q = await pending;
        assert.equal(
          (await session.registerGeometry(quad())).assetId,
          q.assetId,
        );
        const t = await session.registerGeometry(triangle());
        const unused = quad();
        unused.positions[3] = 2;
        await session.registerGeometry(unused);
        await session.render(
          <Scene name="PRIVATE_SCENE_NAME">
            <Group>
              <Mesh geometry={q} name="PRIVATE_MESH_NAME" />
              <Mesh
                geometry={q}
                scale={[-1, 2, 1]}
                material={{ doubleSided: true }}
              />
            </Group>
            <Mesh geometry={q} />
            <Mesh geometry={t} />
            <PerspectiveCamera yfov={0.7} />
            <PointLight />
          </Scene>,
        );
        const before = await session.exportBuffer();
        const analysis = await session.analyze();
        assert.equal(analysis.documentId, session.documentId);
        assert.equal(analysis.revision, session.revision);
        assert.deepEqual(
          {
            meshCount: analysis.meshCount,
            referencedGeometryCount: analysis.referencedGeometryCount,
            uniqueVertexCount: analysis.uniqueVertexCount,
            uniqueTriangleCount: analysis.uniqueTriangleCount,
            instanceVertexCount: analysis.instanceVertexCount,
            instanceTriangleCount: analysis.instanceTriangleCount,
            registeredGeometryBytes: analysis.registeredGeometryBytes,
            referencedGeometryBytes: analysis.referencedGeometryBytes,
            unusedGeometryCount: analysis.unusedGeometryCount,
          },
          {
            meshCount: 4,
            referencedGeometryCount: 2,
            uniqueVertexCount: 7,
            uniqueTriangleCount: 3,
            instanceVertexCount: 15,
            instanceTriangleCount: 7,
            registeredGeometryBytes: 372,
            referencedGeometryBytes: 236,
            unusedGeometryCount: 1,
          },
        );
        assert.deepEqual(
          analysis.meshes.map((row) => row.triangleCount),
          [2, 2, 2, 1],
        );
        assert.deepEqual(
          analysis.meshes.map((row) => row.geometryInstanceCount),
          [3, 3, 3, 1],
        );
        const authoredMeshes = (await session.snapshot()).targets.filter(
          (target) => target.kind === "mesh",
        );
        assert.deepEqual(
          analysis.meshes.map((row) => row.nodeId),
          authoredMeshes.map((target) => target.nodeId),
        );
        assert.ok(Object.isFrozen(analysis));
        assert.ok(Object.isFrozen(analysis.meshes));
        assert.ok(analysis.meshes.every(Object.isFrozen));
        assert.equal((await session.analyze()).revision, analysis.revision);
        assert.deepEqual(await session.exportBuffer(), before);
        assert.ok(
          events.some(
            (event) => (event as { stage?: Stage }).stage === Stage.Layout,
          ),
        );
        assert.doesNotMatch(
          JSON.stringify(events),
          /PRIVATE_SCENE_NAME|PRIVATE_MESH_NAME|positions|indices/,
        );
        if (format === Format.Glb) {
          const parsed = JSON.parse(
            before.subarray(20, 20 + before.readUInt32LE(12)).toString(),
          );
          const triangles = parsed.nodes.flatMap((node: { mesh?: number }) =>
            node.mesh === undefined
              ? []
              : parsed.meshes[node.mesh].primitives.map(
                  (primitive: { indices: number }) =>
                    parsed.accessors[primitive.indices].count / 3,
                ),
          );
          assert.equal(
            triangles.reduce((sum: number, count: number) => sum + count, 0),
            analysis.instanceTriangleCount,
          );
          assert.equal(
            (await validator.validateBytes(before)).issues.numErrors,
            0,
          );
        }
      } finally {
        await session.dispose();
      }
    },
  );

  sceneTest(
    `${format}: empty scene, ordered revision and cancellation`,
    async () => {
      const session = createSession(format);
      try {
        await session.render(
          <Scene>
            <PerspectiveCamera yfov={0.7} />
          </Scene>,
        );
        const empty = await session.analyze();
        assert.equal(empty.meshCount, 0);
        assert.equal(empty.registeredGeometryBytes, 0);
        const q = await session.registerGeometry(quad());
        const unused = await session.analyze();
        assert.equal(unused.meshCount, 0);
        assert.equal(unused.unusedGeometryCount, 1);
        assert.equal(unused.registeredGeometryBytes, 136);
        let release!: () => void;
        const pending = new Promise<void>((resolve) => {
          release = resolve;
        });
        function PendingMesh() {
          use(pending);
          return <Mesh geometry={q} />;
        }
        const first = session.render(
          <Scene>
            <Suspense fallback={null}>
              <PendingMesh />
            </Suspense>
          </Scene>,
        );
        const registering = session.registerGeometry(triangle());
        const atFirst = session.analyze();
        const second = session.render(
          <Scene>
            <Mesh geometry={q} />
            <Mesh geometry={q} />
          </Scene>,
        );
        release();
        await first;
        await registering;
        const firstReport = await atFirst;
        await second;
        assert.equal(firstReport.meshCount, 1);
        assert.equal(firstReport.unusedGeometryCount, 1);
        assert.equal((await session.analyze()).meshCount, 2);
        await assert.rejects(
          session.analyze({ revision: firstReport.revision }),
          { code: ErrorCode.Conflict },
        );
        const abort = new AbortController();
        abort.abort();
        await assert.rejects(session.analyze({ signal: abort.signal }), {
          code: ErrorCode.Cancelled,
        });
        function Broken(): React.ReactNode {
          throw Error("Synthetic scene render failure");
        }
        await assert.rejects(session.render(<Broken />));
        await assert.rejects(session.analyze());
        await session.render(
          <Scene>
            <Mesh geometry={q} />
          </Scene>,
        );
        assert.equal((await session.analyze()).meshCount, 1);
        const never = new Promise<void>(() => {});
        function NeverSettled() {
          use(never);
          return null;
        }
        await session.render(
          <Scene>
            <Suspense fallback={<Mesh geometry={q} />}>
              <NeverSettled />
            </Suspense>
          </Scene>,
        );
        const active = session.analyze();
        const cancelled = assert.rejects(active, (error: unknown) =>
          [ErrorCode.Cancelled, ErrorCode.Disposed].includes(
            (error as { code: ErrorCode }).code,
          ),
        );
        await Promise.all([cancelled, session.dispose()]);
      } finally {
        await session.dispose();
      }
    },
  );

  sceneTest(
    `${format}: stale analysis checks revision before native validation`,
    async () => {
      const session = createSession(format);
      try {
        const q = await session.registerGeometry(quad());
        await session.render(
          <Scene>
            <Mesh geometry={q} />
          </Scene>,
        );
        const first = await session.analyze();
        await session.render(
          <Scene>
            <Mesh geometry={q} material={{ metallic: 2 }} />
          </Scene>,
        );
        await assert.rejects(session.analyze({ revision: first.revision }), {
          code: ErrorCode.Conflict,
        });
      } finally {
        await session.dispose();
      }
    },
  );
}
