import { ForgeError } from "./errors.js";
import type { SerializedNode } from "./renderer.js";
import { Channels, SampleRate, Waveform } from "./sfx.js";
import { ErrorCode, limits } from "./types.js";

const invalid = (message: string): never => { throw new ForgeError(ErrorCode.MalformedInput, message); };
function number(value: unknown, min: number, max: number): number {
  if (typeof value !== "number" || !Number.isFinite(value) || value < min || value > max) invalid("SFX parameters must be finite numbers within their documented ranges.");
  return value as number;
}
function props(node: SerializedNode, allowed: string[]) {
  if (Object.keys(node.props).some(key => !["children", "ref", ...allowed].includes(key))) invalid("Unsupported SFX property.");
  return node.props;
}

export function sfxModel(nodes: SerializedNode[]): Record<string, unknown> {
  if (nodes.length !== 1 || nodes[0]!.type !== "sfx:sound") invalid("SFX requires one Sound root.");
  const root = nodes[0]!;
  const p = props(root, ["duration", "sampleRate", "channels", "gain"]);
  const sampleRate = p.sampleRate === undefined ? SampleRate.Game : p.sampleRate;
  const channels = p.channels === undefined ? Channels.Mono : p.channels;
  if (sampleRate !== SampleRate.Cd && sampleRate !== SampleRate.Game || channels !== Channels.Mono && channels !== Channels.Stereo) invalid("SFX supports 44100/48000 Hz and mono/stereo only.");
  const rate = sampleRate as number;
  const duration = number(p.duration, 2 / rate, limits.sfxSeconds);
  if (root.children.length > limits.sfxLayers) throw new ForgeError(ErrorCode.ResourceLimit, "SFX exceeds 256 layers.");
  if (!root.children.length) invalid("Sound requires at least one Noise or Tone layer.");
  let work = 0;
  const layers = root.children.map(node => {
    if (node.children.length || !["sfx:noise", "sfx:tone"].includes(node.type)) invalid("Sound accepts only leaf Noise and Tone layers.");
    const noise = node.type === "sfx:noise";
    const v = props(node, ["start", "duration", "gain", "pan", "attack", "release", "decay", ...(noise ? ["seed", "lowPass", "highPass"] : ["frequency", "endFrequency", "waveform"])]);
    const start = number(v.start === undefined ? 0 : v.start, 0, duration);
    const length = number(v.duration, 2 / rate, duration);
    if (start + length > duration + 1e-9) invalid("SFX layer extends past the Sound duration.");
    work += Math.round(length * rate);
    const pan = number(v.pan === undefined ? 0 : v.pan, -1, 1);
    if (channels === Channels.Mono && pan !== 0) invalid("Mono SFX layers require centered pan.");
    const attack = number(v.attack === undefined ? Math.max(1 / rate, Math.min(0.001, length / 4)) : v.attack, 1 / rate, length);
    const release = number(v.release === undefined ? Math.max(1 / rate, Math.min(0.01, length / 4)) : v.release, 1 / rate, length);
    if (attack + release > length + 1e-9) invalid("SFX attack and release must fit within the layer.");
    const base = { id: node.id, start, duration: length, gain: number(v.gain === undefined ? 1 : v.gain, 0, 4), pan, attack, release,
      decay: v.decay === undefined ? undefined : number(v.decay, 1 / rate, limits.sfxSeconds) };
    if (noise) {
      const seed = number(v.seed, 1, 0xffffffff);
      if (!Number.isInteger(seed)) invalid("Noise seed must be an unsigned nonzero 32-bit integer.");
      const low_pass = v.lowPass === undefined ? undefined : number(v.lowPass, 20, rate / 2 - 1);
      const high_pass = v.highPass === undefined ? undefined : number(v.highPass, 20, rate / 2 - 1);
      if (low_pass !== undefined && high_pass !== undefined && high_pass >= low_pass) invalid("SFX highPass must be below lowPass.");
      return { ...base, source: { kind: "noise", seed, low_pass, high_pass } };
    }
    const frequency = number(v.frequency, 20, rate / 2 - 1);
    const waveform = v.waveform === undefined ? Waveform.Sine : v.waveform;
    if (!Object.values(Waveform).includes(waveform as Waveform)) invalid("Unsupported SFX waveform.");
    return { ...base, source: { kind: "tone", frequency, end_frequency: v.endFrequency === undefined ? frequency : number(v.endFrequency, 20, rate / 2 - 1), waveform } };
  });
  if (work > limits.sfxVoiceSamples) throw new ForgeError(ErrorCode.ResourceLimit, "SFX exceeds the aggregate synthesis budget.");
  return { id: root.id, sample_rate: rate, channels, duration, gain: number(p.gain === undefined ? 1 : p.gain, 0, 4), layers };
}
