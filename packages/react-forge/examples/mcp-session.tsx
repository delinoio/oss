import { createSession, Format, type McpTaskContext } from "@delino/react-forge";
import { Document, Page, Text } from "@delino/react-forge/pdf";

/** MCP task: repeated calls retain this session and its rendering function. */
export default async function task({ session, state, data, signal }: McpTaskContext) {
  signal.throwIfAborted();
  const document = session ?? createSession(Format.Pdf);
  if (!session) {
    state.set("render", (text: string) => document.render(
      <Document language="en-US"><Page><Text>{text}</Text></Page></Document>,
    ));
  }
  const title = typeof data === "object" && data !== null && "title" in data && typeof data.title === "string"
    ? data.title : "A persistent React Forge session";
  try {
    await (state.get("render") as (text: string) => Promise<void>)(title);
    return document;
  } catch (error) {
    if (!session) await document.dispose();
    throw error;
  }
}
