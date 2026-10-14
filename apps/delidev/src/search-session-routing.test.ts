// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, it } from "vitest";
import { ErrorDetailSchema, FailureCode } from "@delinoio/delidev-api-client";
import {
  RouteFailureDisposition,
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
