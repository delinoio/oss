# Game sound effects (WAV)

**Unreleased:** SFX generation is implemented for the next release and is not
included in npm `0.1.1`. Check [release status](/react-forge/releases) before using
these imports with an installed version.

Use `Format.Wav` and components from `@delino/react-forge/sfx` to compose procedural
game effects. Export a WAV buffer or file for your game engine. Generation is
offline and does not need recordings, audio devices, fonts or a conversion tool.

## A zombie-game gunshot

Save this as `gunshot.tsx` with a version that supports SFX:

```tsx
import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Sound, Noise, Tone, SampleRate } from "@delino/react-forge/sfx";

export default async function task() {
  const session = createSession(Format.Wav);
  await session.render(
    <Sound duration={0.75} sampleRate={SampleRate.Game}>
      <Noise duration={0.095} seed={815} gain={0.85}
        attack={0.0003} release={0.025} decay={0.017}
        highPass={1100} lowPass={15000} />
      <Noise start={0.002} duration={0.21} seed={816} gain={1.4}
        attack={0.001} release={0.06} decay={0.044} lowPass={1800} />
      <Tone duration={0.17} frequency={155} endFrequency={48}
        gain={0.8} attack={0.0008} release={0.05} decay={0.032} />
      <Noise start={0.075} duration={0.035} seed={817} gain={0.14}
        attack={0.0005} release={0.01} decay={0.009}
        highPass={2400} lowPass={11000} />
      <Noise start={0.022} duration={0.58} seed={818} gain={0.24}
        attack={0.006} release={0.18} decay={0.13}
        highPass={160} lowPass={3200} />
    </Sound>,
  );
  return session;
}
```

```sh
react-forge run gunshot.tsx --output zombie-gunshot.wav --json
```

The layers provide a sharp crack, low body, falling tone, mechanical click and
short room tail. Changing the noise seeds gives variations. This is original
procedural game audio, not a recording or a claim of realistic weapon acoustics.
Exported audio is 16-bit PCM WAV. Listen in your target game's mix to choose the
appropriate gain. Files are only generated on explicit export; nothing plays
automatically.

## Components and parameters

| Component | Parameters |
| --- | --- |
| `Sound` | Required `duration`; optional `sampleRate`, `channels`, `gain` |
| `Noise` | Common layer parameters, required `seed`, optional `lowPass` and `highPass` in Hz |
| `Tone` | Common layer parameters, required `frequency`, optional `endFrequency` and `waveform` |

Layer parameters are required `duration`, optional `start`, `gain`, `pan`,
`attack`, `release` and `decay`. Times are seconds and gain is linear. Each
layer must fit entirely inside its Sound. Noise and Tone are leaves; use ordinary
React components and fragments to reuse or repeat layers.

Defaults are `SampleRate.Game` (48000 Hz), `Channels.Mono`, unity gain and start
zero. `SampleRate.Cd` selects 44100 Hz. `Channels.Stereo` enables equal-power pan
from -1 (left) to 1 (right); mono requires pan zero. Gains range from zero to four.
Tone defaults to `Waveform.Sine`; `Waveform.Triangle` is also available. Tone
frequency sweeps linearly to `endFrequency` when supplied.

Attack and release provide linear fades. They default to 1 ms and 10 ms, each
capped at one quarter of the layer and floored at one sample. Each must be at
least one sample and together must fit the layer. Optional `decay` is an
exponential amplitude time constant from one sample to 30 seconds. The first
and last sample of each layer are zero. Peaks above 0.95 are attenuated together
without boosting quiet sounds or changing stereo balance.

Seeds are integers from 1 through 4294967295. Noise filters and tone frequencies
range from 20 Hz to one Hz below half the sample rate. When both filters are used,
`highPass` must be below `lowPass`. Repeatable seeds reproduce the same sound on
the same runtime and host; byte identity across platforms is not guaranteed.

## Sessions, MCP and limits

Use the common [session APIs](/react-forge/sessions), [CLI](/react-forge/cli) or
[MCP tools](/react-forge/mcp). MCP execution accepts the same TSX task; inspect
its session and call `react_forge_export` with an output ending in `.wav`.
`measure` returns `timeline` geometry: x is the sample-rounded start and width
is duration in seconds, with y zero and height one. A changed revision invalidates
an earlier measurement.

Sounds last from two samples to 30 seconds, with one to 256 layers. The sum of
layer durations in samples cannot exceed 16 million. `capabilities` exposes
these limits. Invalid latest renders fail export; cancellation and failed
exports preserve existing destinations. Existing files require explicit
overwrite. Library callers should dispose sessions in `finally`.

SFX supports generation only. Audio import, recorded samples, MP3/OGG, MIDI,
microphone capture and live playback are not supported. WAV sessions reject
image and font registration. Six-host document validation does not by itself
establish SFX validation on those hosts; see [validation scope](/react-forge/releases).

For animated pixel artwork instead of audio, see the separately unreleased
[Sprite preview](/react-forge/formats/sprite/).
