// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import {
  EntityKind,
  ResourceSchema,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createRef } from "react";
import { encode, type Document } from "./documents";
import {
  commandPreview,
  nativeCommandPresentation,
  NativeCommandBody,
} from "./native-command-presentation";
import { ToolTurnTranscript } from "./tool-turn-transcript";
import { conversationProjection } from "./tool-turn-projection";
import { TranscriptItem } from "./session";
afterEach(cleanup);
const sessionId = newRequestId(),
  execution = newRequestId(),
  id = newRequestId();
function row(data: Document, key = id, revision = 1n) {
  return create(ResourceSchema, {
    id: key,
    sessionId,
    kind: EntityKind.MESSAGE,
    schemaVersion: 1,
    revision,
    documentJson: encode({
      role: "tool",
      state: "complete",
      text: "",
      execution_id: execution,
      native_thread_id: "original-thread",
      native_turn_id: "original-turn",
      ...data,
    }),
  });
}
function codex(
  command = "/bin/zsh -lc pwd",
  output: string | null = "/workspace/project",
) {
  const source = {
    command,
    cwd: "/workspace/project",
    source: "agent",
    actions: [],
    process_id: null,
    aggregated_output: null as string | null,
    exit_code: null,
    duration_ms: null,
    plugin_id: null,
    script_path: null,
  };
  return row({
    tool: {
      started: {
        kind: "command",
        status: "running",
        changes: null,
        command: source,
      },
      completed: {
        kind: "command",
        status: "completed",
        changes: null,
        command: { ...source, aggregated_output: output },
      },
      output: "separate stream\n  original",
    },
  });
}
function claude(command = "/bin/zsh -lc pwd") {
  const initial = JSON.stringify({ command: "initial command" }),
    proposed = JSON.stringify({ command: "proposed command" }),
    applied = JSON.stringify({ command });
  return row({
    native_id: "tool_original",
    native_parent_id: "msg_original",
    claude_tool: {
      reference: { id, native_id: "tool_original", name: "Bash" },
      message_id: newRequestId(),
      native_message_id: "msg_original",
      index: 0,
      caller: null,
      initial_input: initial,
      input_delta: proposed,
      proposal: { proposed, applied },
      result: {
        native_event_id: "123e4567-e89b-42d3-a456-426614174000",
        is_error: false,
        text: null,
        blocks: [
          { kind: "text", text: "" },
          { kind: "text", text: "<script>inert()</script>\n  original" },
        ],
        structured: '{"native":true}',
      },
    },
  });
}
function shell(command = "/bin/zsh -lc pwd") {
  const started = {
    kind: "opencode-shell",
    status: "pending",
    shell: { call_id: "original", input: {}, raw: "partial input" },
  };
  const running = {
    kind: "opencode-shell",
    status: "running",
    shell: {
      call_id: "original",
      input: { command, workdir: "/workspace/project" },
      time: { start: 100 },
      metadata: { output: "running preview", exit: null, exit_observed: false },
    },
  };
  const completed = {
    kind: "opencode-shell",
    status: "completed",
    shell: {
      call_id: "original",
      input: { command, workdir: "/workspace/project" },
      time: { start: 100, end: 200 },
      title: "Original title",
      output: "completed output",
      metadata: {
        output: "independent final preview",
        exit: 7,
        exit_observed: true,
        truncated: true,
        outputPath: "https://untrusted.invalid/output",
        interrupted: true,
      },
    },
  };
  return row({
    tool: {
      started,
      states: [{ sequence: 1, snapshot: running }],
      completed,
      output: null,
    },
  });
}
const data = (r: Resource) =>
  JSON.parse(new TextDecoder().decode(r.documentJson)) as Document;
function group(r: Resource) {
  const query = {
    pages: [
      {
        token: "original-page",
        nextPageToken: "",
        rows: [conversationProjection(r, sessionId)],
      },
    ],
    payloadPages: [{ token: "original-page", payload: [r] }],
    nextPageToken: "",
    restore: vi.fn(),
    protect: vi.fn(),
    measure: vi.fn(),
  };
  const props = {
    sessionId,
    query,
    live: new Map<string, Resource>(),
    removed: new Set<string>(),
    arrivals: [],
    root: createRef<HTMLDivElement>(),
    render: (v: Resource) => <TranscriptItem resource={v} />,
  };
  return { query, props };
}
function toggle(node: HTMLDetailsElement) {
  node.open = true;
  fireEvent(node, new Event("toggle"));
}
describe("resident native command presentation", () => {
  it.each([
    ["/bin/zsh -lc pwd", "pwd"],
    ["/bin/bash -lc pwd", "/bin/bash -lc pwd"],
    ["/bin/zsh -l -c pwd", "/bin/zsh -l -c pwd"],
    ["echo /bin/zsh -lc pwd", "echo /bin/zsh -lc pwd"],
    [" /bin/zsh -lc pwd", " /bin/zsh -lc pwd"],
    ['/bin/zsh -lc  printf "한글🙂"\n  exact ', ' printf "한글🙂"\n  exact '],
  ])("removes only the exact Codex prefix from %s", (source, preview) => {
    const p = nativeCommandPresentation(codex(source))!;
    expect(commandPreview(p)).toBe(preview);
    expect(p.command).toBe(source);
  });
  it("uses applied Claude Bash and immutable OpenCode inputs without changing their wrappers", () => {
    for (const r of [claude(), shell()])
      expect(commandPreview(nativeCommandPresentation(r)!)).toBe(
        "/bin/zsh -lc pwd",
      );
    const d = data(claude());
    const c = d.claude_tool as Document;
    c.result = null;
    c.proposal = null;
    c.input_delta = '{"command":"partial';
    expect(
      nativeCommandPresentation(row({ ...d, state: "streaming" }))?.command,
    ).toBe("initial command");
  });
  it("keeps partial/malformed/mixed and foreign payloads on the original renderer", () => {
    const c = data(claude());
    (c.claude_tool as Document).reference = {
      id: newRequestId(),
      native_id: "tool_original",
      name: "Bash",
    };
    expect(nativeCommandPresentation(row(c))).toBeUndefined();
    const missing = data(codex());
    delete ((missing.tool as Document).started as Document).command;
    expect(nativeCommandPresentation(row(missing))).toBeUndefined();
    const s = data(shell());
    (((s.tool as Document).completed as Document).shell as Document).input = {
      command: "changed",
    };
    expect(nativeCommandPresentation(row(s))).toBeUndefined();
    const pending = data(shell());
    delete (pending.tool as Document).completed;
    delete (pending.tool as Document).states;
    expect(
      nativeCommandPresentation(row({ ...pending, state: "streaming" })),
    ).toBeUndefined();
    expect(
      nativeCommandPresentation(
        row({ ...data(codex()), grok_tool: { command: "fake" } }),
      ),
    ).toBeUndefined();
  });
  it("renders aggregate directly, preserves exact original Details and independent stream", () => {
    const { props, query } = group(codex());
    const { container } = render(<ToolTurnTranscript {...props} />);
    const owner = container.querySelector<HTMLDetailsElement>(".tool-turn")!,
      entry = owner.querySelector<HTMLDetailsElement>("li > details")!;
    expect(entry.querySelector(".tool-command-preview")?.textContent).toBe(
      "> pwd",
    );
    toggle(owner);
    toggle(entry);
    expect(entry.querySelector(".tool-command-output")?.textContent).toBe(
      "/workspace/project",
    );
    expect(
      entry.querySelector("article,header,[data-tool-primary]"),
    ).toBeNull();
    expect(entry.querySelector("summary small")).toBeNull();
    const details = entry.querySelector<HTMLDetailsElement>(
      ".tool-command-details",
    )!;
    expect(details.open).toBe(false);
    expect(screen.getByLabelText("Command").textContent).toBe(
      "/bin/zsh -lc pwd",
    );
    expect(screen.getByLabelText("Native streamed output").textContent).toBe(
      "separate stream\n  original",
    );
    expect(query.restore).not.toHaveBeenCalled();
    expect(
      JSON.stringify(
        conversationProjection(codex(), sessionId),
        (_key, value) => (typeof value === "bigint" ? value.toString() : value),
      ),
    ).not.toContain("pwd");
  });
  it("distinguishes empty from absent output without interpreting markup or reading paths", () => {
    let p = nativeCommandPresentation(codex("pwd", ""))!;
    const { container, rerender } = render(<NativeCommandBody value={p} />);
    expect(container.querySelector("pre code")?.textContent).toBe("");
    expect(
      screen.queryByText("No native text output has been observed."),
    ).toBeNull();
    const absent = data(codex("pwd", null));
    (absent.tool as Document).output = null;
    p = nativeCommandPresentation(row(absent))!;
    rerender(<NativeCommandBody value={p} />);
    expect(
      screen.getByText("No native text output has been observed."),
    ).toBeTruthy();
    rerender(
      <NativeCommandBody value={nativeCommandPresentation(claude())!} />,
    );
    expect(
      screen.getByLabelText("Tool result blocks").querySelectorAll("pre code"),
    ).toHaveLength(2);
    expect(
      screen.getByLabelText("Tool result blocks").querySelector("pre code")
        ?.textContent,
    ).toBe("");
    expect(container.querySelectorAll("script,a,button,img")).toHaveLength(0);
    rerender(<NativeCommandBody value={nativeCommandPresentation(shell())!} />);
    expect(screen.getByLabelText("Shell result").textContent).toBe(
      "completed output",
    );
    expect(screen.getByText("Native exit code: 7")).toBeTruthy();
    expect(
      screen.getByText("The native Shell result is truncated."),
    ).toBeTruthy();
    expect(screen.getByText("Native interruption: Observed")).toBeTruthy();
    expect(container.querySelector("a")).toBeNull();
    expect(container.textContent).toContain("https://untrusted.invalid/output");
    expect(container.textContent).toContain("running preview");
    expect(container.textContent).toContain("independent final preview");
  });
  it("preserves command/detail choices and focused entry through revisions and payload eviction", () => {
    const r = codex(),
      { props, query } = group(r),
      { container, rerender } = render(<ToolTurnTranscript {...props} />);
    const owner = container.querySelector<HTMLDetailsElement>(".tool-turn")!,
      entry = owner.querySelector<HTMLDetailsElement>("li > details")!,
      details = entry.querySelector<HTMLDetailsElement>(
        ".tool-command-details",
      )!;
    toggle(owner);
    toggle(entry);
    toggle(details);
    const trigger = entry.querySelector("summary")!;
    trigger.focus();
    rerender(
      <ToolTurnTranscript
        {...props}
        live={new Map([[r.id, { ...r, revision: 2n }]])}
      />,
    );
    expect(entry.open).toBe(true);
    expect(details.open).toBe(true);
    expect(document.activeElement).toBe(trigger);
    rerender(
      <ToolTurnTranscript {...props} query={{ ...query, payloadPages: [] }} />,
    );
    expect(entry.querySelector(".tool-command-preview")).toBeNull();
    expect(entry.textContent).toContain("command");
    fireEvent.click(entry.querySelector("button")!);
    expect(query.restore).toHaveBeenCalledWith("original-page");
    rerender(<ToolTurnTranscript {...props} />);
    expect(
      entry.querySelector<HTMLDetailsElement>(".tool-command-details")!.open,
    ).toBe(true);
    expect(query.pages[0].rows[0].tool).toEqual({
      owner: JSON.stringify([
        sessionId,
        execution,
        "original-thread",
        "original-turn",
      ]),
      name: "command",
      state: "completed",
    });
  });
});
