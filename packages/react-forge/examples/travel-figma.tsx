import React, { type ReactNode } from "react";
import { fileURLToPath } from "node:url";
import { createSession, Format, type AssetHandle } from "@delino/react-forge";
import {
  Document,
  Page,
  Frame,
  Text,
  Image,
  Rectangle,
  Ellipse,
  Component,
  ComponentSet,
  Instance,
  VariableCollection,
  Variable,
  TextStyle,
  openFigma,
  CredentialSource,
  type FigmaTextProps,
} from "@delino/react-forge/figma";

// ROAM is a fictional travel concept. Photography is the original generated
// artwork from travel-ir-assets; no stock imagery or fonts are redistributed.
export const palette = {
  ink: "#142338",
  paper: "#F5F1E8",
  blue: "#2563EB",
  muted: "#586778",
  white: "#FFFFFF",
  paleBlue: "#E9EFFB",
  line: "#DFE3E7",
} as const;
const font = { family: "DM Sans", style: "Regular" };
function Copy({
  size = 14,
  bold = false,
  color = palette.ink,
  children,
  ...props
}: FigmaTextProps & { size?: number; bold?: boolean }) {
  return (
    <Text
      fontName={{ ...font, style: bold ? "Bold" : "Regular" }}
      fontSize={size}
      color={color}
      textAutoResize={props.width === undefined ? "WIDTH_AND_HEIGHT" : "HEIGHT"}
      lineHeight={{ unit: "PERCENT", value: 125 }}
      {...props}
    >
      {children}
    </Text>
  );
}
function Nav({ active }: { active: string }) {
  return (
    <Frame
      name="Navigation"
      x={0}
      y={750}
      width={390}
      height={94}
      fill={palette.white}
      layoutMode="HORIZONTAL"
      paddingTop={20}
      paddingLeft={24}
      paddingRight={24}
      gap={20}
      primaryAxisSizingMode="FIXED"
      counterAxisSizingMode="FIXED"
    >
      {["Explore", "Search", "My trips", "Saved"].map((label) => (
        <Frame
          key={label}
          name={label}
          width={70}
          height={44}
          layoutMode="VERTICAL"
          gap={9}
          counterAxisAlignItems="CENTER"
          fills={[]}
        >
          <Rectangle
            name="Active indicator"
            width={24}
            height={3}
            cornerRadius={1}
            fill={label === active ? palette.blue : palette.line}
          />
          <Copy
            width={70}
            size={12}
            bold={label === active}
            color={label === active ? palette.blue : palette.muted}
            textAlignHorizontal="CENTER"
          >
            {label}
          </Copy>
        </Frame>
      ))}
    </Frame>
  );
}
function Screen({
  name,
  index,
  active,
  children,
}: {
  name: string;
  index: number;
  active: string;
  children: ReactNode;
}) {
  return (
    <Frame
      name={name}
      x={index * 430}
      y={80}
      width={390}
      height={844}
      fill={palette.paper}
      clipsContent
      cornerRadius={32}
      bindings={{ fills: "paper" }}
    >
      <Copy x={24} y={17} width={60} size={13} bold>
        9:41
      </Copy>
      <Copy x={292} y={17} width={74} size={11} textAlignHorizontal="RIGHT">
        5G · 100%
      </Copy>
      {children}
      <Nav active={active} />
      <Rectangle
        name="Home indicator"
        x={128}
        y={831}
        width={134}
        height={5}
        cornerRadius={2}
        fill={palette.ink}
      />
    </Frame>
  );
}
function Pill({
  label,
  x,
  y,
  width = 86,
  active = false,
}: {
  label: string;
  x: number;
  y: number;
  width?: number;
  active?: boolean;
}) {
  return (
    <Frame
      name={label}
      x={x}
      y={y}
      width={width}
      height={36}
      fill={active ? palette.ink : palette.white}
      cornerRadius={18}
      layoutMode="HORIZONTAL"
      primaryAxisAlignItems="CENTER"
      counterAxisAlignItems="CENTER"
      primaryAxisSizingMode="FIXED"
      counterAxisSizingMode="FIXED"
    >
      <Copy size={12} bold color={active ? palette.white : palette.muted}>
        {label}
      </Copy>
    </Frame>
  );
}
function PlaceRow({
  asset,
  title,
  detail,
  y,
}: {
  asset: AssetHandle;
  title: string;
  detail: string;
  y: number;
}) {
  return (
    <Frame
      name={title}
      x={24}
      y={y}
      width={342}
      height={104}
      fill={palette.white}
      cornerRadius={18}
      layoutMode="HORIZONTAL"
      gap={16}
      padding={10}
      primaryAxisSizingMode="FIXED"
      counterAxisSizingMode="FIXED"
      counterAxisAlignItems="CENTER"
    >
      <Image
        name={`${title} photograph`}
        asset={asset}
        width={84}
        height={84}
        cornerRadius={12}
      />
      <Frame
        name="Place details"
        width={220}
        height={70}
        layoutMode="VERTICAL"
        gap={7}
        fills={[]}
      >
        <Copy size={17} bold>
          {title}
        </Copy>
        <Copy size={12} color={palette.muted}>
          {detail}
        </Copy>
        <Copy size={11} color={palette.blue}>
          View collection →
        </Copy>
      </Frame>
    </Frame>
  );
}
export function Roam({
  coast,
  horizon,
}: {
  coast: AssetHandle;
  horizon: AssetHandle;
}) {
  return (
    <Document>
      <VariableCollection name="ROAM" nodeKey="tokens" />
      {Object.entries(palette).map(([name, value]) => (
        <Variable
          key={name}
          nodeKey={name}
          name={`color/${name}`}
          collection="tokens"
          resolvedType="COLOR"
          value={value}
          scopes={["ALL_FILLS"]}
          codeSyntax={{ WEB: `var(--roam-${name})` }}
        />
      ))}
      <TextStyle
        nodeKey="heading"
        name="ROAM/Heading"
        fontName={{ ...font, style: "Bold" }}
        fontSize={32}
        lineHeight={{ unit: "PERCENT", value: 115 }}
      />
      <Page name="ROAM · Mobile">
        <Copy name="Canvas label" x={0} y={0} width={1600} size={18} bold>
          ROAM / A little further from ordinary.
        </Copy>
        <Screen name="01 · Explore" index={0} active="Explore">
          <Copy x={24} y={62} width={270} size={13} bold color={palette.blue}>
            ROAM / YOUR NEXT CHAPTER
          </Copy>
          <Copy name="Explore title" x={24} y={93} width={342} size={34} bold>
            {"Somewhere new.\nSomething you."}
          </Copy>
          <Copy x={24} y={190} width={342} color={palette.muted}>
            Good places. Great company. Less planning.
          </Copy>
          <Frame
            name="Search prompt"
            x={24}
            y={236}
            width={342}
            height={52}
            fill={palette.white}
            cornerRadius={14}
            layoutMode="HORIZONTAL"
            padding={16}
            gap={12}
          >
            <Copy color={palette.muted}>Where would you rather be?</Copy>
            <Copy color={palette.blue}>↗</Copy>
          </Frame>
          <Pill label="For you" x={24} y={310} active />
          <Pill label="Coast" x={118} y={310} width={70} />
          <Pill label="City breaks" x={196} y={310} width={102} />
          <Frame
            name="Featured escape"
            x={24}
            y={366}
            width={342}
            height={292}
            fill={palette.ink}
            cornerRadius={22}
            clipsContent
          >
            <Image name="Explore hero" asset={coast} width={342} height={184} />
            <Copy
              x={18}
              y={18}
              width={170}
              size={11}
              bold
              color={palette.white}
            >
              THE SLOW WEEKEND
            </Copy>
            <Copy
              x={20}
              y={201}
              width={300}
              size={24}
              bold
              color={palette.white}
            >
              A little coastal magic.
            </Copy>
            <Copy x={20} y={239} width={290} size={13} color={palette.white}>
              Amalfi Coast, Italy · 4 days from €420
            </Copy>
          </Frame>
          <Copy x={24} y={687} width={300} size={15} bold>
            Your kind of getaway
          </Copy>
          <Copy x={315} y={687} width={51} color={palette.blue}>
            All →
          </Copy>
        </Screen>
        <Screen name="02 · Search" index={1} active="Search">
          <Copy x={24} y={63} width={342} size={32} bold>
            Find your somewhere.
          </Copy>
          <Copy x={24} y={117} width={330} color={palette.muted}>
            A weekend away starts with a little curiosity.
          </Copy>
          <Frame
            name="Search input"
            x={24}
            y={166}
            width={342}
            height={54}
            fill={palette.white}
            stroke={palette.blue}
            strokeWeight={1}
            cornerRadius={14}
            layoutMode="HORIZONTAL"
            padding={16}
          >
            <Copy width={280} bold>
              Portugal
            </Copy>
            <Copy color={palette.muted}>×</Copy>
          </Frame>
          <Pill label="Any dates" x={24} y={238} width={104} />
          <Pill label="2 travelers" x={136} y={238} width={114} />
          <Pill label="Filters" x={258} y={238} width={108} />
          <Copy x={24} y={303} width={300} size={12} bold color={palette.muted}>
            PLACES TO FALL FOR · 12 RESULTS
          </Copy>
          <PlaceRow
            asset={coast}
            title="Lisbon, at your pace"
            detail="Neighborhoods · food · ocean air"
            y={338}
          />
          <PlaceRow
            asset={horizon}
            title="The quiet Algarve"
            detail="Hidden coves · 3–5 days"
            y={458}
          />
          <Frame
            name="Search tip"
            x={24}
            y={594}
            width={342}
            height={118}
            fill={palette.paleBlue}
            cornerRadius={18}
          >
            <Copy x={18} y={17} width={310} size={15} bold>
              A little less obvious.
            </Copy>
            <Copy x={18} y={47} width={296} size={13} color={palette.muted}>
              {
                "Skip the busiest spots. Discover places\nrecommended by people who linger."
              }
            </Copy>
          </Frame>
        </Screen>
        <Screen name="03 · Destination" index={2} active="Explore">
          <Image
            name="Destination hero"
            asset={coast}
            x={0}
            y={49}
            width={390}
            height={316}
          />
          <Pill label="← Back" x={24} y={65} width={82} />
          <Pill label="Save ♡" x={278} y={65} width={88} />
          <Frame
            name="Destination story"
            x={0}
            y={333}
            width={390}
            height={417}
            fill={palette.paper}
            cornerRadius={30}
          >
            <Copy x={24} y={29} width={300} size={12} bold color={palette.blue}>
              PORTUGAL · CITY & COAST
            </Copy>
            <Copy
              name="Destination title"
              x={24}
              y={59}
              width={342}
              size={34}
              bold
            >
              Lisbon, slowly.
            </Copy>
            <Copy x={24} y={113} width={342} size={13} color={palette.muted}>
              4.9 guest rating · 3 days · Best in spring
            </Copy>
            <Copy x={24} y={156} width={334} size={16}>
              {
                "Golden light, tiled streets and long lunches.\nLeave room for the unexpected."
              }
            </Copy>
            <Frame
              name="Trip facts"
              x={24}
              y={229}
              width={342}
              height={64}
              layoutMode="HORIZONTAL"
              gap={32}
              fills={[]}
            >
              <Copy width={150} size={13} bold>
                {"FROM \u20ac320\nper person"}
              </Copy>
              <Copy width={150} size={13} bold>
                {"YOUR PACE\n3 stops a day"}
              </Copy>
            </Frame>
            <Instance
              name="Plan destination"
              component="primary"
              x={24}
              y={319}
              width={342}
              height={54}
            />
          </Frame>
        </Screen>
        <Screen name="04 · Itinerary" index={3} active="My trips">
          <Copy x={24} y={65} width={342} size={12} bold color={palette.blue}>
            YOUR NEXT GOOD STORY
          </Copy>
          <Copy x={24} y={97} width={342} size={34} bold>
            Lisbon weekend
          </Copy>
          <Copy x={24} y={150} width={342} color={palette.muted}>
            May 16–18 · You + Alex
          </Copy>
          <Frame
            name="Trip cover"
            x={24}
            y={192}
            width={342}
            height={124}
            cornerRadius={18}
            clipsContent
          >
            <Image
              name="Itinerary cover"
              asset={horizon}
              width={342}
              height={124}
            />
            <Frame
              name="Trip badge"
              x={14}
              y={79}
              width={146}
              height={31}
              fill={palette.white}
              cornerRadius={15}
              layoutMode="HORIZONTAL"
              primaryAxisAlignItems="CENTER"
              counterAxisAlignItems="CENTER"
            >
              <Copy size={11} bold>
                Everything in one place
              </Copy>
            </Frame>
          </Frame>
          <Pill label="Fri 16" x={24} y={340} width={108} active />
          <Pill label="Sat 17" x={141} y={340} width={108} />
          <Pill label="Sun 18" x={258} y={340} width={108} />
          <Frame
            name="Itinerary items"
            x={24}
            y={407}
            width={342}
            height={270}
            layoutMode="VERTICAL"
            gap={14}
            fills={[]}
            primaryAxisSizingMode="AUTO"
            counterAxisSizingMode="FIXED"
          >
            {[
              {
                time: "09:30",
                title: "Coffee & a pastel de nata",
                detail: "Manteigaria · Start sweet",
              },
              {
                time: "11:00",
                title: "Get lost in Alfama",
                detail: "A wandering walk · 1.5 hours",
              },
              {
                time: "13:00",
                title: "A long lunch by the water",
                detail: "Seafood, sunshine, no rush",
              },
            ].map((item) => (
              <Frame
                key={item.time}
                name={`Stop ${item.time}`}
                width={342}
                height={76}
                fill={palette.white}
                cornerRadius={16}
                layoutMode="HORIZONTAL"
                gap={16}
                padding={14}
                primaryAxisSizingMode="FIXED"
                counterAxisSizingMode="FIXED"
              >
                <Copy width={44} size={12} bold color={palette.blue}>
                  {item.time}
                </Copy>
                <Frame
                  name="Stop details"
                  width={250}
                  height={47}
                  layoutMode="VERTICAL"
                  gap={6}
                  fills={[]}
                >
                  <Copy size={14} bold>
                    {item.title}
                  </Copy>
                  <Copy size={11} color={palette.muted}>
                    {item.detail}
                  </Copy>
                </Frame>
              </Frame>
            ))}
          </Frame>
          <Copy x={24} y={703} width={342} size={13} bold color={palette.blue}>
            + Leave room for one more
          </Copy>
        </Screen>
        <Screen name="05 · Saved" index={4} active="Saved">
          <Copy x={24} y={66} width={342} size={12} bold color={palette.blue}>
            GOOD IDEAS LIVE HERE
          </Copy>
          <Copy x={24} y={99} width={342} size={34} bold>
            Your someday list.
          </Copy>
          <Copy x={24} y={153} width={342} color={palette.muted}>
            The places you keep coming back to.
          </Copy>
          <Pill label="Collections" x={24} y={199} width={116} active />
          <Pill label="All places · 18" x={150} y={199} width={140} />
          <Frame
            name="Coastal collection"
            x={24}
            y={265}
            width={342}
            height={239}
            fill={palette.white}
            cornerRadius={20}
            clipsContent
          >
            <Image name="Saved coast" asset={coast} width={342} height={154} />
            <Copy x={18} y={172} width={280} size={19} bold>
              Salt in the air
            </Copy>
            <Copy x={18} y={203} width={280} size={12} color={palette.muted}>
              6 places · Made for slow summers
            </Copy>
          </Frame>
          <PlaceRow
            asset={horizon}
            title="Weekends, well spent"
            detail="8 places · A little closer to home"
            y={524}
          />
          <Frame
            name="New collection"
            x={24}
            y={650}
            width={342}
            height={64}
            fill={palette.paleBlue}
            cornerRadius={16}
            layoutMode="HORIZONTAL"
            counterAxisAlignItems="CENTER"
            paddingLeft={18}
            gap={15}
          >
            <Copy size={23} color={palette.blue}>
              +
            </Copy>
            <Copy size={14} bold color={palette.blue}>
              Make room for another idea
            </Copy>
          </Frame>
        </Screen>
        <ComponentSet
          name="ROAM / Button"
          x={0}
          y={1040}
          width={1080}
          height={94}
          layoutMode="HORIZONTAL"
          padding={20}
          gap={20}
        >
          {[
            ["primary", "Default", palette.blue],
            ["pressed", "Pressed", palette.ink],
            ["disabled", "Disabled", palette.muted],
          ].map(([key, state, color]) => (
            <Component
              key={key}
              nodeKey={key}
              name={`State=${state}`}
              width={320}
              height={54}
              fill={color}
              cornerRadius={14}
              layoutMode="HORIZONTAL"
              primaryAxisAlignItems="CENTER"
              counterAxisAlignItems="CENTER"
              primaryAxisSizingMode="FIXED"
              counterAxisSizingMode="FIXED"
              bindings={{
                fills:
                  key === "primary"
                    ? "blue"
                    : key === "pressed"
                      ? "ink"
                      : "muted",
              }}
            >
              <Copy size={15} bold color={palette.white}>
                Plan this escape →
              </Copy>
            </Component>
          ))}
        </ComponentSet>
        <Copy x={0} y={1180} width={1500} size={14} color={palette.muted}>
          ROAM foundation · DM Sans · 4 / 8 / 16 / 24 spacing · Editable frames,
          text, images, variables, variants & instances.
        </Copy>
      </Page>
    </Document>
  );
}
export default async function task({
  data,
  signal,
}: {
  data?: { fileKey?: string; planKey?: string };
  signal: AbortSignal;
}) {
  const session = data?.fileKey
    ? await openFigma(data.fileKey, {
        signal,
        credentials: { source: CredentialSource.Codex },
      })
    : createSession(Format.Figma, {
        fileName: "ROAM — Travel Companion",
        planKey: data?.planKey,
        credentials: { source: CredentialSource.Codex },
      });
  try {
    const coast = await session.registerImage(
      {
        path: fileURLToPath(
          new URL("./travel-ir-assets/coast.png", import.meta.url),
        ),
      },
      { signal },
    );
    const horizon = await session.registerImage(
      {
        path: fileURLToPath(
          new URL("./travel-ir-assets/horizon.png", import.meta.url),
        ),
      },
      { signal },
    );
    await session.render(<Roam coast={coast} horizon={horizon} />);
    return session;
  } catch (error) {
    await session.dispose();
    throw error;
  }
}
