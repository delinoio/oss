import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Sound, Noise, Tone, SampleRate } from "@delino/react-forge/sfx";

/** Original procedural game audio: muzzle crack, body, mechanics and room tail.
 * No recordings, external samples, model service or font installation required.
 */
export function Gunshot({ start = 0, seed = 815 }: { start?: number; seed?: number }) {
  return <>
    <Noise start={start} duration={0.095} seed={seed} gain={0.85}
      attack={0.0003} release={0.025} decay={0.017} highPass={1100} lowPass={15000} />
    <Noise start={start + 0.002} duration={0.21} seed={seed + 1} gain={1.4}
      attack={0.001} release={0.06} decay={0.044} lowPass={1800} />
    <Tone start={start} duration={0.17} frequency={155} endFrequency={48}
      gain={0.8} attack={0.0008} release={0.05} decay={0.032} />
    <Noise start={start + 0.075} duration={0.035} seed={seed + 2} gain={0.14}
      attack={0.0005} release={0.01} decay={0.009} highPass={2400} lowPass={11000} />
    <Noise start={start + 0.022} duration={0.58} seed={seed + 3} gain={0.24}
      attack={0.006} release={0.18} decay={0.13} highPass={160} lowPass={3200} />
  </>;
}

export default async function task({ data }: { data?: unknown } = {}) {
  const seed = data && typeof data === "object" && "seed" in data ? data.seed : 815;
  if (typeof seed !== "number" || !Number.isInteger(seed) || seed < 1 || seed > 0xfffffffc) {
    throw new Error("Gunshot seed must be an integer from 1 through 4294967292.");
  }
  const session = createSession(Format.Wav);
  try {
    await session.render(<Sound duration={0.75} sampleRate={SampleRate.Game}>
      <Gunshot seed={seed} />
    </Sound>);
    return session;
  } catch (error) { await session.dispose(); throw error; }
}
