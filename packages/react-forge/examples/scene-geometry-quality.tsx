import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Mesh, Scene } from "@delino/react-forge/glb";

/** Exact-index quality fixture with overlapping and orientation findings. */
export default async function task({
  data,
}: { data?: { format?: "glb" | "fbx" } } = {}) {
  const session = createSession(
    data?.format === "fbx" ? Format.Fbx : Format.Glb,
  );
  try {
    const geometry = await session.registerGeometry({
      positions: new Float32Array([
        0, 0, 0, 1, 0, 0, 0, 1, 0, 2, 0, 0, 9, 9, 9,
      ]),
      normals: Float32Array.from({ length: 15 }, (_, i) =>
        i % 3 === 2 ? 1 : 0,
      ),
      indices: new Uint32Array([0, 1, 2, 1, 2, 0, 2, 1, 0, 0, 0, 1, 0, 1, 3]),
    });
    await session.render(
      <Scene>
        <Mesh geometry={geometry} />
      </Scene>,
    );
    return session;
  } catch (error) {
    await session.dispose();
    throw error;
  }
}
