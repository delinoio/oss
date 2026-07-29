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
    expect(restoreCapturedUrlParts(result.url)).toBe(
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
    expect(sanitizeCapturedUrl("HTTPS://example.com/")).toMatchObject({
      ok: true,
      url: { value: "https://example.com/" },
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

  it("drops stripped URL parts above the native UTF-8 byte limit", () => {
    const result = sanitizeCapturedUrl(
      `https://example.com/a?${"é".repeat(4_096)}#${"x".repeat(8_193)}`,
    );

    expect(result).toEqual({
      ok: true,
      url: {
        value: "https://example.com/a",
        strippedQuery: null,
        strippedFragment: null,
        warning: null,
      },
    });
  });

  it("rejects a canonical stripped URL above the native UTF-8 byte limit", () => {
    expect(sanitizeCapturedUrl(`https://example.com/${"x".repeat(8_193)}`)).toEqual({
      ok: false,
      reason: "invalid-url",
    });
  });

  it.each([
    "https://example.com/LINE\nTWO",
    "https://example.com/LINE\tTWO",
    "https://example.com/%",
    "https://example.com/%zz",
    "https://%65xample.com/",
    String.raw`https:\example.com`,
    String.raw`https://example.com\repaired`,
    "https:///example.com",
    "https:example.com",
  ])("rejects a Go-invalid raw URL %s", (value) => {
    expect(sanitizeCapturedUrl(value)).toEqual({
      ok: false,
      reason: "invalid-url",
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

  it.each(["LINE\nTWO", "%", "%zz"])(
    "skips a Go-invalid captured URL value and uses an ordered fallback",
    (title) => {
      const rules = [
        rule({
          safeWindowTitlePattern: String.raw`(?s)^(.+)$`,
          urlTemplate: "https://example.com/$1",
        }),
        rule({
          ruleId: "01900000-0000-7000-8000-000000000002",
          safeWindowTitlePattern: "",
          urlTemplate: "https://fallback.example/",
        }),
      ];
      expect(inferDesktopUrl(rules, "code", title)).toMatchObject({
        ok: true,
        url: { value: "https://fallback.example/" },
      });
    },
  );

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
            urlTemplate: "https://example.com/matched",
          }),
        ],
        "code",
        "issue LINE\nTWO",
      ),
    ).toMatchObject({
      ok: true,
      url: { value: "https://example.com/matched" },
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

  it.each([
    String.raw`^?Issue$`,
    String.raw`^Issue$*`,
    String.raw`\A{0,1}Issue$`,
    String.raw`^Issue\z+`,
  ])("matches synchronized quantified anchor in %s", (pattern) => {
    expect(
      inferDesktopUrl(
        [
          rule({
            safeWindowTitlePattern: pattern,
            urlTemplate: "https://example.com/matched",
          }),
        ],
        "code",
        "Issue",
      ),
    ).toMatchObject({
      ok: true,
      url: { value: "https://example.com/matched" },
    });
  });

  it.each(["Issue\r\nnext", "Issue\u2028next", "Issue\u2029next"])(
    "uses only Go newline boundaries for synchronized multiline anchors in %s",
    (title) => {
      const rules = [
        rule({
          safeWindowTitlePattern: String.raw`(?m)^Issue$`,
          urlTemplate: "https://example.com/matched",
        }),
        rule({
          ruleId: "01900000-0000-7000-8000-000000000002",
          safeWindowTitlePattern: "",
          urlTemplate: "https://fallback.example/",
        }),
      ];
      expect(inferDesktopUrl(rules, "code", title)).toMatchObject({
        ok: true,
        url: { value: "https://fallback.example/" },
      });
    },
  );

  it.each([
    ["Issue\r757", true],
    ["Issue\u2028757", true],
    ["Issue\n757", false],
  ])(
    "matches synchronized bare dots against %s with Go semantics",
    (title, matches) => {
      const rules = [
        rule({
          safeWindowTitlePattern: String.raw`^Issue.([0-9]+)$`,
          urlTemplate: "https://example.com/matched",
        }),
        rule({
          ruleId: "01900000-0000-7000-8000-000000000002",
          safeWindowTitlePattern: "",
          urlTemplate: "https://fallback.example/",
        }),
      ];
      expect(inferDesktopUrl(rules, "code", title)).toMatchObject({
        ok: true,
        url: {
          value: matches
            ? "https://example.com/matched"
            : "https://fallback.example/",
        },
      });
    },
  );

  it.each([
    [String.raw`^\a$`, "\u0007"],
    [String.raw`^\x{41}$`, "A"],
    [String.raw`^[\a\x{41}]$`, "A"],
    [String.raw`^\_$`, "_"],
    [String.raw`^\!$`, "!"],
    [String.raw`^[\!]$`, "!"],
  ])("translates synchronized Go character escape %s", (pattern, title) => {
    expect(
      inferDesktopUrl(
        [
          rule({
            safeWindowTitlePattern: pattern,
            urlTemplate: "https://example.com/matched",
          }),
        ],
        "code",
        title,
      ),
    ).toMatchObject({
      ok: true,
      url: { value: "https://example.com/matched" },
    });
  });

  it.each([String.raw`^\u0041$`, String.raw`^\u{41}$`, String.raw`^\cA$`])(
    "rejects JavaScript-only synchronized escape %s",
    (pattern) => {
      expect(
        validateRealQaProcessUrlRules([
          rule({ safeWindowTitlePattern: pattern }),
        ]),
      ).toBe(false);
    },
  );

  it.each([String.raw`^\é$`, String.raw`^[\é]$`, String.raw`^\😀$`])(
    "rejects non-ASCII synchronized identity escape %s",
    (pattern) => {
      expect(
        validateRealQaProcessUrlRules([
          rule({ safeWindowTitlePattern: pattern }),
        ]),
      ).toBe(false);
    },
  );

  it.each([
    [String.raw`^\p{greek}$`, "Ω"],
    [String.raw`^\p{lu}$`, "A"],
    [String.raw`^\p{ANY}$`, "😀"],
    [String.raw`^\p{ascii}$`, "A"],
    [String.raw`^\p{uppercase_letter}$`, "A"],
  ])(
    "normalizes synchronized Go Unicode class alias %s",
    (pattern, title) => {
      expect(
        inferDesktopUrl(
          [
            rule({
              safeWindowTitlePattern: pattern,
              urlTemplate: "https://example.com/matched",
            }),
          ],
          "code",
          title,
        ),
      ).toMatchObject({
        ok: true,
        url: { value: "https://example.com/matched" },
      });
    },
  );

  it.each([String.raw`(?i)^\P{Lu}$`, String.raw`(?i)^\p{^Lu}$`])(
    "preserves synchronized case-insensitive Unicode complement %s",
    (pattern) => {
      const rules = [
        rule({
          safeWindowTitlePattern: pattern,
          urlTemplate: "https://example.com/matched",
        }),
        rule({
          ruleId: "01900000-0000-7000-8000-000000000002",
          safeWindowTitlePattern: "",
          urlTemplate: "https://fallback.example/",
        }),
      ];
      for (const title of ["A", "a"]) {
        expect(inferDesktopUrl(rules, "code", title)).toMatchObject({
          ok: true,
          url: { value: "https://fallback.example/" },
        });
      }
      expect(inferDesktopUrl(rules, "code", "0")).toMatchObject({
        ok: true,
        url: { value: "https://example.com/matched" },
      });
    },
  );

  it.each([
    String.raw`(?i)^[\P{Lu}]$`,
    String.raw`(?i)^[\p{^Lu}]$`,
    String.raw`(?i)^[\P{Lu}0]$`,
    String.raw`(?i)^[\p{^Lu}0]$`,
  ])(
    "preserves synchronized case-insensitive Unicode complement inside a class %s",
    (pattern) => {
      const rules = [
        rule({
          safeWindowTitlePattern: pattern,
          urlTemplate: "https://example.com/matched",
        }),
        rule({
          ruleId: "01900000-0000-7000-8000-000000000002",
          safeWindowTitlePattern: "",
          urlTemplate: "https://fallback.example/",
        }),
      ];
      for (const title of ["A", "a"]) {
        expect(inferDesktopUrl(rules, "code", title)).toMatchObject({
          ok: true,
          url: { value: "https://fallback.example/" },
        });
      }
      expect(inferDesktopUrl(rules, "code", "0")).toMatchObject({
        ok: true,
        url: { value: "https://example.com/matched" },
      });
    },
  );

  it.each([String.raw`(?i)^[^\P{Lu}]$`, String.raw`(?i)^[^\p{^Lu}]$`])(
    "preserves synchronized case-insensitive doubly negated Unicode class %s",
    (pattern) => {
      const rules = [
        rule({
          safeWindowTitlePattern: pattern,
          urlTemplate: "https://example.com/matched",
        }),
        rule({
          ruleId: "01900000-0000-7000-8000-000000000002",
          safeWindowTitlePattern: "",
          urlTemplate: "https://fallback.example/",
        }),
      ];
      for (const title of ["A", "a"]) {
        expect(inferDesktopUrl(rules, "code", title)).toMatchObject({
          ok: true,
          url: { value: "https://example.com/matched" },
        });
      }
      expect(inferDesktopUrl(rules, "code", "0")).toMatchObject({
        ok: true,
        url: { value: "https://fallback.example/" },
      });
    },
  );

  it.each([
    String.raw`(?i)^[^\P{Lu}0]+$`,
    String.raw`(?i)^[^\p{^Lu}0]+$`,
  ])(
    "applies synchronized quantified negated Unicode class %s to every character",
    (pattern) => {
      const rules = [
        rule({
          safeWindowTitlePattern: pattern,
          urlTemplate: "https://example.com/matched",
        }),
        rule({
          ruleId: "01900000-0000-7000-8000-000000000002",
          safeWindowTitlePattern: "",
          urlTemplate: "https://fallback.example/",
        }),
      ];
      expect(inferDesktopUrl(rules, "code", "Aa")).toMatchObject({
        ok: true,
        url: { value: "https://example.com/matched" },
      });
      expect(inferDesktopUrl(rules, "code", "A1")).toMatchObject({
        ok: true,
        url: { value: "https://fallback.example/" },
      });
    },
  );

  it.each([
    [String.raw`^Issue {01}$`, "Issue {01}", "Issue "],
    [String.raw`^a{1,02}$`, "a{1,02}", "aa"],
  ])(
    "treats synchronized leading-zero repetition text %s as literal",
    (pattern, matchingTitle, nonmatchingTitle) => {
      const rules = [
        rule({
          safeWindowTitlePattern: pattern,
          urlTemplate: "https://example.com/matched",
        }),
        rule({
          ruleId: "01900000-0000-7000-8000-000000000002",
          safeWindowTitlePattern: "",
          urlTemplate: "https://fallback.example/",
        }),
      ];
      expect(inferDesktopUrl(rules, "code", matchingTitle)).toMatchObject({
        ok: true,
        url: { value: "https://example.com/matched" },
      });
      expect(inferDesktopUrl(rules, "code", nonmatchingTitle)).toMatchObject({
        ok: true,
        url: { value: "https://fallback.example/" },
      });
    },
  );

  it.each([
    [String.raw`^[\p{Greek}-\p{Latin}]$`, "-"],
    [String.raw`^[\p{Greek}-\p{Latin}]$`, "Ω"],
    [String.raw`^[\d-\w]$`, "-"],
    [String.raw`^[\d-\w]$`, "a"],
  ])(
    "treats hyphens adjacent to synchronized class escapes as literals in %s",
    (pattern, title) => {
      expect(
        inferDesktopUrl(
          [
            rule({
              safeWindowTitlePattern: pattern,
              urlTemplate: "https://example.com/matched",
            }),
          ],
          "code",
          title,
        ),
      ).toMatchObject({
        ok: true,
        url: { value: "https://example.com/matched" },
      });
    },
  );

  it.each([String.raw`^[\b]$`, String.raw`^[\B]$`])(
    "rejects synchronized word-boundary escape inside a class %s",
    (pattern) => {
      expect(
        validateRealQaProcessUrlRules([
          rule({ safeWindowTitlePattern: pattern }),
        ]),
      ).toBe(false);
    },
  );

  it.each([
    [String.raw`^Issue\s([0-9]+)$`, "Issue 757", true],
    [String.raw`^Issue\s([0-9]+)$`, "Issue\u00a0757", false],
    [String.raw`^Issue[\s]([0-9]+)$`, "Issue\t757", true],
    [String.raw`^Issue[\s]([0-9]+)$`, "Issue\u00a0757", false],
    [String.raw`^Issue\S([0-9]+)$`, "Issue\u00a0757", true],
    [String.raw`^Issue[\S]([0-9]+)$`, "Issue\u00a0757", true],
  ])(
    "matches synchronized Perl whitespace class %s with Go semantics",
    (pattern, title, matches) => {
      const rules = [
        rule({ safeWindowTitlePattern: pattern }),
        rule({
          ruleId: "01900000-0000-7000-8000-000000000002",
          safeWindowTitlePattern: "",
          urlTemplate: "https://fallback.example/",
        }),
      ];
      expect(inferDesktopUrl(rules, "code", title)).toMatchObject({
        ok: true,
        url: {
          value: matches
            ? "https://github.com/delinoio/oss/issues/757"
            : "https://fallback.example/",
        },
      });
    },
  );

  it.each([
    [String.raw`(?i)^\bK\b$`, "K", true],
    [String.raw`(?i)^\bK\b$`, "k", true],
    [String.raw`(?i)^\bK\b$`, "K", false],
    [String.raw`(?i)^\BK\B$`, "K", true],
  ])(
    "matches synchronized word boundary %s against %s with Go semantics",
    (pattern, title, matches) => {
      const rules = [
        rule({
          safeWindowTitlePattern: pattern,
          urlTemplate: "https://example.com/matched",
        }),
        rule({
          ruleId: "01900000-0000-7000-8000-000000000002",
          safeWindowTitlePattern: "",
          urlTemplate: "https://fallback.example/",
        }),
      ];
      expect(inferDesktopUrl(rules, "code", title)).toMatchObject({
        ok: true,
        url: {
          value: matches
            ? "https://example.com/matched"
            : "https://fallback.example/",
        },
      });
    },
  );

  it.each([
    [String.raw`(?i)^[[:^alpha:]]$`, "K", false],
    [String.raw`(?i)^[[:^alpha:]]$`, "ſ", false],
    [String.raw`(?i)^[[:^alpha:]]$`, "İ", true],
    [String.raw`(?i)^[[:^upper:]]$`, "k", false],
    [String.raw`(?i)^[[:^upper:]]$`, "0", true],
  ])(
    "matches synchronized negated POSIX class %s against %s with Go semantics",
    (pattern, title, matches) => {
      const rules = [
        rule({
          safeWindowTitlePattern: pattern,
          urlTemplate: "https://example.com/matched",
        }),
        rule({
          ruleId: "01900000-0000-7000-8000-000000000002",
          safeWindowTitlePattern: "",
          urlTemplate: "https://fallback.example/",
        }),
      ];
      expect(inferDesktopUrl(rules, "code", title)).toMatchObject({
        ok: true,
        url: {
          value: matches
            ? "https://example.com/matched"
            : "https://fallback.example/",
        },
      });
    },
  );

  it("rejects malformed disabled rules to match server compilation", () => {
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
    expect(validateRealQaProcessUrlRules(rules)).toBe(false);
  });

  it("limits the total ordered rule slice, including disabled rules", () => {
    const disabledRules = Array.from({ length: 64 }, (_, index) =>
      rule({ ruleId: `disabled-${index}`, enabled: false }),
    );
    expect(validateRealQaProcessUrlRules(disabledRules)).toBe(true);
    expect(
      validateRealQaProcessUrlRules([
        ...disabledRules,
        rule({ ruleId: "enabled-rule" }),
      ]),
    ).toBe(false);
  });

  it.each([
    [String.raw`^\p{Han}+$`, "漢字"],
    [String.raw`^\p{^Greek}+$`, "Letters"],
    [String.raw`^\P{^Greek}+$`, "Ω"],
    [String.raw`^\pL+$`, "Letters"],
    [String.raw`^[\p{Han}]+$`, "漢字"],
    [String.raw`^[[:alpha:]]+$`, "Letters"],
    [String.raw`^[[:^digit:]]+$`, "Letters"],
    ["^Issue {$", "Issue {"],
    ["^Issue }$", "Issue }"],
  ])("translates synchronized title class %s", (pattern, title) => {
    expect(
      inferDesktopUrl(
        [
          rule({
            safeWindowTitlePattern: pattern,
            urlTemplate: "https://example.com/matched",
          }),
        ],
        "code",
        title,
      ),
    ).toMatchObject({
      ok: true,
      url: { value: "https://example.com/matched" },
    });
  });

  it.each([
    "(?=secret)",
    String.raw`(a)\1`,
    "(?<name>a)",
    String.raw`\C`,
    String.raw`\Qliteral\E`,
    "[a-z&&[^x]]",
    "[[:alpha:]&&[^x]]",
    "a{101}",
    "(a+)+$",
    "(?:a|aa)+$",
    "((?:a|aa))+$",
    "([a-z]*)*$",
    "^Issue (",
    "[]",
    "[^]",
    "(?-)^Issue",
    "(?i-)a",
  ])("rejects unsupported shared-regex syntax %s", (pattern) => {
    expect(
      validateRealQaProcessUrlRules([
        rule({ safeWindowTitlePattern: pattern }),
      ]),
    ).toBe(false);
  });

  it("accepts bounded repetitions through 100 and checks both explicit bounds", () => {
    expect(
      validateRealQaProcessUrlRules([
        rule({ safeWindowTitlePattern: String.raw`^a{100}$` }),
      ]),
    ).toBe(true);
    expect(
      validateRealQaProcessUrlRules([
        rule({ safeWindowTitlePattern: String.raw`^a{1,101}$` }),
      ]),
    ).toBe(false);
  });

  it.each([
    String.raw`^\x{101}$`,
    String.raw`^\x{3000}$`,
    String.raw`^\x{1F600}$`,
    String.raw`^(\x{41})+$`,
  ])(
    "does not interpret a synchronized braced hex escape as repetition %s",
    (pattern) => {
      expect(
        validateRealQaProcessUrlRules([
          rule({ safeWindowTitlePattern: pattern }),
        ]),
      ).toBe(true);
    },
  );

  it("treats repetition text after a POSIX class as part of the outer class", () => {
    expect(
      validateRealQaProcessUrlRules([
        rule({ safeWindowTitlePattern: String.raw`^[[:digit:]{101}]$` }),
      ]),
    ).toBe(true);
    expect(
      validateRealQaProcessUrlRules([
        rule({ safeWindowTitlePattern: String.raw`^([[:digit:]{101}])+$` }),
      ]),
    ).toBe(true);
  });

  it("mirrors the server's compiled-regex instruction budget", () => {
    expect(
      validateRealQaProcessUrlRules([
        rule({ safeWindowTitlePattern: `(?:${"a".repeat(20)}){100}` }),
      ]),
    ).toBe(true);
    expect(
      validateRealQaProcessUrlRules([
        rule({ safeWindowTitlePattern: `(?:${"a".repeat(22)}){100}` }),
      ]),
    ).toBe(false);
  });

  it.each(["^issue--draft$", "^issue&&draft$", "^issue~~draft$"])(
    "accepts safe literal class-operator text %s",
    (pattern) => {
      expect(
        validateRealQaProcessUrlRules([
          rule({ safeWindowTitlePattern: pattern }),
        ]),
      ).toBe(true);
    },
  );

  it("uses Go's longest template-name expansion", () => {
    expect(
      inferDesktopUrl(
        [
          rule({
            safeWindowTitlePattern: String.raw`^Issue ([0-9]+)$`,
            urlTemplate: "https://example.com/$1edit",
          }),
        ],
        "code",
        "Issue 783",
      ),
    ).toMatchObject({
      ok: true,
      url: { value: "https://example.com/" },
    });
    expect(
      inferDesktopUrl(
        [
          rule({
            safeWindowTitlePattern: String.raw`^Issue ([0-9]+)$`,
            urlTemplate: "https://example.com/${1}edit",
          }),
        ],
        "code",
        "Issue 783",
      ),
    ).toMatchObject({
      ok: true,
      url: { value: "https://example.com/783edit" },
    });
  });

  it.each([
    "https://example.com/%",
    "https://example.com/%2",
    "https://example.com/%zz",
    "https://%65xample.com/",
    String.raw`https://example.com\repaired`,
  ])("rejects a Go-invalid URL template %s", (urlTemplate) => {
    expect(validateRealQaProcessUrlRules([rule({ urlTemplate })])).toBe(false);
  });

  it("accepts a valid URL template percent escape", () => {
    expect(
      validateRealQaProcessUrlRules([
        rule({ urlTemplate: "https://example.com/%2F" }),
      ]),
    ).toBe(true);
  });

  it("accepts raw percent text in a synchronized URL query", () => {
    const rules = [
      rule({ urlTemplate: "https://example.com/?q=%zz&partial=%2" }),
    ];
    expect(validateRealQaProcessUrlRules(rules)).toBe(true);
    expect(inferDesktopUrl(rules, "code", "Issue 757")).toMatchObject({
      ok: true,
      url: {
        value: "https://example.com/",
        strippedQuery: "?q=%zz&partial=%2",
      },
    });
  });

  it("accepts a URL template with a case-insensitive HTTP(S) scheme", () => {
    expect(
      validateRealQaProcessUrlRules([
        rule({ urlTemplate: "HTTPS://example.com/" }),
      ]),
    ).toBe(true);
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
