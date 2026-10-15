// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import {
  EntityKind,
  ResourceSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { expect, it } from "vitest";
import { encode } from "./documents";
import { installationVersionAllowed } from "./harness-version";
import {
  OpenCodeInstallationCause,
  claudeRunnerObservation,
  installationObservation,
  openCodeInstallationCause,
  validRunnerObservation,
} from "./runner-observation";
const installation = {
  harness: "claude-code",
  state: "detected",
  version: "2.1.236",
  protocol_verified: true,
  protocol: { state: "verified" },
};
const observed = (overrides: Record<string, unknown> = {}) =>
  create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.MACHINE,
    schemaVersion: 1,
    revision: 9007199254740993n,
    documentJson: encode({
      name: "Runner",
      disabled: false,
      worker_capabilities: ["native-claude-subscriptions-v1"],
      installations: [installation],
      ...overrides,
    }),
  });
it("projects independent closed Claude exclusion causes without interpreting native messages", () => {
  expect(claudeRunnerObservation(observed())).toEqual({
    detectedVersion: "2.1.236",
  });
  expect(claudeRunnerObservation(observed({ disabled: true })).cause).toBe(
    "disabled",
  );
  expect(
    claudeRunnerObservation(observed({ worker_capabilities: [] })).cause,
  ).toBe("capability");
  expect(claudeRunnerObservation(observed({ installations: [] })).cause).toBe(
    "missing",
  );
  expect(
    claudeRunnerObservation(
      observed({ installations: [installation, installation] }),
    ).cause,
  ).toBe("duplicate");
  for (const [state, cause] of [
    ["permission-denied", "permission"],
    ["failed", "installationFailed"],
    ["unchecked", "unchecked"],
    ["missing", "missing"],
    ["incompatible", "version"],
  ])
    expect(
      claudeRunnerObservation(
        observed({
          installations: [
            {
              ...installation,
              state,
              problem: { message: "/private/credential.txt" },
            },
          ],
        }),
      ).cause,
    ).toBe(cause);
  expect(
    claudeRunnerObservation(
      observed({ installations: [{ ...installation, version: "2.1.235" }] }),
    ),
  ).toEqual({ cause: "version", detectedVersion: "2.1.235" });
  for (const detectedVersion of ["2.1.237", "3.0.0-rc.1", "2.1.236+build.5"])
    expect(
      claudeRunnerObservation(
        observed({
          installations: [{ ...installation, version: detectedVersion }],
        }),
      ),
    ).toEqual({ detectedVersion });
  expect(
    claudeRunnerObservation(
      observed({
        installations: [{ ...installation, version: "2.1.236-rc.1" }],
      }),
    ),
  ).toEqual({ cause: "version", detectedVersion: "2.1.236-rc.1" });
  expect(
    claudeRunnerObservation(
      observed({
        installations: [
          {
            ...installation,
            version: "2.1.237",
            protocol: { state: "failed" },
            protocol_verified: false,
          },
        ],
      }),
    ),
  ).toEqual({ cause: "protocolFailed", detectedVersion: "2.1.237" });
  expect(
    claudeRunnerObservation(
      observed({
        installations: [
          {
            ...installation,
            protocol: {
              state: "failed",
              problem: { message: "secret native output" },
            },
            protocol_verified: false,
          },
        ],
      }),
    ).cause,
  ).toBe("protocolFailed");
});
it("treats malformed and unknown evidence as unavailable without exposing content", () => {
  for (const patch of [
    { disabled: "true" },
    { worker_capabilities: [20] },
    { installations: "native secret" },
    {
      installations: [
        { ...installation, state: "future-state", version: "/private/native" },
      ],
    },
    { installations: [{ ...installation, version: "/private/native" }] },
  ]) {
    const projection = claudeRunnerObservation(observed(patch));
    expect(projection.cause).toBe("unavailable");
    expect(JSON.stringify(projection)).not.toContain("private");
    expect(JSON.stringify(projection)).not.toContain("secret");
  }
  expect(
    claudeRunnerObservation(
      create(ResourceSchema, {
        ...observed(),
        documentJson: new TextEncoder().encode("{bad native secret"),
      }),
    ).cause,
  ).toBe("unavailable");
});

it("does not accept malformed flags or unbounded observations as current Runner ownership", () => {
  expect(validRunnerObservation(observed({ disabled: "false" }))).toBe(false);
  expect(validRunnerObservation(observed({ installations: [null] }))).toBe(
    false,
  );
  expect(
    validRunnerObservation(
      observed({ worker_capabilities: Array(257).fill("unknown") }),
    ),
  ).toBe(false);
  expect(validRunnerObservation(observed({ disabled: undefined }))).toBe(true);
});

it("retains a newly paired Go Runner owner with null unobserved installations without granting Claude readiness", () => {
  const row = observed({ installations: null });
  expect(validRunnerObservation(row)).toBe(true);
  expect(claudeRunnerObservation(row)).toEqual({ cause: "unavailable" });
  expect(validRunnerObservation(observed({ installations: {} }))).toBe(false);
  expect(validRunnerObservation(observed({ installations: [null] }))).toBe(
    false,
  );
});

it("classifies an under-baseline OpenCode installation as a version problem before protocol states", () => {
  const opencode = {
    harness: "opencode",
    state: "detected",
    version: "1.18.32",
    protocol_verified: true,
    protocol: { state: "verified" },
  };
  const versionAllowed = installationVersionAllowed("opencode");
  expect(
    installationObservation(
      { ...opencode, version: "1.18.20" },
      versionAllowed,
    ),
  ).toEqual({ cause: "version", detectedVersion: "1.18.20" });
  // The version problem wins over every protocol state, including verified.
  for (const protocol of [
    { protocol_verified: false, protocol: { state: "unsupported" } },
    { protocol_verified: false, protocol: { state: "failed" } },
    { protocol_verified: false },
  ])
    expect(
      installationObservation(
        { ...opencode, ...protocol, version: "1.18.20" },
        versionAllowed,
      ),
    ).toEqual({ cause: "version", detectedVersion: "1.18.20" });
  for (const detectedVersion of [
    "1.18.32",
    "1.18.33",
    "1.19.0",
    "1.18.32+build.5",
    "2.0.0-rc.1",
  ])
    expect(
      installationObservation(
        { ...opencode, version: detectedVersion },
        versionAllowed,
      ),
    ).toEqual({ detectedVersion });
  // Admitted versions still reach the protocol states.
  expect(
    installationObservation(
      {
        ...opencode,
        version: "1.18.33",
        protocol_verified: false,
        protocol: { state: "unsupported" },
      },
      versionAllowed,
    ),
  ).toEqual({ cause: "protocolUnsupported", detectedVersion: "1.18.33" });
  expect(
    installationObservation(
      { ...opencode, version: "1.19.0", protocol_verified: false },
      versionAllowed,
    ),
  ).toEqual({ cause: "protocol", detectedVersion: "1.19.0" });
  for (const malformed of ["", "v1.19.0", "1.18", "not-a-version", 118, null])
    expect(
      installationObservation(
        { ...opencode, version: malformed },
        versionAllowed,
      ),
    ).toEqual({ cause: "unavailable" });
});
it("keeps Codex and Grok installation classification protocol-only", () => {
  expect(installationVersionAllowed("codex")).toBeUndefined();
  expect(installationVersionAllowed("grok-build")).toBeUndefined();
  expect(installationVersionAllowed("unknown")).toBeUndefined();
  expect(installationVersionAllowed("claude-code")?.("2.1.235")).toBe(false);
  expect(installationVersionAllowed("claude-code")?.("2.1.236")).toBe(true);
});

// Record shapes mirror the Go discovery output for OpenCode (see the harness
// discovery tests). Each row is a path that leaves the installation unusable.
it.each([
  {
    path: "relative explicit path, non-regular executable, Windows shell wrapper, unrunnable executable or unparseable version output",
    record: { state: "incompatible" },
    cause: OpenCodeInstallationCause.InvalidInstallation,
    observation: { cause: "version" },
  },
  {
    path: "detected version under the minimum",
    record: {
      state: "detected",
      version: "1.18.31",
      protocol_verified: false,
      protocol: { state: "unsupported" },
    },
    cause: OpenCodeInstallationCause.BelowBaseline,
    observation: { cause: "version", detectedVersion: "1.18.31" },
  },
  {
    path: "prerelease of the minimum",
    record: {
      state: "detected",
      version: "1.18.32-rc.1",
      protocol_verified: false,
      protocol: { state: "unsupported" },
    },
    cause: OpenCodeInstallationCause.BelowBaseline,
    observation: { cause: "version", detectedVersion: "1.18.32-rc.1" },
  },
  {
    path: "protocol profile mismatch at the minimum",
    record: {
      state: "detected",
      version: "1.18.32",
      protocol_verified: false,
      protocol: { state: "unsupported" },
    },
    cause: OpenCodeInstallationCause.ProtocolProfileMismatch,
    observation: { cause: "protocolUnsupported", detectedVersion: "1.18.32" },
  },
  {
    path: "protocol profile mismatch at a newer version",
    record: {
      state: "detected",
      version: "1.19.0",
      protocol_verified: false,
      protocol: { state: "unsupported" },
    },
    cause: OpenCodeInstallationCause.ProtocolProfileMismatch,
    observation: { cause: "protocolUnsupported", detectedVersion: "1.19.0" },
  },
  {
    path: "failed protocol validation",
    record: {
      state: "detected",
      version: "1.19.0",
      protocol_verified: false,
      protocol: { state: "failed" },
    },
    cause: OpenCodeInstallationCause.Other,
    observation: { cause: "protocolFailed", detectedVersion: "1.19.0" },
  },
  {
    path: "missing executable",
    record: { state: "missing" },
    cause: OpenCodeInstallationCause.Other,
    observation: { cause: "missing" },
  },
  {
    path: "verified installation",
    record: {
      state: "detected",
      version: "1.19.0",
      protocol_verified: true,
      protocol: { state: "verified" },
    },
    cause: undefined,
    observation: { detectedVersion: "1.19.0" },
  },
])(
  "maps the OpenCode discovery path $path to one remedy cause",
  ({ record, cause, observation }) => {
    const result = installationObservation(
      { harness: "opencode", ...record },
      installationVersionAllowed("opencode"),
    );
    expect(result).toEqual(observation);
    if (result.cause)
      expect(
        openCodeInstallationCause(result.cause, result.detectedVersion),
      ).toBe(cause);
    else expect(cause).toBeUndefined();
  },
);
