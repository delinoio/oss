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

GLB export packs eligible triangle indices into a smaller lossless representation while preserving every authored vertex and triangle. Continue to supply indices as `Uint32Array`; packing needs no option. Output size depends on the whole scene, so a smaller index buffer does not guarantee an exact file-size reduction.

### Explicit geometry compaction (unreleased)

The unreleased API adds `await session.compactGeometry(geometry, { signal })` for GLB and FBX sessions. It returns `{ geometry, inputVertices, outputVertices, triangles, inputBytes, outputBytes }`. Render with the returned handle to use the smaller asset. The original geometry remains available for export; neither registration nor export compacts it automatically.

Compaction removes unreferenced vertices and coalesces only vertices whose complete attributes, including UV seams, tangents, skin weights and morph deltas, have identical Float32 bits. It preserves triangle count, order, winding and duplicate faces. It does **not** reduce triangles or perform approximate welding. Removing unused positions can shrink measured bounds after you render with the returned handle; a scene still using the original handle retains its original bounds. The operation observes session asset and scratch limits and supports cancellation.

Materials accept linear `baseColor` RGBA, `metallic`, `roughness`, `emissive` RGB, `alphaMode: AlphaMode.Opaque | AlphaMode.Blend`, and `doubleSided`. Factors range from 0 to 1. Texture properties are `baseColorTexture`, `metallicTexture`, `roughnessTexture`, `normalTexture`, and `emissiveTexture`. Base-color/emissive maps are sRGB; scalar maps use the linear red channel; normal maps are linear +Y tangent-space. UV0 uses a top-left origin and repeat wrapping. Textures require UVs; normal maps additionally require tangents. Metalness and roughness maps must have equal dimensions when used together. Opaque materials require alpha factor 1 and ignore texture alpha. Transmission, refraction, clearcoat, texture transforms, and extra UV sets are unsupported and rejected.

Perspective cameras take `yfov` in radians, `aspect`, `near`, and `far`; orthographic cameras take half-extents `xmag`/`ymag`, `near`, and `far`. Directional intensity is lux; point/spot intensity is candela. Spot cone angles are radians. Lights use `KHR_lights_punctual`.

## Static geometry simplification (unreleased)

The following explicit API is implemented in source but **not released on npm**. It accepts a registered static geometry handle and creates a separate immutable result. Ordinary registration and export do not simplify geometry.

```ts
const result = await session.simplifyGeometry(geometry, {
  targetTriangles: 100,
  maxError: 0.01,
});
// Use result.geometry in a Mesh only if the returned counts suit your scene.
```

`targetTriangles` is a safe integer between one and the input triangle count. `maxError` is finite and nonnegative. The result includes `inputTriangles`, `outputTriangles`, `inputVertices`, `outputVertices`, `targetReached`, and `estimatedError`. A target may be unreachable because boundaries, seams, or topology must remain intact; check `targetReached` and the actual counts. `estimatedError` is an accumulated plane-quadric estimate, not a visual-quality or maximum-distance guarantee. Skin and morph geometry are unsupported; a static mesh may still be used by a node with translation, rotation, or scale animation. The source handle remains usable.

## Polygon-efficient static authoring

Choose the triangle count for each physical part before you register its geometry. An indexed sheet with `u` by `v` cells has `(u + 1) * (v + 1)` vertices and `2 * u * v` triangles. The original `polygon-efficient.tsx` example in the [React Forge repository](https://github.com/delinoio/oss) uses `z = 0.1 * sin(pi * x) * sin(pi * y)` for a curved sheet. Its 6-by-16 grid has 119 vertices and 192 triangles; its 20-by-60 grid has 1,281 vertices and 2,400 triangles. Sum `indices.length / 3` across the mesh instances you intend to render. No single triangle budget fits every view or device.

Add a back and side edges only when the object needs physical thickness or must be seen from those directions. For two separate grid sheets with matching edge divisions, a closed shell has `4 * u * v + 4 * (u + v)` triangles: 472 for 6-by-16 and 5,120 for 20-by-60. This is a count formula, not a second exported example. A recursive one-to-four triangle split multiplies a surface's triangles by `4^passes`; four passes cost 256 times the starting surface count before adding a back. Preserve real backs, UV seams, tangent frames, and separate material or animation ownership when the model needs them. Do not join unrelated parts only to reduce the mesh count.

The example supplies analytic smooth normals and orthogonal tangents on both grids. Smooth normals can soften shading within the existing silhouette; they do not add silhouette detail. Add subdivisions where the outline or deformation needs them. Geometry reuse can reduce stored geometry bytes, but every rendered instance still contributes its triangles. Compression and smaller index types reduce file bytes, not triangle work.

For painted detail on an opaque surface, put the colors in one precomposited base-color texture. The example's 16-by-16 PNG has a red gradient on top, a blue gradient below, and a small green stripe across the middle. Its badge remains one mesh, one material, four vertices, and two triangles. Changing the texture resolution or colors does not change those geometry counts. The separate transparent sheet uses the same quad and a white RGBA texture whose alpha rises from transparent at the left edge to opaque at the right edge. It uses `AlphaMode.Blend` on one material. Keep an opaque body `Opaque`: changing the body to `Blend` is not equivalent to painting on it, and blending can increase render cost or change sorting.

After you build the package, run any variant as a one-shot task. The companion `polygon-efficient-assets.ts` source generates the images in memory. The CLI writes only the requested model; it does not need a network download or an image converter.

```sh
react-forge run polygon-efficient.tsx --data '{"format":"glb","variant":"low"}' --output low.glb
react-forge run polygon-efficient.tsx --data '{"format":"glb","variant":"dense"}' --output dense.glb
react-forge run polygon-efficient.tsx --data '{"format":"glb","variant":"badge"}' --output badge.glb
react-forge run polygon-efficient.tsx --data '{"format":"glb","variant":"alpha"}' --output alpha.glb
```

Copy both source files beside each other before running an installed CLI task. Use `"format":"fbx"` and an `.fbx` output path to export the same authored surfaces as FBX. A structural validator can confirm mesh topology and material bindings. Check the actual colors, alpha fade, winding, and performance in the target application. GLB viewers that support glTF honor `doubleSided`; Blender 4.5 ignores FBX culling flags and shades both sides. Neither behavior makes a sheet physically closed.

## Revisions and limits

Use `snapshot()` for a settled revision and targets. `measure(handle, { revision })` returns `{ coordinateSpace: "world", min, max, revision }`, a mesh/descendant world-space bounding box in meters. Cameras and empty groups have no mesh bounds. Stale revisions fail. `inspect()` is synchronous; `onDiagnostic()` subscribes to content-free diagnostics. React updates, Suspense waiting, cancellation, atomic file export, and `dispose()` retain the session behavior described in [sessions](/react-forge/sessions).

### Scene complexity analysis (unreleased)

The source implementation adds `await session.analyze({ revision?, signal? })`. It settles the scene and returns an immutable report before any export. Use its `meshCount`, `referencedGeometryCount`, `uniqueVertexCount`, `uniqueTriangleCount`, `instanceVertexCount`, `instanceTriangleCount`, `registeredGeometryBytes`, `referencedGeometryBytes`, and `unusedGeometryCount` to distinguish shared geometry storage from authored mesh instances. The `meshes` array lists node IDs, vertex and triangle counts, and geometry instance counts, largest triangle count first. Ties keep authored depth-first order. The byte counts describe registered geometry data, not GPU memory or final GLB size. An explicit stale revision fails. Transforms, materials, animation and camera/light nodes do not multiply topology. This feature is not in the released `0.2.0` static API; check [release status](/react-forge/releases) for availability.

### Caller geometry budgets (unreleased)

Trusted task code can choose limits before creating a GLB or FBX session:

```ts
import { createSession, Format, SceneBudgetAction } from "@delino/react-forge";

const session = createSession(Format.Glb, {
  geometryBudget: {
    action: SceneBudgetAction.Warn,
    maxSceneTriangles: 10_000,
    maxMeshTriangles: 2_000,
    maxMeshCount: 100,
  },
});
const unsubscribe = session.onSceneWarning((warning) => {
  // warning has format, revision, metric, actual and limit.
});
```

Set at least one threshold; each must be a nonnegative safe integer. `maxSceneTriangles` counts every authored Mesh instance, including repeated uses of one registered geometry. `maxMeshTriangles` applies to each Mesh, and `maxMeshCount` counts Mesh nodes. Equality passes. Unused registered geometry, transforms, materials, cameras, lights and animation playback do not increase topology counts. With no `geometryBudget`, exports keep their current behavior. Native byte and tree ceilings still apply.

`SceneBudgetAction.Warn` preserves geometry and export bytes. Buffer export reports warnings through the listener; file export also returns a `warnings` array when violations exist. CLI text reports warnings on stderr, CLI JSON and MCP export include structured warnings. A warning can occur before a later cancellation or file error, so check the export's publication result before treating the file as committed. `SceneBudgetAction.Error` stops before serialization and preserves an existing destination. It raises `resource_limit` with `context.sceneBudget` for the first violation and `published: false`. `analyze()` reports every `budgetViolations` entry without enforcing the policy. To recover, render a smaller scene or create a session with a different caller policy, then export explicitly. This option is implemented in source and is not part of the released `0.2.0` static API; check [release status](/react-forge/releases) before using it from npm.
Pass `geometryQuality: true` to opt into exact-index checks on referenced base geometry. Each geometry report contains its asset handle, vertex and triangle totals, and counts for unused vertices, repeated-index triangles, exact zero-area triangles, duplicate triangles, and opposite-winding triangles. Each finding includes up to 32 ascending vertex or triangle indices and a `truncated` flag. A repeated-index triangle also counts as zero area. Duplicate and reverse-winding counts compare index triples against the first occurrence; they do not merge coincident positions at different indices. The pass runs before transforms, skinning, and morph weights. Findings are observations and never remove faces or block export. Animation may open a base-pose zero-area triangle, and reverse faces may be intentional. Omit the option to skip the pass. This is a diagnostic pass; it does not reduce vertex storage or triangle count. The CLI accepts `analyze --geometry-quality`; MCP accepts `geometryQuality: true` only with `view: "scene_analysis"` and returns reports for the current mesh page. This addition is implemented in source and remains unreleased.

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
