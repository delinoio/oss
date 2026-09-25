import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Scene, Mesh, OrthographicCamera, SpotLight } from "@delino/react-forge/fbx";
import { roundedBox } from "./audio-studio-assets/geometry.js";

export default async function task() {
  const session = createSession(Format.Fbx);
  try {
    const geometry = await session.registerGeometry(roundedBox([0.2, 0.06, 0.17], 0.008));
    await session.render(<Scene name="Product block"><Mesh geometry={geometry} material={{ baseColor: [0.3, 0.35, 0.4, 1], metallic: 0.8, roughness: 0.35 }}/><OrthographicCamera xmag={0.3} ymag={0.3} translation={[0, 0, 1]}/><SpotLight intensity={1} translation={[0, 0, 1]}/></Scene>);
    return session;
  } catch (error) { await session.dispose(); throw error; }
}
