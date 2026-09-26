# GLB static scenes

Available in npm `0.2.0`. Check [release status](/react-forge/releases) for the current package version and validation scope.

`createSession(Format.Glb)` returns a `SceneSession`. Its `@delino/react-forge/glb` components are `Scene`, `Group`, `Mesh`, `PerspectiveCamera`, `OrthographicCamera`, `DirectionalLight`, `PointLight`, and `SpotLight`. A scene has one root; groups carry reusable hierarchies and transforms. The released static API does not include existing GLB import/editing, animation, skinning, or morph targets. The separate animation extension is described below.

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

## Animation authoring (unreleased)

The following extension is implemented but has **not been released on npm**.
The static API above remains available since `0.2.0`. Animation adds `Joint`,
`AnimationClip`, and `AnimationTrack` to both `@delino/react-forge/glb` and `@delino/react-forge/fbx` with the same API.
Declare uniquely named clips directly under `Scene`. Tracks target a spatial
node's translation, rotation, scale, or a mesh's morph weights. Targets are
same-session `NodeHandle` values or React object refs; refs resolve after the
render commits, so a joint, mesh and its clip can be declared together.

```tsx
import React, { createRef } from "react";
import { createSession, Format, type NodeHandle } from "@delino/react-forge";
import {
  Scene, Mesh, AnimationClip, AnimationTrack, AnimationPath,
} from "@delino/react-forge/glb";

export default async function task() {
  const session = createSession(Format.Glb);
  try {
    const mesh = createRef<NodeHandle>();
    const clip = createRef<NodeHandle>();
    const geometry = await session.registerGeometry({
      positions: new Float32Array([0, 0, 0, 1, 0, 0, 0, 1, 0]),
      normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1]),
      indices: new Uint32Array([0, 1, 2]),
    });
    const move = await session.bakeAnimationSampler({
      path: AnimationPath.Translation,
      duration: 2,
      sample: time => [time, Math.sin(Math.PI * time) * 0.2, 0],
    });
    await session.render(<Scene>
      <Mesh ref={mesh} geometry={geometry}/>
      <AnimationClip ref={clip} name="move">
        <AnimationTrack target={mesh} sampler={move}/>
      </AnimationClip>
    </Scene>);
    const { revision } = await session.snapshot();
    await session.measure(mesh.current!, {
      revision, animation: { clip: clip.current!, time: 1 },
    });
    return session;
  } catch (error) { await session.dispose(); throw error; }
}
```

`bakeAnimationSampler({ path, duration, fps, sample }, { signal })` calls a
synchronous `sample(time)` in Node, includes time zero and the duration endpoint,
and registers a LINEAR sampler. Duration must be positive; fps defaults to 60
and accepts integers 1–240. Return an array or `Float32Array` with 3 translation
or scale values, or 4 normalized xyzw rotation values. For `AnimationPath.Weights`,
provide `components` equal to the morph count. The bake periodically yields and
checks cancellation; it cannot interrupt a currently running callback. Keep
callbacks bounded. Export waits for pending bakes and registrations.

For hand-authored keys, use `registerAnimationSampler({ path, times, values,
interpolation, inTangents, outTangents }, { signal })`. Arrays are copied
`Float32Array` assets. Times are strictly increasing, finite, nonnegative seconds;
values are flattened in key order. `AnimationInterpolation.Linear` is the default;
`Step` holds the preceding key, and `Cubic` uses Hermite derivatives per second.
Cubic requires at least two keys and input/output tangent arrays matching the
value length. Other modes reject tangent arrays. Translation/scale have 3 values
per key, rotation has 4, and weights have one per morph. Quaternion LINEAR takes
the shortest path; CUBIC normalizes its result. Zero scale, scale crossings,
singular quaternion curves and duplicate tracks for one target/path are errors.

### Skinning and morphs

Add `skin: { joints: Uint16Array, weights: Float32Array }` to registered geometry.
Each vertex has exactly four indices and weights; unused slots have zero weight
and valid indices. Nonnegative weights sum to one within `1e-6`; nonzero
influences cannot repeat a joint. Indices address `Mesh`'s ordered
`skin={{ joints: [hipRef, armRef, ...] }}` list of distinct `Joint` refs or handles.
A `Joint` accepts the same transforms and hierarchy as `Group`. The complete base
pose defines the binding; manual inverse bind matrices are unnecessary.

Geometry also accepts `morphTargets: [{ name, positions, normals? }]`, with at
most 64 uniquely named targets. Position and optional normal arrays contain
three **deltas** per vertex, not absolute target coordinates. `Mesh.morphWeights`
supplies initial weights in that order, defaulting to zero. A weights sampler
animates that same ordered vector. Morphing precedes skinning. Morph tangent
deltas are unsupported.

Do not animate the TRS of a skinned mesh itself; this fails validation. Put the
mesh and its joints in the same `Group` and animate that group to move the whole
character. Handles from another session, unmounted refs, or removed targets fail
instead of falling back to a matching name.

`measure(handle, { revision, animation: { clip, time } })` returns the deformed
world AABB at that time without changing the revision. Omitting animation measures
the base pose. Each track holds its endpoint outside its key range. Looping is
the consuming application's decision; React Forge exports clips and does not
provide a playback or blending runtime.

Sampler, skin and morph data share the 64 MiB individual-asset and 256 MiB
aggregate/output ceilings. Expanded bindings and baked curves are bounded too.
Local acceptance checks cover Khronos validation, independent web playback, and
Blender 4.5.14 import/deformation of an authored character. They do not establish
Unity/Unreal compatibility. Existing-file import, automatic rigging/weight
generation, IK, retargeting and physics remain excluded.
