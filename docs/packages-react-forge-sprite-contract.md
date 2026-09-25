# React Forge Sprite Contract

## Scope
`packages/react-forge/src/sprite.ts` owns the `/sprite` components; `crates/forge-sprite` owns the independent pixel model and renderer. The existing session, N-API, CLI and MCP interfaces expose `Format.Sprite`. This extension is unreleased and does not change the original Office/PDF or Figma requirements.

## Runtime and Language
Use the existing Node.js 24, React 19.2.8 and six-host Rust native matrix. JavaScript runs React; Rust performs bounded rasterization without a browser, GPU, system fonts or external converters.

## Users and Operators
Developers and trusted local MCP tasks creating reusable game sprites and frame animations from React components or registered local PNG/JPEG assets.

## Interfaces and Contracts
`SpriteProject` declares integer logical `width`/`height`, optional integer `scale` (default 1), `columns` (default ceil(sqrt(frame count))), output-pixel `padding` (default 1), and an optional palette. It contains named `Animation` elements (`loop` defaults true), each with one or more `Frame` elements (`durationMs` required, optional logical-pixel `pivot`, default bottom center). Frames contain `Layer`, `Pixel`, `Rect`, `Ellipse`, `PixelGrid` and `Image`. Layers translate integer coordinates and optionally hide their children. Drawing order follows React child order. Coordinates may be negative; raster output clips to the logical frame. Leaves reject children and unsupported props are errors.

Colors are explicit `#RRGGBB` or `#RRGGBBAA` values with straight alpha. Palette keys are one printable ASCII character except `.`; `PixelGrid.rows` are equal-width ASCII strings, with `.` transparent and all other characters resolved through the project palette. Images use explicit destination dimensions, optional in-bounds integer source crop and horizontal/vertical flips. Sampling and output enlargement use nearest neighbor. Shapes use hard pixel edges, source-over alpha compositing and no antialiasing. There is no implicit color quantization, generated artwork service, rigging, trimming, atlas rotation or source-format import.

Exports are deterministic ZIP bundles (`.sprite.zip`) containing `sheet.png`, `sprite.json` and `frames/0000.png` onwards. Metadata uses the Aseprite JSON-array frame fields (`filename`, `frame`, `rotated`, `trimmed`, `spriteSourceSize`, `sourceSize`, `duration`) and `meta.image`, `meta.size`, `meta.scale`, `meta.frameTags`. Additional `meta.reactForge` version/animation loop information and frame `pivot` preserve Forge semantics. All metadata geometry is in scaled output pixels; `measure` reports logical pixels in `sprite_frame` coordinates with `page` identifying the zero-based flattened frame index. Frames and drawing leaves have geometry; projects, animations and grouping layers do not. No engine-specific importer compatibility is claimed without direct evidence.

Snapshot, refs, React updates/Suspense, diagnostics, revision pinning, cancellation and disposal reuse `DocumentSession`. Import and mounted editing of exported sprite archives are unsupported; update the React source and export again. CLI and MCP export validate `.sprite.zip`; library file exports retain the existing caller-selected-path behavior.

## Storage
Memory-only sessions and explicit local export reuse the project's local-storage exception. One atomic archive publication keeps PNGs and JSON on the same revision. Output conflicts, explicit overwrite, directory serialization and cleanup retain the existing file contract. User extraction is separate from publication.

## Security
Limit logical dimensions to 1–4096, scale to 1–16, padding to 0–64, frames to 1024, frame durations to 1–60,000 ms, and names to 1–64 ASCII letters/digits/underscore/hyphen. Columns must be 1–frame count. Limit both the sheet and total scaled frame pixels to 64,000,000; decoded referenced images to 64,000,000 pixels in aggregate; visited raster pixels to 256,000,000. Apply existing 16 MiB/48-depth/20,000-node tree, image-byte and package limits. Validate hidden nodes too. Reject fractional/non-finite dimensions, unknown props, malformed palettes, missing/foreign assets and duplicate identities before publication. Archive paths are generated, never derived from user labels. No network or credentials are added.

## Logging
Reuse structured JavaScript/native operation, stage, format, revision, duration, status and stable error codes. Never log palette contents, pixel rows, image bytes, animation names or host paths.

## Build and Test
Run root `cargo test`, targeted native renderer and Forge regression tests, package build/typecheck/lint/test, standalone examples and installed CLI/MCP coverage. Verify decoded pixels, compositing, clipping, crop/flips, scaling, transparent padding, timings, pivots, geometry, limits, invalid latest renders, cancellation and atomic output behavior. Generate and visually inspect the committed sprite example's output; generated archives/previews remain untracked. Six-host CI includes the new engine; local evidence must identify the actual host.

## Dependencies and Integrations
Reuse the pinned `image` crate for PNG/JPEG decoding and PNG encoding, `forge-package` for bounded deterministic ZIP output and `forge-tree-doc` cancellation/validation. `react-forge-node` remains the adapter. The PNG/JSON export pattern follows [Aseprite's CLI documentation](https://www.aseprite.org/docs/cli/); Aseprite is not a runtime dependency.

## Change Triggers
Update the project/native/Node/MCP contracts, capabilities, package exports, examples, CI, relevant AGENTS rules and validation evidence together. Public guides describe the feature only when released.

## References
- [Project](project-react-forge.md).
- [Node sessions](packages-react-forge-contract.md).
- [Native engines](crates-react-forge-contract.md).
- [Repository defaults](repository-defaults.md).
