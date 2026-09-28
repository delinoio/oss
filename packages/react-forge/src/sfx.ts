import { createElement } from "react";
import type { ElementProps } from "./types.js";

export enum SampleRate { Cd = 44100, Game = 48000 }
export enum Channels { Mono = 1, Stereo = 2 }
export enum Waveform { Sine = "sine", Triangle = "triangle" }

export interface SoundProps extends ElementProps {
  /** Total output length in seconds, including silence and tails (at most 30). */
  duration: number;
  sampleRate?: SampleRate;
  channels?: Channels;
  /** Linear master gain, 0–4. Peaks above 0.95 are attenuated together. */
  gain?: number;
}
export interface LayerProps extends Omit<ElementProps, "children"> {
  /** Offset and length in seconds. The complete layer must fit the Sound. */
  start?: number;
  duration: number;
  gain?: number;
  /** Stereo equal-power pan, -1 (left) to 1 (right); mono requires zero. */
  pan?: number;
  attack?: number;
  release?: number;
  /** Exponential amplitude decay time constant in seconds. */
  decay?: number;
}
export interface NoiseProps extends LayerProps {
  /** Reproducible xorshift32 seed, 1–4294967295. */
  seed: number;
  lowPass?: number;
  highPass?: number;
}
export interface ToneProps extends LayerProps {
  frequency: number;
  /** Linear frequency sweep end in Hz; defaults to frequency. */
  endFrequency?: number;
  waveform?: Waveform;
}
export const Sound = (props: SoundProps) => createElement("sfx:sound", props);
export const Noise = (props: NoiseProps) => createElement("sfx:noise", props);
export const Tone = (props: ToneProps) => createElement("sfx:tone", props);
