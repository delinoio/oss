import React from "react";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import validator from "gltf-validator";
import { join } from "node:path";
import { createSession, Format } from "../dist/index.js";
import { Scene, Group, Mesh, PerspectiveCamera, OrthographicCamera, DirectionalLight, PointLight, SpotLight, AlphaMode } from "../dist/glb.js";
import { roundedBox, quaternion } from "../examples/audio-studio-assets/geometry.ts";
const output = process.argv[2];
if (!output) throw Error("Expected explicit profile output directory");
await mkdir(output, { recursive: true });
for (const format of [Format.Glb, Format.Fbx]) {
  const session = createSession(format);
  try {
    const geometry = await session.registerGeometry(roundedBox([.2, .1, .3], .01));
    const e = React.createElement;
    await session.render(e(Scene, { name: "Compatibility" },
      e(Group, { name: "Mirrored nonuniform parent", scale: [-1, 2, .5], rotation: quaternion([0, 1, 0], .32), translation: [.2, .1, -.3] },
        e(Mesh, { name: "Opacity probe", geometry, material: { baseColor: [.2, .4, .6, .4], metallic: .7, roughness: .35, emissive: [.1, .2, .3], alphaMode: AlphaMode.Blend, doubleSided: true } })),
      e(PerspectiveCamera, { name: "Perspective", yfov: .7, aspect: 1.5, near: .02, far: 50, translation: [.3, .4, 1], rotation: quaternion([1, 0, 0], -.2) }),
      e(OrthographicCamera, { name: "Orthographic", xmag: .4, ymag: .2, near: .03, far: 60, translation: [0, 0, 1] }),
      e(DirectionalLight, { name: "Sun", color: [.2, .4, .6], intensity: 100 }),
      e(PointLight, { name: "Point", color: [.5, .7, .9], intensity: 30, translation: [0, 1, 0] }),
      e(SpotLight, { name: "Spot", intensity: 20, innerCone: .2, outerCone: .5, translation: [0, 0, 1] })));
    await session.exportFile(join(output, `profile.${format}`), { overwrite: true });
    if (format === Format.Glb) {
      const result = await validator.validateBytes(await readFile(join(output, 'profile.glb')));
      if (result.issues.numErrors) throw Error(JSON.stringify(result.issues));
      await writeFile(join(output, 'validator.json'), JSON.stringify(result.issues, null, 2) + '\n');
    }
  } finally { await session.dispose(); }
}
