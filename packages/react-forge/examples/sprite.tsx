import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Animation, Ellipse, Frame, Layer, PixelGrid, SpriteProject } from "@delino/react-forge/sprite";

// Original pixel artwork. Shared body, palette and poses make variants reusable.
const body = [
  ".....oooooo.....",
  "...ooggggggoo...",
  "..oggggggggggo..",
  ".ogglggggglgggo.",
  ".oggggggggggggo.",
  "ogggwogggwoggggo",
  "ogggwogggwoggggo",
  "oggggggggggggggo",
  ".oggpggggggpggo.",
  ".oggggooogggggo.",
  "..oggggggggggo..",
  "...ooggggggoo...",
  ".....oooooo.....",
];
function Slime({ bounce }: { bounce: number }) {
  return <>
    <Ellipse x={8} y={26} width={16} height={3} fill="#17213555" />
    <Layer x={8} y={12 - bounce}><PixelGrid rows={body} /></Layer>
  </>;
}
export default async function () {
  const session = createSession(Format.Sprite);
  try {
    await session.render(<SpriteProject width={32} height={32} columns={4} scale={4} padding={4}
      palette={{ o: "#172135", g: "#5acb79", l: "#a7ef99", w: "#fff8dd", p: "#f394a4" }}>
      <Animation name="idle"><Frame durationMs={450}><Slime bounce={0} /></Frame>
        <Frame durationMs={450}><Slime bounce={1} /></Frame></Animation>
      <Animation name="hop" loop={false}>
        {[0, 3, 6, 7, 4, 0].map((bounce, index) => <Frame key={index} durationMs={100} pivot={{ x: 16, y: 28 }}><Slime bounce={bounce} /></Frame>)}
      </Animation>
    </SpriteProject>);
    return session;
  } catch (error) { await session.dispose(); throw error; }
}
