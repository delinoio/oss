import { describe, expect, it } from "vitest";

import { ShortcutKey, ShortcutModifier } from "../../persistence/contracts";
import {
  removeDraftField,
  removeDraftUrl,
  type RealQaDraftContent,
} from "./contracts";
import {
  applySynchronizedPreset,
  inferChromeUrl,
  inferDesktopUrl,
  validateRealQaProcessUrlRules,
  type RealQaDeviceState,
  type RealQaProcessUrlRule,
  type SynchronizedRealQaPreset,
} from "./presets";
import { restoreCapturedUrlParts, sanitizeCapturedUrl } from "./url";

const rule = (
  overrides: Partial<RealQaProcessUrlRule> = {},
): RealQaProcessUrlRule => ({
  ruleId: "01900000-0000-7000-8000-000000000001",
  exactProcessName: "code",
  safeWindowTitlePattern: String.raw`^Issue ([0-9]+)$`,
  urlTemplate: "https://github.com/delinoio/oss/issues/$1",
  enabled: true,
  ...overrides,
});

describe("RealQA URL capture boundary", () => {
  it("accepts only credential-free HTTP(S), strips query/fragment, and explicitly restores them", () => {
    const result = sanitizeCapturedUrl(
      "https://example.com/report?token=sensitive#private",
    );
    expect(result).toEqual({
      ok: true,
      url: {
        value: "https://example.com/report",
        strippedQuery: "?token=sensitive",
        strippedFragment: "#private",
        warning: null,
      },
    });
    if (!result.ok) throw new Error("fixture must be valid");
    expect(restoreCapturedUrlParts(result.url).value).toBe(
      "https://example.com/report?token=sensitive#private",
    );
    expect(sanitizeCapturedUrl("https://user:password@example.com/")).toEqual({
      ok: false,
      reason: "credentials-forbidden",
    });
    expect(sanitizeCapturedUrl("file:///tmp/private")).toEqual({
      ok: false,
      reason: "unsupported-scheme",
    });
  });

  it.each([
    "http://localhost:3000/path",
    "http://localhost./path",
    "https://api.localhost/path",
    "http://127.0.0.1/path",
    "https://10.0.0.5/path",
    "https://172.16.1.2/path",
    "https://192.168.1.2/path",
    "http://[::1]/path",
    "http://[::ffff:127.0.0.1]/path",
    "http://[fd00::1]/path",
  ])("warns without rejecting local/private destination %s", (value) => {
    const result = sanitizeCapturedUrl(value);
    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.url.warning).toBe("localhost-or-private-host");
    }
  });

  it("uses the active Chrome tab URL through the same sanitation boundary", () => {
    expect(inferChromeUrl("https://example.com/a?session=secret#view")).toMatchObject({
      ok: true,
      url: { value: "https://example.com/a" },
    });
  });
});

describe("RealQA synchronized presets and ordered desktop rules", () => {
  it("matches exact process names, expands titles, and stops at the first safe match", () => {
    const rules = [
      rule(),
      rule({
        ruleId: "01900000-0000-7000-8000-000000000002",
        safeWindowTitlePattern: "",
        urlTemplate: "https://fallback.example/",
      }),
    ];
    expect(inferDesktopUrl(rules, "Code", "Issue 757")).toBeNull();
    expect(inferDesktopUrl(rules, "code", "Issue 757")).toMatchObject({
      ok: true,
      url: { value: "https://github.com/delinoio/oss/issues/757" },
    });
  });

  it("skips an unsafe expanded URL and uses an ordered fallback; no match stays blank", () => {
    const rules = [
      rule({
        safeWindowTitlePattern: String.raw`^(.+)$`,
        urlTemplate: "https://$1.example.com/",
      }),
      rule({
        ruleId: "01900000-0000-7000-8000-000000000002",
        safeWindowTitlePattern: "",
        urlTemplate: "https://fallback.example/",
      }),
    ];
    expect(inferDesktopUrl(rules, "code", "user@example")).toMatchObject({
      ok: true,
      url: { value: "https://fallback.example/" },
    });
    expect(inferDesktopUrl(rules, "terminal", "Issue 757")).toBeNull();
  });

  it("skips an expanded URL above the native limit and uses an ordered fallback", () => {
    const rules = [
      rule({
        safeWindowTitlePattern: String.raw`^(.+)$`,
        urlTemplate: "https://example.com/$1$1",
      }),
      rule({
        ruleId: "01900000-0000-7000-8000-000000000002",
        safeWindowTitlePattern: "",
        urlTemplate: "https://fallback.example/",
      }),
    ];
    expect(inferDesktopUrl(rules, "code", "a".repeat(4_100))).toMatchObject({
      ok: true,
      url: { value: "https://fallback.example/" },
    });
  });

  it("supports synchronized scoped title flags, including ungreedy matching", () => {
    expect(
      inferDesktopUrl(
        [
          rule({
            safeWindowTitlePattern: String.raw`(?i)^issue (?U:(.+)) END`,
            urlTemplate: "https://example.com/$1",
          }),
        ],
        "code",
        "ISSUE first END second END",
      ),
    ).toMatchObject({
      ok: true,
      url: { value: "https://example.com/first" },
    });
    expect(
      inferDesktopUrl(
        [
          rule({
            safeWindowTitlePattern: String.raw`(?s)^issue (?i:(.+))$`,
            urlTemplate: "https://example.com/$1",
          }),
        ],
        "code",
        "issue LINE\nTWO",
      ),
    ).toMatchObject({
      ok: true,
      url: { value: "https://example.com/LINETWO" },
    });
    expect(
      inferDesktopUrl(
        [
          rule({
            safeWindowTitlePattern: String.raw`(?m)^Issue ([0-9]+)$`,
          }),
        ],
        "code",
        "ignored\nIssue 783\nignored",
      ),
    ).toMatchObject({
      ok: true,
      url: { value: "https://github.com/delinoio/oss/issues/783" },
    });
  });

  it("ignores malformed disabled rules and continues with enabled safe rules", () => {
    const rules = [
      rule({
        enabled: false,
        safeWindowTitlePattern: "(?=unsupported)",
        urlTemplate: "not a URL",
      }),
      rule({
        ruleId: "01900000-0000-7000-8000-000000000002",
        safeWindowTitlePattern: "",
        urlTemplate: "https://fallback.example/",
      }),
    ];
    expect(validateRealQaProcessUrlRules(rules)).toBe(true);
    expect(inferDesktopUrl(rules, "code", "anything")).toMatchObject({
      ok: true,
      url: { value: "https://fallback.example/" },
    });
  });

  it.each([
    "(?=secret)",
    String.raw`(a)\1`,
    "(?<name>a)",
    String.raw`\C`,
    String.raw`\Qliteral\E`,
    "[a-z&&[^x]]",
    "a{101}",
    "(a+)+$",
    "(?:a|aa)+$",
    "((?:a|aa))+$",
    "([a-z]*)*$",
    "^Issue (",
  ])("rejects unsupported shared-regex syntax %s", (pattern) => {
    expect(
      validateRealQaProcessUrlRules([
        rule({ safeWindowTitlePattern: pattern }),
      ]),
    ).toBe(false);
  });

  it("retains permission, registration outcomes, and pairing locally during sync", () => {
    const deviceState: RealQaDeviceState = {
      capturePermission: "granted",
      shortcutRegistrations: { shortcut: "active" },
      extensionPairing: "paired",
    };
    const preset: SynchronizedRealQaPreset = {
      presetId: "preset",
      revision: 4,
      destination: {
        destinationId: "destination",
        provider: "github",
        installationId: "installation",
        repository: {
          repositoryId: "repository",
          owner: "delinoio",
          name: "oss",
        },
      },
      repositoryDefinition: {
        definitionId: "bugs.yml",
        kind: "issue-form",
        name: "Bug report",
        relativePath: ".github/ISSUE_TEMPLATE/bugs.yml",
        revision: "etag",
      },
      processUrlRules: [rule()],
      shortcut: {
        shortcutId: "shortcut",
        accelerator: {
          modifiers: [ShortcutModifier.Control, ShortcutModifier.Shift],
          key: ShortcutKey.K,
        },
        active: true,
      },
    };
    const applied = applySynchronizedPreset(deviceState, preset);
    expect(applied.preset).toBe(preset);
    expect(applied.deviceState).toBe(deviceState);
  });
});

describe("RealQA removable local metadata", () => {
  it("removes each environment, URL, and DOM value independently", () => {
    const content: RealQaDraftContent = {
      title: "Issue title",
      body: "Issue body",
      presetId: null,
      presetRevision: null,
      destinationId: null,
      repositoryDefinitionId: null,
      environment: [
        { id: "os", label: "OS", value: "Linux" },
        { id: "version", label: "DevHud", value: "0.1.0" },
      ],
      url: {
        value: "https://example.com/",
        strippedQuery: null,
        strippedFragment: null,
        warning: null,
      },
      dom: [{ id: "role", label: "Role", value: "button" }],
      images: [],
    };
    expect(removeDraftField(content.environment, "os")).toEqual([
      { id: "version", label: "DevHud", value: "0.1.0" },
    ]);
    expect(removeDraftField(content.dom, "role")).toEqual([]);
    expect(removeDraftUrl(content).url).toBeNull();
  });
});
