import assert from "node:assert/strict";
import test from "node:test";
import React, { useState } from "react";
import { mkdtemp, rm, writeFile, readFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import {
  FigmaSession,
  FigmaPublishError,
  PublishStatus,
} from "../src/figma/session.js";
import {
  Document,
  Page,
  Frame,
  Text,
  Rectangle,
  Component,
  Instance,
} from "../src/figma.js";
import {
  CredentialReader,
  CredentialSource,
  FIGMA_ENDPOINT,
  storeAccounts,
} from "../src/figma/credentials.js";
import { ErrorCode, Format } from "../src/types.js";
import { ForgeError } from "../src/errors.js";
import { FakeConnection, FILE } from "./figma/fake.js";

const options = { fileName: "Fixture", planKey: "team::1" };
const tree = (text = "Hello") => (
  <Document>
    <Page name="Screens">
      <Frame name="Screen" width={390} height={844}>
        <Text name="Title" fontSize={28}>
          {text}
        </Text>
        <Rectangle name="Card" fill="#F2F2F2" />
      </Frame>
    </Page>
  </Document>
);
const isCode = (code: ErrorCode) => (e: unknown) =>
  e instanceof ForgeError && e.code === code;
test("Figma creates editable nodes, preserves IDs, and skips unchanged writes", async () => {
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  try {
    await session.render(tree());
    const first = await session.publish();
    assert.equal(first.status, PublishStatus.Complete);
    assert.equal(first.fileKey, FILE);
    const title = [...connection.canvas.nodes.values()].find(
      (n) => n.name === "Title",
    );
    assert.equal(title.characters, "Hello");
    const id = title.id;
    const writes = connection.writes;
    await session.publish();
    assert.equal(connection.writes, writes);
    await session.render(tree("Changed"));
    await session.publish();
    assert.equal(connection.canvas.nodes.get(id).characters, "Changed");
    assert.equal(
      [...connection.canvas.nodes.values()].filter((n) => n.name === "Title")
        .length,
      1,
    );
    assert.ok(connection.canvas.fonts.length);
    assert.ok(!JSON.stringify(session.receipt).includes("Changed"));
  } finally {
    await session.dispose();
  }
  assert.equal(connection.closed, true);
});
test("external files mount selected text without Forge metadata and preserve foreign nodes", async () => {
  const connection = new FakeConnection();
  const frame = connection.canvas.node("FRAME");
  frame.name = "External";
  const original = connection.canvas.node("TEXT");
  original.characters = "Original";
  original.fontName = { family: "Avenir Next", style: "Regular" };
  frame.appendChild(original);
  const foreign = connection.canvas.node("VECTOR");
  foreign.vectorPaths = [{ data: "M0 0 L10 10", windingRule: "NONZERO" }];
  frame.appendChild(foreign);
  const session = await FigmaSession.open(FILE, {}, connection);
  try {
    await session.refresh({ pageId: connection.canvas.page.id });
    const target = session
      .inspect()
      .targets.find((n) => n.remoteId === original.id)!;
    await session.mount(target, <Text>Replacement</Text>);
    await session.publish();
    assert.equal(original.characters, "Replacement");
    assert.equal(original.fontName.family, "Avenir Next");
    assert.equal(foreign.parent.id, frame.id);
    assert.deepEqual(foreign.vectorPaths, [
      { data: "M0 0 L10 10", windingRule: "NONZERO" },
    ]);
    await assert.rejects(
      session.mount(target, <Text>Again</Text>),
      isCode(ErrorCode.Conflict),
    );
  } finally {
    await session.dispose();
  }
});
test("external changes conflict before mutation and invalid replacements have no side effects", async () => {
  const connection = new FakeConnection();
  const n = connection.canvas.node("TEXT");
  n.characters = "Original";
  const session = await FigmaSession.open(FILE, {}, connection);
  try {
    await session.refresh({ pageId: connection.canvas.page.id });
    const target = session.inspect().targets.find((t) => t.remoteId === n.id)!;
    await session.mount(target, <Text>Ours</Text>);
    connection.beforeWrite = () => {
      n.characters = "External save";
    };
    await assert.rejects(session.publish(), isCode(ErrorCode.Conflict));
    assert.equal(n.characters, "External save");
  } finally {
    await session.dispose();
  }
  const next = new FigmaSession(options, new FakeConnection());
  try {
    await next.render(
      <Page name="Invalid">
        <Text fontSize={-1}>Invalid</Text>
      </Page>,
    );
    await assert.rejects(next.publish(), isCode(ErrorCode.MalformedInput));
  } finally {
    await next.dispose();
  }
});
test("managed insertion deletion and reordering preserve unselected children", async () => {
  const connection = new FakeConnection();
  const frame = connection.canvas.node("FRAME");
  const foreign = connection.canvas.node("TEXT");
  foreign.characters = "Keep";
  frame.appendChild(foreign);
  const session = await FigmaSession.open(FILE, {}, connection);
  try {
    await session.refresh({ pageId: connection.canvas.page.id });
    const target = session
      .inspect()
      .targets.find((t) => t.remoteId === frame.id)!;
    const children = (items: string[]) => (
      <Frame>
        {items.map((item) => (
          <Text key={item} name={item}>
            {item}
          </Text>
        ))}
      </Frame>
    );
    const mount = await session.mount(target, children(["a", "b"]));
    await session.publish();
    await mount.render(children(["b", "c"]));
    await session.publish();
    assert.deepEqual(
      frame.children.map((n: any) => n.characters),
      ["Keep", "b", "c"],
    );
    await mount.unmount();
    await session.publish();
    assert.equal(frame.children[0].id, foreign.id);
  } finally {
    await session.dispose();
  }
});
test("React state commits are reflected in explicit publication", async () => {
  let update!: (text: string) => void;
  function Screen() {
    const [text, set] = useState("First");
    update = set;
    return (
      <Page name="State">
        <Text name="state">{text}</Text>
      </Page>
    );
  }
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  try {
    await session.render(<Screen />);
    await session.publish();
    update("Second");
    await new Promise((resolve) => setImmediate(resolve));
    await session.publish();
    assert.equal(
      [...connection.canvas.nodes.values()].find((n) => n.name === "state")
        .characters,
      "Second",
    );
  } finally {
    await session.dispose();
  }
});
test("partial setters retain created IDs and retry does not duplicate known creations", async () => {
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  try {
    await session.render(tree());
    connection.canvas.failProperty = "fontSize";
    await assert.rejects(
      session.publish(),
      (e) =>
        e instanceof FigmaPublishError &&
        e.receipt.status === PublishStatus.Partial,
    );
    const created = new Set(connection.canvas.nodes.keys());
    await session.publish();
    for (const id of created) assert.ok(connection.canvas.nodes.has(id));
    assert.equal(
      [...connection.canvas.nodes.values()].filter((n) => n.name === "Title")
        .length,
      1,
    );
  } finally {
    await session.dispose();
  }
});
test("lost write responses are reconciled but ambiguous creation is never replayed", async () => {
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  try {
    await session.render(tree());
    connection.loseResponse = true;
    await assert.rejects(
      session.publish(),
      (e) =>
        e instanceof FigmaPublishError &&
        e.receipt.status === PublishStatus.Unknown,
    );
    const writes = connection.writes;
    await assert.rejects(session.publish(), isCode(ErrorCode.UnknownOutcome));
    assert.equal(connection.writes, writes);
  } finally {
    await session.dispose();
  }
});
test("receipt path conflicts precede remote creation; outputs contain no design content", async () => {
  const dir = await mkdtemp(join(tmpdir(), "figma-receipt-"));
  const path = join(dir, "result.figma.json");
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  try {
    await session.render(tree("PRIVATE-TEXT"));
    await writeFile(path, "old");
    await assert.rejects(session.exportFile(path), isCode(ErrorCode.Conflict));
    assert.equal(connection.writes, 0);
    await session.exportFile(path, { overwrite: true });
    const raw = await readFile(path, "utf8");
    assert.ok(!raw.includes("PRIVATE-TEXT"));
    const receipt = JSON.parse(raw);
    assert.equal(receipt.format, Format.Figma);
    const reopened = await FigmaSession.open(receipt, {}, connection);
    await reopened.refresh({
      pageId: [...connection.canvas.nodes.values()].find(
        (n) => n.name === "Screens",
      ).id,
    });
    assert.ok(reopened.inspect().targets.some((n) => n.name === "Title"));
    await reopened.dispose();
  } finally {
    await session.dispose();
    await rm(dir, { recursive: true, force: true });
  }
});
test("cancel before publish performs no writes and disposed sessions reject work", async () => {
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  await session.render(tree());
  const c = new AbortController();
  c.abort();
  await assert.rejects(
    session.publish({ signal: c.signal }),
    isCode(ErrorCode.Cancelled),
  );
  assert.equal(connection.writes, 0);
  await session.dispose();
  assert.throws(() => session.render(tree()), isCode(ErrorCode.Disposed));
});
test("Codex and Claude adapters read only matching Figma credentials and never expose raw failures", async () => {
  const token = "SYNTHETIC-FIGMA-SECRET";
  const expiry = Date.now() + 3600000;
  const accounts = storeAccounts("figma");
  for (const source of [CredentialSource.Codex, CredentialSource.ClaudeCode]) {
    const seen: string[] = [];
    const reader = new CredentialReader(
      { source },
      async (service, account) => {
        seen.push(`${service}:${account}`);
        if (source === CredentialSource.Codex)
          return JSON.stringify({
            server_name: "figma",
            url: FIGMA_ENDPOINT,
            expires_at: expiry,
            token_response: { access_token: token },
          });
        return JSON.stringify({
          mcpOAuth: {
            [accounts[0]!]: {
              serverName: "figma",
              serverUrl: FIGMA_ENDPOINT,
              accessToken: token,
              expiresAt: expiry,
            },
          },
          claudeAiOauth: { accessToken: "UNRELATED" },
        });
      },
    );
    assert.equal(await reader.load(), token);
    assert.ok(
      seen.every((s) =>
        s.startsWith(
          source === CredentialSource.Codex
            ? "Codex MCP Credentials:"
            : "Claude Code-credentials:",
        ),
      ),
    );
    reader.clear();
  }
  const invalid = new CredentialReader(
    { source: CredentialSource.Codex },
    async () =>
      JSON.stringify({
        server_name: "figma",
        url: "https://example.com",
        token_response: { access_token: token },
      }),
  );
  await assert.rejects(
    invalid.load(),
    (e) => isCode(ErrorCode.Authentication)(e) && !String(e).includes(token),
  );
});
test("expired selected credentials reread once and do not fall back to another provider", async () => {
  let reads = 0;
  const reader = new CredentialReader({}, async (service) => {
    assert.equal(service, "Codex MCP Credentials");
    reads++;
    return JSON.stringify({
      server_name: "figma",
      url: FIGMA_ENDPOINT,
      expires_at: 1,
      token_response: { access_token: "SYNTHETIC" },
    });
  });
  await assert.rejects(reader.load(), isCode(ErrorCode.Authentication));
  assert.equal(reads, 2);
});
