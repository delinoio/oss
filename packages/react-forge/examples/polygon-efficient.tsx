import React from "react";
import { createSession, Format } from "@delino/react-forge";
import {
  AlphaMode,
  Mesh as GlbMesh,
  Scene as GlbScene,
} from "@delino/react-forge/glb";
import { Mesh as FbxMesh, Scene as FbxScene } from "@delino/react-forge/fbx";
import {
  curvedSheet,
  paintedQuad,
  paintedTexture,
  sheetSegments,
  SurfaceVariant,
} from "./polygon-efficient-assets.js";

export interface TaskData {
  format?: "glb" | "fbx";
  variant?: SurfaceVariant;
}

export default async function task({ data = {} }: { data?: TaskData } = {}) {
  const format = data.format ?? "glb";
  const variant = data.variant ?? SurfaceVariant.Low;
  if (format !== "glb" && format !== "fbx") {
    throw new RangeError("Choose format glb or fbx.");
  }
  if (!Object.values(SurfaceVariant).includes(variant)) {
    throw new RangeError("Choose variant low, dense, badge, or alpha.");
  }
  const session = createSession(format === "glb" ? Format.Glb : Format.Fbx);
  try {
    const geometry =
      variant === SurfaceVariant.Low || variant === SurfaceVariant.Dense
        ? curvedSheet(sheetSegments[variant][0], sheetSegments[variant][1])
        : paintedQuad();
    const triangleCount = geometry.indices.length / 3;
    const expectedCount =
      variant === SurfaceVariant.Low
        ? 192
        : variant === SurfaceVariant.Dense
          ? 2400
          : 2;
    if (triangleCount !== expectedCount) {
      throw new Error(
        `Expected ${expectedCount} triangles, got ${triangleCount}.`,
      );
    }
    const registered = await session.registerGeometry(geometry);
    const texture =
      variant === SurfaceVariant.Badge || variant === SurfaceVariant.Alpha
        ? await session.registerTexture(
            paintedTexture(variant === SurfaceVariant.Alpha),
          )
        : undefined;
    const Scene = format === "glb" ? GlbScene : FbxScene;
    const Mesh = format === "glb" ? GlbMesh : FbxMesh;
    await session.render(
      <Scene name={`Polygon-efficient ${variant}`}>
        <Mesh
          name={variant}
          geometry={registered}
          material={{
            baseColor: [1, 1, 1, 1],
            baseColorTexture: texture,
            roughness: 1,
            alphaMode:
              variant === SurfaceVariant.Alpha
                ? AlphaMode.Blend
                : AlphaMode.Opaque,
          }}
        />
      </Scene>,
    );
    return session;
  } catch (error) {
    await session.dispose();
    throw error;
  }
}
