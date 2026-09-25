# FBX static scenes — unreleased

FBX authoring is an unreleased extension and is not included in npm `0.1.1`. It supports creation and export of static scenes using `createSession(Format.Fbx)` and the `@delino/react-forge/fbx` entry point. Existing FBX import/editing, animation, rigging, and advanced shaders are excluded.

Use the same components, registered geometry/textures, transforms, measurement, and lifecycle as the [GLB guide](/react-forge/glb). Change the format to `Format.Fbx`, import the components from `@delino/react-forge/fbx`, and export to a `.fbx` destination. The result is binary FBX 7.4 with embedded textures. Generation needs no Blender, Autodesk SDK, or conversion process.

## Compatibility profile

The supported material profile targets Blender 4.5 LTS. It carries base color, metallic, roughness, normal, emissive, and opacity through the importer's legacy material properties and texture connections. Color and scalar factors are baked into embedded texture copies where the importer replaces connected values. Original registered bytes stay unchanged. Assets use meters and right-handed Y-up; camera/light axes and UV orientation are converted by the exporter.

FBX applications do not agree on all material conventions. Check the actual exported file in your target application; identical shading in arbitrary FBX consumers is not promised. Basic transparency is supported; refraction, transmission, clearcoat, and layered/procedural materials are not. Unknown or unsupported authoring properties fail rather than disappearing silently.

The accompanying GLB is useful for comparison because glTF defines a standard metallic-roughness model. Local acceptance checks use Blender 4.5.14 LTS for both exported formats, and an independent web viewer for GLB. These checks do not establish validation on every operating system or every FBX application. See [release and validation status](/react-forge/releases).

Blender 4.5 ignores FBX mesh culling flags and shades both sides. Use closed surfaces when matching GLB appearance; a single-sided open surface can look different from its GLB counterpart.
