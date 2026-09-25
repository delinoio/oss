# AURA audio studio

Original fictional product family: H01 over-ear headphones, D01 desktop DAC/amplifier, and S01 stand. `audio-studio.tsx` composes reusable TSX parts from deterministic geometry and PNG textures. All code, bitmap glyphs, textures, shapes, and branding were authored for this repository under Apache-2.0. No downloaded models, fonts, photography, or third-party artwork are included; there are no external asset attributions to reproduce. AURA is a concept label, not an affiliation or manufacturing claim.

`geometry.ts` constructs beveled boxes, lathed housings, rings, capped elliptical bands, and planes with explicit normals, UVs, tangents, and indexed triangles. `textures.mjs` deterministically regenerates the five source PNGs: brushed-metal roughness, leather roughness/normal, emissive display, and identity mark. The display's bitmap alphabet is original code. Run `node examples/audio-studio-assets/textures.mjs` from the package directory to reproduce the PNG bytes.

After building, run from the repository root:

```sh
pnpm --filter @delino/react-forge example:scenes --output /tmp/aura
pnpm --filter @delino/react-forge cli run examples/audio-studio.tsx --data '{"format":"fbx","product":"headphones"}' --output /tmp/headphones.fbx
REACT_FORGE_BLENDER=/path/to/blender pnpm --filter @delino/react-forge test:scenes --output /tmp/aura-verified
node packages/react-forge/scripts/scene-viewer.mjs --input /tmp/aura-verified
```

Products are `studio`, `headphones`, `dac`, and `stand`; format is `glb` or `fbx`. The CLI defaults to the studio GLB. Separate sample tasks `scene-glb.tsx` and `scene-fbx.tsx` demonstrate the small public API. The generator emits eight files plus hashes, Khronos results, and revision-bound measurements. UUID-v7 identities differ between sessions; geometry and source textures are deterministic, not the entire fresh-session file hash.

The verification harness pins Blender 4.5.14 LTS, independently parses each FBX with ufbx, reimports each file into an empty scene, checks world bounds, and renders four views at 2048×2048. A test-only studio adds lights, backdrop, and camera without changing imported meshes or materials. The loopback viewer at port 46319 bundles pinned Three.js locally and loads the exact GLBs; drag or use its rotation/zoom controls. The port fails on conflicts. Generated models, renders, browser captures, and reports stay outside source control. Review exact versions, hashes, observations, and unexecuted-platform limits in the committed scene evidence and the internal validation contract.

Use `--device METAL` with `test:scenes` for an explicitly selected macOS GPU; CPU is the default and the report records the device. `python scripts/compare-scenes.py <output>` uses the pinned test Pillow dependency to produce GLB/FBX contact sheets and observational pixel differences. The profile fixture additionally covers negative/nonuniform parent scale, opacity, both camera projections and all three light types. Compare actual world vertices because Blender applies glTF/FBX axis conversion at different hierarchy levels.
