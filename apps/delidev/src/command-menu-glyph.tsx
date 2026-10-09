// SPDX-License-Identifier: Apache-2.0
import { ConversationKind } from "./home-navigation";
export enum CommandGlyph { WorkSession, Chat, Branch, Sidechat, Folder, Search, Settings, Help, Plus, PullRequests, Usage, Schedule, Inbox }
const paths: Record<CommandGlyph, string> = {
 [CommandGlyph.WorkSession]: "M3 4h18v16H3zM6 8l3 3-3 3m6 0h5", [CommandGlyph.Chat]: "M4 4h16v12H9l-5 4z", [CommandGlyph.Branch]: "M6 3v12a4 4 0 0 0 4 4h8M6 9h8a4 4 0 0 0 4-4V3", [CommandGlyph.Sidechat]: "M3 3h13v10H8l-5 4zM10 16h7l4 4V9h-3", [CommandGlyph.Folder]: "M3 6h7l2 2h9v11H3z", [CommandGlyph.Search]: "M10 3a7 7 0 1 0 0 14 7 7 0 0 0 0-14m5 12 6 6", [CommandGlyph.Settings]: "M9 3h6l1 3 3 1 2 5-2 5-3 1-1 3H9l-1-3-3-1-2-5 2-5 3-1zM12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8", [CommandGlyph.Help]: "M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20M9 9a3 3 0 0 1 6 0c0 2-3 2-3 5m0 3v1", [CommandGlyph.Plus]: "M12 4v16M4 12h16", [CommandGlyph.PullRequests]: "M6 3v18M18 21V9a4 4 0 0 0-4-4h-2m3-3-3 3 3 3", [CommandGlyph.Usage]: "M5 20V12M12 20V4M19 20V8", [CommandGlyph.Schedule]: "M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20m0 4v6l4 3", [CommandGlyph.Inbox]: "M4 3h16v17H4zM4 14h5l1 3h4l1-3h5",
};
export function commandGlyph(value: string): CommandGlyph {
 if (value.startsWith("settings:") || value === "navigate:settings") return CommandGlyph.Settings;
 if (value.startsWith("help:")) return CommandGlyph.Help;
 return ({ "navigate:pull-requests": CommandGlyph.PullRequests, "navigate:usage": CommandGlyph.Usage, "navigate:schedules": CommandGlyph.Schedule, "navigate:inbox": CommandGlyph.Inbox, "navigate:search": CommandGlyph.Search, "create:new-session": CommandGlyph.Plus, "create:new-project": CommandGlyph.Folder } as Record<string, CommandGlyph>)[value] ?? CommandGlyph.Chat;
}
export const sessionGlyph = (kind: ConversationKind) => kind === ConversationKind.WorkSession ? CommandGlyph.WorkSession : kind === ConversationKind.Fork ? CommandGlyph.Branch : kind === ConversationKind.Sidechat ? CommandGlyph.Sidechat : CommandGlyph.Chat;
export function CommandIcon({ glyph }: { glyph: CommandGlyph }) { return <svg aria-hidden="true" focusable="false" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d={paths[glyph]}/></svg>; }
