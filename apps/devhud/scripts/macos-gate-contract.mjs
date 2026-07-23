export const gateTargets = Object.freeze({
  "aarch64-apple-darwin": Object.freeze({
    architecture: "arm64",
    executableArchitecture: "arm64",
  }),
  "x86_64-apple-darwin": Object.freeze({
    architecture: "x64",
    executableArchitecture: "x86_64",
  }),
});

export const requiredRuntimeEvents = Object.freeze([
  "devhud.probe.bundled_asset_ready",
  "devhud.probe.capability_denial_observed",
  "devhud.probe.macos_resident_shell_ready",
  "devhud.probe.window_close_hidden",
  "devhud.probe.system_theme_change_ready",
  "devhud.probe.macos_gate_conditions_passed",
]);

const requiredTopLevelEvidenceKeys = Object.freeze([
  "schemaVersion",
  "target",
  "upstream",
  "runtime",
  "failures",
  "packaging",
  "updater",
  "diagnostics",
  "passed",
]);

export function assertSafeDiagnostics(text, excludedValues) {
  for (const value of excludedValues) {
    if (value && text.includes(value)) {
      throw new Error("macOS gate diagnostic redaction failed");
    }
  }
}

export function safeFailureSummary(text) {
  const pathPattern =
    /(?:[A-Za-z]:)?[/\\](?:[^/\\\s:'"=]+[/\\])*[^/\\\s:'"=]*/gu;
  const sensitivePattern =
    /\b(?:[A-Za-z0-9+/]{64,}={0,2}|[A-Fa-f0-9]{48,})\b/gu;
  const shortcutPattern =
    /\b(?:control|ctrl|alt|option|shift|F18)(?:[+\s-]*(?:control|ctrl|alt|option|shift|F18))*\b/giu;
  const relevant = text
    .split(/\r?\n/u)
    .filter((line) =>
      /\b(?:caused by|error|expected|failed|failure|found|unsupported)\b/iu.test(
        line,
      ),
    )
    .slice(-24)
    .map((line) =>
      line
        .replace(pathPattern, "<path>")
        .replace(sensitivePattern, "<sensitive>")
        .replace(shortcutPattern, "<shortcut>")
        .trim()
        .slice(0, 400),
    )
    .filter(Boolean);

  return relevant.length > 0 ? relevant : ["subprocess-failure"];
}

export function validateSafeEvidence(evidence) {
  if (
    Object.keys(evidence).join(",") !== requiredTopLevelEvidenceKeys.join(",") ||
    evidence.schemaVersion !== 1 ||
    evidence.passed !== true
  ) {
    throw new Error("macOS gate evidence does not match the safe schema");
  }

  const booleans = [
    ...Object.values(evidence.runtime).filter(
      (value) => typeof value === "boolean",
    ),
    ...Object.values(evidence.failures).filter(
      (value) => typeof value === "boolean",
    ),
    ...Object.values(evidence.packaging).filter(
      (value) => typeof value === "boolean",
    ),
    ...Object.values(evidence.updater).filter(
      (value) => typeof value === "boolean",
    ),
    ...Object.values(evidence.diagnostics),
  ];
  if (!booleans.every((value) => value === true)) {
    throw new Error("macOS gate evidence contains a failed condition");
  }
  if (
    evidence.runtime.repeatedCycles < 3 ||
    evidence.runtime.helperCountBeforeShutdown < 1 ||
    evidence.runtime.helperCountAfterShutdown !== 0
  ) {
    throw new Error("macOS gate lifecycle evidence is incomplete");
  }

  const stringValues = [];
  const collectStrings = (value) => {
    if (typeof value === "string") {
      stringValues.push(value);
    } else if (Array.isArray(value)) {
      value.forEach(collectStrings);
    } else if (value && typeof value === "object") {
      Object.values(value).forEach(collectStrings);
    }
  };
  collectStrings(evidence);
  if (
    stringValues.some((value) =>
      /(?:[/\\][^",]{2,}|private.?key|certificate|password|shortcut.?value)/iu.test(
        value,
      ),
    )
  ) {
    throw new Error("macOS gate evidence contains prohibited diagnostic data");
  }
}
