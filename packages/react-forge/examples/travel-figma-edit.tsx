import React from "react";
import { fileURLToPath } from "node:url";
import {
  openFigma,
  CredentialSource,
  Frame,
  Text,
  Image,
  type FigmaTarget,
} from "@delino/react-forge/figma";

// This example explicitly selects named targets from the ROAM fixture. The
// engine never infers ownership or missing IDs from names during recovery.
export default async function task({
  data,
  signal,
}: {
  data: { fileKey: string; alternate?: boolean };
  signal: AbortSignal;
}) {
  const session = await openFigma(data.fileKey, {
    signal,
    credentials: { source: CredentialSource.Codex },
  });
  const one = (name: string): FigmaTarget => {
    const matches = session.inspect().targets.filter((t) => t.name === name);
    if (matches.length !== 1)
      throw new Error("Expected one target in the ROAM fixture.");
    return matches[0]!;
  };
  try {
    await session.refresh({ pageId: one("ROAM · Mobile").remoteId, signal });
    const asset = await session.registerImage(
      {
        path: fileURLToPath(
          new URL(
            data.alternate
              ? "./travel-ir-assets/coast.png"
              : "./travel-ir-assets/horizon.png",
            import.meta.url,
          ),
        ),
      },
      { signal },
    );
    await session.mount(
      one("Explore title"),
      <Text fontSize={32}>
        {data.alternate
          ? "A slower kind\nof weekend."
          : "Your next chapter.\nStarts by the sea."}
      </Text>,
    );
    await session.mount(one("Explore hero"), <Image asset={asset} />);
    const stop = one("Stop 09:30");
    const originalTitle = session
      .inspect()
      .targets.find((t) =>
        [
          "Coffee & a pastel de nata",
          "Coffee, then the waterfront",
          "Itinerary first title",
        ].includes(t.name),
      )!;

    await session.mount(
      one("Trip cover"),
      <Frame y={data.alternate ? 188 : 184} />,
    );
    const existingNote = session
      .inspect()
      .targets.find((t) => t.name === "Evening idea");
    const existingLabel = session
      .inspect()
      .targets.find((t) => t.name === "Evening idea label");
    // Explicitly select existing IDs, preserving undeclared fields and children.
    // The optional note binding also makes a second process run duplicate-free.
    await session.mount(
      one("Itinerary items"),
      <Frame gap={data.alternate ? 10 : 8}>
        <Frame target={stop.remoteId}>
          <Frame target={originalTitle.parentId}>
            <Text target={originalTitle.remoteId} name="Itinerary first title">
              {data.alternate
                ? "Morning coffee, no hurry."
                : "Coffee, then the waterfront"}
            </Text>
          </Frame>
        </Frame>
        <Frame target={one("Stop 11:00").remoteId} />
        <Frame target={one("Stop 13:00").remoteId} />
        <Frame
          target={existingNote?.remoteId}
          name="Evening idea"
          width={342}
          height={34}
          fill="#E9EFFB"
          cornerRadius={12}
          layoutMode="HORIZONTAL"
          paddingLeft={14}
          counterAxisAlignItems="CENTER"
          primaryAxisSizingMode="FIXED"
          counterAxisSizingMode="FIXED"
        >
          <Text
            target={existingLabel?.remoteId}
            name="Evening idea label"
            fontName={{ family: "DM Sans", style: "Medium" }}
            fontSize={12}
            color="#2563EB"
            textAutoResize="WIDTH_AND_HEIGHT"
          >
            18:00 Sunset by the river
          </Text>
        </Frame>
      </Frame>,
    );
    return session;
  } catch (error) {
    await session.dispose();
    throw error;
  }
}
