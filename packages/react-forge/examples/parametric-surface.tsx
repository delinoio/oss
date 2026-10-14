import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Mesh, Scene } from "@delino/react-forge/glb";

export default async function task() {
  const session = createSession(Format.Glb);
  try {
    // One explicitly bounded surface. The material controls backface rendering.
    const surface = await session.bakeParametricGeometry({
      uSegments: 20,
      vSegments: 12,
      maxTriangles: 480,
      sample: (u, v) => {
        const x = u - 0.5;
        const y = v - 0.5;
        const du = 0.08 * y;
        const dv = 0.08 * x;
        const normalLength = Math.hypot(du, dv, 1);
        const tangentLength = Math.hypot(1, du);
        return {
          position: [x, y, 0.08 * x * y],
          normal: [-du / normalLength, -dv / normalLength, 1 / normalLength],
          uv: [u, 1 - v],
          tangent: [1 / tangentLength, 0, du / tangentLength, -1],
        };
      },
    });
    await session.render(
      <Scene>
        <Mesh
          geometry={surface}
          material={{ baseColor: [0.2, 0.5, 0.8, 1], doubleSided: true }}
        />
      </Scene>,
    );
    return session;
  } catch (error) {
    await session.dispose();
    throw error;
  }
}
