// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { createRouterTransport } from "@connectrpc/connect";
import { describe, expect, it, vi } from "vitest";
import {
  EntityKind,
  ErrorDetailSchema,
  FailureCode,
  ResourceSchema,
  ResourceService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { ConversationKind } from "./home-navigation";
import {
  RouteFailureDisposition,
  SearchResolutionLogging,
  SearchSessionRoute,
  loadedSessionProvenance,
  resolveSearchSession,
  inaccessibleFailure,
  routeFailureDisposition,
  routeFailureDispositions,
} from "./search-session-routing";

const withDetail = (code: FailureCode) =>
  new ConnectError("failed", Code.Unknown, undefined, [
    { desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code }) },
  ]);

describe("route failure classification table", () => {
  it("classifies every failure code exactly once", () => {
    expect(Object.keys(routeFailureDispositions).sort()).toEqual(
      Object.values(FailureCode).sort(),
    );
  });

  const definitive = new Set([
    FailureCode.InvalidArgument,
    FailureCode.NotFound,
    FailureCode.PermissionDenied,
    FailureCode.Unauthenticated,
  ]);

  it.each(Object.values(FailureCode))(
    "classifies the server detail code %s",
    (code) => {
      const expected = definitive.has(code)
        ? RouteFailureDisposition.Clear
        : RouteFailureDisposition.Retain;
      expect(routeFailureDisposition(withDetail(code))).toBe(expected);
      expect(inaccessibleFailure(withDetail(code))).toBe(
        expected === RouteFailureDisposition.Clear,
      );
    },
  );

  it.each([
    [Code.InvalidArgument, RouteFailureDisposition.Clear],
    [Code.NotFound, RouteFailureDisposition.Clear],
    [Code.PermissionDenied, RouteFailureDisposition.Clear],
    [Code.Unauthenticated, RouteFailureDisposition.Clear],
    [Code.FailedPrecondition, RouteFailureDisposition.Retain],
    [Code.Unavailable, RouteFailureDisposition.Retain],
    [Code.DeadlineExceeded, RouteFailureDisposition.Retain],
    [Code.Canceled, RouteFailureDisposition.Retain],
    [Code.Internal, RouteFailureDisposition.Retain],
    [Code.Unknown, RouteFailureDisposition.Retain],
  ])("classifies the transport code %s", (code, expected) => {
    expect(routeFailureDisposition(new ConnectError("failed", code))).toBe(
      expected,
    );
  });

  it("retains a non-Connect error", () => {
    expect(routeFailureDisposition(new TypeError("network"))).toBe(
      RouteFailureDisposition.Retain,
    );
  });
});

describe("loaded Session provenance", () => {
  const sidechatDocument = (parent: string) => ({
    workspace: "general-chat",
    fork: {
      source_session_id: parent,
      sidechat_parent_snapshot: { revision: "1" },
    },
  });
  const row = (
    document: Record<string, unknown>,
    overrides: Partial<{
      kind: EntityKind;
      revision: bigint;
      schemaVersion: number;
      sessionId: string;
      id: string;
    }> = {},
  ) => {
    const id = overrides.id ?? newRequestId();
    return create(ResourceSchema, {
      id,
      sessionId: overrides.sessionId ?? id,
      kind: overrides.kind ?? EntityKind.SESSION,
      revision: overrides.revision ?? 3n,
      schemaVersion: overrides.schemaVersion ?? 1,
      documentJson: encode(document),
    });
  };

  it("classifies a valid Sidechat and an ordinary Session", () => {
    const parent = newRequestId();
    const sidechat = row(sidechatDocument(parent));
    expect(loadedSessionProvenance(sidechat, sidechat.id)).toMatchObject({
      conversationKind: ConversationKind.Sidechat,
      sidechatParent: parent,
    });
    const ordinary = row({ workspace: "worktree" });
    expect(
      loadedSessionProvenance(ordinary, ordinary.id).conversationKind,
    ).toBe(ConversationKind.WorkSession);
  });

  it.each([
    ["a missing workspace", () => row({ name: "No workspace" })],
    ["empty Fork metadata", () => row({ workspace: "worktree", fork: {} })],
    [
      "a non-positive revision",
      () => row(sidechatDocument(newRequestId()), { revision: 0n }),
    ],
    [
      "another entity kind",
      () =>
        row(sidechatDocument(newRequestId()), { kind: EntityKind.REPOSITORY }),
    ],
    [
      "a mismatched owning session",
      () =>
        row(sidechatDocument(newRequestId()), { sessionId: newRequestId() }),
    ],
  ])("is Unknown for %s", (_name, make) => {
    const resource = make();
    expect(
      loadedSessionProvenance(resource, resource.id).conversationKind,
    ).toBe(ConversationKind.Unknown);
  });

  it("is Unknown for a resource that is not the requested ID", () => {
    const resource = row(sidechatDocument(newRequestId()));
    expect(
      loadedSessionProvenance(resource, newRequestId()).conversationKind,
    ).toBe(ConversationKind.Unknown);
  });
});

// Log fields per resolution mode. The current-window mode keeps the opaque ID
// for diagnostics; a destination window request logs the closed reason only.
describe("Search resolution diagnostics", () => {
  const sidechatOf = (id: string, parent: string) =>
    create(ResourceSchema, {
      id,
      sessionId: id,
      kind: EntityKind.SESSION,
      revision: 3n,
      schemaVersion: 1,
      documentJson: encode({
        workspace: "general-chat",
        fork: {
          source_session_id: parent,
          sidechat_parent_snapshot: { revision: "1" },
        },
      }),
    });
  const unavailableRows: [
    string,
    string,
    (id: string) => Resource | "throw" | undefined,
  ][] = [
    ["a failed read", "read-failed", () => "throw"],
    ["a missing resource", "missing-or-mismatched-resource", () => undefined],
    [
      "a mismatched resource",
      "missing-or-mismatched-resource",
      () => sidechatOf(newRequestId(), newRequestId()),
    ],
    [
      "an invalid envelope",
      "invalid-resource-envelope",
      (id) => ({ ...sidechatOf(id, newRequestId()), revision: 0n }),
    ],
    [
      "malformed parent metadata",
      "malformed-parent-metadata",
      (id) =>
        create(ResourceSchema, {
          id,
          sessionId: id,
          kind: EntityKind.SESSION,
          revision: 3n,
          schemaVersion: 1,
          documentJson: encode({ name: "No workspace" }),
        }),
    ],
  ];

  const resolve = async (
    make: (id: string) => Resource | "throw" | undefined,
    logging?: SearchResolutionLogging,
  ) => {
    const id = newRequestId();
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const info = vi.spyOn(console, "info").mockImplementation(() => {});
    try {
      const transport = createRouterTransport((router) => {
        router.service(ResourceService, {
          getResource: () => {
            const value = make(id);
            if (value === "throw")
              throw new ConnectError("unavailable", Code.Unavailable);
            return { resource: value };
          },
        });
      });
      const resolution = await resolveSearchSession(
        new QueryClient(),
        transport,
        id,
        undefined,
        logging,
      );
      return {
        id,
        resolution,
        warn: [...warn.mock.calls],
        info: [...info.mock.calls],
      };
    } finally {
      warn.mockRestore();
      info.mockRestore();
    }
  };

  it.each(unavailableRows)(
    "logs only the closed reason in anonymous mode for %s",
    async (_name, reason, make) => {
      const result = await resolve(make, SearchResolutionLogging.Anonymous);
      expect(result.resolution.route).toBe(SearchSessionRoute.Unavailable);
      expect(result.warn).toEqual([
        ["delidev.search.session_route_unavailable", { reason }],
      ]);
      expect(JSON.stringify([...result.warn, ...result.info])).not.toContain(
        result.id,
      );
    },
  );

  it.each(unavailableRows)(
    "keeps the opaque ID in the default current-window mode for %s",
    async (_name, reason, make) => {
      const result = await resolve(make);
      expect(result.warn).toEqual([
        [
          "delidev.search.session_route_unavailable",
          { sessionId: result.id, reason },
        ],
      ]);
    },
  );

  it("logs nothing for a resolvable result in either mode", async () => {
    for (const logging of Object.values(SearchResolutionLogging)) {
      const result = await resolve(
        (id) => sidechatOf(id, newRequestId()),
        logging,
      );
      expect(result.resolution.route).toBe(SearchSessionRoute.Sidechat);
      expect(result.warn).toEqual([]);
    }
  });
});
