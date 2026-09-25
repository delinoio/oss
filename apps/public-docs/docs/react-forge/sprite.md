# Pixel sprites and animation

**Unreleased:** Sprite authoring is implemented in source but is not included in npm `0.1.1`. Check [release status](/react-forge/releases) and the installed package's capabilities before using these imports. The example below requires a version that includes Sprite.

Use `Format.Sprite` and components from `@delino/react-forge/sprite` to draw pixel artwork and timed frames with React. Export one `.sprite.zip` archive containing a sprite sheet, individual frame PNGs, and JSON metadata. Generation is local and needs no browser, GPU, system fonts, network service, or external converter.

## Create an animated sprite

Save this task as `hero.tsx` with a version that supports Sprite:

```tsx
import React from "react";
import {
  createSession,
  Format,
} from "@delino/react-forge";
import {
  Animation,
  Frame,
  PixelGrid,
  SpriteProject,
} from "@delino/react-forge/sprite";

export default async function task() {
  const session = createSession(
    Format.Sprite,
  );
  try {
    await session.render(
      <SpriteProject
        width={16}
        height={16}
        scale={4}
        palette={{ R: "#e85d75" }}
      >
        <Animation name="idle">
          <Frame durationMs={200}>
            <PixelGrid
              x={6}
              y={6}
              rows={[".RR.", "RRRR", ".RR."]}
            />
          </Frame>
          <Frame durationMs={200}>
            <PixelGrid
              x={6}
              y={5}
              rows={[".RR.", "RRRR", "RRRR"]}
            />
          </Frame>
        </Animation>
      </SpriteProject>,
    );
    return session;
  } catch (error) {
    await session.dispose();
    throw error;
  }
}
```

```sh
react-forge run hero.tsx --output hero.sprite.zip
```

The CLI exports and disposes the returned session. With a supporting version, the archive contains `sheet.png`, `sprite.json`, and `frames/0000.png` onwards. Extract it to use the PNGs. The JSON records frame rectangles, durations in milliseconds, pivots, animation tags, and loop flags. Its frame layout follows the Aseprite JSON-array fields, but automatic compatibility with a game-engine importer has not been verified. Exports are explicit and revision-pinned; an existing destination requires explicit overwrite.

## Draw and animate

`SpriteProject` sets logical canvas dimensions, output scale, atlas columns, padding, and an optional palette. Named `Animation` elements contain one or more `Frame` elements; frames set `durationMs` and may set a pivot. Animations loop by default. `Layer` translates or hides its children. `Pixel`, `Rect`, `Ellipse`, and `PixelGrid` draw in React child order. Use `#RRGGBB` or `#RRGGBBAA` colors; a grid uses equal-width ASCII rows, palette characters, and transparent `.` dots.

Register a local PNG/JPEG with the [session asset API](/react-forge/sessions#fonts-and-assets) before passing its handle to the Sprite `Image` component. Set its destination width and height; an optional integer source crop and `flipX`/`flipY` control sampling. Drawing clips to the logical frame, and enlargement uses nearest-neighbor pixels without antialiasing. Hidden nodes still need valid props. Unsupported props and nonempty children on drawing leaves fail instead of being ignored.

## Limits and boundaries

Logical width and height are each 1–4096 pixels; scale is 1–16 and output-pixel padding is 0–64. Up to 1,024 frames share one canvas size, and each frame lasts 1–60,000 ms. Both the sheet and total scaled frames are capped at 64 million pixels; referenced decoded images are capped at 64 million pixels in aggregate, and raster work at 256 million visited pixels. Existing tree, image-byte, and archive limits also apply. See a supporting version's `capabilities.formats.sprite.limits` for the complete bounds.

Sessions, snapshots, React updates, cancellation, diagnostics, and disposal follow the [common session API](/react-forge/sessions). The [CLI](/react-forge/cli) and [MCP](/react-forge/mcp) use the same task and `.sprite.zip` output. Measurements use logical pixels in `sprite_frame` coordinates, with `page` identifying the zero-based flattened frame index; metadata coordinates use scaled output pixels.

Sprite supports generation only. To change an archive, update the React source and export again; archive import and mounted editing are unavailable. There is no automatic artwork generation, rigging, trimming, atlas rotation, or confirmed engine-specific importer compatibility. Sprite validation is separate from the published Office/PDF six-host evidence; see [Releases and validation](/react-forge/releases).
