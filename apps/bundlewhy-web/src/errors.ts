import { Code, ConnectError } from "@connectrpc/connect";
import { splitCodedMessage } from "./format";

export interface ErrorDescription {
  /** bundlewhy code from a `BW_XXX:` message prefix, when present. */
  code?: string;
  /** Connect code name, for example `unauthenticated`. */
  connectCode: string;
  message: string;
  recovery: string;
}

function connectCodeName(code: Code): string {
  const name = Code[code] ?? "unknown";
  // Connect wire names are snake_case, for example `failed_precondition`.
  return name.replace(/([a-z])([A-Z])/g, "$1_$2").toLowerCase();
}

function recoveryFor(code: Code): string {
  switch (code) {
    case Code.Unauthenticated:
    case Code.PermissionDenied:
      return "Reopen the URL printed by `bundlewhy analyze` or `bundlewhy open`.";
    case Code.Unavailable:
    case Code.Unknown:
      return "Make sure the bundlewhy command that printed this URL is still running, then reload this page.";
    case Code.NotFound:
      return "The requested data is not part of this run. Reload the page or open another report.";
    case Code.FailedPrecondition:
      return "This action is not available for the current report.";
    default:
      return "Reload the page. If the problem continues, run `bundlewhy analyze` again.";
  }
}

/**
 * Recovery guidance for stable bundlewhy codes. A product code is more
 * specific than the Connect status it travels with (for example
 * BW_EXPORT_FAILED arrives as `internal`), so it takes precedence.
 */
const RECOVERY_BY_BW_CODE: Readonly<Record<string, string>> = Object.freeze({
  BW_ACCESS_DENIED:
    "Reopen the URL printed by `bundlewhy analyze` or `bundlewhy open`.",
  BW_ACCESS_TOKEN_MISSING:
    "Reopen the URL printed by `bundlewhy analyze` or `bundlewhy open`.",
  BW_EXPORT_FAILED:
    "Check that the run directory is writable and has free space, then export again. You can also run `bundlewhy export <run-dir> --output <file.html>`.",
  BW_SOURCE_NOT_FOUND:
    "The source is not stored in this run (it may have been analyzed with --exclude-sources).",
  BW_RESULT_INVALID:
    "The saved result is damaged or was not written by bundlewhy. Start a new run with `bundlewhy analyze`.",
  BW_SCHEMA_UNSUPPORTED:
    "Open the result with the bundlewhy version that created it, or start a new run.",
  BW_RESULT_TOO_LARGE:
    "Analyze with --exclude-sources, or analyze a smaller build.",
  BW_USAGE:
    "Reload the page. If the problem continues, run the bundlewhy command again.",
});

export function recoveryForCode(
  code: string | undefined,
  connect: Code,
): string {
  return (code && RECOVERY_BY_BW_CODE[code]) || recoveryFor(connect);
}

export function describeError(error: unknown): ErrorDescription {
  const e = ConnectError.from(error);
  const split = splitCodedMessage(e.rawMessage);
  return {
    code: split.code,
    connectCode: connectCodeName(e.code),
    message: split.message,
    recovery: recoveryForCode(split.code, e.code),
  };
}
