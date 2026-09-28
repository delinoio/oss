# FBX static scenes

Available in npm `0.2.0`. Create and export static scenes using `createSession(Format.Fbx)` and the `@delino/react-forge/fbx` entry point. The released static API excludes existing FBX import/editing, animation, rigging, and advanced shaders.

Use the same components, registered geometry/textures, transforms, measurement, and lifecycle as the [GLB guide](/react-forge/formats/glb/). Change the format to `Format.Fbx`, import the components from `@delino/react-forge/fbx`, and export to a `.fbx` destination. The result is binary FBX 7.4 with embedded textures. Generation needs no Blender, Autodesk SDK, or conversion process.

## Compatibility profile

The supported material profile targets Blender 4.5 LTS. It carries base color, metallic, roughness, normal, emissive, and opacity through the importer's legacy material properties and texture connections. Color and scalar factors are baked into embedded texture copies where the importer replaces connected values. Original registered bytes stay unchanged. Assets use meters and right-handed Y-up; camera/light axes and UV orientation are converted by the exporter.

FBX applications do not agree on all material conventions. Check the actual exported file in your target application; identical shading in arbitrary FBX consumers is not promised. Basic transparency is supported; refraction, transmission, clearcoat, and layered/procedural materials are not. Unknown or unsupported authoring properties fail rather than disappearing silently.

The accompanying GLB is useful for comparison because glTF defines a standard metallic-roughness model. Local acceptance checks use Blender 4.5.14 LTS for both exported formats, and an independent web viewer for GLB. These checks do not establish validation on every operating system or every FBX application. See [release and validation status](/react-forge/releases).

Blender 4.5 ignores FBX mesh culling flags and shades both sides. Use closed surfaces when matching GLB appearance; a single-sided open surface can look different from its GLB counterpart.

## Animation authoring (unreleased)

The animation extension is implemented but **not released on npm**. It shares
`Joint`, `AnimationClip`, `AnimationTrack`, skin/morph geometry,
`registerAnimationSampler`, `bakeAnimationSampler`, and clip/time measurement with
the [GLB animation API](/react-forge/formats/glb/#animation-authoring-unreleased).
Use `@delino/react-forge/fbx` imports, `Format.Fbx`, and an `.fbx` destination. Clips, bones, skin
bindings and blend shapes remain native FBX data. Distinct mesh instances retain
their own skin bindings and morph state. Group the mesh and skeleton for whole-rig
movement; TRS tracks directly on a skinned mesh are rejected.

```ts
const session = createSession(Format.Fbx, { animationBakeFps: 60 });
```

`animationBakeFps` defaults to 60 and accepts integers 1–240. STEP and directly
representable linear scalar curves retain their interpolation. Quaternion
rotation and CUBIC curves are sampled at that rate plus every original key time;
rotations use continuous Euler representations with the supported camera/light
axis conversions. Values **between baked samples are approximations**. Raise the
rate for rapidly changing motion and verify the result in the target application;
no fixed accuracy is promised. Extremely close keys that collapse onto one FBX
time tick and curves exceeding output budgets fail instead of silently losing
keys. This export setting is separate from the fps used to bake a JavaScript
callback into a sampler.

Native measurement evaluates the source curves, so it can differ from FBX
playback between samples. Each source track clamps outside its key range, and
looping belongs to the consumer. Local checks use ufbx time evaluation and
Blender 4.5.14 re-import/rendering of idle, walk and wave clips with skinning and
expression morphs. These checks do not establish Unity/Unreal or arbitrary FBX
application compatibility. Existing-file import, automatic rigging/weights, IK,
retargeting, physics and a clip playback/blending runtime remain excluded.
