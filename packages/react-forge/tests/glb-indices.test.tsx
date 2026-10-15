import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";
import React, { createRef } from "react";
import { createSession, Format, type NodeHandle } from "../src/index.js";
import {
  Scene,
  Joint,
  Mesh,
  AnimationClip,
  AnimationTrack,
  AnimationPath,
  type GeometryInput,
} from "../src/glb.js";

const sceneTest =
  process.env.REACT_FORGE_SKIP_SCENE_TESTS === "1" ? test.skip : test;
const validator = createRequire(import.meta.url)("gltf-validator") as {
  validateBytes(
    bytes: Uint8Array,
  ): Promise<{ issues: { numErrors: number; messages: unknown[] } }>;
};

interface Gltf {
  accessors: {
    bufferView: number;
    byteOffset?: number;
    componentType: number;
    count: number;
    type: string;
  }[];
  bufferViews: { byteOffset: number; byteLength: number }[];
  meshes: {
    primitives: {
      attributes: Record<string, number>;
      indices: number;
      mode: number;
    }[];
  }[];
  images?: { bufferView: number }[];
  samplers?: {
    magFilter?: number;
    minFilter?: number;
    wrapS?: number;
    wrapT?: number;
  }[];
  nodes: { mesh?: number; weights?: number[] }[];
  animations?: unknown[];
  skins?: unknown[];
}

function parseGlb(bytes: Buffer) {
  assert.equal(bytes.toString("ascii", 0, 4), "glTF");
  assert.equal(bytes.readUInt32LE(8), bytes.length);
  const jsonLength = bytes.readUInt32LE(12);
  assert.equal(bytes.toString("ascii", 16, 20), "JSON");
  const json = JSON.parse(
    bytes.subarray(20, 20 + jsonLength).toString(),
  ) as Gltf;
  const binHeader = 20 + jsonLength;
  assert.equal(bytes.toString("ascii", binHeader + 4, binHeader + 8), "BIN\0");
  assert.equal((binHeader + 8) % 4, 0);
  assert.equal(bytes.readUInt32LE(binHeader) + binHeader + 8, bytes.length);
  for (const view of json.bufferViews) {
    assert.equal(view.byteOffset % 4, 0);
    assert.ok(
      view.byteOffset + view.byteLength <= bytes.length - binHeader - 8,
    );
  }
  return { json, binary: bytes.subarray(binHeader + 8) };
}

function indices(parsed: ReturnType<typeof parseGlb>, mesh: number) {
  const primitive = parsed.json.meshes[mesh]!.primitives[0]!;
  const accessor = parsed.json.accessors[primitive.indices]!;
  const view = parsed.json.bufferViews[accessor.bufferView]!;
  const start = view.byteOffset + (accessor.byteOffset ?? 0);
  const stride = accessor.componentType === 5123 ? 2 : 4;
  assert.equal(view.byteLength, accessor.count * stride);
  const values = Array.from({ length: accessor.count }, (_, i) =>
    stride === 2
      ? parsed.binary.readUInt16LE(start + i * stride)
      : parsed.binary.readUInt32LE(start + i * stride),
  );
  return { primitive, accessor, view, values };
}

async function valid(bytes: Buffer) {
  const result = await validator.validateBytes(bytes);
  assert.equal(
    result.issues.numErrors,
    0,
    JSON.stringify(result.issues.messages),
  );
}

function geometry(vertexCount: number, values: number[]): GeometryInput {
  const positions = new Float32Array(vertexCount * 3);
  const normals = new Float32Array(vertexCount * 3);
  for (let i = 0; i < vertexCount; i++) {
    positions[i * 3] = i;
    positions[i * 3 + 1] = i % 2;
    normals[i * 3 + 2] = 1;
  }
  return { positions, normals, indices: new Uint32Array(values) };
}

sceneTest(
  "GLB quad packs exact indices and shares one aligned accessor",
  async () => {
    const session = createSession(Format.Glb);
    const texturePath = fileURLToPath(
      new URL("../examples/sample.png", import.meta.url),
    );
    try {
      const quad = await session.registerGeometry({
        positions: new Float32Array([0, 0, 0, 1, 0, 0, 1, 1, 0, 0, 1, 0]),
        normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1]),
        uv: new Float32Array([0, 0, 1, 0, 1, 1, 0, 1]),
        indices: new Uint32Array([0, 1, 2, 0, 2, 3]),
      });
      const other = await session.registerGeometry({
        ...geometry(3, [0, 1, 2]),
        uv: new Float32Array([0, 0, 1, 0, 0, 1]),
      });
      const texture = await session.registerTexture({ path: texturePath });
      await session.render(
        <Scene>
          <Mesh geometry={quad} translation={[0, 0, 0]} />
          <Mesh geometry={quad} translation={[2, 0, 0]} />
          <Mesh
            geometry={other}
            translation={[4, 0, 0]}
            material={{ baseColorTexture: texture }}
          />
        </Scene>,
      );
      const bytes = await session.exportBuffer();
      assert.deepEqual(await session.exportBuffer(), bytes);
      const parsed = parseGlb(bytes);
      assert.deepEqual(parsed.json.samplers, [
        { magFilter: 9729, minFilter: 9987, wrapS: 10497, wrapT: 10497 },
      ]);
      const first = indices(parsed, 0);
      const second = indices(parsed, 1);
      assert.equal(first.accessor.componentType, 5123);
      assert.equal(first.accessor.count, 6);
      assert.equal(first.view.byteLength, 12);
      assert.deepEqual(first.values, [0, 1, 2, 0, 2, 3]);
      assert.equal(first.primitive.mode, 4);
      assert.equal(
        parsed.json.meshes[0]!.primitives[0]!.indices,
        parsed.json.meshes[1]!.primitives[0]!.indices,
      );
      assert.deepEqual(second.values, first.values);
      assert.equal(parsed.json.accessors.length, 8);
      assert.deepEqual(indices(parsed, 2).values, [0, 1, 2]);
      const imageView =
        parsed.json.bufferViews[parsed.json.images![0]!.bufferView]!;
      assert.deepEqual(
        parsed.binary.subarray(
          imageView.byteOffset,
          imageView.byteOffset + imageView.byteLength,
        ),
        await readFile(texturePath),
      );
      // Six indices use 12 payload bytes here, versus 24 in the prior u32 output.
      assert.equal(6 * 4 - first.view.byteLength, 12);
      await valid(bytes);
    } finally {
      await session.dispose();
    }
  },
);

for (const maximum of [65534, 65535, 65536]) {
  sceneTest(`GLB reserved index boundary ${maximum}`, async () => {
    const session = createSession(Format.Glb);
    try {
      const input = geometry(maximum + 1, [0, 1, maximum]);
      const handle = await session.registerGeometry(input);
      await session.render(
        <Scene>
          <Mesh geometry={handle} />
        </Scene>,
      );
      const bytes = await session.exportBuffer();
      const parsed = parseGlb(bytes);
      const actual = indices(parsed, 0);
      assert.equal(
        actual.accessor.componentType,
        maximum <= 65534 ? 5123 : 5125,
      );
      assert.deepEqual(actual.values, [0, 1, maximum]);
      assert.equal(
        parsed.json.accessors[actual.primitive.attributes.POSITION]!.count,
        maximum + 1,
      );
      assert.equal(actual.view.byteLength, maximum <= 65534 ? 6 : 12);
      await valid(bytes);
    } finally {
      await session.dispose();
    }
  });
}

sceneTest(
  "GLB index width follows references, not the vertex count",
  async () => {
    const session = createSession(Format.Glb);
    try {
      const handle = await session.registerGeometry(geometry(70000, [0, 1, 2]));
      await session.render(
        <Scene>
          <Mesh geometry={handle} />
        </Scene>,
      );
      const bytes = await session.exportBuffer();
      const parsed = parseGlb(bytes);
      const actual = indices(parsed, 0);
      assert.equal(actual.accessor.componentType, 5123);
      assert.deepEqual(actual.values, [0, 1, 2]);
      for (const attribute of ["POSITION", "NORMAL"]) {
        assert.equal(
          parsed.json.accessors[actual.primitive.attributes[attribute]!]!.count,
          70000,
        );
      }
      await valid(bytes);
    } finally {
      await session.dispose();
    }
  },
);

sceneTest(
  "GLB animation retains shared geometry and distinct node morph weights",
  async () => {
    const session = createSession(Format.Glb);
    const joint = createRef<NodeHandle>();
    const first = createRef<NodeHandle>();
    const second = createRef<NodeHandle>();
    const clip = createRef<NodeHandle>();
    try {
      const input = geometry(3, [0, 1, 2]);
      input.skin = {
        joints: new Uint16Array(12),
        weights: new Float32Array([1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0]),
      };
      input.morphTargets = [
        {
          name: "lift",
          positions: new Float32Array([0, 0, 0, 0, 1, 0, 0, 0, 0]),
          normals: new Float32Array(9),
        },
      ];
      const handle = await session.registerGeometry(input);
      const motion = await session.registerAnimationSampler({
        path: AnimationPath.Translation,
        times: new Float32Array([0, 1]),
        values: new Float32Array([0, 0, 0, 2, 0, 0]),
      });
      await session.render(
        <Scene>
          <Joint ref={joint} />
          <Mesh
            ref={first}
            geometry={handle}
            skin={{ joints: [joint] }}
            morphWeights={[0]}
          />
          <Mesh
            ref={second}
            geometry={handle}
            skin={{ joints: [joint] }}
            morphWeights={[1]}
          />
          <AnimationClip ref={clip} name="move">
            <AnimationTrack target={joint} sampler={motion} />
          </AnimationClip>
        </Scene>,
      );
      const snapshot = await session.snapshot();
      const bounds = async (mesh: NodeHandle, time: number) => {
        const value = await session.measure(mesh, {
          revision: snapshot.revision,
          animation: { clip: clip.current!, time },
        });
        return { min: value.min, max: value.max };
      };
      assert.deepEqual(await bounds(first.current!, 0), {
        min: [0, 0, 0],
        max: [2, 1, 0],
      });
      assert.deepEqual(await bounds(second.current!, 0), {
        min: [0, 0, 0],
        max: [2, 2, 0],
      });
      assert.deepEqual(await bounds(first.current!, 0.5), {
        min: [1, 0, 0],
        max: [3, 1, 0],
      });
      assert.deepEqual(await bounds(second.current!, 0.5), {
        min: [1, 0, 0],
        max: [3, 2, 0],
      });
      assert.deepEqual(await bounds(first.current!, 1), {
        min: [2, 0, 0],
        max: [4, 1, 0],
      });
      assert.deepEqual(await bounds(second.current!, 1), {
        min: [2, 0, 0],
        max: [4, 2, 0],
      });
      const bytes = await session.exportBuffer();
      const parsed = parseGlb(bytes);
      assert.equal(parsed.json.animations?.length, 1);
      assert.equal(parsed.json.skins?.length, 2);
      assert.deepEqual(
        parsed.json.nodes
          .filter((node) => node.mesh !== undefined)
          .map((node) => node.weights),
        [[0], [1]],
      );
      assert.equal(
        parsed.json.meshes[0]!.primitives[0]!.indices,
        parsed.json.meshes[1]!.primitives[0]!.indices,
      );
      assert.equal(indices(parsed, 0).accessor.componentType, 5123);
      assert.deepEqual(indices(parsed, 0).values, [0, 1, 2]);
      await valid(bytes);
    } finally {
      await session.dispose();
    }
  },
);
