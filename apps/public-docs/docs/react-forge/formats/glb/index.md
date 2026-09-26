# GLB static scenes

Available in npm `0.2.0`. Check [release status](/react-forge/releases) for the current package version and validation scope.

`createSession(Format.Glb)` returns a `SceneSession`. Its `@delino/react-forge/glb` components are `Scene`, `Group`, `Mesh`, `PerspectiveCamera`, `OrthographicCamera`, `DirectionalLight`, `PointLight`, and `SpotLight`. A scene has one root; groups carry reusable hierarchies and transforms. Existing GLB import/editing, animation, skinning, and morph targets are outside this version.

```tsx
import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Scene, Mesh } from "@delino/react-forge/glb";

export default async function task() {
  const session = createSession(Format.Glb);
  try {
    const geometry = await session.registerGeometry({
      positions: new Float32Array([0, 0, 0, 1, 0, 0, 0, 1, 0]),
      normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1]),
      indices: new Uint32Array([0, 1, 2]),
    });
    await session.render(<Scene name="Triangle">
      <Mesh geometry={geometry} material={{
        baseColor: [0.25, 0.4, 0.6, 1], metallic: 0.7, roughness: 0.35,
      }}/>
    </Scene>);
    return session;
  } catch (error) { await session.dispose(); throw error; }
}
```

Run this task with `react-forge run triangle.tsx --output triangle.glb`. Library callers use `exportBuffer()` or `exportFile()` and dispose the session in `finally`. Files contain glTF 2.0 binary geometry and embedded images. No Blender or external converter is needed to generate them.

## Geometry and materials

Coordinates use meters, right-handed Y-up. `translation` and `scale` are three-element tuples; `rotation` is a normalized xyzw quaternion. Cameras and directional/spot lights face local -Z. Negative and nonuniform nonzero scales are supported. Normals and tangent xyz must be unit vectors and orthogonal; tangent w is +1 or -1. Optional UVs have two floats per vertex and tangents have four. Triangles use counterclockwise index order.

`registerGeometry()` copies typed arrays immediately. Keep large arrays in registered geometry, not React props. `registerTexture(bytesOrPath)` copies PNG/JPEG data or reads an explicit local file; URLs are not fetched. Asset handles belong to their creating session.

Materials accept linear `baseColor` RGBA, `metallic`, `roughness`, `emissive` RGB, `alphaMode: AlphaMode.Opaque | AlphaMode.Blend`, and `doubleSided`. Factors range from 0 to 1. Texture properties are `baseColorTexture`, `metallicTexture`, `roughnessTexture`, `normalTexture`, and `emissiveTexture`. Base-color/emissive maps are sRGB; scalar maps use the linear red channel; normal maps are linear +Y tangent-space. UV0 uses a top-left origin and repeat wrapping. Textures require UVs; normal maps additionally require tangents. Metalness and roughness maps must have equal dimensions when used together. Opaque materials require alpha factor 1 and ignore texture alpha. Transmission, refraction, clearcoat, texture transforms, and extra UV sets are unsupported and rejected.

Perspective cameras take `yfov` in radians, `aspect`, `near`, and `far`; orthographic cameras take half-extents `xmag`/`ymag`, `near`, and `far`. Directional intensity is lux; point/spot intensity is candela. Spot cone angles are radians. Lights use `KHR_lights_punctual`.

## Revisions and limits

Use `snapshot()` for a settled revision and targets. `measure(handle, { revision })` returns `{ coordinateSpace: "world", min, max, revision }`, a mesh/descendant world-space bounding box in meters. Cameras and empty groups have no mesh bounds. Stale revisions fail. `inspect()` is synchronous; `onDiagnostic()` subscribes to content-free diagnostics. React updates, Suspense waiting, cancellation, atomic file export, and `dispose()` retain the session behavior described in [sessions](/react-forge/sessions).

Registered geometry/textures together and each output are capped at 256 MiB. Individual geometry/texture data is capped at 64 MiB; textures at 64 million pixels. The existing 16 MiB, 20,000-node, depth-48 React tree limits remain. Unsupported inputs produce errors instead of being silently dropped.

For Blender's FBX material profile and interchange limitations, see [FBX](/react-forge/formats/fbx/). A successful structural validator does not replace checking your exported model in the application that will consume it.
