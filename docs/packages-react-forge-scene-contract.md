# React Forge 3D scenes and animation

## Scope and ownership

The GLB/FBX extension adds generation-only local scenes. `packages/react-forge` owns React components, in-memory `SceneSession`, CLI/MCP integration and examples. `forge-scene` owns bounded scene/geometry/material validation and world bounds; `forge-glb` and `forge-fbx` own independent exporters. `react-forge-node` remains a worker adapter. Import, mounted edits, automatic rigging/weight generation, IK, retargeting, physics, clip blending/playback, refraction and advanced coating shaders are excluded. The animation extension is implemented in source and remains unreleased; static GLB/FBX creation has been available since npm 0.2.0.

## Interfaces

`Format.Glb` and `Format.Fbx` select `SceneSession` through `createSession`. `/glb` and `/fbx` expose the same authoring vocabulary: Scene, Group, Mesh, PerspectiveCamera, OrthographicCamera, DirectionalLight, PointLight and SpotLight, plus Joint, AnimationClip and AnimationTrack. Scene, Group and Joint accept spatial children; Scene also accepts AnimationClip children, whose only children are AnimationTrack elements. Unknown properties fail; UUID-v7 refs are document-scoped. Scene is the single root; meshes have one material. Units are meters, right-handed Y-up; cameras and directional/spot lights face local -Z. Rotation is a normalized xyzw quaternion; nonuniform and negative nonzero scales are supported.

`registerGeometry` copies Float32 position/normal arrays, optional Float32 UV/tangents, and Uint32 triangle indices. Normals/tangents must be unit length, tangent xyz orthogonal to the normal, and tangent w is +1 or -1. UV0 uses glTF's top-left convention, repeat wrapping and linear filtering. Normal maps require UVs and tangents; any texture requires UVs. `registerTexture` accepts explicit regular local files or copied PNG/JPEG bytes. Handles cannot cross sessions. Binary arrays remain registered assets rather than React JSON props.

Materials support linear baseColor RGBA, metallic/roughness factors, emissive RGB, opaque/blend alpha and doubleSided. PNG/JPEG base-color/emissive maps use sRGB; metallic/roughness use linear red-channel scalar maps and normal maps use linear +Y tangent space. Factors are 0–1. Opaque materials require alpha factor 1 and ignore texture alpha. Packed GLB metallic/roughness maps require equal source dimensions. Separate AO, texture transforms, alternate UV sets and procedural runtime shaders are excluded.

Sessions expose render, synchronous inspect, settled snapshot, revision-pinned world AABB measure, Buffer/file export, diagnostics and idempotent disposal. Empty groups and camera/light-only targets have no mesh bounds. Inspect reports authored nodes as non-mountable; updates use React render. CLI/MCP export accepts matching .glb/.fbx extensions. MCP retains the same nine tools and protocol isolation.

## Animation authoring and evaluation

Both format entry points expose `AnimationPath` (Translation, Rotation, Scale,
Weights), `AnimationInterpolation` (Step, Linear, Cubic), and session-scoped
`AnimationSamplerHandle` assets. `registerAnimationSampler` copies Float32 time,
value and optional in/out tangent arrays at invocation. Times are finite,
nonnegative and strictly increasing seconds; values are key-major with three
components for translation/scale, four xyzw components for rotation, or one
component per morph target. Cubic uses at least two keys and matching derivative
arrays; the other modes reject tangents. Linear is the default. Rotation keys are
unit quaternions; evaluation uses shortest-path slerp or normalized cubic Hermite.
Scale curves cannot cross zero, and cubic quaternion singularities are rejected.

Named nonempty clips contain tracks targeting the same session's NodeHandle or
React object ref, resolved after the commit. Null, removed, foreign, non-spatial,
or duplicate target/property references fail. Clip names are unique, NUL-free and
at most 256 UTF-8 bytes. Inspection includes optional authored spatial names and non-mountable clip handles; names never resolve target identity. Clip
length is the largest last-key time. Each track clamps outside its own key range;
looping and automatic playback are consumer decisions.

Geometry optionally registers four Uint16 joint indices and four Float32 weights
per vertex, plus at most 64 named morph targets with dense position deltas and
optional normal deltas. Weights are nonnegative, sum to one within 1e-6 and cannot
repeat a nonzero joint influence. Joint indices address the ordered unique Joint
refs in `Mesh.skin.joints`. Zero-weight indices must also be valid. Names are
unique within geometry and follow the 256-byte clip-name bound. Mesh morphWeights
default to zero and must match the target count when provided. Morph tangent
animation is excluded. Native preparation derives inverse bind matrices from the
complete authored rest pose per mesh, applies morphs before linear blend skinning,
and never shares FBX deformers between independently bound mesh instances.
Skinned mesh TRS tracks fail: place mesh and skeleton in a Group for whole-rig
movement. The GLB exporter omits ignored skinned-node TRS while preserving its
rest transform in the bind matrices.

`bakeAnimationSampler({path,duration,fps,sample,components}, {signal})` executes
synchronous JavaScript callbacks only on the Node side, samples both endpoints,
and registers LINEAR data. Duration is finite and positive; fps defaults to 60
and is an integer from 1 to 240. Weights require components in 1–64. Reservations
precede callbacks/allocations; the loop yields every 128 samples and polls
cancellation before each callback. It does not interrupt a currently executing
synchronous callback or automatically rerun it. Failed/cancelled work releases
reservations. Exports wait for pending baking and registration work.

`measure(handle,{revision,animation:{clip,time}})` evaluates world AABBs from
transformed vertices at a finite time; omission evaluates the base pose including
base morph weights. Measurement never changes the revision. MCP keeps nine tools
and adds optional `{animation:{clip:clipNodeId,time}}` to measure for scenes only.

GLB emits standard embedded glTF animation/skin/morph data. FBX uses native
AnimationStack/Layer/Curve, LimbNode, Skin/Cluster, bind-pose and BlendShape records.
`createSession(Format.Fbx,{animationBakeFps})` defaults to 60, accepts integers
1–240, and bakes quaternion/CUBIC tracks with all original keys retained. STEP and
linear scalar curves retain their interpolation; Euler conversion follows the
existing camera/light basis adjustments and unwraps consecutive orientations.
FBX rotation/CUBIC values between baked samples are approximations. The complete
baked export is preflight-bounded, and sub-tick or overflowing FBX key times fail
instead of merging keys. Validate adjacent source-time intervals before baking
or tick rounding, even when rounding would produce distinct ticks. Runtime
generation requires no external converter.

The existing 64 MiB per-asset, 256 MiB aggregate/output and React tree limits also
cover skin, morph and sampler bytes. Per-mesh bind-palette expansion is independently capped at 256 MiB before allocation. Private FSG2 and FSA1 worker envelopes carry
binary data outside React JSON; FSG1 static geometry remains accepted. Native
validation and evaluation poll cancellation and emit only the existing redacted
stage/classification diagnostics, never callback data, names, or array contents.

## Engines and interoperability

GLB uses glTF 2.0 with embedded binary geometry/images and optional KHR_lights_punctual. FBX uses binary 7.4 via fbxcel, Y-up axes, meter units, embedded Video/Texture payloads and editable native meshes/materials. No Blender, Python, Autodesk SDK, network or converter is needed to generate either format. Dependencies retain their original license notices.

FBX materials target Blender 4.5's legacy importer: metalness uses ReflectionFactor, roughness maps to Shininess and the ShininessExponent texture connection, and standard diffuse/normal/emissive/opacity connections carry the remaining supported channels. Factors are baked into embedded FBX maps in the correct color space because the importer replaces connected values. This profile is not a promise of identical rendering in arbitrary FBX applications. GLB light intensity uses lux/candela; FBX retains that source value as a property and converts the imported Blender energy using the glTF SPEC 683 lm/W convention. Camera and light local axes are converted explicitly. FBX records mesh culling flags, but Blender 4.5 does not apply them and shades both sides; closed product surfaces provide the compared profile. GLB honors doubleSided in compatible viewers.

## Safety and lifecycle

Explicit scene renders enter the per-session operation queue. Concurrent render
calls commit independently in invocation order, including their layout effects;
an intervening export observes that position in the queue. Failed renders reject
their own calls and block queued exports until a later successful render, without
preventing the queue from accepting that recovery. File exports reserve their
place in both the session queue and the shared output-directory queue at
invocation. Waiting for either queue must not let later renders or file exports
from another session overtake the operation.

Registered assets and output are each capped at 256 MiB, individual geometry/texture payloads at 64 MiB, images at 64 million pixels, and the existing 16 MiB/20,000-node/depth-48 tree limits apply. Check malformed arrays, indices, numbers, transforms, references and resource budgets before output. Native cancellation checkpoints cover validation, traversal, texture conversion and serialization; bounded third-party decoding calls are indivisible. JavaScript never runs in workers.

Exports wait for relevant React work and registrations before pinning immutable model/assets. Existing directory-identity publication queues and same-filesystem atomic publication apply. Cancellation before publication preserves prior output; completed writes remain truthful. Sessions have no automatic timeout or recovery storage. Diagnostics contain only format, stage, revision, duration and stable classifications, never asset contents or host paths. Native scene measurement events and failures use the layout stage; document inspection retains import, and generation/update retains export. The Node bridge derives these stages from the operation and format rather than forwarding arbitrary native text.

## Validation

AURA's seven source PNG textures under `packages/react-forge/examples/audio-studio-assets`
are tracked by seven explicit file paths in the root `.gitattributes` using Git
LFS. Local source consumers run `git lfs install` and `git lfs pull` before example
validation; source-consuming CI and release build checkouts enable LFS. Keep the
texture generator, provenance, compact evidence, and TSX geometry in ordinary Git,
and generated GLB/FBX files and renders outside source control. Storage migrations
must preserve every texture's SHA-256 and size and verify actual hydrated bytes,
not only pointer syntax. The existing async-commit-hook execution limitation for
LFS repositories still applies; this does not add LFS support to that tool.

Require Khronos GLB validation, independent ufbx FBX parsing, and Blender re-imports with no material/mesh repair as local acceptance evidence. Product samples additionally require independent local web GLB viewing and actual image inspection. Evidence records exact tool versions, output hashes, structure checks and observed differences. React, installed CLI/MCP and existing document regressions remain part of the React Forge host matrix; the React scene test suite is skipped there. Scene engine tests/Clippy, interoperability, visual inspection, and render jobs are removed from React Forge CI and release host validation by the originating change. Render tools are local acceptance dependencies; generated exports, screenshots and dist are untracked. GLB and FBX were included in the published `@delino/react-forge` `0.2.0` release; the successful release workflow does not claim identical behavior in every FBX application.


Animation acceptance uses the original `examples/animated-character*.tsx` tasks:
seven explicit joints, three skinned meshes, an expression morph, idle/walk/wave,
callback sampling, manual cubic group motion, and camera/light tracks. Use
`scripts/verify-animation.mjs` for native/Khronos/Three.js comparisons,
`forge-fbx --example inspect_animation` for ufbx time evaluation, and
`scripts/inspect-animation.py` for Blender imports, sampled geometry, 2048px
start/middle/end and motion-extreme stills, plus compact playback videos. Inspect
the real artifacts in the local animation viewer. Keep generated assets outside
the checkout and commit only source and compact evidence. Local animated tests
and installed CLI/MCP animation cases honor `REACT_FORGE_SKIP_SCENE_TESTS`; no
hosted scene-validation gate is added. See the dated validation entry for the
actual executed host, exact versions, warnings and errors.

## Animation references

- [glTF 2.0 skin, morph and animation specification](https://registry.khronos.org/glTF/specs/2.0/glTF-2.0.html).
- [ufbx independent animation evaluation](https://ufbx.github.io/elements/animation/).

## Project requirements

- `react-forge`: affected source changes require Node 24 native, installed-CLI, document rendering and benchmark validation on Windows/glibc Linux x64/arm64 in ordinary PR/main CI; documentation-only changes do not select the native job. Manual CI and the exact-tag release also require macOS x64/arm64, using the same package-owned host validation command on both Darwin rows. Require real Windows console cancellation and macOS/Linux Office/PDF rendering. Retain existing Forge regression jobs when shared package primitives change. Native/system-font work is uncached, render evidence expires after seven days, and CI cannot publish packages or install production dependencies at runtime. Complete candidate assembly and npm publication belong to the exact-tag release workflow. Source-consuming CI and release build checkouts must hydrate the AURA PNG textures tracked by Git LFS; `.gitattributes` changes select React Forge validation. React Forge scene engine tests, React scene tests, interoperability and Blender visual/render checks remain local acceptance work.

- React Forge also owns `crates/forge-scene`, `crates/forge-glb` and `crates/forge-fbx` for generation-only GLB/FBX with bounded animation. Follow `packages-react-forge-scene-contract.md`; runtime generation remains local and converter-free.

## Application integration

- React Forge GLB/FBX public guides document availability from npm `0.2.0`. Keep the released static API separate from the unreleased animation extension, document FBX rotation/CUBIC sampling limits, and preserve Blender 4.5 compatibility limits; package availability does not establish identical results in every FBX application.

## Rust component integration

- `forge-scene`, `forge-glb` and `forge-fbx` own bounded 3D scene/animation validation and converter-free GLB/FBX generation under `packages-react-forge-scene-contract.md`. Use cancellation checkpoints, preserve meter/Y-up semantics, embed textures, and verify FBX independently with ufbx. Never copy GPL exporter code into repository-owned engines.

- FBX animation export must reject adjacent source-key intervals shorter than one FBX tick before baking or rounding; distinct rounded ticks do not prove a representable source interval.

## Package integration

- React Forge GLB/FBX scene and animation sessions follow `packages-react-forge-scene-contract.md`; preserve the shared React, atomic export and MCP lifecycle while keeping the 3D model independent from Office/PDF.

## packages/react-forge constraints

- GLB/FBX creation and animation follow `packages-react-forge-scene-contract.md`. Keep binary geometry outside React JSON, 3D bounds separate from 2D geometry, aggregate admission bounded, and source assets immutable across pinned exports. Blender/web viewers are verification tools only; never repair imported validation assets to conceal exporter faults.

- Native scene measurement diagnostics and errors use the layout stage; preserve document import and export stages when adapting native events, using trusted format/operation metadata.

- Queue explicit scene renders with the other session operations so concurrent calls commit independently in invocation order. A later recovery render must not hide an earlier render failure or let an intervening export bypass it. File exports must reserve both the session operation queue and the shared output-directory queue at invocation, before waiting on either.

- Keep AURA's original procedural geometry, deterministic texture sources, provenance and GLB/FBX profile probes together. Scene verification uses pinned Blender 4.5.14, independent Khronos/ufbx checks and actual-file imports; normalize only importer-invented labels when comparing hierarchy. Keep complete rendering artifacts separate from compact committed evidence, and mark unexecuted hosts and unreleased APIs honestly.

- Track every `examples/audio-studio-assets/*.png` source texture in Git LFS using explicit file paths in the root `.gitattributes`. Hydrate them before scene or installed-example validation, keep their decoded bytes unchanged during storage migrations, and retain the deterministic generator and provenance as ordinary Git source. Generated models and renders remain untracked.

- AURA quality revisions retain prior acceptance records as historical evidence. Keep fine surface details in the exported geometry/textures, verify winding and tangent frames for new procedural meshes, and record per-image dimensions when combining 4K hero views with 2048px inspection views. Presentation lighting must remain separate from imported product materials.

- Scene preparation alone is not completed visual evidence. Static AURA acceptance renders compare both GLB and FBX across all four views, reject incomplete/duplicate or changed artifacts and images, and retain the 2048px minimum before producing comparison reports.

- The shared `scripts/validate-host.sh` native test and Clippy gates exclude the scene, GLB and FBX engines on each selected CI or release host. Hosted React Forge validation skips `tests/scene.test.tsx`, `tests/scene-animation.test.tsx` and animated installed-consumer cases; generic Rust workspace gates also exclude the three scene crates. Keep engine tests, Blender scene preparation, interoperability and product rendering as local acceptance evidence.

- Scene animation follows the scene contract: register binary samplers/skin/morph data, resolve document-scoped refs after React commits, keep JavaScript baking outside native workers, and preserve revision-pinned animated bounds. Validate generated clips with Khronos, independent ufbx deformation, and Blender 4.5.14 locally. Keep character start/middle/end and motion-extreme stills at 2048px, with independent playback and sampled bounds; compact playback videos may use lower resolution. Mark animation unreleased until publication; keep static format availability since 0.2.0.
