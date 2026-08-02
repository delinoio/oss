import { describe, expect, it } from "vitest";

import { RealQaFailureCode, realQaFailureGuidance } from "./contracts";

describe("RealQA typed failure guidance", () => {
  it("provides content-free guidance for every closed failure state", () => {
    const codes = Object.values(RealQaFailureCode);

    expect(Object.keys(realQaFailureGuidance)).toHaveLength(codes.length);
    for (const code of codes) {
      const guidance = realQaFailureGuidance[code];
      expect(guidance.length).toBeGreaterThan(20);
      expect(guidance).not.toMatch(/request body|response body|screenshot bytes|issue body/iu);
    }
  });

  it("keeps provider validation guidance distinct from GitHub availability", () => {
    expect(realQaFailureGuidance[RealQaFailureCode.ProviderValidationFailed]).toMatch(
      /review the labels, assignees, milestone, and form answers/iu,
    );
    expect(realQaFailureGuidance[RealQaFailureCode.ProviderValidationFailed]).not.toBe(
      realQaFailureGuidance[RealQaFailureCode.GitHubUnavailable],
    );
  });
});
