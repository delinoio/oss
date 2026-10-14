// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { ErrorDetailSchema } from "./gen/delidev/v1/worker_pb.js";
import {
  clientFailure,
  FailureCode,
  FailureCause,
  failureCause,
} from "./errors.js";

describe("canonical protocol error details", () => {
  it("retains the original typed guidance without a compatibility descriptor", () => {
    const detail = create(ErrorDetailSchema, {
      code: "conflict",
      guidance: "Read the original revision.",
    });
    expect(
      clientFailure(
        new ConnectError("The source changed.", Code.Aborted, undefined, [
          { desc: ErrorDetailSchema, value: detail },
        ]),
      ),
    ).toMatchObject({
      code: FailureCode.Conflict,
      message: "The source changed.",
      guidance: "Read the original revision.",
    });
  });
  it("redacts untyped transport diagnostics and preserves retry classification", () => {
    const failure = clientFailure(
      new ConnectError("https://secret.invalid/raw", Code.Unavailable),
    );
    expect(failure.code).toBe(FailureCode.ServerUnavailable);
    expect(failure.message).not.toContain("secret");
  });
});

it("projects only confirmed configuration name conflict causes", () => {
  const failure = (code: string, cause: string) =>
    clientFailure(
      new ConnectError("Choose another name.", Code.Aborted, undefined, [
        {
          desc: ErrorDetailSchema,
          value: create(ErrorDetailSchema, { code, cause }),
        },
      ]),
    );
  expect(failure("conflict", "configuration_name_conflict").cause).toBe(
    FailureCause.ConfigurationNameConflict,
  );
  expect(failure("conflict", "revision_changed").cause).toBeUndefined();
  expect(
    failure("recovery_required", "configuration_name_conflict").cause,
  ).toBeUndefined();
  expect(
    clientFailure(new ConnectError("configuration_name_conflict", Code.Aborted))
      .cause,
  ).toBeUndefined();
});

it("projects only the exact quota code/cause pair, including retained documents", () => {
  for (const [code, cause, expected] of [
    ["missing_input", "quota_exhausted", FailureCause.QuotaExhausted],
    ["missing_input", "rate_limited", undefined],
    ["missing_input", "", undefined],
    ["resource_exhausted", "quota_exhausted", undefined],
    ["conflict", "quota_exhausted", undefined],
    ["unrecognized", "quota_exhausted", undefined],
  ] as const) {
    expect(failureCause(code, cause)).toBe(expected);
    const failure = clientFailure(
      new ConnectError("Safe cause.", Code.InvalidArgument, undefined, [
        {
          desc: ErrorDetailSchema,
          value: create(ErrorDetailSchema, { code, cause }),
        },
      ]),
    );
    expect(failure.cause).toBe(expected);
  }
  expect(
    failureCause("missing_input", { cause: "quota_exhausted" }),
  ).toBeUndefined();
  expect(failureCause("missing_input", undefined)).toBeUndefined();
  expect(
    clientFailure(new ConnectError("quota_exhausted", Code.InvalidArgument))
      .cause,
  ).toBeUndefined();
});

it("projects only proven Tailscale check causes with their exact codes", () => {
  const failure = (code: string, cause: string) =>
    clientFailure(
      new ConnectError("Unavailable.", Code.Unavailable, undefined, [
        {
          desc: ErrorDetailSchema,
          value: create(ErrorDetailSchema, { code, cause }),
        },
      ]),
    );
  const pairs: Array<[FailureCode, FailureCause]> = [
    [FailureCode.Unavailable, FailureCause.TailscaleConnectionRefused],
    [FailureCode.Unavailable, FailureCause.TailscaleTimeout],
    [FailureCode.Unavailable, FailureCause.TailscaleCertificateInvalid],
    [FailureCode.Unavailable, FailureCause.TailscaleUnexpectedHttp],
    [FailureCode.Unsupported, FailureCause.TailscaleInvalidIdentity],
  ];
  for (const [code, cause] of pairs) {
    expect(failure(code, cause).cause).toBe(cause);
    expect(failureCause(code, cause)).toBe(cause);
    // A cause with any other code is not a typed cause.
    expect(failureCause(FailureCode.Internal, cause)).toBeUndefined();
    expect(failureCause(FailureCode.Conflict, cause)).toBeUndefined();
  }
  expect(
    failureCause("unsupported", FailureCause.TailscaleConnectionRefused),
  ).toBeUndefined();
  expect(
    failureCause("unavailable", FailureCause.TailscaleInvalidIdentity),
  ).toBeUndefined();
  expect(failure("unavailable", "dns_failure").cause).toBeUndefined();
  expect(failure("unavailable", "").cause).toBeUndefined();
  expect(
    clientFailure(new ConnectError("connection_refused", Code.Unavailable))
      .cause,
  ).toBeUndefined();

  // Collision class: generic emitters (for example the Codex app-server
  // handshake timeout, unavailable + "timeout") must never decode as a
  // Check-only Tailscale cause, whatever the code.
  for (const cause of [
    "timeout",
    "connection_refused",
    "certificate_invalid",
    "unexpected_http",
    "invalid_identity",
  ])
    for (const code of Object.values(FailureCode)) {
      expect(failureCause(code, cause)).toBeUndefined();
      expect(failure(code, cause).cause).toBeUndefined();
    }
});

it("keeps every Tailscale Check cause namespaced on the wire", () => {
  const tailscale = Object.entries(FailureCause).filter(([name]) =>
    name.startsWith("Tailscale"),
  );
  expect(tailscale.map(([, wire]) => wire).sort()).toEqual([
    "tailscale_certificate_invalid",
    "tailscale_connection_refused",
    "tailscale_invalid_identity",
    "tailscale_timeout",
    "tailscale_unexpected_http",
  ]);
  const generic = Object.entries(FailureCause).filter(
    ([name]) => !name.startsWith("Tailscale"),
  );
  for (const [, wire] of generic) expect(wire.startsWith("tailscale_")).toBe(false);
});
