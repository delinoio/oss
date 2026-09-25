import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Scene, Mesh, PerspectiveCamera, DirectionalLight } from "@delino/react-forge/glb";
import { roundedBox } from "./audio-studio-assets/geometry.js";

export default async function task() {
  const session = createSession(Format.Glb);
  try {
    const geometry = await session.registerGeometry(roundedBox([0.2, 0.06, 0.17], 0.008));
    await session.render(<Scene name="Product block"><Mesh geometry={geometry} material={{ baseColor: [0.3, 0.35, 0.4, 1], metallic: 0.8, roughness: 0.35 }}/><PerspectiveCamera yfov={0.7} translation={[0, 0, 1]}/><DirectionalLight intensity={1}/></Scene>);
    return session;
  } catch (error) { await session.dispose(); throw error; }
}
