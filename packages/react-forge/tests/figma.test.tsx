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
  await assert.rejects(reader.load(), isCode(ErrorCode.Authentication));
  await assert.rejects(reader.reread(), isCode(ErrorCode.Authentication));
  assert.equal(reads, 2);
});

test("lost responses to known edits are confirmed without replay", async () => {
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  try {
    await session.render(tree());
    await session.publish();
    await session.render(tree("After loss"));
    const before = connection.writes;
    connection.loseResponse = true;
    const receipt = await session.publish();
    assert.equal(receipt.status, PublishStatus.Complete);
    assert.equal(connection.writes, before + 1);
    assert.equal(
      [...connection.canvas.nodes.values()].find((n) => n.name === "Title")
        .characters,
      "After loss",
    );
    await session.publish();
    assert.equal(connection.writes, before + 1);
  } finally {
    await session.dispose();
  }
});
test("deleting a managed container never deletes an inspected foreign descendant", async () => {
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  try {
    await session.render(
      <Page name="Page">
        <Frame name="Managed" />
      </Page>,
    );
    await session.publish();
    const parent = [...connection.canvas.nodes.values()].find(
      (n) => n.name === "Managed",
    );
    const child = connection.canvas.node("TEXT");
    parent.appendChild(child);
    await session.refresh({ pageId: parent.parent.id });
    await session.render(<Page name="Page" />);
    await assert.rejects(session.publish(), isCode(ErrorCode.UnsupportedEdit));
    assert.ok(connection.canvas.nodes.has(child.id));
    assert.ok(connection.canvas.nodes.has(parent.id));
  } finally {
    await session.dispose();
  }
});
test("paged inspection and large batches stay below the live MCP response envelope", async () => {
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  try {
    await session.render(
      <Page name="Many">
        {Array.from({ length: 100 }, (_, i) => (
          <Text key={i} name={`Text ${i}`}>
            {"Unicode 여행 ✈️".repeat(25)}
          </Text>
        ))}
      </Page>,
    );
    await session.publish();
    const page = [...connection.canvas.nodes.values()].find(
      (n) => n.name === "Many",
    );
    const before = connection.stats.calls;
    await session.refresh({ pageId: page.id });
    assert.equal(connection.stats.calls - before, 4);
    assert.equal(
      session.inspect().targets.filter((n) => n.kind === "TEXT").length,
      100,
    );
  } finally {
    await session.dispose();
  }
});
test("images use scoped submit URLs once and reuse content hashes on other nodes", async () => {
  const connection = new FakeConnection();
  let uploads = 0;
  const uploadFetch: typeof fetch = async (input, init) => {
    uploads++;
    assert.equal(new Headers(init?.headers).has("Authorization"), false);
    assert.equal(init?.redirect, "error");
    const id = new URL(String(input)).pathname.split("/").at(-1)!;
    connection.canvas.nodes.get(id).fills = [
      { type: "IMAGE", imageHash: "fixture-image-hash", scaleMode: "FILL" },
    ];
    return new Response(
      JSON.stringify({ success: true, imageHash: "fixture-image-hash" }),
      { headers: { "Content-Type": "application/json" } },
    );
  };
  const session = new FigmaSession(options, connection, uploadFetch);
  try {
    const { Image } = await import("../src/figma.js");
    const bytes = Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==",
      "base64",
    );
    const asset = await session.registerImage(bytes);
    await session.render(
      <Page name="Images">
        <Image name="one" asset={asset} />
        <Image name="two" asset={asset} />
      </Page>,
    );
    await session.publish();
    assert.equal(uploads, 1);
    assert.equal(
      [...connection.canvas.nodes.values()].filter(
        (n) => n.fills?.[0]?.imageHash === "fixture-image-hash",
      ).length,
      2,
    );
    await session.publish();
    assert.equal(uploads, 1);
  } finally {
    await session.dispose();
  }
});
test("components, variants, instances, variables and styles remain native resources", async () => {
  const { VariableCollection, Variable, PaintStyle, TextStyle, ComponentSet } =
    await import("../src/figma.js");
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  try {
    await session.render(
      <Document>
        <VariableCollection name="Tokens" nodeKey="tokens" />
        <Variable
          name="Blue"
          nodeKey="blue"
          collection="tokens"
          resolvedType="COLOR"
          value="#2563EB"
          scopes={["ALL_FILLS"]}
          codeSyntax={{ WEB: "var(--blue)" }}
        />
        <PaintStyle name="Paint" nodeKey="paint" fill="#FFFFFF" />
        <TextStyle
          name="Heading"
          nodeKey="heading"
          fontName={{ family: "Inter", style: "Bold" }}
          fontSize={28}
        />
        <Page name="Components">
          <ComponentSet name="Button">
            <Component
              name="State=Default"
              nodeKey="button"
              fill="#2563EB"
              bindings={{ fills: "blue" }}
            >
              <Text textStyle="heading">Go</Text>
            </Component>
            <Component name="State=Disabled">
              <Text>Go</Text>
            </Component>
          </ComponentSet>
          <Instance component="button" name="Instance" />
        </Page>
      </Document>,
    );
    await session.publish();
    const nodes = [...connection.canvas.nodes.values()];
    assert.equal(nodes.filter((n) => n.type === "COMPONENT_SET").length, 1);
    assert.equal(nodes.filter((n) => n.type === "INSTANCE").length, 1);
    assert.equal(
      nodes.find((n) => n.type === "VARIABLE").codeSyntax.WEB,
      "var(--blue)",
    );
    const writes = connection.writes;
    await session.publish();
    assert.equal(connection.writes, writes);
    await session.refresh({ resources: true });
  } finally {
    await session.dispose();
  }
});

test("sandbox SHA-256 matches standard UTF-8 vectors and block boundaries", async () => {
  const { createHash } = await import("node:crypto");
  const { runtime } = await import("../src/figma/runtime.js");
  const code = runtime.slice(
    runtime.indexOf("function sha256"),
    runtime.indexOf("function snapshot"),
  );
  const connection = new FakeConnection();
  for (const value of [
    "",
    "abc",
    "여행 ✈️",
    "x".repeat(55),
    "x".repeat(64),
    "x".repeat(10000),
  ])
    assert.equal(
      await connection.canvas.execute(
        `${code}\nreturn sha256(${JSON.stringify(value)});`,
      ),
      createHash("sha256").update(value).digest("hex"),
    );
});
test("concurrent selected reads deduplicate and include ancestry; mounted refs measure native IDs", async () => {
  const { createRef } = await import("react");
  const connection = new FakeConnection();
  const parent = connection.canvas.node("FRAME");
  const child = connection.canvas.node("TEXT");
  parent.appendChild(child);
  const session = await FigmaSession.open(FILE, {}, connection);
  try {
    const calls = connection.stats.calls;
    const opts = { pageId: connection.canvas.page.id, nodeIds: [child.id] };
    await Promise.all([session.refresh(opts), session.refresh(opts)]);
    assert.equal(connection.stats.calls, calls + 1);
    const target = session
      .inspect()
      .targets.find((t) => t.remoteId === child.id)!;
    assert.equal(target.parentId, parent.id);
    const ref = createRef<any>();
    await session.mount(target, <Text ref={ref}>Reference</Text>);
    await session.publish();
    assert.equal(
      (await session.measure(ref.current, { revision: session.revision }))
        .coordinateSpace,
      "page",
    );
  } finally {
    await session.dispose();
  }
});
test("cancellation after a confirmed batch reports partial changes without rollback", async () => {
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  const c = new AbortController();
  try {
    await session.render(tree());
    connection.beforeWrite = () => c.abort();
    await assert.rejects(
      session.publish({ signal: c.signal }),
      (e) =>
        e instanceof FigmaPublishError &&
        e.code === ErrorCode.Cancelled &&
        e.receipt.status === PublishStatus.Partial &&
        e.receipt.createdNodeIds.length > 0,
    );
    assert.ok(connection.canvas.nodes.size > 2);
  } finally {
    await session.dispose();
  }
});

test("a failing setter on an existing node reports its confirmed partial changes", async () => {
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  try {
    await session.render(tree());
    await session.publish();
    const id = [...connection.canvas.nodes.values()].find(
      (n) => n.name === "Title",
    ).id;
    await session.render(tree("Changed before failure"));
    connection.canvas.failProperty = "fontSize";
    await assert.rejects(
      session.publish(),
      (e) =>
        e instanceof FigmaPublishError &&
        e.receipt.status === PublishStatus.Partial &&
        e.receipt.mutatedNodeIds.includes(id),
    );
    await session.publish();
    assert.equal(
      connection.canvas.nodes.get(id).characters,
      "Changed before failure",
    );
  } finally {
    await session.dispose();
  }
});


test("batched variable creation refreshes the existing collection guard", async () => {
  const { Variable } = await import("../src/figma.js");
  const connection = new FakeConnection();
  const collection = connection.canvas.api.variables.createVariableCollection("External tokens");
  const session = await FigmaSession.open(FILE, {}, connection);
  try {
    await session.refresh({ resources: true });
    await session.render(<Document>{Array.from({ length: 30 }, (_, i) =>
      <Variable key={i} nodeKey={`token-${i}`} name={`Token ${i}`} collection={`@${collection.id}`} resolvedType="FLOAT" value={i} scopes={["GAP"]} />
    )}</Document>);
    const receipt = await session.publish();
    assert.equal(receipt.status, PublishStatus.Complete);
    assert.equal(collection.variableIds.length, 30);
    assert.ok(connection.writes >= 2);
    const writes = connection.writes;
    await session.publish();
    assert.equal(connection.writes, writes);
  } finally { await session.dispose(); }
});

test("variant reparenting refreshes component guards for subsequent edits", async () => {
  const { ComponentSet } = await import("../src/figma.js");
  const connection = new FakeConnection();
  const session = new FigmaSession(options, connection);
  const design = (text: string) => <Page><ComponentSet name="Choices">{Array.from({ length: 16 }, (_, i) =>
    <Component key={i} name={`Choice=${i}`}><Text>{text}</Text></Component>
  )}</ComponentSet></Page>;
  try {
    await session.render(design("First"));
    await session.publish();
    await session.render(design("Second"));
    assert.equal((await session.publish()).status, PublishStatus.Complete);
    const nodes = [...connection.canvas.nodes.values()];
    assert.equal(nodes.filter(n => n.type === "COMPONENT_SET").length, 1);
    assert.equal(nodes.filter(n => n.type === "TEXT" && n.characters === "Second").length, 16);
  } finally { await session.dispose(); }
});

test("nested pages fail native planning before any remote creation", async () => {
  for (const children of [<Page><Page /></Page>, <Page><Frame><Page /></Frame></Page>]) {
    const connection = new FakeConnection();
    const session = new FigmaSession(options, connection);
    try {
      await session.render(children);
      await assert.rejects(session.publish(), isCode(ErrorCode.InvalidTarget));
      assert.equal(connection.writes, 0);
      assert.equal(connection.canvas.root.children.length, 1);
    } finally { await session.dispose(); }
  }
});

test("remounting transfers retained ownership without deleting omitted descendants", async () => {
  const connection = new FakeConnection();
  const frame = connection.canvas.node("FRAME");
  const foreign = connection.canvas.node("VECTOR");
  frame.appendChild(foreign);
  const session = await FigmaSession.open(FILE, {}, connection);
  try {
    await session.refresh({ pageId: connection.canvas.page.id });
    const target = session.inspect().targets.find(t => t.remoteId === frame.id)!;
    const first = await session.mount(target, <Frame><Text name="Owned">First</Text></Frame>);
    await session.publish();
    const child = frame.children.find((n: any) => n.name === "Owned");
    await first.unmount();
    const second = await session.mount(target, <Frame width={240} />);
    const result = await session.publish();
    assert.equal(result.status, PublishStatus.Complete);
    assert.deepEqual(result.createdNodeIds, []);
    assert.deepEqual(result.deletedNodeIds, []);
    assert.equal(child.parent.id, frame.id);
    assert.equal(foreign.parent.id, frame.id);
    await second.unmount();
    const third = await session.mount(target, <Frame><Text target={child.id}>Third</Text></Frame>);
    const edited = await session.publish();
    assert.deepEqual(edited.createdNodeIds, []);
    assert.deepEqual(edited.deletedNodeIds, []);
    assert.equal(child.characters, "Third");
    assert.equal(frame.width, 240);
    assert.equal(foreign.parent.id, frame.id);
    const calls = connection.stats.calls;
    await session.publish();
    assert.equal(connection.stats.calls, calls);
    await third.unmount();
  } finally { await session.dispose(); }
});
