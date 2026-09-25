# Limits and troubleshooting

Start with `ForgeError.code` when an operation fails, then use the recovery table below. The installed package exposes its current budgets through `limits` and `capabilities`. A resource-limit or unsupported-package error is explicit; React Forge does not silently flatten content or download a replacement dependency.

| Resource | Ceiling |
| --- | ---: |
| Office input/output and PDF output | 256 MiB |
| Expanded Office package | 512 MiB |
| ZIP entries / individual part | 10,000 / 64 MiB |
| XML depth / nodes per part | 128 / 1,000,000 |
| Newly rendered React tree | 16 MiB, depth 48, 20,000 nodes |
| Individual image | 64 MiB and 64 million pixels |
| Registered assets together | 256 MiB |

An explicit font is bounded to 64 MiB. Existing PPTX constraints include 1,000 slides and tables up to 1,000 rows by 128 columns. Chart expansion is bounded to 200,000 cells and XLSX merge expansion to 250,000 cells. Figma images are limited to 10 MiB and 64 million pixels each. Check the installed package's `limits` and `capabilities` for the applicable full set.

The unreleased [SFX](/react-forge/formats/sfx/#sessions-mcp-and-limits) and [Sprite](/react-forge/formats/sprite/#limits-and-boundaries) previews have their own bounds. They are absent from npm `0.1.1`; do not use their source-only APIs with that installed version.

## Common failures

| Symptom or code | Recovery |
| --- | --- |
| `unsupported_package` or missing native package | Verify Node.js 24, supported OS/CPU/glibc, and enabled optional dependencies; reinstall the matching package version. |
| `missing_font` | Install a font covering the required characters or call `registerFont()`; check the consuming Office environment too. |
| `invalid_target` or `unsupported_edit` | Inspect the imported file again, choose a supported nonoverlapping region, and export to a path distinct from the source. |
| `conflict` or existing output | Choose a new output path or explicitly request overwrite of a separate destination. Reimport after an external source change. |
| `resource_limit` or `layout_overflow` | Reduce document, image, or indivisible layout size; inspect `ForgeError.code` and safe stage context. |
| `cancelled`, `disposed`, or `unknown_outcome` | Inspect any published local file or Figma receipt before retrying; cancellation cannot reverse completed publication. |

`ForgeError.code` separates malformed input, unsupported edits, target conflicts, resource limits, missing fonts, overflow, cancellation, I/O, and React-render failures. Diagnostics provide operation/stage, revision, duration, and stable classification without source text, XML, bytes, credentials, or host paths. Subscriber failures do not change session outcomes. Do not send private document content when reporting a problem through [GitHub issues](https://github.com/delinoio/oss/issues).

## Boundaries

Local document work has no hosted service, telemetry, URL fetching, automatic recovery, runtime converter, or automatic timeout. Figma is the explicit remote exception and has [separate receipt and retry rules](/react-forge/formats/figma/#publication-outcomes-and-receipts). Integrators are responsible for authenticating their own callers and governing process-wide resources; task code is trusted and runs with caller permissions.

## Unreleased static 3D extension

[GLB](/react-forge/formats/glb/) and [FBX](/react-forge/formats/fbx/) creation are documented as an unreleased extension, absent from npm 0.1.1. They use SceneSession with registered geometry/textures, explicit file export, and revision-bound world-space bounds. Existing-file editing, animation and rigging are excluded. The FBX material profile targets Blender 4.5; local verification does not establish results on other hosts or FBX applications.
