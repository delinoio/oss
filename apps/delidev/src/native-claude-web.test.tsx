import timestampVectors from "../../../cmds/delidev-cli/internal/domain/testdata/claude-web-timestamps.json";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import {
  NativeClaudeMessage,
  validNativeClaudeMessage,
} from "./native-claude-message";

afterEach(cleanup);
function fixture(name = "web_search", problem = "") {
  return {
    model: "fixture-model",
    blocks: [
      {
        index: 0,
        block: {
          kind: "server_tool_use",
          text: "",
          web: {
            native_id: "srvtool_original",
            name,
            caller: null,
            call: {
              initial_input:
                name === "web_search"
                  ? '{"query":"Original query"}'
                  : '{"url":"https://fixture.invalid"}',
              input_delta: null,
            },
          },
        },
        state: "stopped",
      },
      {
        index: 1,
        block: {
          kind:
            name === "web_search"
              ? "web_search_tool_result"
              : "web_fetch_tool_result",
          text: "",
          web: {
            native_id: "srvtool_original",
            name,
            caller: "direct",
            result: {
              problem,
              search:
                !problem && name === "web_search"
                  ? [
                      {
                        url: "https://fixture.invalid/<script>inert()</script>",
                        title: "Original source <img src=x>",
                        page_age: "Yesterday",
                      },
                    ]
                  : null,
              fetch:
                !problem && name === "web_fetch"
                  ? {
                      url: "https://fixture.invalid",
                      retrieved_at: "2026-10-09T00:00:00Z",
                      document: {
                        source: "text",
                        media: "text/plain",
                        title: "Original document",
                        context: null,
                        citations: true,
                        text: "Original native content <script>inert()</script>",
                      },
                    }
                  : null,
            },
          },
        },
        state: "stopped",
      },
    ],
    stop_reason: "end_turn",
    stop_sequence: null,
  };
}
test.each(["web_search", "web_fetch"])(
  "renders original %s provenance without fetching or executing",
  (name) => {
    const data = fixture(name);
    expect(validNativeClaudeMessage(data, "complete")).toBe(true);
    const { container } = render(
      <NativeClaudeMessage content={data} state="complete" />,
    );
    expect(container.textContent).toContain(name);
    expect(container.textContent).toContain("https://fixture.invalid");
    expect(container.textContent).toContain(
      name === "web_search"
        ? "Original source <img src=x>"
        : "Original native content <script>inert()</script>",
    );
    expect(container.querySelector("script, img, a, button")).toBeNull();
  },
);
test.each(["unavailable", "url_not_allowed", "url_not_in_prior_context"])(
  "keeps native fetch error %s independent of completion",
  (problem) => {
    const data = fixture("web_fetch", problem);
    render(<NativeClaudeMessage content={data} state="complete" />);
    expect(screen.getByText(problem)).toBeTruthy();
    expect(screen.queryByText(/message unavailable/i)).toBeNull();
  },
);
test.each([
  "foreign",
  "family",
  "mixed",
  "encrypted",
  "input",
  "duplicate",
  "unknown",
  "unfinished",
])("rejects invalid retained web tool %s", (change) => {
  const data = fixture();
  const result = data.blocks[1]!.block.web;
  switch (change) {
    case "foreign":
      result.native_id = "foreign";
      break;
    case "family":
      result.name = "code_execution";
      break;
    case "mixed":
      result.result!.problem = "unavailable";
      break;
    case "encrypted":
      Object.assign(result.result!, { encrypted_content: "private" });
      break;
    case "input":
      data.blocks[0]!.block.web.call!.initial_input =
        '{"query":"original","code":"unsupported"}';
      break;
    case "duplicate":
      data.blocks.push({ ...data.blocks[1]!, index: 2 });
      break;
    case "unknown":
      Object.assign(result, { arbitrary: true });
      break;
    case "unfinished":
      data.blocks = data.blocks.slice(0, 1);
      break;
  }
  expect(validNativeClaudeMessage(data, "complete")).toBe(false);
  render(<NativeClaudeMessage content={data} state="complete" />);
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
});

test("rejects normalized impossible native retrieval dates", () => {
  const data = fixture("web_fetch");
  data.blocks[1]!.block.web.result!.fetch!.retrieved_at =
    "2026-02-30T00:00:00Z";
  expect(validNativeClaudeMessage(data, "complete")).toBe(false);
});

// Use the backend admission vectors against complete transcript rendering.
test.each(timestampVectors)(
  "preserves timestamp readability parity: $name",
  ({ value, valid }) => {
    const data = fixture("web_fetch");
    const fetch = data.blocks[1]!.block.web.result!.fetch!;
    Object.assign(fetch, { retrieved_at: value });
    expect(validNativeClaudeMessage(data, "complete")).toBe(valid);
    const { container } = render(
      <NativeClaudeMessage content={data} state="complete" />,
    );
    if (valid) {
      expect(container.textContent).toContain(
        "Original native content <script>inert()</script>",
      );
      if (value !== null) expect(container.textContent).toContain(value);
      expect(fetch.retrieved_at).toBe(value);
    } else {
      expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
    }
  },
);

const retainedWebLimit = 256 * 1024;

function fetchWithRetainedBytes(target: number, characters: string) {
  const data = fixture("web_fetch");
  const document = data.blocks[1]!.block.web.result!.fetch!.document;
  document.text = "";
  // The fixed envelope has only ASCII without HTML-significant characters.
  // Every character in the supplied token costs six bytes under Go escaping.
  const envelope = data.blocks.reduce(
    (total, entry) =>
      total + new TextEncoder().encode(JSON.stringify(entry.block.web)).length,
    0,
  );
  const remaining = target - envelope;
  const tokenBytes = characters.length * 6;
  document.text =
    characters.repeat(Math.floor(remaining / tokenBytes)) +
    "x".repeat(remaining % tokenBytes);
  return data;
}

test.each(["<", ">", "&", "\u2028", "\u2029", "<>&\u2028\u2029"])(
  "enforces exact escaped aggregate boundaries for %j",
  (characters) => {
    for (const offset of [-1, 0, 1]) {
      const data = fetchWithRetainedBytes(
        retainedWebLimit + offset,
        characters,
      );
      expect(validNativeClaudeMessage(data, "complete")).toBe(offset <= 0);
    }
  },
);

test("rejects the reported escaped document before rendering any metadata", () => {
  const data = fixture("web_fetch");
  data.blocks[1]!.block.web.result!.fetch!.document.text = "<".repeat(50_000);
  expect(validNativeClaudeMessage(data, "complete")).toBe(false);
  const { container } = render(
    <NativeClaudeMessage content={data} state="complete" />,
  );
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
  expect(screen.queryByText("Original document")).toBeNull();
  expect(container.querySelector("script, img, a, button")).toBeNull();
});

test("aggregates separate valid web results instead of resetting the limit", () => {
  const first = fixture("web_fetch");
  const second = fixture("web_fetch");
  for (const [data, id] of [
    [first, "srvtool_first"],
    [second, "srvtool_second"],
  ] as const) {
    for (const entry of data.blocks) entry.block.web.native_id = id;
    data.blocks[1]!.block.web.result!.fetch!.document.text = "&".repeat(25_000);
    expect(validNativeClaudeMessage(data, "complete")).toBe(true);
  }
  const combined = {
    ...first,
    blocks: [...first.blocks, ...second.blocks].map((entry, index) => ({
      ...entry,
      index,
    })),
  };
  expect(validNativeClaudeMessage(combined, "complete")).toBe(false);
});

test("combines escaped web metadata with ordinary text under one bound", () => {
  const data = fetchWithRetainedBytes(retainedWebLimit - 100, "<>&");
  const text = {
    index: 2,
    block: { kind: "text", text: "x".repeat(100) },
    state: "stopped",
  };
  const combined = { ...data, blocks: [...data.blocks, text] };
  expect(validNativeClaudeMessage(combined, "complete")).toBe(true);
  text.block.text += "x";
  expect(validNativeClaudeMessage(combined, "complete")).toBe(false);
});

test("preserves the original call/result order for an ordinary message", () => {
  const data = fixture("web_fetch");
  const { container } = render(
    <NativeClaudeMessage content={data} state="complete" />,
  );
  const text = container.textContent!;
  expect(text.indexOf('{"url":"https://fixture.invalid"}')).toBeLessThan(
    text.indexOf("Original native content <script>inert()</script>"),
  );
});
