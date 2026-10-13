// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import {
  EntityKind,
  ResourceSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { encode } from "./documents";
import { TranscriptItem } from "./session";

afterEach(cleanup);

it("shows a managed MCP call's server, tool and bounded outcome from its original snapshots", () => {
  const row = create(ResourceSchema, {
    id: newRequestId(),
    sessionId: newRequestId(),
    kind: EntityKind.MESSAGE,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({
      role: "tool",
      state: "complete",
      text: "",
      execution_id: newRequestId(),
      native_thread_id: "thread",
      native_turn_id: "turn",
      tool: {
        started: {
          kind: "mcp",
          status: "running",
          changes: null,
          mcp: { server: "fx_abcdef0123", tool: "write" },
        },
        completed: {
          kind: "mcp",
          status: "failed",
          changes: null,
          mcp: {
            server: "fx_abcdef0123",
            tool: "write",
            error: "user cancelled MCP tool call",
            result_text: "partial <b>text</b>",
          },
        },
      },
    }),
  });
  const view = render(<TranscriptItem resource={row} />);
  expect(view.container.textContent).toContain("fx_abcdef0123 / write");
  expect(view.container.textContent).toContain("user cancelled MCP tool call");
  expect(view.container.textContent).toContain("partial <b>text</b>");
  expect(view.container.querySelector("b")).toBeNull();
});
