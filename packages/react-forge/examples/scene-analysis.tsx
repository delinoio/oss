import React from "react";
import {
  createSession,
  Format,
  type SceneGeometryBudget,
} from "@delino/react-forge";
import {
  Scene,
  Group,
  Mesh,
  PerspectiveCamera,
  PointLight,
} from "@delino/react-forge/glb";

/** Portable scene with repeated and unused geometry for analysis consumers. */
export default async function task({
  data,
}: {
  data?: { format?: "glb" | "fbx"; geometryBudget?: SceneGeometryBudget };
} = {}) {
  const session = createSession(
    data?.format === "fbx" ? Format.Fbx : Format.Glb,
    { geometryBudget: data?.geometryBudget },
  );
  try {
    const q = await session.registerGeometry({
      positions: new Float32Array([0, 0, 0, 1, 0, 0, 1, 1, 0, 0, 1, 0]),
      normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1]),
      indices: new Uint32Array([0, 1, 2, 0, 2, 3]),
    });
    const t = await session.registerGeometry({
      positions: new Float32Array([0, 0, 0, 1, 0, 0, 0, 1, 0]),
      normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1]),
      indices: new Uint32Array([0, 1, 2]),
    });
    await session.registerGeometry({
      positions: new Float32Array([0, 0, 0, 2, 0, 0, 2, 1, 0, 0, 1, 0]),
      normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1]),
      indices: new Uint32Array([0, 1, 2, 0, 2, 3]),
    });
    await session.render(
      <Scene>
        <Group>
          <Mesh geometry={q} />
          <Mesh geometry={q} />
        </Group>
        <Mesh geometry={q} />
        <Mesh geometry={t} />
        <PerspectiveCamera yfov={0.7} />
        <PointLight />
      </Scene>,
    );
    return session;
  } catch (error) {
    await session.dispose();
    throw error;
  }
}
