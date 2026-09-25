# Releases and validation

`@delino/react-forge` is public on npm. Version `0.1.1` is its first functional release; `0.0.1` only reserved package names, and `0.1.0` was not published. Check the [npm package](https://www.npmjs.com/package/@delino/react-forge) for the current dist-tag before installing or pinning a version. The main package uses matching native optional packages for macOS, Windows, and glibc Linux on x64 and arm64. Keep optional dependencies enabled and versions matched; there is no runtime binary download or compile step.

## Choose by availability

| Workflow | Availability |
| --- | --- |
| Local PPTX, DOCX, XLSX, PDF, CLI, and MCP | Published in `0.1.1` for the six supported native hosts. |
| Figma creation and editing | Published in `0.1.1`; live authentication has the narrower macOS arm64 validation described below. |
| [GLB](/react-forge/formats/glb/) and [FBX](/react-forge/formats/fbx/) | Unreleased source previews, absent from npm `0.1.1`. |
| [Game SFX](/react-forge/formats/sfx/) and [pixel sprites](/react-forge/formats/sprite/) | Unreleased source previews, absent from npm `0.1.1`. |

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

## Unreleased static 3D extension

[GLB](/react-forge/formats/glb/) and [FBX](/react-forge/formats/fbx/) creation are documented as an unreleased extension, absent from npm 0.1.1. They use SceneSession with registered geometry/textures, explicit file export, and revision-bound world-space bounds. Existing-file editing, animation and rigging are excluded. The FBX material profile targets Blender 4.5; local verification does not establish results on other hosts or FBX applications.

## Unreleased SFX extension

Procedural game SFX authoring and PCM WAV export are implemented for the next release, but are absent from npm `0.1.1`. The [SFX guide](/react-forge/formats/sfx/) describes the planned public API and a zombie-game gunshot example. SFX has separate synthesis and CLI/MCP checks; the earlier six-host document release is not SFX acceptance evidence. No game-engine listening evaluation is claimed.

## Unreleased Sprite extension

Pixel sprite authoring and `.sprite.zip` export are implemented in source but absent from npm `0.1.1`. The [Sprite guide](/react-forge/formats/sprite/) previews the API, archive contents, and resource limits. The earlier six-host document release is not Sprite acceptance evidence, and no game-engine importer compatibility is claimed. Check the current npm dist-tag and package capabilities before using the preview's imports.
