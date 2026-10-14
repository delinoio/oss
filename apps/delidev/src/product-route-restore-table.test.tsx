// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import {
  RouteReadOutcome,
  SessionReadKind,
  SessionRestoreAction,
  classifySessionRead,
  readRouteSession,
  sessionSlotTable,
  sidechatSlotTable,
} from "./product-route-restore";
import { SearchSessionRoute } from "./search-session-routing";

type Fork = Record<string, unknown>;
const session = (
  name: string,
  options: {
    fork?: Fork;
    workspace?: string;
    revision?: bigint;
    kind?: EntityKind;
    sessionId?: string;
  } = {},
) => {
  const id = newRequestId();
  return create(ResourceSchema, {
    id,
    sessionId: options.sessionId ?? id,
    kind: options.kind ?? EntityKind.SESSION,
    revision: options.revision ?? 3n,
    schemaVersion: 1,
    documentJson: encode({
      name,
      ...(options.workspace === undefined
        ? {}
        : { workspace: options.workspace }),
      outcome: "stopped",
      archive: "active",
      ...(options.fork ? { fork: options.fork } : {}),
    }),
  });
};

/** Every resource and failure a restore read can meet. */
function world() {
  const top = session("Top", { workspace: "general-chat" });
  const otherTop = session("Other top", { workspace: "worktree" });
  const fork = session("Fork", {
    workspace: "worktree",
    fork: { source_session_id: otherTop.id },
  });
  const sidechatOf = (parent: Resource, name: string) =>
    session(name, {
      workspace: "general-chat",
      fork: {
        source_session_id: parent.id,
        sidechat_parent_snapshot: { revision: "1" },
      },
    });
  const sidechat = sidechatOf(top, "Side of top");
  const foreignSidechat = sidechatOf(otherTop, "Side of other");
  const noWorkspace = session("No workspace");
  const emptyFork = session("Empty fork", {
    workspace: "worktree",
    fork: {},
  });
  const emptySnapshot = session("Empty snapshot", {
    workspace: "worktree",
    fork: {
      source_session_id: top.id,
      sidechat_parent_snapshot: {},
    },
  });
  const badRevision = session("Bad revision", {
    workspace: "worktree",
    revision: 0n,
  });
  // The server answers a Session read with a resource of another kind.
  const wrongKindEcho = session("Echo", {
    workspace: "worktree",
    kind: EntityKind.REPOSITORY,
  });
  const resources = new Map<string, Resource>(
    [
      top,
      otherTop,
      fork,
      sidechat,
      foreignSidechat,
      noWorkspace,
      emptyFork,
      emptySnapshot,
      badRevision,
      wrongKindEcho,
    ].map((row) => [row.id, row]),
  );
  const failures = {
    notFound: newRequestId(),
    denied: newRequestId(),
    unauthenticated: newRequestId(),
    // An ID of another entity kind is an invalid argument for GetResource.
    otherKind: newRequestId(),
    unavailable: newRequestId(),
    internal: newRequestId(),
    deadline: newRequestId(),
  };
  const codes = new Map<string, Code>([
    [failures.notFound, Code.NotFound],
    [failures.denied, Code.PermissionDenied],
    [failures.unauthenticated, Code.Unauthenticated],
    [failures.otherKind, Code.InvalidArgument],
    [failures.unavailable, Code.Unavailable],
    [failures.internal, Code.Internal],
    [failures.deadline, Code.DeadlineExceeded],
  ]);
  const read = vi.fn<(id: string) => void>();
  const transport = createRouterTransport((router) => {
    router.service(ResourceService, {
      getResource: (request) => {
        read(request.id);
        const code = codes.get(request.id);
        if (code !== undefined) throw new ConnectError("failed", code);
        return { resource: resources.get(request.id) };
      },
    });
  });
  return {
    transport,
    read,
    ids: {
      top: top.id,
      otherTop: otherTop.id,
      fork: fork.id,
      sidechat: sidechat.id,
      foreignSidechat: foreignSidechat.id,
      noWorkspace: noWorkspace.id,
      emptyFork: emptyFork.id,
      emptySnapshot: emptySnapshot.id,
      badRevision: badRevision.id,
      wrongKindEcho: wrongKindEcho.id,
      missing: newRequestId(),
      ...failures,
    },
    names: { sidechat: "Side of top" },
  };
}
type Ids = ReturnType<typeof world>["ids"];

const restore = (
  value: ReturnType<typeof world>,
  selection: { id: string; sidechat?: string },
) =>
  readRouteSession(
    new QueryClient(),
    value.transport,
    selection,
    new AbortController().signal,
  );

describe("restore decision tables", () => {
  const kinds = Object.values(SessionReadKind);

  it("keeps the session-slot table row by row", () => {
    const {
      TopLevel,
      SidechatOfRequestedParent,
      SidechatOfOtherParent,
      Rejected,
      Transient,
    } = SessionReadKind;
    const {
      Clear,
      OpenSession,
      VerifySidechat,
      RetainSession,
      RetainSidechat,
    } = SessionRestoreAction;
    const rows: [SessionReadKind, SessionRestoreAction, SessionRestoreAction][] =
      [
        [TopLevel, OpenSession, VerifySidechat],
        [SidechatOfRequestedParent, Clear, Clear],
        [SidechatOfOtherParent, Clear, Clear],
        [Rejected, Clear, Clear],
        [Transient, RetainSession, RetainSidechat],
      ];
    expect(rows.map(([kind]) => kind).sort()).toEqual([...kinds].sort());
    for (const [kind, alone, withSidechat] of rows)
      expect(sessionSlotTable[kind], kind).toEqual({ alone, withSidechat });
  });

  it("keeps the sidechat-slot table row by row", () => {
    const {
      TopLevel,
      SidechatOfRequestedParent,
      SidechatOfOtherParent,
      Rejected,
      Transient,
    } = SessionReadKind;
    const { OpenSession, OpenSidechat, RetainSidechat } = SessionRestoreAction;
    const rows: [SessionReadKind, SessionRestoreAction][] = [
      [TopLevel, OpenSession],
      [SidechatOfRequestedParent, OpenSidechat],
      [SidechatOfOtherParent, OpenSession],
      [Rejected, OpenSession],
      [Transient, RetainSidechat],
    ];
    expect(rows.map(([kind]) => kind).sort()).toEqual([...kinds].sort());
    for (const [kind, action] of rows)
      expect(sidechatSlotTable[kind], kind).toBe(action);
  });

  it("never trusts a Sidechat in the top-level slot, whatever parent it retains", () => {
    for (const kind of [
      SessionReadKind.SidechatOfRequestedParent,
      SessionReadKind.SidechatOfOtherParent,
    ]) {
      expect(sessionSlotTable[kind].alone).toBe(SessionRestoreAction.Clear);
      expect(sessionSlotTable[kind].withSidechat).toBe(
        SessionRestoreAction.Clear,
      );
    }
  });

  it("classifies a read relative to the requested parent", () => {
    const sidechat = {
      route: SearchSessionRoute.Sidechat,
      parent: "p",
      name: "n",
    } as const;
    expect(classifySessionRead({ route: SearchSessionRoute.TopLevel })).toBe(
      SessionReadKind.TopLevel,
    );
    expect(classifySessionRead(sidechat, "p")).toBe(
      SessionReadKind.SidechatOfRequestedParent,
    );
    expect(classifySessionRead(sidechat, "q")).toBe(
      SessionReadKind.SidechatOfOtherParent,
    );
    expect(classifySessionRead(sidechat)).toBe(
      SessionReadKind.SidechatOfOtherParent,
    );
    expect(
      classifySessionRead({
        route: SearchSessionRoute.Unavailable,
        transient: true,
      }),
    ).toBe(SessionReadKind.Transient);
    expect(
      classifySessionRead({ route: SearchSessionRoute.Unavailable }),
    ).toBe(SessionReadKind.Rejected);
  });
});

type Expected =
  | { outcome: RouteReadOutcome.Missing }
  | {
      outcome: RouteReadOutcome.Found | RouteReadOutcome.Failed;
      open: (ids: Ids) => { id: string; parent?: string; name?: string };
    };
const clear: Expected = { outcome: RouteReadOutcome.Missing };
const found = (open: (ids: Ids) => { id: string; parent?: string }): Expected =>
  ({ outcome: RouteReadOutcome.Found, open }) as Expected;
const retained = (
  open: (ids: Ids) => { id: string; parent?: string },
): Expected => ({ outcome: RouteReadOutcome.Failed, open }) as Expected;

/** A read outcome the world can produce for one ID, by name. */
type Source = keyof Ids;

// Every resource kind returned and every read outcome, for the session slot.
const rejectedSources: Source[] = [
  "noWorkspace",
  "emptyFork",
  "emptySnapshot",
  "badRevision",
  "wrongKindEcho",
  "missing",
  "notFound",
  "denied",
  "unauthenticated",
  "otherKind",
];
const transientSources: Source[] = ["unavailable", "internal", "deadline"];

describe("session slot rows (no sidechat selection)", () => {
  const rows: [string, Source, Expected][] = [
    ["Session non-sidechat", "top", found((ids) => ({ id: ids.top }))],
    ["Fork (a top-level Session)", "fork", found((ids) => ({ id: ids.fork }))],
    // The reported row: a Sidechat ID in the top-level slot is not restorable,
    // even though its retained parent could be opened.
    ["Sidechat of an existing parent", "sidechat", clear],
    ["Sidechat of another parent", "foreignSidechat", clear],
    ...rejectedSources.map((source): [string, Source, Expected] => [
      `rejected (${source})`,
      source,
      clear,
    ]),
    ...transientSources.map((source): [string, Source, Expected] => [
      `transient (${source})`,
      source,
      retained((ids) => ({ id: ids[source] })),
    ]),
  ];

  it.each(rows)("%s", async (_label, source, expected) => {
    const value = world();
    const result = await restore(value, { id: value.ids[source] });
    expect(result.outcome).toBe(expected.outcome);
    if (expected.outcome !== RouteReadOutcome.Missing && "open" in result)
      expect(result.open).toEqual(expected.open(value.ids));
    // Only the requested ID is ever read: a retained parent is never trusted.
    expect(value.read.mock.calls).toEqual([[value.ids[source]]]);
  });
});

describe("sidechat slot rows (session slot is a verified top-level Session)", () => {
  const rows: [string, Source, Expected][] = [
    [
      "Sidechat of the requested parent",
      "sidechat",
      {
        outcome: RouteReadOutcome.Found,
        open: (ids) => ({
          id: ids.sidechat,
          parent: ids.top,
          name: "Side of top",
        }),
      },
    ],
    [
      "Sidechat of another parent",
      "foreignSidechat",
      found((ids) => ({ id: ids.top })),
    ],
    ["Session non-sidechat", "otherTop", found((ids) => ({ id: ids.top }))],
    ["Fork (a top-level Session)", "fork", found((ids) => ({ id: ids.top }))],
    ...rejectedSources.map((source): [string, Source, Expected] => [
      `rejected (${source})`,
      source,
      found((ids) => ({ id: ids.top })),
    ]),
    ...transientSources.map((source): [string, Source, Expected] => [
      `transient (${source})`,
      source,
      retained((ids) => ({ id: ids[source], parent: ids.top })),
    ]),
  ];

  it.each(rows)("%s", async (_label, source, expected) => {
    const value = world();
    const result = await restore(value, {
      id: value.ids.top,
      sidechat: value.ids[source],
    });
    expect(result.outcome).toBe(expected.outcome);
    if (expected.outcome !== RouteReadOutcome.Missing && "open" in result)
      expect(result.open).toEqual(expected.open(value.ids));
    expect(value.read.mock.calls).toEqual([
      [value.ids.top],
      [value.ids[source]],
    ]);
  });
});

describe("session slot rows with a sidechat selection (parent outcomes)", () => {
  const rows: [string, Source, Expected][] = [
    // The parent is not a verified top-level Session, so the child is never
    // read and nothing opens, whatever the child is.
    ["parent is a Sidechat of an existing Session", "sidechat", clear],
    ["parent is a Sidechat of another parent", "foreignSidechat", clear],
    ...rejectedSources.map((source): [string, Source, Expected] => [
      `parent rejected (${source})`,
      source,
      clear,
    ]),
    // A transient parent failure retains the requested child under it.
    ...transientSources.map((source): [string, Source, Expected] => [
      `parent transient (${source})`,
      source,
      retained((ids) => ({ id: ids.sidechat, parent: ids[source] })),
    ]),
  ];

  it.each(rows)("%s", async (_label, source, expected) => {
    const value = world();
    const result = await restore(value, {
      id: value.ids[source],
      sidechat: value.ids.sidechat,
    });
    expect(result.outcome).toBe(expected.outcome);
    if (expected.outcome !== RouteReadOutcome.Missing && "open" in result)
      expect(result.open).toEqual(expected.open(value.ids));
    expect(value.read.mock.calls).toEqual([[value.ids[source]]]);
  });
});

describe("aborted restore", () => {
  it("does not read the sidechat slot after the restore was aborted", async () => {
    const value = world();
    const controller = new AbortController();
    const pending = readRouteSession(
      new QueryClient(),
      value.transport,
      { id: value.ids.top, sidechat: value.ids.sidechat },
      controller.signal,
    );
    controller.abort();
    await pending;
    expect(value.read.mock.calls.length).toBeLessThanOrEqual(1);
  });
});
