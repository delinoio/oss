# React Forge SFX contract

## Scope and ownership

The explicit SFX follow-up adds offline procedural game sound generation and WAV
export to React Forge. `packages/react-forge` owns `/sfx` components, model
lowering, sessions, CLI/MCP integration and the zombie-game gunshot example.
`crates/forge-sfx` owns an independent serializable sound model, validation,
synthesis and PCM encoding. `crates/react-forge-node` runs it on the existing
native worker boundary. Existing Office/PDF and Figma contracts remain intact.

SFX was not part of npm `0.1.1`; it was included in the published npm `0.2.0`
release. Do not claim game-engine listening evaluation from package availability
or six-host installed-consumer checks.

## Runtime and users

Use the existing Node.js 24, React 19.2.8 and six native macOS/Windows/glibc Linux
x64/arm64 targets. Developers compose reusable React sounds without an audio
device, external recording, synthesis service or converter. Native synthesis
never executes React or caller callbacks and performs no file or network I/O.

## Public interface

`createSession(Format.Wav)` returns the existing `DocumentSession`.
`@delino/react-forge/sfx` exports `Sound`, `Noise`, `Tone`, their prop types,
`SampleRate.Cd` (44100), `SampleRate.Game` (48000), `Channels.Mono` (1),
`Channels.Stereo` (2), and `Waveform.Sine`/`Waveform.Triangle`.

- Exactly one `Sound` contains one to 256 leaf `Noise`/`Tone` nodes. React
  components, fragments, hooks, refs and Suspense follow the existing reconciler.
  Unsupported nesting, text, properties and null/nonfinite numeric values fail.
- Sound duration is explicit, from two samples through 30 seconds. Defaults are
  48000 Hz, mono and unity master gain. Sound and layer gains are linear 0–4.
- Each layer has required duration and optional start (default zero), gain
  (one), pan (zero), attack, release and decay. Times are seconds. The full layer
  must fit within the sound; start and length round to nearest samples, with the
  final frame bounded by the sound. Decimal boundary comparisons tolerate 1 ns.
- Attack/release are linear edge fades, each at least one sample and jointly no
  longer than the layer. Defaults are 1 ms/10 ms, each capped at a quarter of
  layer duration and floored at one sample. Optional decay is an exponential
  amplitude time constant, between one sample and 30 seconds. Both endpoint
  samples are zero. Output outside layers is silence.
- Stereo uses equal-power pan from -1 to 1. Mono rejects nonzero pan.
- `Noise` requires a nonzero unsigned 32-bit seed and uses xorshift32. Optional
  `lowPass`/`highPass` are one-pole filters at 20 Hz through one Hz below Nyquist;
  high-pass must be below low-pass when both are present.
- `Tone` requires frequency, optionally endFrequency and waveform. Both
  frequencies are 20 Hz through one Hz below Nyquist. Frequency sweeps linearly;
  sine is default. Triangle sums odd harmonics through 31 below instantaneous
  Nyquist. This bounded procedural oscillator does not claim studio synthesis
  or perceptually accurate reproduction of a real weapon.
- Sum layers, then attenuate the entire mix only if absolute peak exceeds 0.95.
  This preserves layer and stereo balance, does not boost quiet sounds, and
  leaves headroom before signed PCM16 quantization. No dithering is applied.

Identical model parameters produce repeatable bytes on the same runtime/host;
cross-platform transcendental floating-point results are not a byte-identity
guarantee. Node UUIDs do not enter the WAV bytes.

## Export, inspection and storage

`exportBuffer`, `exportFile`, revisions, diagnostic subscriptions, cancellation,
output reservations and atomic publication use the existing session contract.
The CLI requires `.wav`. MCP supports `/sfx` imports under the canonical module
identity and existing execute/inspect/measure/export/close tools; it adds no
new tool or transport. Nothing plays or exports automatically.

Sound/layer refs measure in `timeline` coordinates: x is sample-quantized start
seconds, width is duration seconds, y is zero and height is one. Measurements
retain exact revision checks. SFX root updates use React; imports and mounting
imported audio are unsupported. Image/font registration rejects for WAV.
MP3/OGG, sample ingestion, microphone capture, streaming playback, music/MIDI
and an effects-plugin API are outside this extension.

WAV uses a 44-byte RIFF/WAVE header with 16-byte PCM `fmt ` and interleaved
little-endian signed 16-bit `data`; it includes no identifying metadata.
See Microsoft's [WAVEFORMATEX definition](https://learn.microsoft.com/en-us/windows/win32/api/mmreg/ns-mmreg-waveformatex)
and the [RIFF reference](https://learn.microsoft.com/en-us/windows/win32/xaudio2/resource-interchange-file-format--riff-).
Sessions and models stay in memory; only explicit exports persist. No imported
or recorded third-party samples are bundled. Source and original procedural
example are repository-owned Apache-2.0 material.

## Limits and diagnostics

Publish `sfxSeconds=30`, `sfxLayers=256`, and
`sfxVoiceSamples=16_000_000` through common capabilities. The last limit is the
sum of each layer's rounded duration in samples, independent of channel count.
Validate all bounds before allocating sample output; native validation repeats
the check independently of TypeScript. Existing model byte/tree limits apply.
Maximum output is 5,760,044 bytes (30 seconds, stereo, 48000 Hz, PCM16).

Check cancellation between layers and every 4096 synthesis, peak-scan and
encoding samples. Existing operation-scoped structured native and JavaScript
diagnostics report format `wav`, revision, stage, duration and stable error
classification. Never log source props, seed, PCM, task text or output paths.

## Validation and example

`examples/zombie-gunshot.tsx` composes original muzzle crack, low-frequency body,
falling tone, mechanical click and filtered tail. It generates a 0.75-second
48000 Hz mono gunshot; optional task data `{ "seed": 815 }` selects a reproducible
variation (1–4294967292, reserving the next three seeds). No external samples,
fonts or download tools are needed. Generated WAV files remain outside source.

Native tests independently decode headers and PCM, count a reference tone's
cycles, check pan, repeatability, silence, clipping, validation boundaries and
cancellation. Package tests exercise actual React updates, native diagnostics,
latest-invalid-render rejection, timing measurements, file conflict/cancellation
preservation and workspace/installed CLI plus MCP exports. Six-host CI and
release gates include `forge-sfx`; evidence records actual executed hosts only.
Run root Cargo tests, package build/typecheck/lint/test/example checks, public
docs tests and assembled installed-consumer checks. Remove generated `dist`.

## Change policy

Keep this contract, project/native/session/MCP docs, scoped ownership rules,
capabilities, example, public guide and validation evidence synchronized.
