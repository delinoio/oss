# AURA audio studio

Original fictional product family: H01 over-ear headphones, D01 desktop DAC/amplifier, and S01 stand. `audio-studio.tsx` composes reusable TSX parts from deterministic geometry and PNG textures. All code, bitmap glyphs, textures, shapes, and branding were authored for this repository under Apache-2.0. No downloaded models, fonts, photography, or third-party artwork are included; there are no external asset attributions to reproduce. AURA is a concept label, not an affiliation or manufacturing claim.

`geometry.ts` constructs beveled boxes, machined housings with quarter-circle bevels, sculpted oval leather pads, capped elliptical bands, stitched seams, integral encoder flutes, and a closed lid with 36 rounded through-slots. Its normals follow the displaced surfaces, and geometry includes UVs, tangents, and indexed triangles. The thin-plate bevel is clamped to the smallest half-extent. Triangle winding and tangent frames have independent regression coverage.

`textures.mjs` deterministically regenerates seven source PNGs: 2048px brushed-metal roughness/normal and cellular leather roughness/normal, a 1024px woven-fabric normal map, a 2048×768 OLED display, and a 2048px transparent engraving atlas. The antialiased monoline alphabet is original vector-path code, without a font dependency. Run `node examples/audio-studio-assets/textures.mjs` from the package directory to reproduce the PNG bytes.

The refined family uses champagne anodized aluminum, satin graphite, leather and woven acoustic liners. H01 includes a supported adjustment mechanism, stitched padding, pivot hardware, channel engravings and cable sockets. D01 includes an OLED volume readout, a 112-flute encoder, balanced/6.35 mm outputs, USB-C/RCA/DC connectors, a perforated lid with recessed dust screens and side cooling ribs. S01 includes a leather saddle, structural spine, base pad, mounting screws and etched identification. These are fictional industrial-design studies, not manufacturing drawings or electrical specifications.

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

For the refined presentation, use `test:scenes --output <directory> --device METAL --samples 256 --hero-resolution 4096`. The other views remain 2048×2048; the studio hero is 4096×2731 and the individual heroes are 4096×4096. Softbox lighting, a seamless backdrop, AgX and a fixed seed are shared by both importers. The local gallery switches between GLB/FBX hero, rear and detail renders and the interactive GLB. Its 3D view adds only a studio environment, lights and a shadow-catching floor; it does not replace the imported materials. If renders are absent, the gallery reports that explicitly and the interactive mode remains available. Retain the initial evidence as a historical baseline and record quality revisions separately. Generated comparison sheets and rendered images are evidence, not substitutes for the actual GLB/FBX geometry.
