// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import {
  EntityKind,
  ResourceSchema,
  SubagentDisplayNameSchema,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";

export function subagentFixture(sessionId: string) {
  const id = newRequestId(),
    root = newRequestId();
  return create(ResourceSchema, {
    id,
    sessionId,
    kind: EntityKind.SUBAGENT,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      execution_id: newRequestId(),
      harness: "codex",
      native_version: "0.151.0",
      root_id: root,
      first_sequence: "3",
      last_sequence: "3",
      sources: [
        {
          source: "codex-history",
          source_id: "original-history",
          sequence: "3",
        },
      ],
      observation: {
        id,
        native_id: newRequestId(),
        parent_id: root,
        source: "codex-history",
        source_id: "original-history",
        status: "running",
        requested_model: "requested-only",
        observed_model: "observed-model",
        output: {
          native_message_id: "original-child-message",
          text: "Original child output",
          partial: true,
        },
        usage: null,
      },
    }),
  });
}

export function openCodeSubagentFixture(sessionId: string) {
  const id = newRequestId(),
    root = "ses_01960dcbe1faabcdefghijklmn",
    child = "ses_01960dcbe1fbABCDEFGHIJKLMN";
  const usage = {
    scope: "child-response",
    input: "30",
    output: "6",
    total: "36",
    native_report:
      '{"input":30,"output":6,"reasoning":0,"total":36,"cache":{"read":0,"write":0}}',
  };
  return create(ResourceSchema, {
    id,
    sessionId,
    kind: EntityKind.SUBAGENT,
    schemaVersion: 1,
    revision: 2n,
    documentJson: encode({
      execution_id: newRequestId(),
      harness: "opencode",
      native_version: "1.18.32",
      root_id: root,
      first_sequence: "3",
      last_sequence: "4",
      sources: [
        { source: "opencode-task", source_id: "original-task", sequence: "3" },
        {
          source: "opencode-child-history",
          source_id: "original-child-read",
          sequence: "4",
          usage,
        },
      ],
      observation: {
        id,
        native_id: child,
        parent_id: root,
        parent_tool_id: "prt_01960dcbe1fa1234567890ABCD",
        opencode_tool: {
          id: newRequestId(),
          message_id: "msg_01960dcbe1faABCDEFGHIJKLMN",
          part_id: "prt_01960dcbe1fa1234567890ABCD",
          call_id: "original-task-call",
        },
        source: "opencode-child-history",
        source_id: "original-child-read",
        status: "completed",
        requested_model: "same-model",
        observed_model: "same-model",
        output: {
          native_message_id: "msg_01960dcbe1fbABCDEFGHIJKLMN",
          text: "Original foreground child output",
          partial: true,
        },
        usage,
      },
    }),
  });
}

/** Gives one fixture child an additive native display name like the server. */
export function withSubagentName<T extends Resource>(row: T, name: string): T {
  row.subagentDisplay = create(SubagentDisplayNameSchema, {
    childId: row.id,
    name,
  });
  return row;
}

export const subagentTriggers = () =>
  Array.from(
    globalThis.document.querySelectorAll<HTMLElement>(".subagent-name"),
  );

/** Focuses one name trigger (immediate keyboard open) and returns its card. */
export function openSubagentCard(index = 0) {
  const trigger = subagentTriggers()[index];
  if (!trigger) throw new Error("No subagent name is mounted");
  fireEvent.focus(trigger);
  return screen.getByRole("tooltip");
}

/** Waits for the loaded list, then opens the card of one child. */
export async function openLoadedSubagentCard(index = 0) {
  await waitFor(() => {
    if (subagentTriggers().length <= index) throw new Error("not loaded");
  });
  return openSubagentCard(index);
}
