# Releases and validation

`@delino/react-forge` is public on npm. Version `0.1.1` was its first functional release; `0.0.1` only reserved package names, and `0.1.0` was not published. Version `0.2.0` added GLB, FBX, SFX, and Sprite, so every format documented here is now available from the package. Check the [npm package](https://www.npmjs.com/package/@delino/react-forge) for the current dist-tag before installing or pinning a version. The main package uses matching native optional packages for macOS, Windows, and glibc Linux on x64 and arm64. Keep optional dependencies enabled and versions matched; there is no runtime binary download or compile step.

## Choose by availability

| Workflow | Availability |
| --- | --- |
| Local PPTX, DOCX, XLSX, PDF, CLI, and MCP | Available since `0.1.1` for the six supported native hosts. |
| Figma creation and editing | Available since `0.1.1`; live authentication has the narrower macOS arm64 validation described below. |
| [GLB](/react-forge/formats/glb/) and [FBX](/react-forge/formats/fbx/) | Available since `0.2.0` for the six supported native hosts. |
| [Game SFX](/react-forge/formats/sfx/) and [pixel sprites](/react-forge/formats/sprite/) | Available since `0.2.0` for the six supported native hosts. |

```sh
npm install @delino/react-forge
npm list @delino/react-forge
```

## Validation scope

The first functional release passed installed-consumer checks across the six native hosts, including local format exports, imports, CLI, and MCP usage. Automated tests cover React session behavior, preserved Office edits, limits, cancellation, fonts, and PDF semantics. Test renderers are verification tools, not runtime dependencies. There is no numerical latency, memory, or throughput guarantee.

Office interoperability evidence uses structural checks and LibreOffice/Poppler rendering on tested hosts. It is **not** direct Microsoft Office validation. Tagged PDF output has semantic structure checks but **not** PDF/UA certification. Figma creation and preservation-aware editing have macOS arm64 live acceptance plus simulated transport coverage; other local-document hosts do not imply live Figma authentication support there.

## Update or roll back

Pin a verified npm version in your project and install its matching optional native package. To roll back, select an earlier **functional** published version, reinstall, and verify `npm list @delino/react-forge` before running tasks. Do not select the name-reservation `0.0.1` or unpublished `0.1.0`. Existing exported documents remain separate files; React Forge does not migrate or replay an in-memory session after a package change.

See [Installation](/react-forge/installation) for supported hosts and [limits and troubleshooting](/react-forge/limits-and-troubleshooting) for recoverable errors. Report reproducible problems through [Delino OSS issues](https://github.com/delinoio/oss/issues).

## Static 3D formats

[GLB](/react-forge/formats/glb/) and [FBX](/react-forge/formats/fbx/) creation are available starting in npm `0.2.0`. They use SceneSession with registered geometry/textures, explicit file export, and revision-bound world-space bounds. The released static API excludes existing-file editing, animation and rigging. The FBX material profile targets Blender 4.5; local visual checks do not establish identical results in other FBX applications.

## Game SFX

Procedural game SFX authoring and PCM WAV export are available starting in npm `0.2.0`. The [SFX guide](/react-forge/formats/sfx/) describes the public API and a zombie-game gunshot example. The release workflow exercises installed consumers on all six supported native hosts. No game-engine listening evaluation is claimed.

## Pixel sprites

Pixel sprite authoring and `.sprite.zip` export are available starting in npm `0.2.0`. The [Sprite guide](/react-forge/formats/sprite/) describes the API, archive contents, and resource limits. The release workflow exercises installed consumers on all six supported native hosts. Game-engine importer compatibility is not claimed. Check the current npm dist-tag and package capabilities before using these imports.

## 3D animation extension: unreleased

Object keyframes, joints/skinning, morph targets, callback baking and clip/time
measurement are implemented but have not been published to npm. This does not
change GLB/FBX static availability since `0.2.0`. Local acceptance covers Khronos
zero-error GLB validation, web playback, ufbx and Blender 4.5.14 deformation and
rendering of an original three-clip character. FBX rotation/CUBIC export remains
a sampled approximation. No new cross-platform animation acceptance, Unity/Unreal
validation or npm publication is claimed.
