import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Scene, Mesh } from "@delino/react-forge/glb";

/** An explicit static mesh reduction; the source handle remains available. */
export default async function task() {
  const session = createSession(Format.Glb);
  try {
    const positions = new Float32Array([
      0, 0, 0, 0.5, 0, 0, 1, 0, 0, 0, 0.5, 0, 0.5, 0.5, 0, 1, 0.5, 0, 0, 1, 0,
      0.5, 1, 0, 1, 1, 0,
    ]);
    const normals = new Float32Array(9 * 3);
    for (let vertex = 0; vertex < 9; vertex++) normals[vertex * 3 + 2] = 1;
    const indices = new Uint32Array([
      0, 1, 4, 0, 4, 3, 1, 2, 5, 1, 5, 4, 3, 4, 7, 3, 7, 6, 4, 5, 8, 4, 8, 7,
    ]);
    const original = await session.registerGeometry({
      positions,
      normals,
      indices,
    });
    const result = await session.simplifyGeometry(original, {
      targetTriangles: 6,
      maxError: 0,
    });
    await session.render(
      <Scene name="Simplified grid">
        <Mesh geometry={result.geometry} />
      </Scene>,
    );
    return session;
  } catch (error) {
    await session.dispose();
    throw error;
  }
}
