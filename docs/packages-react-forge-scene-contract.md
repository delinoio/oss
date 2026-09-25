# React Forge static 3D scenes

## Scope and ownership

The GLB/FBX extension adds generation-only local static scenes. `packages/react-forge` owns React components, in-memory `SceneSession`, CLI/MCP integration and examples. `forge-scene` owns bounded scene/geometry/material validation and world bounds; `forge-glb` and `forge-fbx` own independent exporters. `react-forge-node` remains a worker adapter. Import, mounted edits, animation, skins, morph targets, refraction and advanced coating shaders are excluded.

## Interfaces

`Format.Glb` and `Format.Fbx` select `SceneSession` through `createSession`. `/glb` and `/fbx` expose the same authoring vocabulary: Scene, Group, Mesh, PerspectiveCamera, OrthographicCamera, DirectionalLight, PointLight and SpotLight. Only Scene/Group accept children. Unknown properties fail; UUID-v7 refs are document-scoped. Scene is the single root; meshes have one material. Units are meters, right-handed Y-up; cameras and directional/spot lights face local -Z. Rotation is a normalized xyzw quaternion; nonuniform and negative nonzero scales are supported.

`registerGeometry` copies Float32 position/normal arrays, optional Float32 UV/tangents, and Uint32 triangle indices. Normals/tangents must be unit length, tangent xyz orthogonal to the normal, and tangent w is +1 or -1. UV0 uses glTF's top-left convention, repeat wrapping and linear filtering. Normal maps require UVs and tangents; any texture requires UVs. `registerTexture` accepts explicit regular local files or copied PNG/JPEG bytes. Handles cannot cross sessions. Binary arrays remain registered assets rather than React JSON props.

Materials support linear baseColor RGBA, metallic/roughness factors, emissive RGB, opaque/blend alpha and doubleSided. PNG/JPEG base-color/emissive maps use sRGB; metallic/roughness use linear red-channel scalar maps and normal maps use linear +Y tangent space. Factors are 0–1. Opaque materials require alpha factor 1 and ignore texture alpha. Packed GLB metallic/roughness maps require equal source dimensions. Separate AO, texture transforms, alternate UV sets and procedural runtime shaders are excluded.

Sessions expose render, synchronous inspect, settled snapshot, revision-pinned world AABB measure, Buffer/file export, diagnostics and idempotent disposal. Empty groups and camera/light-only targets have no mesh bounds. Inspect reports authored nodes as non-mountable; updates use React render. CLI/MCP export accepts matching .glb/.fbx extensions. MCP retains the same nine tools and protocol isolation.

## Engines and interoperability

GLB uses glTF 2.0 with embedded binary geometry/images and optional KHR_lights_punctual. FBX uses binary 7.4 via fbxcel, Y-up axes, meter units, embedded Video/Texture payloads and editable native meshes/materials. No Blender, Python, Autodesk SDK, network or converter is needed to generate either format. Dependencies retain their original license notices.

FBX materials target Blender 4.5's legacy importer: metalness uses ReflectionFactor, roughness maps to Shininess and the ShininessExponent texture connection, and standard diffuse/normal/emissive/opacity connections carry the remaining supported channels. Factors are baked into embedded FBX maps in the correct color space because the importer replaces connected values. This profile is not a promise of identical rendering in arbitrary FBX applications. GLB light intensity uses lux/candela; FBX retains that source value as a property and converts the imported Blender energy using the glTF SPEC 683 lm/W convention. Camera and light local axes are converted explicitly. FBX records mesh culling flags, but Blender 4.5 does not apply them and shades both sides; closed product surfaces provide the compared profile. GLB honors doubleSided in compatible viewers.

## Safety and lifecycle

Registered assets and output are each capped at 256 MiB, individual geometry/texture payloads at 64 MiB, images at 64 million pixels, and the existing 16 MiB/20,000-node/depth-48 tree limits apply. Check malformed arrays, indices, numbers, transforms, references and resource budgets before output. Native cancellation checkpoints cover validation, traversal, texture conversion and serialization; bounded third-party decoding calls are indivisible. JavaScript never runs in workers.

Exports wait for relevant React work and registrations before pinning immutable model/assets. Existing directory-identity publication queues and same-filesystem atomic publication apply. Cancellation before publication preserves prior output; completed writes remain truthful. Sessions have no automatic timeout or recovery storage. Diagnostics contain only format, stage, revision, duration and stable classifications, never asset contents or host paths.

## Validation

Require Khronos GLB validation, independent ufbx FBX parsing, and Blender re-imports with no material/mesh repair. Product samples additionally require independent local web GLB viewing and actual image inspection. Evidence records exact tool versions, output hashes, structure checks and observed differences. Native, React, installed CLI/MCP, six-host CI and existing document regressions remain required. Render tools are test-only; generated exports, screenshots and dist are untracked. The extension is unreleased until an explicit release includes it.
