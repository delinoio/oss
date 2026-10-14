// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, it } from "vitest";
import {
  EntityKind,
  ErrorDetailSchema,
  FailureCode,
  ResourceSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { ConversationKind } from "./home-navigation";
import {
  RouteFailureDisposition,
  loadedSessionProvenance,
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
