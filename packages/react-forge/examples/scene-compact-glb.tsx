import React from "react";
import { createSession, Format } from "../src/index.js";
import { Mesh, Scene } from "../src/glb.js";

// Two identical faces, with two unused positions in the original asset.
export default async function task() {
  const session = createSession(Format.Glb);
  try {
    const original = await session.registerGeometry({
      positions: new Float32Array([
        0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 5, 5, 5, 6, 6, 6,
      ]),
      normals: new Float32Array([
        0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1,
      ]),
      indices: new Uint32Array([0, 1, 2, 3, 1, 2]),
    });
    const { geometry, outputVertices, triangles } =
      await session.compactGeometry(original);
    if (outputVertices !== 3 || triangles !== 2)
      throw new Error("Unexpected geometry compaction result.");
    await session.render(
      <Scene name="Compacted triangles">
        <Mesh geometry={geometry} />
      </Scene>,
    );
    return session;
  } catch (error) {
    await session.dispose();
    throw error;
  }
}
