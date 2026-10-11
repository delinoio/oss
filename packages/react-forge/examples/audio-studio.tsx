import React from "react";
import { fileURLToPath } from "node:url";
import { createSession, Format, type SceneSession } from "@delino/react-forge";
import {
  Scene,
  Group,
  Mesh,
  PerspectiveCamera,
  PointLight,
  AlphaMode,
  type Material,
  type GeometryHandle,
} from "@delino/react-forge/glb";
import {
  roundedBox,
  cylinder,
  ring,
  arc,
  plane,
  machinedCylinder,
  cushion,
  torus,
  flutedGrip,
  stitches,
  tube,
  merge,
  label,
  perforatedLid,
  planarCaps,
  quaternion as q,
} from "./audio-studio-assets/geometry.js";

export enum Product {
  Studio = "studio",
  Headphones = "headphones",
  Dac = "dac",
  Stand = "stand",
}
type Assets = Awaited<ReturnType<typeof assets>>;
const front = q([1, 0, 0], Math.PI / 2),
  top = q([1, 0, 0], -Math.PI / 2);

async function assets(session: SceneSession, signal?: AbortSignal) {
  const bandStitch = merge(
    [-1, 1].flatMap((side) =>
      Array.from({ length: 126 }, (_, i) =>
        tube(
          Array.from({ length: 4 }, (_, j) => {
            const a = 0.12 + ((Math.PI - 0.24) * (i + j / 5)) / 126;
            return [
              0.1012 * Math.cos(a),
              0.1272 * Math.sin(a),
              side * 0.0165,
            ] as const;
          }),
          0.00014,
          6,
        ),
      ),
    ),
  );
  const shapes = {
    shell: machinedCylinder(0.049, 0.019, 0.0035),
    cap: machinedCylinder(0.045, 0.004, 0.0018),
    capInset: machinedCylinder(0.0416, 0.001, 0.0004),
    rim: torus(0.0467, 0.0007, 1.22),
    capLine: torus(0.042, 0.0002, 1.22),
    cushion: cushion(),
    padSeam: torus(0.047, 0.00045, 1.24),
    padStitch: stitches(0.047, 1.24),
    inner: planarCaps(machinedCylinder(0.029, 0.002, 0.0008), 0.029, 0.002),
    hinge: machinedCylinder(0.0062, 0.003, 0.0008),
    hingeInset: machinedCylinder(0.004, 0.0008, 0.0003),
    headband: arc(0.108, 0.138, 0.0032, 0.035, 0.025, Math.PI - 0.025, 192),
    padding: arc(0.103, 0.129, 0.009, 0.036, 0.1, Math.PI - 0.1, 192),
    bandStitch,
    yoke: arc(0.048, 0.064, 0.0034, 0.0055, 0.0, Math.PI, 160),
    slider: roundedBox([0.007, 0.053, 0.004], 0.0015, 20),
    sleeve: roundedBox([0.011, 0.025, 0.007], 0.0025, 20),
    bridge: roundedBox([0.026, 0.0045, 0.006], 0.002, 16),
    adjustment: roundedBox([0.004, 0.0005, 0.00025], 0.0001, 4),
    base: roundedBox([0.157, 0.018, 0.138], 0.014, 24),
    baseInset: roundedBox([0.14, 0.002, 0.122], 0.013, 20),
    stem: roundedBox([0.012, 0.31, 0.025], 0.006, 24),
    stemInlay: roundedBox([0.0015, 0.267, 0.0006], 0.0003, 8),
    saddle: roundedBox([0.078, 0.012, 0.044], 0.006, 24),
    saddleBase: roundedBox([0.08, 0.005, 0.043], 0.0025, 20),
    dac: roundedBox([0.254, 0.05, 0.183], 0.009, 24),
    front: roundedBox([0.25, 0.048, 0.005], 0.005, 24),
    rear: roundedBox([0.246, 0.043, 0.003], 0.003, 20),
    lid: perforatedLid(),
    foot: machinedCylinder(0.013, 0.007, 0.0015),
    footRim: torus(0.0115, 0.0004),
    knob: machinedCylinder(0.0218, 0.017, 0.0015),
    knobFace: machinedCylinder(0.0203, 0.001, 0.0004),
    knobRim: torus(0.0208, 0.00045),
    knurl: flutedGrip(0.0218, 0.013),
    knobRing: torus(0.024, 0.00065),
    port: ring(0.0055, 0.0033, 0.004, 128),
    balanced: ring(0.0065, 0.0048, 0.004, 128),
    portInner: machinedCylinder(0.0048, 0.004, 0.0003),
    smallInner: machinedCylinder(0.00325, 0.004, 0.0003),
    pin: machinedCylinder(0.00065, 0.003, 0.0002, 32),
    screw: machinedCylinder(0.0022, 0.0009, 0.0003, 48),
    screwSlot: roundedBox([0.0026, 0.0004, 0.0002], 0.0001, 4),
    screen: roundedBox([0.092, 0.034, 0.0015], 0.002, 24),
    display: plane(0.085, 0.03),
    marker: roundedBox([0.0012, 0.0032, 0.0003], 0.0002, 8),
    led: machinedCylinder(0.00075, 0.0006, 0.0002, 32),
    button: machinedCylinder(0.004, 0.002, 0.0006, 64),
    ventBacking: plane(0.05, 0.064),
    fin: roundedBox([0.0016, 0.035, 0.0018], 0.0006, 12),
    usb: roundedBox([0.009, 0.0036, 0.003], 0.0016, 16),
    usbInner: roundedBox([0.006, 0.0009, 0.0031], 0.0004, 12),
    rca: ring(0.0042, 0.0022, 0.007, 96),
    rcaSleeve: torus(0.0044, 0.00075),
    power: ring(0.005, 0.003, 0.004, 96),
    brand: label(0.029, 0.006, 0),
    smallBrand: label(0.022, 0.0046, 0),
    headphoneLabel: label(0.036, 0.0025, 1),
    dacLabel: label(0.037, 0.0024, 2),
    standLabel: label(0.033, 0.0025, 3),
    balancedLabel: label(0.016, 0.0019, 4),
    jackLabel: label(0.012, 0.0019, 5),
    usbLabel: label(0.012, 0.0024, 6),
    lineLabel: label(0.019, 0.0024, 7),
    powerLabel: label(0.015, 0.0024, 8),
    tagline: label(0.076, 0.0033, 9),
    serial: label(0.034, 0.0024, 10),
    leftLabel: label(0.0026, 0.0038, 11),
    rightLabel: label(0.0026, 0.0038, 12),
    inputLabel: label(0.01, 0.002, 14),
  };
  const entries = await Promise.all(
    Object.entries(shapes).map(
      async ([key, data]) =>
        [key, await session.registerGeometry(data, { signal })] as const,
    ),
  );
  const geometry = Object.fromEntries(entries) as Record<
    keyof typeof shapes,
    GeometryHandle
  >;
  const image = async (name: string) =>
    session.registerTexture(
      {
        path: fileURLToPath(
          new URL(`./audio-studio-assets/${name}.png`, import.meta.url),
        ),
      },
      { signal },
    );
  const [brush, brushNormal, leather, normal, fabric, display, wordmark] =
    await Promise.all([
      image("brushed-roughness"),
      image("brushed-normal"),
      image("leather-roughness"),
      image("leather-normal"),
      image("fabric-normal"),
      image("display"),
      image("wordmark"),
    ]);
  const material = {
    metal: {
      baseColor: [0.54, 0.42, 0.28, 1],
      metallic: 1,
      roughness: 0.48,
      roughnessTexture: brush,
      normalTexture: brushNormal,
    },
    silver: { baseColor: [0.67, 0.69, 0.71, 1], metallic: 1, roughness: 0.23 },
    graphite: {
      baseColor: [0.028, 0.035, 0.039, 1],
      metallic: 0.85,
      roughness: 0.5,
      roughnessTexture: brush,
      normalTexture: brushNormal,
    },
    blackMetal: {
      baseColor: [0.012, 0.017, 0.019, 1],
      metallic: 0.8,
      roughness: 0.4,
    },
    rubber: {
      baseColor: [0.009, 0.012, 0.014, 1],
      roughness: 0.8,
      roughnessTexture: leather,
      normalTexture: normal,
    },
    fabric: {
      baseColor: [0.015, 0.019, 0.022, 1],
      roughness: 0.9,
      normalTexture: fabric,
    },
    inset: { baseColor: [0.003, 0.004, 0.005, 1], roughness: 0.65 },
    thread: { baseColor: [0.105, 0.089, 0.069, 1], roughness: 0.85 },
    orange: {
      baseColor: [0.6, 0.24, 0.061, 1],
      metallic: 0.65,
      roughness: 0.32,
    },
    screen: {
      baseColor: [0.005, 0.012, 0.015, 1],
      metallic: 0.1,
      roughness: 0.18,
    },
    display: {
      baseColorTexture: display,
      emissiveTexture: display,
      emissive: [0.38, 0.38, 0.38],
      roughness: 0.23,
    },
    wordmark: {
      baseColorTexture: wordmark,
      metallic: 0.55,
      roughness: 0.48,
      alphaMode: AlphaMode.Blend,
    },
    led: {
      baseColor: [0.62, 0.77, 0.67, 1],
      emissive: [0.3, 0.65, 0.46],
      roughness: 0.3,
    },
    red: { baseColor: [0.32, 0.025, 0.019, 1], roughness: 0.55 },
    white: { baseColor: [0.62, 0.61, 0.54, 1], roughness: 0.55 },
  } satisfies Record<string, Material>;
  return { geometry, material };
}

function Fastener({ a }: { a: Assets }) {
  return (
    <Group>
      <Mesh
        name="Black-oxide countersunk screw"
        geometry={a.geometry.screw}
        material={a.material.blackMetal}
        rotation={front}
      />
      <Mesh
        name="Machined screw drive"
        geometry={a.geometry.screwSlot}
        material={a.material.inset}
        translation={[0, 0, 0.00055]}
      />
    </Group>
  );
}

function Headphones({ a }: { a: Assets }) {
  const { geometry: g, material: m } = a;
  return (
    <Group name="AURA H01 headphones">
      <Group name="Stitched suspension headband" translation={[0, 0.213, 0]}>
        <Mesh
          name="Spring aluminum headband"
          geometry={g.headband}
          material={m.metal}
        />
        <Mesh
          name="Leather wrapped memory-foam headband"
          geometry={g.padding}
          material={m.rubber}
        />
        <Mesh
          name="Double saddle-stitch rows"
          geometry={g.bandStitch}
          material={m.thread}
        />
      </Group>
      {([-1, 1] as const).map((side) => (
        <Group
          key={side}
          name={side < 0 ? "Left earcup assembly" : "Right earcup assembly"}
          translation={[side * 0.105, 0.193, 0]}
        >
          <Mesh
            name="Precision height adjustment rail"
            geometry={g.slider}
            material={m.metal}
            translation={[0, 0.034, 0]}
          />
          <Mesh
            name="Slider bearing sleeve"
            geometry={g.sleeve}
            material={m.blackMetal}
            translation={[0, 0.018, 0]}
          />
          {Array.from({ length: 5 }, (_, i) => (
            <Mesh
              key={i}
              name="Adjustment witness line"
              geometry={g.adjustment}
              material={m.inset}
              translation={[0, 0.041 + i * 0.003, 0.0021]}
            />
          ))}
          <Mesh
            name="Fork crown bridge"
            geometry={g.bridge}
            material={m.metal}
            translation={[side * 0.011, 0.064, 0]}
          />
          <Mesh
            name="Cast aluminum suspension fork"
            geometry={g.yoke}
            material={m.metal}
            translation={[side * 0.024, 0, 0]}
            rotation={q([0, 1, 0], Math.PI / 2)}
          />
          <Group
            name="Angled sealed acoustic capsule"
            rotation={q([0, 0, 1], (-side * Math.PI) / 2)}
          >
            <Mesh
              name="Continuous oval housing"
              geometry={g.shell}
              material={m.metal}
              translation={[0, 0.005, 0]}
              scale={[1.22, 1, 1]}
            />
            <Mesh
              name="Satin graphite faceplate"
              geometry={g.cap}
              material={m.graphite}
              translation={[0, 0.016, 0]}
              scale={[1.22, 1, 1]}
            />
            <Mesh
              name="Fine concentric outer bezel"
              geometry={g.rim}
              material={m.silver}
              translation={[0, 0.014, 0]}
            />
            <Mesh
              name="Inset faceplate island"
              geometry={g.capInset}
              material={m.graphite}
              translation={[0, 0.0182, 0]}
              scale={[1.22, 1, 1]}
            />
            <Mesh
              name="Diamond-cut perimeter hairline"
              geometry={g.capLine}
              material={m.metal}
              translation={[0, 0.0186, 0]}
            />
            <Mesh
              name="Sculpted leather ear cushion"
              geometry={g.cushion}
              material={m.rubber}
              translation={[0, -0.018, 0]}
            />
            <Mesh
              name="Cushion rolled seam"
              geometry={g.padSeam}
              material={m.rubber}
              translation={[0, -0.022, 0]}
            />
            <Mesh
              name="Cushion hand-stitch detail"
              geometry={g.padStitch}
              material={m.thread}
              translation={[0, -0.026, 0]}
            />
            <Mesh
              name="Woven acoustic liner"
              geometry={g.inner}
              material={m.fabric}
              translation={[0, -0.015, 0]}
              scale={[1.2, 1, 1]}
            />
          </Group>
          {([-1, 1] as const).map((z) => (
            <Group
              key={z}
              translation={[side * 0.024, 0, z * 0.047]}
              rotation={q([0, 0, 1], (-side * Math.PI) / 2)}
            >
              <Mesh
                name="Suspension pivot boss"
                geometry={g.hinge}
                material={m.blackMetal}
              />
              <Mesh
                name="Pivot machine-finished cap"
                geometry={g.hingeInset}
                material={m.metal}
                translation={[0, 0.0017, 0]}
              />
            </Group>
          ))}
          <Group
            name="Laser-etched earcup identity"
            translation={[side * 0.01885, 0, 0]}
            rotation={q([0, 1, 0], (side * Math.PI) / 2)}
          >
            <Mesh
              name="AURA earcup logotype"
              geometry={g.brand}
              material={m.wordmark}
              translation={[0, 0.009, 0]}
            />
            <Mesh
              name="H01 reference engraving"
              geometry={g.headphoneLabel}
              material={m.wordmark}
              translation={[0, -0.003, 0]}
            />
            <Mesh
              name="Channel marking"
              geometry={side < 0 ? g.leftLabel : g.rightLabel}
              material={m.wordmark}
              translation={[0, -0.033, 0]}
            />
          </Group>
          <Group
            name="Lower acoustic vent and cable socket"
            translation={[0, -0.057, 0]}
            rotation={q([1, 0, 0], Math.PI)}
          >
            <Mesh
              name="Recessed cable jack"
              geometry={g.port}
              material={m.blackMetal}
              scale={[0.6, 1, 0.6]}
            />
            <Mesh
              name="Cable jack opening"
              geometry={g.smallInner}
              material={m.inset}
              scale={[0.6, 1, 0.6]}
              translation={[0, -0.002, 0]}
            />
          </Group>
        </Group>
      ))}
    </Group>
  );
}

function Stand({ a }: { a: Assets }) {
  const { geometry: g, material: m } = a;
  return (
    <Group name="AURA S01 stand">
      <Mesh
        name="Weighted continuous-radius base"
        geometry={g.base}
        material={m.graphite}
        translation={[0, 0.013, 0]}
      />
      <Mesh
        name="Recessed nonslip base pad"
        geometry={g.baseInset}
        material={m.rubber}
        translation={[0, 0.003, 0]}
      />
      <Mesh
        name="Brushed aluminum structural spine"
        geometry={g.stem}
        material={m.metal}
        translation={[0, 0.172, -0.029]}
      />
      <Mesh
        name="Spine inset accent"
        geometry={g.stemInlay}
        material={m.blackMetal}
        translation={[0, 0.166, -0.0163]}
      />
      <Mesh
        name="Machined saddle cradle"
        geometry={g.saddleBase}
        material={m.metal}
        translation={[0, 0.328, 0]}
      />
      <Mesh
        name="Leather saddle contact pad"
        geometry={g.saddle}
        material={m.rubber}
        translation={[0, 0.3315, 0]}
      />
      <Mesh
        name="Laser-etched base logo"
        geometry={g.smallBrand}
        material={m.wordmark}
        translation={[0, 0.02215, 0.035]}
        rotation={top}
      />
      <Mesh
        name="Stand model engraving"
        geometry={g.standLabel}
        material={m.wordmark}
        translation={[0, 0.0222, 0.045]}
        rotation={top}
      />
      {([-1, 1] as const).map((x) => (
        <Group key={x} translation={[x * 0.053, 0.0221, -0.044]} rotation={top}>
          <Fastener a={a} />
        </Group>
      ))}
    </Group>
  );
}

function Dac({ a }: { a: Assets }) {
  const { geometry: g, material: m } = a;
  return (
    <Group name="AURA D01 desktop DAC" translation={[0, 0.009, 0]}>
      <Mesh
        name="Extruded satin chassis"
        geometry={g.dac}
        material={m.graphite}
        translation={[0, 0.026, 0]}
      />
      <Mesh
        name="Precision brushed front fascia"
        geometry={g.front}
        material={m.metal}
        translation={[0, 0.026, 0.091]}
      />
      <Mesh
        name="Perforated lid with 36 beveled through-slots"
        geometry={g.lid}
        material={m.graphite}
        translation={[0, 0.052, 0]}
      />
      <Mesh
        name="Rear connector panel"
        geometry={g.rear}
        material={m.blackMetal}
        translation={[0, 0.026, -0.092]}
      />
      <Mesh
        name="Recessed display perimeter"
        geometry={g.screen}
        material={m.blackMetal}
        translation={[-0.012, 0.027, 0.094]}
      />
      <Mesh
        name="High-resolution OLED volume readout"
        geometry={g.display}
        material={m.display}
        translation={[-0.012, 0.027, 0.09485]}
      />
      <Group
        name="112-flute machined volume encoder"
        translation={[0.089, 0.027, 0.102]}
        rotation={front}
      >
        <Mesh
          name="Monolithic encoder body"
          geometry={g.knob}
          material={m.metal}
        />
        <Mesh
          name="Integral fine knurl"
          geometry={g.knurl}
          material={m.metal}
        />
        <Mesh
          name="Flat brushed control face"
          geometry={g.knobFace}
          material={m.graphite}
          translation={[0, 0.00865, 0]}
        />
        <Mesh
          name="Polished face chamfer"
          geometry={g.knobRim}
          material={m.silver}
          translation={[0, 0.0084, 0]}
        />
        <Mesh
          name="Amber index inlay"
          geometry={g.marker}
          material={m.orange}
          translation={[0, 0.00925, -0.014]}
          rotation={top}
        />
      </Group>
      <Mesh
        name="Encoder shadow reveal"
        geometry={g.knobRing}
        material={m.blackMetal}
        translation={[0.089, 0.027, 0.094]}
        rotation={front}
      />
      <Group
        name="Headphone output connectors"
        translation={[-0.102, 0.027, 0.094]}
      >
        <Mesh
          name="Balanced 4.4 mm socket"
          geometry={g.balanced}
          material={m.blackMetal}
          rotation={front}
        />
        <Mesh
          name="Balanced jack cavity"
          geometry={g.portInner}
          material={m.inset}
          translation={[0, 0, -0.001]}
          rotation={front}
        />
        <Mesh
          name="Balanced socket label"
          geometry={g.balancedLabel}
          material={m.wordmark}
          translation={[0, -0.014, 0]}
        />
        <Mesh
          name="6.35 mm socket"
          geometry={g.port}
          material={m.blackMetal}
          translation={[0.024, 0, 0]}
          rotation={front}
        />
        <Mesh
          name="6.35 mm jack cavity"
          geometry={g.smallInner}
          material={m.inset}
          translation={[0.024, 0, -0.001]}
          rotation={front}
        />
        <Mesh
          name="6.35 socket label"
          geometry={g.jackLabel}
          material={m.wordmark}
          translation={[0.024, -0.014, 0]}
        />
      </Group>
      <Mesh
        name="Input selector button"
        geometry={g.button}
        material={m.graphite}
        translation={[0.046, 0.021, 0.095]}
        rotation={front}
      />
      <Mesh
        name="Input label"
        geometry={g.inputLabel}
        material={m.wordmark}
        translation={[0.046, 0.01, 0.094]}
      />
      <Mesh
        name="Soft white status indicator"
        geometry={g.led}
        material={m.led}
        translation={[0.046, 0.037, 0.094]}
        rotation={front}
      />
      {([-1, 1] as const).flatMap((x) =>
        ([-1, 1] as const).map((z) => (
          <Group
            key={`${x}:${z}`}
            translation={[x * 0.099, -0.0035, z * 0.064]}
          >
            <Mesh
              name="Elastomer isolation foot"
              geometry={g.foot}
              material={m.rubber}
            />
            <Mesh
              name="Foot retaining ring"
              geometry={g.footRim}
              material={m.metal}
              translation={[0, 0.002, 0]}
            />
          </Group>
        )),
      )}
      {[-0.066, 0.066].map((x) => (
        <Mesh
          key={x}
          name="Recessed ventilation dust screen"
          geometry={g.ventBacking}
          material={m.fabric}
          translation={[x, 0.0511, -0.039]}
          rotation={top}
        />
      ))}
      {([-1, 1] as const).flatMap((side) =>
        Array.from({ length: 26 }, (_, i) => (
          <Mesh
            key={`${side}:${i}`}
            name="Chassis heat-dissipation rib"
            geometry={g.fin}
            material={m.blackMetal}
            translation={[side * 0.1273, 0.026, -0.069 + i * 0.0053]}
          />
        )),
      )}
      <Group
        name="Rear I/O"
        translation={[0, 0, -0.094]}
        rotation={q([0, 1, 0], Math.PI)}
      >
        {[-0.054, -0.026].map((x, i) => (
          <Group key={i} translation={[x, 0.026, 0]}>
            <Mesh
              name="RCA line-output gold contact"
              geometry={g.rca}
              material={m.metal}
              rotation={front}
            />
            <Mesh
              name="RCA channel identifier"
              geometry={g.rcaSleeve}
              material={i ? m.red : m.white}
              rotation={front}
            />
            <Mesh
              name="Line-output cavity"
              geometry={g.smallInner}
              material={m.inset}
              scale={[0.65, 0.65, 0.65]}
              rotation={front}
              translation={[0, 0, -0.002]}
            />
          </Group>
        ))}
        <Mesh
          name="Line output legend"
          geometry={g.lineLabel}
          material={m.wordmark}
          translation={[-0.04, 0.01, 0.0003]}
        />
        <Mesh
          name="USB-C receptacle"
          geometry={g.usb}
          material={m.inset}
          translation={[0.018, 0.026, 0]}
        />
        <Mesh
          name="USB-C contact tongue"
          geometry={g.usbInner}
          material={m.silver}
          translation={[0.018, 0.026, 0.0001]}
        />
        <Mesh
          name="USB-C legend"
          geometry={g.usbLabel}
          material={m.wordmark}
          translation={[0.018, 0.012, 0.0003]}
        />
        <Mesh
          name="DC supply jack"
          geometry={g.power}
          material={m.blackMetal}
          translation={[0.063, 0.026, 0]}
          rotation={front}
        />
        <Mesh
          name="DC opening"
          geometry={g.smallInner}
          material={m.inset}
          translation={[0.063, 0.026, -0.002]}
          rotation={front}
        />
        <Mesh
          name="DC center pin"
          geometry={g.pin}
          material={m.silver}
          translation={[0.063, 0.026, 0]}
          rotation={front}
        />
        <Mesh
          name="Power legend"
          geometry={g.powerLabel}
          material={m.wordmark}
          translation={[0.063, 0.012, 0.0003]}
        />
        {[-0.111, 0.111].map((x) => (
          <Group key={x} translation={[x, 0.026, 0]}>
            <Fastener a={a} />
          </Group>
        ))}
      </Group>
      <Mesh
        name="Etched top AURA wordmark"
        geometry={g.brand}
        material={m.wordmark}
        translation={[0, 0.0532, 0.033]}
        rotation={top}
      />
      <Mesh
        name="D01 model engraving"
        geometry={g.dacLabel}
        material={m.wordmark}
        translation={[0, 0.0532, 0.044]}
        rotation={top}
      />
    </Group>
  );
}

export default async function task({
  data,
  signal,
}: {
  data?: { format?: Format.Glb | Format.Fbx; product?: Product };
  signal?: AbortSignal;
} = {}) {
  const format = data?.format ?? Format.Glb,
    product = data?.product ?? Product.Studio;
  if (
    ![Format.Glb, Format.Fbx].includes(format) ||
    !Object.values(Product).includes(product)
  )
    throw new Error("Invalid audio-studio task selection");
  const s = createSession(format);
  try {
    const a = await assets(s, signal);
    await s.render(
      <Scene name={`AURA / ${product}`}>
        {product === Product.Headphones || product === Product.Studio ? (
          <Headphones a={a} />
        ) : null}
        {product === Product.Stand || product === Product.Studio ? (
          <Stand a={a} />
        ) : null}
        {product === Product.Dac ? (
          <Dac a={a} />
        ) : product === Product.Studio ? (
          <Group
            name="Desktop companion"
            translation={[0.275, 0, 0.052]}
            rotation={q([0, 1, 0], -0.1)}
          >
            <Dac a={a} />
          </Group>
        ) : null}
        <PerspectiveCamera
          name="Product camera"
          yfov={0.5}
          aspect={1}
          near={0.01}
          far={100}
          translation={[0.6, 0.48, 0.95]}
          rotation={q([1, 0, 0], -0.3)}
        />
        <PointLight
          name="Soft accent source"
          color={[0.8, 0.92, 1]}
          intensity={0.05}
          translation={[-0.5, 0.6, 0.4]}
        />
      </Scene>,
    );
    return s;
  } catch (error) {
    await s.dispose();
    throw error;
  }
}
