import React from "react";
import { importOffice, Format, ForgeError, ErrorCode } from "react-forge";
import { Text } from "react-forge/pptx";
import { Paragraph } from "react-forge/docx";
import { Cell } from "react-forge/xlsx";

export default async function task({ data, signal }: { data?: { format: Format; source: string; text?: string }; signal: AbortSignal }) {
  if (!data || ![Format.Pptx, Format.Docx, Format.Xlsx].includes(data.format) || typeof data.source !== "string") {
    throw new ForgeError(ErrorCode.MalformedInput, "Editing requires data.format (pptx/docx/xlsx) and data.source (an explicit local path).");
  }
  const session = await importOffice(data.format, { path: data.source }, { signal });
  try {
    const kind = data.format === Format.Pptx ? "text" : data.format === Format.Docx ? "paragraph" : "cell";
    const target = session.inspect().targets.find(target => target.kind === kind && target.editable);
    if (!target) throw new ForgeError(ErrorCode.InvalidTarget, "No supported text region is available in this document.");
    const text = data.text ?? "Edited by React Forge";
    await session.mount(target, data.format === Format.Pptx ? <Text>{text}</Text>
      : data.format === Format.Docx ? <Paragraph>{text}</Paragraph> : <Cell value={text} />);
    return session;
  } catch (error) { await session.dispose(); throw error; }
}
